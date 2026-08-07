// Copyright (c) the go-freedesktop/mime authors
//
// SPDX-License-Identifier: BSD-3-Clause

package mime

import (
	"bytes"
	"testing"
)

// hdr prepends the binary magic header to a body.
func hdr(body string) []byte {
	return append([]byte("MIME-Magic\x00\n"), []byte(body)...)
}

func TestMagicLookupFixture(t *testing.T) {
	db := fixtureDB(t)
	cases := []struct {
		data []byte
		want string
	}{
		{[]byte("%PDF-1.7\n"), "application/pdf"},
		{[]byte("\x89PNG\r\n\x1a\n....."), "image/png"},
		{[]byte{0x1f, 0x8b, 0x08}, "application/gzip"},
		{[]byte("<?xml version='1.0'?>"), "application/xml"},
		{append(bytes.Repeat([]byte(" "), 10), []byte("<svg/>")...), "image/svg+xml"}, // within range
		{[]byte("AB\x00\x00CD...."), "application/x-nested"},
		{[]byte{0xf3, 0x00, 0x99}, "application/x-masked"},
		{[]byte{0x34, 0x12, 0x00}, "application/x-word"}, // word-size byte-swapped
		{[]byte{0xab, 0xcd, 0xef}, ""},                   // matches nothing
	}
	for _, c := range cases {
		got, _ := db.magicLookup(c.data)
		if got != c.want {
			t.Errorf("magicLookup(% x) = %q, want %q", c.data, got, c.want)
		}
	}
}

func TestMagicNestedChildFails(t *testing.T) {
	db := fixtureDB(t)
	// Parent "AB" matches but child "CD" at offset 4 is absent.
	if got, _ := db.magicLookup([]byte("AB\x00\x00XX")); got == "application/x-nested" {
		t.Errorf("nested should not match without child, got %q", got)
	}
}

func TestMagicMaskedNoMatch(t *testing.T) {
	db := fixtureDB(t)
	if got, _ := db.magicLookup([]byte{0x03, 0x00}); got == "application/x-masked" {
		t.Errorf("masked matched wrong high nibble")
	}
}

func TestSelfMatchEdges(t *testing.T) {
	// Empty value never matches.
	m := &magicMatch{value: nil, rangeLen: 1}
	if m.selfMatch([]byte("abc")) {
		t.Error("empty value should not match")
	}
	// Negative start offset breaks out immediately.
	m2 := &magicMatch{rangeStart: -1, rangeLen: 1, value: []byte("A")}
	if m2.selfMatch([]byte("A")) {
		t.Error("negative offset should not match")
	}
	// Range that walks past the data length.
	m3 := &magicMatch{rangeStart: 0, rangeLen: 8, value: []byte("XY")}
	if m3.selfMatch([]byte("Z")) {
		t.Error("value longer than data should not match")
	}
}

func TestMatchNoChildren(t *testing.T) {
	m := &magicMatch{rangeStart: 0, rangeLen: 1, value: []byte("Hi")}
	if !m.match([]byte("Hi there")) {
		t.Error("leaf match failed")
	}
	if m.match([]byte("no")) {
		t.Error("leaf should not match")
	}
}

func TestEqualMaskedFalse(t *testing.T) {
	if equalMasked([]byte{0x01}, []byte{0x02}, []byte{0xff}) {
		t.Error("masked compare should be false")
	}
	if !equalMasked([]byte{0x12}, []byte{0x12}, nil) {
		t.Error("unmasked equal compare should be true")
	}
}

func TestByteSwap(t *testing.T) {
	b := []byte{1, 2, 3, 4}
	byteSwap(b, 2)
	if !bytes.Equal(b, []byte{2, 1, 4, 3}) {
		t.Errorf("byteSwap size 2 => % x", b)
	}
	// Size <= 1, nil, or non-divisible length are no-ops.
	c := []byte{9, 8, 7}
	byteSwap(c, 1)
	byteSwap(c, 2) // 3 not divisible by 2
	byteSwap(nil, 2)
	if !bytes.Equal(c, []byte{9, 8, 7}) {
		t.Errorf("byteSwap no-op changed data: % x", c)
	}
}

func TestAtoiStrict(t *testing.T) {
	if _, err := atoiStrict(""); err == nil {
		t.Error("empty should error")
	}
	if _, err := atoiStrict("12a"); err == nil {
		t.Error("non-digit should error")
	}
	if n, err := atoiStrict("00123"); err != nil || n != 123 {
		t.Errorf("atoiStrict(00123) = %d, %v", n, err)
	}
}

func TestIndexHelpers(t *testing.T) {
	if indexByteFrom([]byte("abc"), 0, 'z') != -1 {
		t.Error("indexByteFrom miss")
	}
	if indexByteStr("+\n", '\n') != 1 {
		t.Error("indexByteStr")
	}
}

func TestParseMagicErrors(t *testing.T) {
	bad := map[string][]byte{
		"short header":       []byte("MIME"),
		"wrong header":       []byte("XXXXXXXXXXXX"),
		"not a section":      hdr("foo"),
		"unterminated hdr":   hdr("[50:text/plain"),
		"hdr not newline":    hdr("[50:text/plain]X"),
		"hdr eof":            hdr("[50:text/plain]"),
		"missing colon":      hdr("[50text]\n"),
		"empty priority":     hdr("[:text]\n>0=\x00\x01A\n"),
		"bad priority":       hdr("[xx:text]\n>0=\x00\x01A\n"),
		"no rules":           hdr("[50:text/plain]\n"),
		"no gt after indent": hdr("[50:t]\n123X"),
		"no eq after offset": hdr("[50:t]\n>12X"),
		"trunc length":       hdr("[50:t]\n>0=\x00"),
		"zero length":        hdr("[50:t]\n>0=\x00\x00\n"),
		"trunc value":        hdr("[50:t]\n>0=\x00\x05AB"),
		"trunc mask":         hdr("[50:t]\n>0=\x00\x02AB&X"),
		"word no digits":     hdr("[50:t]\n>0=\x00\x02AB~\n"),
		"word unterminated":  hdr("[50:t]\n>0=\x00\x02AB~2"),
		"word bad term":      hdr("[50:t]\n>0=\x00\x02AB~2X"),
		"range no digits":    hdr("[50:t]\n>0=\x00\x02AB+\n"),
		"rule not newline":   hdr("[50:t]\n>0=\x00\x02ABX"),
		"rule eof":           hdr("[50:t]\n>0=\x00\x02AB"),
		"indent jump":        hdr("[50:t]\n2>0=\x00\x02AB\n"),
	}
	for name, b := range bad {
		db := New()
		if err := db.parseMagic(b); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestParseMagicGoodRangeAndMask(t *testing.T) {
	db := New()
	b := hdr("[50:x/y]\n>0=\x00\x02AB&\xff\x00~2+3\n")
	if err := db.parseMagic(b); err != nil {
		t.Fatalf("parseMagic: %v", err)
	}
	if len(db.magics) != 1 || db.magics[0].typ != "x/y" {
		t.Fatalf("unexpected rule set: %+v", db.magics)
	}
	m := db.magics[0].matches[0]
	if m.rangeLen != 4 { // +3 => 3+1
		t.Errorf("rangeLen = %d, want 4", m.rangeLen)
	}
	if m.mask == nil {
		t.Error("mask not parsed")
	}
}
