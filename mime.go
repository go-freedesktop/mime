// Copyright (c) the go-freedesktop/mime authors
//
// SPDX-License-Identifier: BSD-3-Clause

// Package mime implements the freedesktop.org Shared MIME-info Database
// specification in pure Go (CGO-free): the piece a file manager or
// Spotlight-style finder needs to answer "what type is this file?".
//
// It resolves a canonical MIME type from a file's name (glob matching), from
// its content (magic sniffing), or from both together following the spec's
// glob-versus-magic tiebreak. It also resolves aliases and answers subclass
// ("is-a") queries.
//
// The database is read from the on-disk Shared MIME-info directories
// ($XDG_DATA_HOME/mime and $XDG_DATA_DIRS/mime), either from the generated
// files an update-mime-database run produces (globs2, magic, aliases,
// subclasses) or directly from the source packages/*.xml. Base-directory
// resolution reuses github.com/adrg/xdg, matching the rest of the
// go-freedesktop family.
//
// Spec: https://specifications.freedesktop.org/shared-mime-info-spec/latest/
package mime

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/adrg/xdg"
)

// Well-known canonical types the spec singles out for fallbacks.
const (
	// OctetStream is the fallback for content that does not look like text.
	OctetStream = "application/octet-stream"
	// PlainText is the fallback for content that looks like text.
	PlainText = "text/plain"
	// ZeroSize is the type reported for an empty (zero-byte) file.
	ZeroSize = "application/x-zerosize"
)

// magicSniffLen is how many leading bytes a launcher typically reads to sniff
// content; callers may pass more, but this is the recommended window.
const magicSniffLen = 256

// Database is a parsed Shared MIME-info database. The zero value is not ready
// for use; build one with New, Load, or LoadDir. A Database is safe for
// concurrent reads once fully built.
type Database struct {
	globs      []glob
	magics     []magicRule
	aliases    map[string]string   // alias -> canonical
	revAliases map[string][]string // canonical -> aliases
	subclasses map[string][]string // subtype -> declared parents
}

// New returns an empty Database ready to be populated with AddXML,
// AddPackagesDir, or the loader helpers.
func New() *Database {
	return &Database{
		aliases:    map[string]string{},
		revAliases: map[string][]string{},
		subclasses: map[string][]string{},
	}
}

// Load reads and merges every Shared MIME-info database found on the system:
// $XDG_DATA_HOME/mime first, then each $XDG_DATA_DIRS/mime, resolved through
// github.com/adrg/xdg. Missing directories are skipped; a directory that
// exists but contains malformed data yields an error.
func Load() (*Database, error) {
	db := New()
	dirs := append([]string{xdg.DataHome}, xdg.DataDirs...)
	for _, d := range dirs {
		mimeDir := filepath.Join(d, "mime")
		if fi, err := os.Stat(mimeDir); err != nil || !fi.IsDir() {
			continue
		}
		if err := db.loadDir(mimeDir); err != nil {
			return nil, err
		}
	}
	return db, nil
}

// LoadDir reads a single Shared MIME-info directory (the "mime" directory that
// holds globs2, magic, aliases, subclasses and packages/). It prefers the
// generated files; if none are present it falls back to parsing packages/*.xml.
func LoadDir(dir string) (*Database, error) {
	db := New()
	if err := db.loadDir(dir); err != nil {
		return nil, err
	}
	return db, nil
}

// loadDir merges one mime directory into db.
func (db *Database) loadDir(dir string) error {
	generated := false

	if b, err := os.ReadFile(filepath.Join(dir, "globs2")); err == nil {
		if err := db.parseGlobs2(string(b)); err != nil {
			return err
		}
		generated = true
	} else if b, err := os.ReadFile(filepath.Join(dir, "globs")); err == nil {
		if err := db.parseGlobs1(string(b)); err != nil {
			return err
		}
		generated = true
	}

	if b, err := os.ReadFile(filepath.Join(dir, "magic")); err == nil {
		if err := db.parseMagic(b); err != nil {
			return err
		}
		generated = true
	}

	if b, err := os.ReadFile(filepath.Join(dir, "aliases")); err == nil {
		db.parseAliases(string(b))
		generated = true
	}

	if b, err := os.ReadFile(filepath.Join(dir, "subclasses")); err == nil {
		db.parseSubclasses(string(b))
		generated = true
	}

	if !generated {
		return db.AddPackagesDir(filepath.Join(dir, "packages"))
	}
	return nil
}

// Unalias resolves t through the alias table to its canonical type. If t is
// not an alias it is returned unchanged.
func (db *Database) Unalias(t string) string {
	if c, ok := db.aliases[t]; ok {
		return c
	}
	return t
}

// Aliases returns the alternative type strings declared as aliases of the
// canonical type t, in the order they were registered.
func (db *Database) Aliases(t string) []string {
	return db.revAliases[t]
}

// Parents returns the directly declared parent types (sub-class-of) of t.
func (db *Database) Parents(t string) []string {
	return db.subclasses[db.Unalias(t)]
}

// IsSubclassOf reports whether t is the same as, or a subclass ("is-a") of,
// parent. It follows declared sub-class-of relations transitively and honours
// the spec's implicit rules: every non-inode type is a subclass of
// application/octet-stream, and every text/* type is a subclass of text/plain.
func (db *Database) IsSubclassOf(t, parent string) bool {
	t = db.Unalias(t)
	parent = db.Unalias(parent)
	return db.isSubclass(t, parent, map[string]bool{})
}

func (db *Database) isSubclass(t, parent string, seen map[string]bool) bool {
	if t == parent {
		return true
	}
	if seen[t] {
		return false
	}
	seen[t] = true

	// Implicit rules from the spec.
	if parent == OctetStream && !strings.HasPrefix(t, "inode/") {
		return true
	}
	if parent == PlainText && strings.HasPrefix(t, "text/") {
		return true
	}

	for _, p := range db.subclasses[t] {
		if db.isSubclass(db.Unalias(p), parent, seen) {
			return true
		}
	}
	return false
}

// TypeByContent sniffs data and returns the best matching canonical type. An
// empty slice yields ZeroSize; if no magic rule matches, the result is
// PlainText when the data looks like text and OctetStream otherwise.
func (db *Database) TypeByContent(data []byte) string {
	if len(data) == 0 {
		return ZeroSize
	}
	if t, _ := db.magicLookup(data); t != "" {
		return t
	}
	if looksLikeText(data) {
		return PlainText
	}
	return OctetStream
}

// TypeByName returns the single best canonical type for a file name using glob
// matching, or "" when no glob matches. Only the file's base name is
// considered.
func (db *Database) TypeByName(name string) string {
	types := db.TypesByName(name)
	if len(types) == 0 {
		return ""
	}
	return types[0]
}

// TypesByName returns every canonical type in the highest-precedence glob tier
// that matches name, ordered by descending weight. It is empty when nothing
// matches. Precedence follows the spec: literal file-name matches beat
// suffix (*.ext) matches beat full fnmatch globs, and among suffix matches the
// longest matching suffix wins.
func (db *Database) TypesByName(name string) []string {
	return db.globMatches(base(name))
}

// TypeByNameAndContent combines glob and magic matching following the spec's
// recommended checking order and its glob-versus-magic tiebreak, and always
// returns a concrete type (falling back to ZeroSize / PlainText /
// OctetStream). Pass data==nil to indicate the content is unavailable; an
// empty non-nil slice means a genuinely zero-byte file.
func (db *Database) TypeByNameAndContent(name string, data []byte) string {
	if data != nil && len(data) == 0 {
		return ZeroSize
	}

	globTypes := db.TypesByName(name)

	if data == nil {
		if len(globTypes) > 0 {
			return globTypes[0]
		}
		return OctetStream
	}

	magicType, magicPrio := db.magicLookup(data)

	if len(globTypes) == 0 {
		return db.TypeByContent(data)
	}
	if magicType == "" {
		return globTypes[0]
	}

	glob := globTypes[0]
	switch {
	case magicType == glob:
		return glob
	case db.IsSubclassOf(magicType, glob):
		// Magic identified a more specific subtype of the glob result; the
		// sniffed type is more precise, so use it.
		return magicType
	case db.IsSubclassOf(glob, magicType):
		// Glob is the more specific of the two related types.
		return glob
	case magicPrio >= 80:
		// Strong, unrelated magic overrides the file name.
		return magicType
	default:
		// The file name is authoritative for unrelated weak magic.
		return glob
	}
}

// base returns the final path element of name using the unix separator, as the
// Shared MIME-info database always describes names with '/'.
func base(name string) string {
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		return name[i+1:]
	}
	return name
}

// ---- process-wide default database ----

var (
	defaultOnce sync.Once
	defaultDB   *Database
)

// Default returns the process-wide database, loaded once from the system
// Shared MIME-info directories with Load. If loading fails it returns an empty
// database so lookups degrade to the spec fallbacks rather than panicking.
func Default() *Database {
	defaultOnce.Do(func() { defaultDB = loadDefault() })
	return defaultDB
}

// loadDefault loads the system database, degrading to an empty database if
// loading fails so lookups never panic.
func loadDefault() *Database {
	db, err := Load()
	if err != nil || db == nil {
		return New()
	}
	return db
}

// TypeByName is TypeByName on the Default database.
func TypeByName(name string) string { return Default().TypeByName(name) }

// TypeByContent is TypeByContent on the Default database.
func TypeByContent(data []byte) string { return Default().TypeByContent(data) }

// TypeByNameAndContent is TypeByNameAndContent on the Default database.
func TypeByNameAndContent(name string, data []byte) string {
	return Default().TypeByNameAndContent(name, data)
}

// Unalias is Unalias on the Default database.
func Unalias(t string) string { return Default().Unalias(t) }

// IsSubclassOf is IsSubclassOf on the Default database.
func IsSubclassOf(t, parent string) bool { return Default().IsSubclassOf(t, parent) }
