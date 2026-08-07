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

// xdgMimeDir is the fixture mime directory populated with generated files.
var xdgMimeDir string

// TestMain points XDG at the committed testdata fixture so Load and the
// package-level Default database resolve deterministically, regardless of any
// real /usr/share/mime on the CI host.
func TestMain(m *testing.M) {
	home, err := filepath.Abs("testdata/xdgdata")
	if err != nil {
		panic(err)
	}
	xdgMimeDir = filepath.Join(home, "mime")

	// A non-empty, non-existent data-dirs entry: adrg/xdg keeps it verbatim
	// (an empty value would fall back to the OS defaults and read the real
	// system database), and our loader simply skips the missing directory.
	missing, _ := filepath.Abs("testdata/does-not-exist")

	os.Setenv("XDG_DATA_HOME", home)
	os.Setenv("XDG_DATA_DIRS", missing)
	xdg.Reload()

	os.Exit(m.Run())
}

// fixtureDB loads the generated-file fixture directory.
func fixtureDB(t *testing.T) *Database {
	t.Helper()
	db, err := LoadDir(xdgMimeDir)
	if err != nil {
		t.Fatalf("LoadDir(%s): %v", xdgMimeDir, err)
	}
	return db
}
