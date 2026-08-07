// Copyright (c) the go-freedesktop/mime authors
//
// SPDX-License-Identifier: BSD-3-Clause

package mime

import (
	"reflect"
	"testing"
)

func TestClassifyGlob(t *testing.T) {
	cases := map[string]globKind{
		"Makefile": globLiteral,
		"*.txt":    globSuffix,
		"*.tar.gz": globSuffix,
		"*~":       globSuffix,
		"*.[ch]":   globFull,
		"core.*":   globFull,
		"log??":    globFull,
		"*abc*":    globFull,
	}
	for pat, want := range cases {
		if got := classifyGlob(pat); got != want {
			t.Errorf("classifyGlob(%q) = %v, want %v", pat, got, want)
		}
	}
}

func TestGlobPrecedence(t *testing.T) {
	db := fixtureDB(t)

	// Literal beats everything.
	if got := db.TypeByName("Makefile"); got != "text/x-makefile" {
		t.Errorf("Makefile => %q", got)
	}
	// Suffix match.
	if got := db.TypeByName("photo.png"); got != "image/png" {
		t.Errorf("photo.png => %q", got)
	}
	// Longest suffix wins: *.tar.gz (weight 70) over *.gz.
	if got := db.TypeByName("archive.tar.gz"); got != "application/x-compressed-tar" {
		t.Errorf("archive.tar.gz => %q", got)
	}
	// Plain *.gz.
	if got := db.TypeByName("blob.gz"); got != "application/gzip" {
		t.Errorf("blob.gz => %q", got)
	}
	// Path is reduced to its base name.
	if got := db.TypeByName("/home/u/Documents/report.pdf"); got != "application/pdf" {
		t.Errorf("path => %q", got)
	}
	// No match.
	if got := db.TypeByName("mystery.zzz"); got != "" {
		t.Errorf("mystery.zzz => %q, want empty", got)
	}
}

func TestGlobCaseSensitivity(t *testing.T) {
	db := fixtureDB(t)
	// *.svg is case-sensitive: lower-case matches, upper-case does not.
	if got := db.TypeByName("drawing.svg"); got != "image/svg+xml" {
		t.Errorf("drawing.svg => %q", got)
	}
	if got := db.TypeByName("drawing.SVG"); got == "image/svg+xml" {
		t.Errorf("drawing.SVG should not match a case-sensitive glob, got %q", got)
	}
	// *.txt is case-insensitive.
	if got := db.TypeByName("READ.TXT"); got != "text/plain" {
		t.Errorf("READ.TXT => %q", got)
	}
}

func TestGlobWeightAndSuffixTie(t *testing.T) {
	db := New()
	db.addGlob("*.jpg", "image/jpeg", 40, false)
	db.addGlob("*.jpg", "image/pjpeg", 60, false) // higher weight wins
	if got := db.TypeByName("a.jpg"); got != "image/pjpeg" {
		t.Errorf("weight tie => %q", got)
	}
	// TypesByName returns both, weight-ordered and deduplicated.
	db2 := New()
	db2.addGlob("*.doc", "application/msword", 50, false)
	db2.addGlob("*.doc", "application/x-word", 30, false)
	db2.addGlob("*.doc", "application/msword", 50, false) // duplicate type
	got := db2.TypesByName("f.doc")
	want := []string{"application/msword", "application/x-word"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("TypesByName = %v, want %v", got, want)
	}
}

func TestGlobLiteralCaseSensitive(t *testing.T) {
	db := New()
	db.addGlob("Makefile", "text/x-makefile", 50, true) // case-sensitive literal
	if got := db.TypeByName("Makefile"); got != "text/x-makefile" {
		t.Errorf("cs literal exact => %q", got)
	}
	if got := db.TypeByName("makefile"); got != "" {
		t.Errorf("cs literal mismatched case => %q, want empty", got)
	}
}

func TestGlobSuffixCaseSensitive(t *testing.T) {
	db := New()
	db.addGlob("*.C", "text/x-c++src", 50, true)
	if got := db.TypeByName("a.C"); got != "text/x-c++src" {
		t.Errorf("cs suffix => %q", got)
	}
	if got := db.TypeByName("a.c"); got != "" {
		t.Errorf("cs suffix mismatch => %q", got)
	}
}

func TestGlobFullPattern(t *testing.T) {
	db := New()
	db.addGlob("*.[ch]", "text/x-csrc", 50, false)
	db.addGlob("core.*", "application/x-core", 50, false)
	if got := db.TypeByName("main.c"); got != "text/x-csrc" {
		t.Errorf("full [ch] => %q", got)
	}
	if got := db.TypeByName("core.1234"); got != "application/x-core" {
		t.Errorf("full core.* => %q", got)
	}
	if got := db.TypeByName("main.o"); got != "" {
		t.Errorf("full no-match => %q", got)
	}
}

func TestFnmatch(t *testing.T) {
	cases := []struct {
		pat, s string
		want   bool
	}{
		{"", "", true},
		{"", "x", false},
		{"*", "anything", true},
		{"*", "", true},
		{"a*", "abc", true},
		{"a*c", "abc", true},
		{"a*c", "abx", false},
		{"a?c", "abc", true},
		{"a?c", "ac", false},
		{"a?c", "abbc", false},
		{"*.txt", "note.txt", true},
		{"*.tar.*", "x.tar.gz", true},
		{"[abc]d", "bd", true},
		{"[abc]d", "zd", false},
		{"[a-c]d", "bd", true},
		{"[a-c]d", "zd", false},
		{"[!a-c]d", "zd", true},
		{"[!a-c]d", "bd", false},
		{"[^x]y", "ay", true},
		{"[]]x", "]x", true},   // ']' as first class member
		{"[abc", "[abc", true}, // malformed class: literal '['
		{"[abc", "x", false},
		{"foo*bar*baz", "fooXbarYbaz", true},
		{"foo*bar", "fooXbarY", false},
		{"a*", "a", true},
		{"*a", "ba", true},
		{"*a", "ab", false},
	}
	for _, c := range cases {
		if got := fnmatch(c.pat, c.s); got != c.want {
			t.Errorf("fnmatch(%q,%q)=%v want %v", c.pat, c.s, got, c.want)
		}
	}
}

func TestParseGlobs2Errors(t *testing.T) {
	db := New()
	if err := db.parseGlobs2("50:text/plain"); err == nil {
		t.Error("expected error for too-few fields")
	}
	db2 := New()
	if err := db2.parseGlobs2("notaweight:text/plain:*.txt"); err == nil {
		t.Error("expected error for bad weight")
	}
	// Comments and blank lines are skipped; cs flag parsed.
	db3 := New()
	if err := db3.parseGlobs2("# c\n\n50:image/svg+xml:*.svg:cs\n"); err != nil {
		t.Fatalf("parseGlobs2: %v", err)
	}
	if got := db3.TypeByName("a.SVG"); got != "" {
		t.Errorf("cs flag not honoured: %q", got)
	}
}

func TestParseGlobs1(t *testing.T) {
	db := New()
	if err := db.parseGlobs1("# comment\n\ntext/plain:*.txt\n"); err != nil {
		t.Fatalf("parseGlobs1: %v", err)
	}
	if got := db.TypeByName("a.txt"); got != "text/plain" {
		t.Errorf("globs1 => %q", got)
	}
	db2 := New()
	if err := db2.parseGlobs1("bad-line-no-colon"); err == nil {
		t.Error("expected error for missing colon")
	}
}
