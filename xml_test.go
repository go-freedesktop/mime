// Copyright (c) the go-freedesktop/mime authors
//
// SPDX-License-Identifier: BSD-3-Clause

package mime

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pkgDB loads the packages-only fixture (no generated files), exercising the
// XML fallback path in loadDir.
func pkgDB(t *testing.T) *Database {
	t.Helper()
	dir, err := filepath.Abs("testdata/pkgonly/mime")
	if err != nil {
		t.Fatal(err)
	}
	db, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir(pkgonly): %v", err)
	}
	return db
}

func TestXMLGlobsAndRelations(t *testing.T) {
	db := pkgDB(t)

	if got := db.TypeByName("doc.pdf"); got != "application/pdf" {
		t.Errorf("pdf glob => %q", got)
	}
	if got := db.TypeByName("Makefile"); got != "text/plain" {
		t.Errorf("Makefile glob (weight 60) => %q", got)
	}
	// alias resolution
	if got := db.Unalias("application/x-gzip"); got != "application/gzip" {
		t.Errorf("Unalias => %q", got)
	}
	if aliases := db.Aliases("image/png"); len(aliases) != 1 || aliases[0] != "image/x-png" {
		t.Errorf("Aliases(image/png) => %v", aliases)
	}
	// subclass chain: svg -> xml -> text/plain
	if !db.IsSubclassOf("image/svg+xml", "text/plain") {
		t.Error("svg should be a subclass of text/plain")
	}
	if got := db.Parents("image/svg+xml"); len(got) != 1 || got[0] != "application/xml" {
		t.Errorf("Parents(svg) => %v", got)
	}
}

func TestXMLMagic(t *testing.T) {
	db := pkgDB(t)
	cases := []struct {
		data []byte
		want string
	}{
		{[]byte("%PDF-1.4"), "application/pdf"},
		{[]byte("\x89PNG\r\n\x1a\n"), "image/png"},
		{[]byte{0x1f, 0x8b, 0x00}, "application/gzip"}, // nested byte match
		{[]byte{0xca, 0xfe, 0, 0}, "application/x-numbers"},
		{[]byte{0xfe, 0xca, 0, 0}, "application/x-numbers"},
		{[]byte{0, 0, 0, 0, 0x04, 0x03, 0x02, 0x01}, "application/x-numbers"},
		{[]byte{0, 0, 0, 0, 0, 0, 0, 0, 7}, "application/x-numbers"},
		{[]byte{0xf3, 0x00}, "application/x-masked"},
		{[]byte{'\t', '\n', '\r', '\f', '\v', '\b', '\\', '4'}, "application/x-escapes"},
	}
	for _, c := range cases {
		if got, _ := db.magicLookup(c.data); got != c.want {
			t.Errorf("magicLookup(% x) = %q, want %q", c.data, got, c.want)
		}
	}
	// gzip nested child absent: 0x1f without 0x8b.
	if got, _ := db.magicLookup([]byte{0x1f, 0x00}); got == "application/gzip" {
		t.Error("gzip should require the 0x8b child")
	}
}

func TestAddXMLErrors(t *testing.T) {
	bad := []string{
		`<mime-info><mime-type></mime-type`,   // malformed XML
		`<mime-info><mime-type/></mime-info>`, // no type attr
		`<mime-info><mime-type type="a/b"><glob pattern="*.x" weight="z"/></mime-type></mime-info>`,                                                                            // bad weight
		`<mime-info><mime-type type="a/b"><magic priority="z"><match type="string" value="x" offset="0"/></magic></mime-type></mime-info>`,                                     // bad priority
		`<mime-info><mime-type type="a/b"><magic></magic></mime-type></mime-info>`,                                                                                             // magic with no match
		`<mime-info><mime-type type="a/b"><magic><match type="weird" value="1" offset="0"/></magic></mime-type></mime-info>`,                                                   // bad match type
		`<mime-info><mime-type type="a/b"><magic><match type="string" value="x" offset="9:2"/></magic></mime-type></mime-info>`,                                                // bad offset range
		`<mime-info><mime-type type="a/b"><magic><match type="string" value="x" mask="ff" offset="0"/></magic></mime-type></mime-info>`,                                        // bad mask
		`<mime-info><mime-type type="a/b"><magic><match type="string" value="x" offset="0"><match type="weird" value="1" offset="1"/></match></magic></mime-type></mime-info>`, // bad child
	}
	for i, doc := range bad {
		db := New()
		if err := db.AddXML(strings.NewReader(doc)); err == nil {
			t.Errorf("case %d: expected error for %q", i, doc)
		}
	}
}

func TestAddXMLPriorityDefault(t *testing.T) {
	db := New()
	doc := `<mime-info><mime-type type="a/b"><magic><match type="string" value="XY" offset="0"/></magic></mime-type></mime-info>`
	if err := db.AddXML(strings.NewReader(doc)); err != nil {
		t.Fatal(err)
	}
	if db.magics[0].priority != 50 {
		t.Errorf("default priority = %d, want 50", db.magics[0].priority)
	}
}

func TestAddPackagesDir(t *testing.T) {
	// Missing directory is not an error.
	db := New()
	if err := db.AddPackagesDir("/no/such/packages"); err != nil {
		t.Errorf("missing dir => %v", err)
	}
	// A path that is a regular file yields a read error.
	f := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := New().AddPackagesDir(f); err == nil {
		t.Error("expected error reading a file as a dir")
	}

	// Directory with a subdir, a non-xml file, a good xml, and a bad xml.
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub.xml"), 0o755); err != nil { // a directory named *.xml is skipped
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignore"), 0o644)
	os.WriteFile(filepath.Join(dir, "good.xml"),
		[]byte(`<mime-info><mime-type type="a/b"><glob pattern="*.ab"/></mime-type></mime-info>`), 0o644)
	db2 := New()
	if err := db2.AddPackagesDir(dir); err != nil {
		t.Fatalf("good dir => %v", err)
	}
	if db2.TypeByName("x.ab") != "a/b" {
		t.Error("good.xml not loaded")
	}

	// A dangling symlink named *.xml surfaces a ReadFile error regardless of uid.
	dir3 := t.TempDir()
	if err := os.Symlink(filepath.Join(dir3, "missing-target"), filepath.Join(dir3, "broken.xml")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if err := New().AddPackagesDir(dir3); err == nil {
		t.Error("expected error from dangling symlink")
	}

	// A malformed xml surfaces a wrapped AddXML error.
	dir4 := t.TempDir()
	os.WriteFile(filepath.Join(dir4, "bad.xml"), []byte("<mime-info><oops"), 0o644)
	if err := New().AddPackagesDir(dir4); err == nil {
		t.Error("expected error from malformed xml file")
	}
}

func TestDecodeStringValue(t *testing.T) {
	good := map[string][]byte{
		`plain`:      []byte("plain"),
		`a\tb`:       []byte("a\tb"),
		`\n\r\f\v\b`: {'\n', '\r', '\f', '\v', '\b'},
		`\\`:         {'\\'},
		`\x41\x42`:   []byte("AB"),
		`\101`:       []byte("A"), // octal 101 = 65 = 'A'
	}
	for in, want := range good {
		got, err := decodeStringValue(in)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("decodeStringValue(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	bad := []string{
		``,     // empty
		`\`,    // trailing backslash
		`\q`,   // unknown escape
		`\x1`,  // truncated hex
		`\xZZ`, // bad hex
		`\777`, // octal overflow (> 255)
	}
	for _, in := range bad {
		if _, err := decodeStringValue(in); err == nil {
			t.Errorf("decodeStringValue(%q) expected error", in)
		}
	}
}

func TestDecodeMagicValueNumeric(t *testing.T) {
	cases := []struct {
		typ, val string
		want     []byte
	}{
		{"byte", "0x41", []byte{0x41}},
		{"byte", "65", []byte{0x41}},
		{"big16", "0x1234", []byte{0x12, 0x34}},
		{"little16", "0x1234", []byte{0x34, 0x12}},
		{"host16", "0x1234", []byte{0x34, 0x12}},
		{"big32", "0x01020304", []byte{1, 2, 3, 4}},
		{"little32", "0x01020304", []byte{4, 3, 2, 1}},
		{"host32", "0x01020304", []byte{4, 3, 2, 1}},
	}
	for _, c := range cases {
		got, err := decodeMagicValue(c.typ, c.val)
		if err != nil || !bytes.Equal(got, c.want) {
			t.Errorf("decodeMagicValue(%s,%s) = % x, %v; want % x", c.typ, c.val, got, err, c.want)
		}
	}
	badVals := [][2]string{
		{"byte", "0xZZ"},
		{"big16", "nope"},
		{"big32", "nope"},
		{"weird", "1"},
	}
	for _, c := range badVals {
		if _, err := decodeMagicValue(c[0], c[1]); err == nil {
			t.Errorf("decodeMagicValue(%s,%s) expected error", c[0], c[1])
		}
	}
}

func TestParseOffset(t *testing.T) {
	cases := []struct {
		in            string
		start, length int
		wantErr       bool
	}{
		{"", 0, 1, false},
		{"5", 5, 1, false},
		{"0:64", 0, 65, false},
		{"5:2", 0, 0, true},
		{"a:1", 0, 0, true},
		{"1:b", 0, 0, true},
		{"x", 0, 0, true},
	}
	for _, c := range cases {
		s, l, err := parseOffset(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseOffset(%q) expected error", c.in)
			}
			continue
		}
		if err != nil || s != c.start || l != c.length {
			t.Errorf("parseOffset(%q) = %d,%d,%v", c.in, s, l, err)
		}
	}
}

func TestDecodeMask(t *testing.T) {
	if _, err := decodeMask("ff00", 2); err == nil {
		t.Error("mask without 0x should error")
	}
	if _, err := decodeMask("0xff", 2); err == nil {
		t.Error("wrong-length mask should error")
	}
	if _, err := decodeMask("0xZZZZ", 2); err == nil {
		t.Error("bad hex mask should error")
	}
	m, err := decodeMask("0xff00", 2)
	if err != nil || !bytes.Equal(m, []byte{0xff, 0x00}) {
		t.Errorf("decodeMask = % x, %v", m, err)
	}
}
