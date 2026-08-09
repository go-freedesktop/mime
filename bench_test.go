// Copyright (c) the go-freedesktop/mime authors
//
// SPDX-License-Identifier: BSD-3-Clause

package mime

import (
	"testing"
)

// benchDB loads the committed real freedesktop.org Shared MIME-info database
// (globs2 + binary magic + aliases + subclasses, shared-mime-info v2.4.0:
// 1232 globs, 553 magic rules, 323 aliases, 496 subclasses) so the lookup
// benchmarks run against a representative production-scale database rather than
// the tiny functional-test fixtures.
func benchDB(tb testing.TB) *Database {
	tb.Helper()
	db, err := LoadDir("testdata/benchdb")
	if err != nil {
		tb.Fatalf("LoadDir(benchdb): %v", err)
	}
	return db
}

// benchNames is a representative spread of file names a file manager classifies:
// common suffix hits (short and multi-part), literal matches, a full-glob hit,
// case variation, and misses.
var benchNames = []string{
	"photo.jpg",
	"document.pdf",
	"index.html",
	"archive.tar.gz",
	"source.go",
	"styles.css",
	"README.md",
	"Makefile",
	"data.CSV",
	"movie.mkv",
	"music.flac",
	"page.xhtml",
	"backup.tar.bz2",
	"noextension",
	"weird.unknownext",
	"script.py",
	"config.yaml",
	"image.PNG",
	"book.epub",
	"sheet.xlsx",
}

// benchContent pairs a label with a leading-byte sample covering the common
// magic families a launcher sniffs, plus a text sample and a no-match blob.
var benchContent = func() [][]byte {
	png := append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, make([]byte, 248)...)
	pdf := append([]byte("%PDF-1.7\n"), make([]byte, 247)...)
	gz := append([]byte{0x1f, 0x8b, 0x08}, make([]byte, 253)...)
	zip := append([]byte{'P', 'K', 0x03, 0x04}, make([]byte, 252)...)
	elf := append([]byte{0x7f, 'E', 'L', 'F'}, make([]byte, 252)...)
	ogg := append([]byte("OggS"), make([]byte, 252)...)
	text := []byte("#!/bin/sh\necho hello world\n" + string(make([]byte, 0)))
	blob := make([]byte, 256)
	for i := range blob {
		blob[i] = byte(i%7) + 1 // non-text, matches no magic
	}
	return [][]byte{png, pdf, gz, zip, elf, ogg, text, blob}
}()

func BenchmarkTypeByName(b *testing.B) {
	db := benchDB(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = db.TypeByName(benchNames[i%len(benchNames)])
	}
}

func BenchmarkTypesByName(b *testing.B) {
	db := benchDB(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = db.TypesByName(benchNames[i%len(benchNames)])
	}
}

func BenchmarkTypeByContent(b *testing.B) {
	db := benchDB(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = db.TypeByContent(benchContent[i%len(benchContent)])
	}
}

func BenchmarkTypeByNameAndContent(b *testing.B) {
	db := benchDB(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = db.TypeByNameAndContent(benchNames[i%len(benchNames)], benchContent[i%len(benchContent)])
	}
}
