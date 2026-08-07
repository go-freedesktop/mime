// Copyright (c) the go-freedesktop/mime authors
//
// SPDX-License-Identifier: BSD-3-Clause

package mime_test

import (
	"fmt"
	"strings"

	"github.com/go-freedesktop/mime"
)

// ExampleDatabase shows a file manager resolving a file's type from its name,
// its content, and both together, using a small database built from a Shared
// MIME-info XML package.
func ExampleDatabase() {
	db := mime.New()
	_ = db.AddXML(strings.NewReader(`
      <mime-info xmlns="http://www.freedesktop.org/standards/shared-mime-info">
        <mime-type type="text/plain"><glob pattern="*.txt"/></mime-type>
        <mime-type type="application/pdf">
          <glob pattern="*.pdf"/>
          <magic priority="50"><match type="string" value="%PDF-" offset="0"/></magic>
        </mime-type>
      </mime-info>`))

	fmt.Println(db.TypeByName("notes.txt"))
	fmt.Println(db.TypeByContent([]byte("%PDF-1.7")))
	// A file misnamed .txt but whose content sniffs as PDF: here the weak
	// (priority < 80) magic does not override the file name.
	fmt.Println(db.TypeByNameAndContent("invoice.txt", []byte("%PDF-1.7")))
	// Output:
	// text/plain
	// application/pdf
	// text/plain
}
