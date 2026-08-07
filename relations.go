// Copyright (c) the go-freedesktop/mime authors
//
// SPDX-License-Identifier: BSD-3-Clause

package mime

import "strings"

// addAlias records that alias resolves to canonical.
func (db *Database) addAlias(alias, canonical string) {
	if alias == "" || canonical == "" {
		return
	}
	if _, ok := db.aliases[alias]; ok {
		return
	}
	db.aliases[alias] = canonical
	db.revAliases[canonical] = append(db.revAliases[canonical], alias)
}

// addSubclass records that sub is-a parent.
func (db *Database) addSubclass(sub, parent string) {
	if sub == "" || parent == "" {
		return
	}
	for _, p := range db.subclasses[sub] {
		if p == parent {
			return
		}
	}
	db.subclasses[sub] = append(db.subclasses[sub], parent)
}

// parseAliases parses the generated aliases file: "alias canonical" per line.
func (db *Database) parseAliases(text string) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if a, c, ok := splitTwo(line); ok {
			db.addAlias(a, c)
		}
	}
}

// parseSubclasses parses the generated subclasses file: "subtype parent".
func (db *Database) parseSubclasses(text string) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if s, p, ok := splitTwo(line); ok {
			db.addSubclass(s, p)
		}
	}
}

// splitTwo splits a line into its first whitespace-separated field and the
// remainder (also trimmed). It reports false when there is no second field.
func splitTwo(line string) (string, string, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return "", "", false
	}
	return fields[0], fields[1], true
}

// looksLikeText reports whether data appears to be text rather than binary,
// used for the text/plain versus application/octet-stream fallback. It follows
// the common heuristic: a NUL byte or a control character that is not ordinary
// whitespace marks the data as binary.
func looksLikeText(data []byte) bool {
	for _, b := range data {
		if b >= 0x20 { // printable ASCII and any high byte (UTF-8 continuation etc.)
			continue
		}
		switch b {
		case '\t', '\n', '\r', '\f', '\v', 0x1b: // tab, LF, CR, FF, VT, ESC
			continue
		default:
			return false
		}
	}
	return true
}
