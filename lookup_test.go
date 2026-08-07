// Copyright (c) the go-freedesktop/mime authors
//
// SPDX-License-Identifier: BSD-3-Clause

package mime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adrg/xdg"
)

var (
	pdfData = []byte("%PDF-1.4\n%…")
	svgData = []byte("<svg xmlns='http://www.w3.org/2000/svg'/>")
	xmlData = []byte("<?xml version='1.0'?><root/>")
	txtData = []byte("just some plain text\n")
	binData = []byte{0x00, 0x01, 0x02, 0x03}
)

func TestTypeByContent(t *testing.T) {
	db := fixtureDB(t)
	if got := db.TypeByContent(nil); got != ZeroSize {
		t.Errorf("empty => %q", got)
	}
	if got := db.TypeByContent(pdfData); got != "application/pdf" {
		t.Errorf("pdf => %q", got)
	}
	if got := db.TypeByContent(txtData); got != PlainText {
		t.Errorf("text => %q", got)
	}
	if got := db.TypeByContent(binData); got != OctetStream {
		t.Errorf("binary => %q", got)
	}
}

func TestLooksLikeText(t *testing.T) {
	if !looksLikeText([]byte("hello\tworld\n\r\f\v\x1b")) {
		t.Error("whitespace/ESC controls should read as text")
	}
	if !looksLikeText([]byte{0xc3, 0xa9}) { // high bytes (UTF-8) are allowed
		t.Error("high bytes should read as text")
	}
	if looksLikeText([]byte{'a', 0x00}) {
		t.Error("NUL should read as binary")
	}
	if looksLikeText([]byte{0x01}) {
		t.Error("control byte should read as binary")
	}
}

func TestTypeByNameAndContent(t *testing.T) {
	db := fixtureDB(t)
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"a.txt", nil, "text/plain"},          // name only, glob present
		{"mystery.zzz", nil, OctetStream},     // name only, no glob
		{"a.txt", []byte{}, ZeroSize},         // zero-byte file
		{"noext", pdfData, "application/pdf"}, // no glob, magic decides
		{"noext", txtData, PlainText},         // no glob, text fallback
		{"a.txt", txtData, "text/plain"},      // glob, magic silent
		{"a.txt", pdfData, "text/plain"},      // weak unrelated magic: name wins
		{"a.txt", svgData, "image/svg+xml"},   // magic subtype of glob's text/plain
		{"a.svg", xmlData, "image/svg+xml"},   // glob is the more specific of the pair
		{"a.pdf", pdfData, "application/pdf"}, // glob and magic agree
		{"a.png", svgData, "image/svg+xml"},   // strong (>=80) unrelated magic wins
	}
	for _, c := range cases {
		if got := db.TypeByNameAndContent(c.name, c.data); got != c.want {
			t.Errorf("TypeByNameAndContent(%q, %d bytes) = %q, want %q", c.name, len(c.data), got, c.want)
		}
	}
}

func TestIsSubclassOf(t *testing.T) {
	db := fixtureDB(t)
	if !db.IsSubclassOf("text/plain", "text/plain") {
		t.Error("reflexive")
	}
	if !db.IsSubclassOf("image/png", OctetStream) {
		t.Error("everything (non-inode) is octet-stream")
	}
	if db.IsSubclassOf("inode/directory", OctetStream) {
		t.Error("inode types are not octet-stream")
	}
	if !db.IsSubclassOf("text/x-anything", PlainText) {
		t.Error("text/* is text/plain")
	}
	if db.IsSubclassOf("image/png", PlainText) {
		t.Error("png is not text/plain")
	}
	// alias is resolved before the query
	if !db.IsSubclassOf("application/x-gzip", "application/gzip") {
		t.Error("alias should resolve to canonical")
	}

	// Declared cycle must terminate via the seen-set.
	c := New()
	c.addSubclass("a/x", "b/x")
	c.addSubclass("b/x", "a/x")
	if c.IsSubclassOf("a/x", "c/z") {
		t.Error("cycle should not yield a false positive")
	}
}

func TestBase(t *testing.T) {
	if base("/a/b/c.txt") != "c.txt" {
		t.Error("base with slashes")
	}
	if base("c.txt") != "c.txt" {
		t.Error("base without slashes")
	}
}

func TestAliasesAndParentsEmpty(t *testing.T) {
	db := New()
	if db.Aliases("nothing") != nil {
		t.Error("no aliases")
	}
	if db.Parents("nothing") != nil {
		t.Error("no parents")
	}
	// Guards: empty and duplicate entries are ignored.
	db.addAlias("", "x")
	db.addAlias("x", "")
	db.addAlias("a", "b")
	db.addAlias("a", "c") // duplicate alias ignored
	if db.Unalias("a") != "b" {
		t.Error("duplicate alias should not override")
	}
	db.addSubclass("", "p")
	db.addSubclass("s", "")
	db.addSubclass("s", "p")
	db.addSubclass("s", "p") // duplicate ignored
	if got := db.Parents("s"); len(got) != 1 {
		t.Errorf("Parents(s) = %v", got)
	}
}

func TestRelationParsersSkip(t *testing.T) {
	db := New()
	db.parseAliases("# c\n\nonlyonefield\nalias canonical\n")
	if db.Unalias("alias") != "canonical" {
		t.Error("parseAliases")
	}
	db.parseSubclasses("# c\n\nlonely\nsub parent\n")
	if got := db.Parents("sub"); len(got) != 1 || got[0] != "parent" {
		t.Errorf("parseSubclasses => %v", got)
	}
}

func TestLoadDirLegacyGlobsAndErrors(t *testing.T) {
	// Legacy "globs" file (used when globs2 is absent).
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "globs"), []byte("text/plain:*.txt\n"), 0o644)
	db, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("legacy globs: %v", err)
	}
	if db.TypeByName("a.txt") != "text/plain" {
		t.Error("legacy globs not applied")
	}

	// Malformed generated files each surface an error.
	badGlobs2 := t.TempDir()
	os.WriteFile(filepath.Join(badGlobs2, "globs2"), []byte("notaweight:t:*.x\n"), 0o644)
	if _, err := LoadDir(badGlobs2); err == nil {
		t.Error("bad globs2 should error")
	}

	badGlobs := t.TempDir()
	os.WriteFile(filepath.Join(badGlobs, "globs"), []byte("no-colon\n"), 0o644)
	if _, err := LoadDir(badGlobs); err == nil {
		t.Error("bad globs should error")
	}

	badMagic := t.TempDir()
	os.WriteFile(filepath.Join(badMagic, "magic"), []byte("not a magic file"), 0o644)
	if _, err := LoadDir(badMagic); err == nil {
		t.Error("bad magic should error")
	}

	// An empty directory (no generated files, no packages/) is a valid empty DB.
	empty := t.TempDir()
	if _, err := LoadDir(empty); err != nil {
		t.Errorf("empty dir => %v", err)
	}
}

func TestDefaultAndPackageFuncs(t *testing.T) {
	// TestMain has pointed XDG at the fixture, so Default resolves it.
	if got := TypeByName("report.pdf"); got != "application/pdf" {
		t.Errorf("package TypeByName => %q", got)
	}
	if got := TypeByContent(pdfData); got != "application/pdf" {
		t.Errorf("package TypeByContent => %q", got)
	}
	if got := TypeByNameAndContent("a.txt", txtData); got != "text/plain" {
		t.Errorf("package TypeByNameAndContent => %q", got)
	}
	if got := Unalias("application/x-gzip"); got != "application/gzip" {
		t.Errorf("package Unalias => %q", got)
	}
	if !IsSubclassOf("image/svg+xml", "text/plain") {
		t.Error("package IsSubclassOf")
	}
	if Default() == nil {
		t.Error("Default returned nil")
	}
}

func TestLoadError(t *testing.T) {
	// Point XDG at a directory whose mime/globs2 is malformed and confirm Load
	// propagates the error; restore the fixture afterwards.
	prev := os.Getenv("XDG_DATA_HOME")
	defer func() {
		os.Setenv("XDG_DATA_HOME", prev)
		xdg.Reload()
	}()

	broken := t.TempDir()
	if err := os.MkdirAll(filepath.Join(broken, "mime"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(broken, "mime", "globs2"), []byte("bad:line\n"), 0o644)
	os.Setenv("XDG_DATA_HOME", broken)
	xdg.Reload()

	if _, err := Load(); err == nil {
		t.Error("Load should propagate a malformed-database error")
	}

	// loadDefault degrades a failed load to an empty (non-nil) database.
	if db := loadDefault(); db == nil {
		t.Error("loadDefault should never return nil")
	} else if db.TypeByName("x.txt") != "" {
		t.Error("loadDefault on a broken database should be empty")
	}
}
