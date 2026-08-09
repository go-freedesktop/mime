// Copyright (c) the go-freedesktop/mime authors
//
// SPDX-License-Identifier: BSD-3-Clause

package mime

import (
	"reflect"
	"strings"
	"testing"
)

// allGlobs reconstructs the flat list of every registered glob from the
// precomputed indexes, in registration (seq) order, so a reference
// implementation of the original linear algorithm can be run against it.
func (db *Database) allGlobs() []glob {
	var gs []glob
	for _, bucket := range db.litCS {
		gs = append(gs, bucket...)
	}
	for _, bucket := range db.litCI {
		gs = append(gs, bucket...)
	}
	var walk func(*suffixNode)
	walk = func(n *suffixNode) {
		if n == nil {
			return
		}
		gs = append(gs, n.globs...)
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(db.suffixRoot)
	gs = append(gs, db.fullGlobs...)
	// Restore registration order.
	for i := 1; i < len(gs); i++ {
		for j := i; j > 0 && gs[j].seq < gs[j-1].seq; j-- {
			gs[j-1], gs[j] = gs[j], gs[j-1]
		}
	}
	return gs
}

// refGlobMatches is the original pre-optimization linear-scan algorithm, kept
// here as an executable oracle for the indexed implementation.
func refGlobMatches(gs []glob, name string) []string {
	lower := strings.ToLower(name)
	var literals, suffixes, fulls []glob
	bestSuffixLen := 0
	for _, g := range gs {
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
		default:
			target, pat := name, g.pattern
			if !g.cs {
				target, pat = lower, strings.ToLower(g.pattern)
			}
			if fnmatch(pat, target) {
				fulls = append(fulls, g)
			}
		}
	}
	pick := func(list []glob) []string { return topByWeight(append([]glob(nil), list...)) }
	if len(literals) > 0 {
		return pick(literals)
	}
	if len(suffixes) > 0 {
		var longest []glob
		for _, g := range suffixes {
			if len(g.suffix) == bestSuffixLen {
				longest = append(longest, g)
			}
		}
		return pick(longest)
	}
	if len(fulls) > 0 {
		return pick(fulls)
	}
	return nil
}

// TestGlobMatchesEquivReal asserts the indexed lookup returns exactly what the
// original linear algorithm returns, across a large body of names derived from
// the real freedesktop.org database (every literal and suffix, in lower/upper
// case, plus misses).
func TestGlobMatchesEquivReal(t *testing.T) {
	db := benchDB(t)
	gs := db.allGlobs()

	seen := map[string]bool{}
	var names []string
	add := func(n string) {
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	for _, g := range gs {
		switch g.kind {
		case globLiteral:
			add(g.pattern)
			add(strings.ToUpper(g.pattern))
			add(strings.ToLower(g.pattern))
		case globSuffix:
			add("file" + g.suffix)
			add("File" + strings.ToUpper(g.suffix))
			add("x.y" + g.suffix)
			add("deep/path/to/file" + g.suffix)
		default:
			add(g.pattern)
		}
	}
	// A spread of misses and edge names.
	for _, n := range []string{"", "noext", "a.unknownzzz", "x.", ".hidden", "UPPER.ZZZ", "core.9999"} {
		add(n)
	}

	for _, n := range names {
		want := refGlobMatches(gs, base(n))
		got := db.TypesByName(n)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("TypesByName(%q) = %v, want %v", n, got, want)
		}
	}
	t.Logf("checked %d names against the linear oracle", len(names))
}
