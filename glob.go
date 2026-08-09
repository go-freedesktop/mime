// Copyright (c) the go-freedesktop/mime authors
//
// SPDX-License-Identifier: BSD-3-Clause

package mime

import (
	"fmt"
	"strconv"
	"strings"
)

// globKind classifies a glob pattern for the spec's matching precedence.
type globKind int

const (
	globLiteral globKind = iota // no wildcards: an exact file name (e.g. "Makefile")
	globSuffix                  // "*" followed by a wildcard-free tail (e.g. "*.tar.gz")
	globFull                    // any other pattern, matched with fnmatch
)

// glob is one registered pattern -> type association.
type glob struct {
	pattern    string
	patternLow string // lower-cased pattern, precomputed for case-insensitive fnmatch
	typ        string
	weight     int
	cs         bool // case-sensitive match
	kind       globKind
	suffix     string // the tail after '*' for globSuffix (original case)
	suffixLow  string // lower-cased suffix, precomputed
	seq        int    // registration order, for stable weight ties
}

// suffixNode is one node of the reverse trie built over "*.ext" suffix tails.
// A name matches a stored suffix when, reading the name from its last byte
// backwards, every byte follows an edge until the stored suffix is exhausted;
// the deepest such node with a matching glob is the longest matching suffix.
type suffixNode struct {
	children map[byte]*suffixNode
	globs    []glob // globs whose (lower-cased) suffix ends exactly here
}

// defaultGlobWeight is the weight assigned when a pattern declares none.
const defaultGlobWeight = 50

// addGlob classifies and registers a pattern into the appropriate lookup index.
func (db *Database) addGlob(pattern, typ string, weight int, cs bool) {
	g := glob{
		pattern:    pattern,
		patternLow: strings.ToLower(pattern),
		typ:        typ,
		weight:     weight,
		cs:         cs,
		kind:       classifyGlob(pattern),
		seq:        db.globSeq,
	}
	db.globSeq++

	switch g.kind {
	case globLiteral:
		if cs {
			if db.litCS == nil {
				db.litCS = map[string][]glob{}
			}
			db.litCS[pattern] = append(db.litCS[pattern], g)
		} else {
			if db.litCI == nil {
				db.litCI = map[string][]glob{}
			}
			low := g.patternLow
			db.litCI[low] = append(db.litCI[low], g)
		}
	case globSuffix:
		g.suffix = pattern[1:]
		g.suffixLow = strings.ToLower(g.suffix)
		if db.suffixRoot == nil {
			db.suffixRoot = &suffixNode{}
		}
		node := db.suffixRoot
		s := g.suffixLow
		for i := len(s) - 1; i >= 0; i-- {
			if node.children == nil {
				node.children = map[byte]*suffixNode{}
			}
			next := node.children[s[i]]
			if next == nil {
				next = &suffixNode{}
				node.children[s[i]] = next
			}
			node = next
		}
		node.globs = append(node.globs, g)
	default: // globFull
		db.fullGlobs = append(db.fullGlobs, g)
	}
}

// classifyGlob determines which precedence tier a pattern falls into.
func classifyGlob(pattern string) globKind {
	if !strings.ContainsAny(pattern, "*?[") {
		return globLiteral
	}
	if strings.HasPrefix(pattern, "*") && !strings.ContainsAny(pattern[1:], "*?[") {
		return globSuffix
	}
	return globFull
}

// globMatches applies the spec precedence rules to a base file name and returns
// the winning tier's types ordered by descending weight (deduplicated). It
// consults the precomputed indexes in precedence order: literal names, then the
// longest matching "*.ext" suffix, then arbitrary fnmatch patterns.
func (db *Database) globMatches(name string) []string {
	lower := strings.ToLower(name)

	// Literal tier: an exact name (case-sensitive) or its lower-cased form
	// (case-insensitive). Both index buckets belong to the same tier.
	if literals := db.literalMatches(name, lower); len(literals) > 0 {
		return topByWeight(literals)
	}

	// Suffix tier: walk the reverse trie from the end of the name and use the
	// deepest node that holds at least one glob actually matching this name
	// (a case-sensitive glob must also match in its original case). Because a
	// string has exactly one suffix of any given length, that deepest node is
	// the unique longest-matching-suffix tier.
	if suffixes := db.suffixMatches(name, lower); len(suffixes) > 0 {
		return topByWeight(suffixes)
	}

	// Full-glob tier: only arbitrary fnmatch patterns remain here.
	var fulls []glob
	for _, g := range db.fullGlobs {
		target, pat := name, g.pattern
		if !g.cs {
			target, pat = lower, g.patternLow
		}
		if fnmatch(pat, target) {
			fulls = append(fulls, g)
		}
	}
	if len(fulls) > 0 {
		return topByWeight(fulls)
	}
	return nil
}

// literalMatches gathers the literal-tier globs matching name from both the
// case-sensitive and case-insensitive buckets.
func (db *Database) literalMatches(name, lower string) []glob {
	var out []glob
	if gs, ok := db.litCS[name]; ok {
		out = append(out, gs...)
	}
	if gs, ok := db.litCI[lower]; ok {
		out = append(out, gs...)
	}
	return out
}

// suffixMatches returns the globs of the longest "*.ext" suffix that matches
// name, or nil. It records every trie node reached while reading name backwards
// and then, from the deepest reached node outward, returns the first node whose
// globs actually match (honouring case sensitivity).
func (db *Database) suffixMatches(name, lower string) []glob {
	node := db.suffixRoot
	if node == nil {
		return nil
	}
	// Collect terminal-bearing nodes along the path, deepest last.
	var path []*suffixNode
	for i := len(lower) - 1; i >= 0; i-- {
		next := node.children[lower[i]]
		if next == nil {
			break
		}
		node = next
		if len(node.globs) > 0 {
			path = append(path, node)
		}
	}
	for i := len(path) - 1; i >= 0; i-- {
		matched := suffixNodeMatches(path[i].globs, name)
		if len(matched) > 0 {
			return matched
		}
	}
	return nil
}

// suffixNodeMatches filters a node's globs to those that match name. The lower
// path already guarantees the suffix matches case-insensitively; a
// case-sensitive glob must additionally match in the name's original case.
func suffixNodeMatches(gs []glob, name string) []glob {
	var out []glob
	for _, g := range gs {
		if g.cs && !strings.HasSuffix(name, g.suffix) {
			continue
		}
		out = append(out, g)
	}
	return out
}

// topByWeight returns the distinct types of gs ordered by descending weight,
// breaking ties by ascending registration order (seq) so the result is
// independent of the order the index buckets yielded the globs in. It sorts gs
// in place; callers pass freshly built, owned slices. The only allocation is
// the returned slice, and dedup is a linear scan because match sets are tiny.
func topByWeight(gs []glob) []string {
	// Insertion sort by (weight desc, seq asc); lists are tiny.
	for i := 1; i < len(gs); i++ {
		for j := i; j > 0 && less(gs[j], gs[j-1]); j-- {
			gs[j-1], gs[j] = gs[j], gs[j-1]
		}
	}
	out := make([]string, 0, len(gs))
	for _, g := range gs {
		if !containsString(out, g.typ) {
			out = append(out, g.typ)
		}
	}
	return out
}

// containsString reports whether s is already present in out.
func containsString(out []string, s string) bool {
	for _, v := range out {
		if v == s {
			return true
		}
	}
	return false
}

// less orders globs by descending weight, then ascending registration order.
func less(a, b glob) bool {
	if a.weight != b.weight {
		return a.weight > b.weight
	}
	return a.seq < b.seq
}

// fnmatch reports whether pattern matches s using shell glob semantics:
// '*' (any run, including empty), '?' (any single byte) and '[...]' /
// '[!...]' character classes. There is no special treatment of '/' because
// only base names are matched.
func fnmatch(pattern, s string) bool {
	// Iterative match with backtracking for '*'.
	var star int = -1
	var starS int
	p, i := 0, 0
	for i < len(s) {
		if p < len(pattern) {
			switch pattern[p] {
			case '?':
				p++
				i++
				continue
			case '[':
				if ok, np := matchClass(pattern, p, s[i]); ok {
					p = np
					i++
					continue
				}
			case '*':
				star = p
				starS = i
				p++
				continue
			default:
				if pattern[p] == s[i] {
					p++
					i++
					continue
				}
			}
		}
		if star >= 0 {
			p = star + 1
			starS++
			i = starS
			continue
		}
		return false
	}
	for p < len(pattern) && pattern[p] == '*' {
		p++
	}
	return p == len(pattern)
}

// matchClass matches a single character c against the '[...]' class beginning
// at pattern[p]. It returns whether c matched and the pattern index just past
// the closing ']'. A malformed class (no closing ']') is treated as a literal
// '[' so matching stays total.
func matchClass(pattern string, p int, c byte) (bool, int) {
	j := p + 1
	negate := false
	if j < len(pattern) && (pattern[j] == '!' || pattern[j] == '^') {
		negate = true
		j++
	}
	matched := false
	first := true
	for j < len(pattern) && (pattern[j] != ']' || first) {
		first = false
		// range a-b
		if j+2 < len(pattern) && pattern[j+1] == '-' && pattern[j+2] != ']' {
			if pattern[j] <= c && c <= pattern[j+2] {
				matched = true
			}
			j += 3
			continue
		}
		if pattern[j] == c {
			matched = true
		}
		j++
	}
	if j >= len(pattern) { // no closing ']': literal '['
		return c == '[', p + 1
	}
	return matched != negate, j + 1
}

// parseGlobs2 parses the generated globs2 file: "weight:type:pattern[:flags]"
// lines, '#' comments and blank lines. flags may contain "cs" (case-sensitive).
func (db *Database) parseGlobs2(text string) error {
	for n, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ":")
		if len(fields) < 3 {
			return fmt.Errorf("globs2 line %d: expected weight:type:glob, got %q", n+1, line)
		}
		weight, err := strconv.Atoi(fields[0])
		if err != nil {
			return fmt.Errorf("globs2 line %d: bad weight %q: %w", n+1, fields[0], err)
		}
		cs := false
		for _, f := range fields[3:] {
			if f == "cs" {
				cs = true
			}
		}
		db.addGlob(fields[2], fields[1], weight, cs)
	}
	return nil
}

// parseGlobs1 parses the legacy globs file: "type:pattern" lines.
func (db *Database) parseGlobs1(text string) error {
	for n, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.IndexByte(line, ':')
		if i < 0 {
			return fmt.Errorf("globs line %d: expected type:glob, got %q", n+1, line)
		}
		db.addGlob(line[i+1:], line[:i], defaultGlobWeight, false)
	}
	return nil
}
