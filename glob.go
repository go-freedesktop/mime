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
	pattern string
	typ     string
	weight  int
	cs      bool // case-sensitive match
	kind    globKind
	suffix  string // the tail after '*' for globSuffix
}

// defaultGlobWeight is the weight assigned when a pattern declares none.
const defaultGlobWeight = 50

// addGlob classifies and registers a pattern.
func (db *Database) addGlob(pattern, typ string, weight int, cs bool) {
	g := glob{pattern: pattern, typ: typ, weight: weight, cs: cs, kind: classifyGlob(pattern)}
	if g.kind == globSuffix {
		g.suffix = pattern[1:]
	}
	db.globs = append(db.globs, g)
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
// the winning tier's types ordered by descending weight (deduplicated).
func (db *Database) globMatches(name string) []string {
	lower := strings.ToLower(name)

	var literals, suffixes, fulls []glob
	var bestSuffixLen int

	for _, g := range db.globs {
		switch g.kind {
		case globLiteral:
			if g.cs {
				if name == g.pattern {
					literals = append(literals, g)
				}
			} else if lower == strings.ToLower(g.pattern) {
				literals = append(literals, g)
			}
		case globSuffix:
			suf := g.suffix
			hit := false
			if g.cs {
				hit = strings.HasSuffix(name, suf)
			} else {
				hit = strings.HasSuffix(lower, strings.ToLower(suf))
			}
			if hit {
				if len(suf) > bestSuffixLen {
					bestSuffixLen = len(suf)
				}
				suffixes = append(suffixes, g)
			}
		default: // globFull
			target := name
			pat := g.pattern
			if !g.cs {
				target, pat = lower, strings.ToLower(g.pattern)
			}
			if fnmatch(pat, target) {
				fulls = append(fulls, g)
			}
		}
	}

	if len(literals) > 0 {
		return topByWeight(literals)
	}
	if len(suffixes) > 0 {
		// Keep only the longest matching suffix, then rank by weight.
		longest := suffixes[:0]
		for _, g := range suffixes {
			if len(g.suffix) == bestSuffixLen {
				longest = append(longest, g)
			}
		}
		return topByWeight(longest)
	}
	if len(fulls) > 0 {
		return topByWeight(fulls)
	}
	return nil
}

// topByWeight returns the distinct types of gs ordered by descending weight,
// preserving registration order among equal weights.
func topByWeight(gs []glob) []string {
	// Stable insertion sort by weight (small lists; keeps first-seen order on ties).
	sorted := make([]glob, len(gs))
	copy(sorted, gs)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j-1].weight < sorted[j].weight; j-- {
			sorted[j-1], sorted[j] = sorted[j], sorted[j-1]
		}
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(sorted))
	for _, g := range sorted {
		if !seen[g.typ] {
			seen[g.typ] = true
			out = append(out, g.typ)
		}
	}
	return out
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
