// Copyright (c) the go-freedesktop/mime authors
//
// SPDX-License-Identifier: BSD-3-Clause

package mime

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// XML schema of a Shared MIME-info source package (packages/*.xml).
type xmlMimeInfo struct {
	Types []xmlMimeType `xml:"mime-type"`
}

type xmlMimeType struct {
	Type       string        `xml:"type,attr"`
	Globs      []xmlGlob     `xml:"glob"`
	Magics     []xmlMagic    `xml:"magic"`
	Aliases    []xmlAlias    `xml:"alias"`
	SubClassOf []xmlSubclass `xml:"sub-class-of"`
}

type xmlGlob struct {
	Pattern       string `xml:"pattern,attr"`
	Weight        string `xml:"weight,attr"`
	CaseSensitive string `xml:"case-sensitive,attr"`
}

type xmlMagic struct {
	Priority string     `xml:"priority,attr"`
	Match    []xmlMatch `xml:"match"`
}

type xmlMatch struct {
	Type   string     `xml:"type,attr"`
	Value  string     `xml:"value,attr"`
	Offset string     `xml:"offset,attr"`
	Mask   string     `xml:"mask,attr"`
	Match  []xmlMatch `xml:"match"`
}

type xmlAlias struct {
	Type string `xml:"type,attr"`
}

type xmlSubclass struct {
	Type string `xml:"type,attr"`
}

// AddPackagesDir parses every *.xml file in dir (a Shared MIME-info packages/
// directory) into the database, in sorted file-name order. A missing directory
// is not an error; a malformed file is.
func (db *Database) AddPackagesDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".xml") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		if err := db.AddXML(strings.NewReader(string(b))); err != nil {
			return fmt.Errorf("%s: %w", e.Name(), err)
		}
	}
	return nil
}

// AddXML parses one Shared MIME-info XML document (a <mime-info> element) from
// r and merges its globs, magic rules, aliases and subclass relations.
func (db *Database) AddXML(r io.Reader) error {
	var info xmlMimeInfo
	if err := xml.NewDecoder(r).Decode(&info); err != nil {
		return fmt.Errorf("mime: parsing packages XML: %w", err)
	}
	for _, mt := range info.Types {
		if mt.Type == "" {
			return fmt.Errorf("mime: <mime-type> without a type attribute")
		}
		for _, g := range mt.Globs {
			weight := defaultGlobWeight
			if g.Weight != "" {
				w, err := strconv.Atoi(g.Weight)
				if err != nil {
					return fmt.Errorf("mime: %s glob weight %q: %w", mt.Type, g.Weight, err)
				}
				weight = w
			}
			db.addGlob(g.Pattern, mt.Type, weight, g.CaseSensitive == "true")
		}
		for _, a := range mt.Aliases {
			db.addAlias(a.Type, mt.Type)
		}
		for _, s := range mt.SubClassOf {
			db.addSubclass(mt.Type, s.Type)
		}
		for _, mg := range mt.Magics {
			rule, err := xmlToMagicRule(mt.Type, mg)
			if err != nil {
				return err
			}
			db.magics = append(db.magics, rule)
		}
	}
	db.sortMagics()
	return nil
}

// xmlToMagicRule converts an <magic> element into a magicRule.
func xmlToMagicRule(typ string, mg xmlMagic) (magicRule, error) {
	prio := 50
	if mg.Priority != "" {
		p, err := strconv.Atoi(mg.Priority)
		if err != nil {
			return magicRule{}, fmt.Errorf("mime: %s magic priority %q: %w", typ, mg.Priority, err)
		}
		prio = p
	}
	rule := magicRule{typ: typ, priority: prio}
	for _, m := range mg.Match {
		node, err := xmlToMatch(typ, m)
		if err != nil {
			return magicRule{}, err
		}
		rule.matches = append(rule.matches, node)
	}
	if len(rule.matches) == 0 {
		return magicRule{}, fmt.Errorf("mime: %s <magic> has no <match>", typ)
	}
	return rule, nil
}

// xmlToMatch converts a <match> element (and its children) into a magicMatch.
func xmlToMatch(typ string, m xmlMatch) (*magicMatch, error) {
	value, err := decodeMagicValue(m.Type, m.Value)
	if err != nil {
		return nil, fmt.Errorf("mime: %s match value %q (%s): %w", typ, m.Value, m.Type, err)
	}
	start, length, err := parseOffset(m.Offset)
	if err != nil {
		return nil, fmt.Errorf("mime: %s match offset %q: %w", typ, m.Offset, err)
	}
	node := &magicMatch{rangeStart: start, rangeLen: length, value: value}
	if m.Mask != "" {
		mask, err := decodeMask(m.Mask, len(value))
		if err != nil {
			return nil, fmt.Errorf("mime: %s match mask %q: %w", typ, m.Mask, err)
		}
		node.mask = mask
	}
	for _, c := range m.Match {
		child, err := xmlToMatch(typ, c)
		if err != nil {
			return nil, err
		}
		node.children = append(node.children, child)
	}
	return node, nil
}

// parseOffset parses "N" or "N:M"; length is M-N+1 (min 1).
func parseOffset(s string) (start, length int, err error) {
	if s == "" {
		return 0, 1, nil
	}
	if i := strings.IndexByte(s, ':'); i >= 0 {
		start, err = strconv.Atoi(s[:i])
		if err != nil {
			return 0, 0, err
		}
		end, err := strconv.Atoi(s[i+1:])
		if err != nil {
			return 0, 0, err
		}
		if end < start {
			return 0, 0, fmt.Errorf("range end %d before start %d", end, start)
		}
		return start, end - start + 1, nil
	}
	start, err = strconv.Atoi(s)
	if err != nil {
		return 0, 0, err
	}
	return start, 1, nil
}

// decodeMagicValue turns an XML match value into the bytes to compare, per its
// declared type. String values honour C-style escapes; numeric types are
// encoded in the requested byte order. "host16"/"host32" are treated as
// little-endian, matching the primary (amd64/arm64) targets.
func decodeMagicValue(typ, raw string) ([]byte, error) {
	switch typ {
	case "", "string":
		return decodeStringValue(raw)
	case "byte":
		v, err := parseUint(raw, 8)
		if err != nil {
			return nil, err
		}
		return []byte{byte(v)}, nil
	case "big16", "little16", "host16":
		v, err := parseUint(raw, 16)
		if err != nil {
			return nil, err
		}
		if typ == "big16" {
			return []byte{byte(v >> 8), byte(v)}, nil
		}
		return []byte{byte(v), byte(v >> 8)}, nil
	case "big32", "little32", "host32":
		v, err := parseUint(raw, 32)
		if err != nil {
			return nil, err
		}
		if typ == "big32" {
			return []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}, nil
		}
		return []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)}, nil
	default:
		return nil, fmt.Errorf("unknown match type %q", typ)
	}
}

// parseUint parses a decimal or 0x-hex unsigned integer of at most bits width.
func parseUint(s string, bits int) (uint64, error) {
	base := 10
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		base, s = 16, s[2:]
	}
	return strconv.ParseUint(s, base, bits)
}

// decodeStringValue decodes a shared-mime-info string value, expanding the
// escapes update-mime-database accepts: \\, \n, \r, \t, \f, \v, \b, \xHH and
// octal \NNN.
func decodeStringValue(s string) ([]byte, error) {
	var out []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' {
			out = append(out, c)
			continue
		}
		i++
		if i >= len(s) {
			return nil, fmt.Errorf("trailing backslash")
		}
		switch e := s[i]; e {
		case '\\':
			out = append(out, '\\')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'f':
			out = append(out, '\f')
		case 'v':
			out = append(out, '\v')
		case 'b':
			out = append(out, '\b')
		case 'x':
			if i+2 >= len(s) {
				return nil, fmt.Errorf("truncated \\x escape")
			}
			v, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
			if err != nil {
				return nil, fmt.Errorf("bad \\x escape: %w", err)
			}
			out = append(out, byte(v))
			i += 2
		case '0', '1', '2', '3', '4', '5', '6', '7':
			// Up to three octal digits.
			j := i
			for j < len(s) && j < i+3 && s[j] >= '0' && s[j] <= '7' {
				j++
			}
			v, err := strconv.ParseUint(s[i:j], 8, 8)
			if err != nil {
				return nil, fmt.Errorf("bad octal escape: %w", err)
			}
			out = append(out, byte(v))
			i = j - 1
		default:
			return nil, fmt.Errorf("unknown escape \\%c", e)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty value")
	}
	return out, nil
}

// decodeMask decodes a "0x…" hex mask into want bytes.
func decodeMask(s string, want int) ([]byte, error) {
	if !strings.HasPrefix(s, "0x") && !strings.HasPrefix(s, "0X") {
		return nil, fmt.Errorf("mask must be 0x-prefixed hex")
	}
	hexDigits := s[2:]
	if len(hexDigits) != want*2 {
		return nil, fmt.Errorf("mask has %d hex digits, value needs %d", len(hexDigits), want*2)
	}
	out := make([]byte, want)
	for i := 0; i < want; i++ {
		v, err := strconv.ParseUint(hexDigits[i*2:i*2+2], 16, 8)
		if err != nil {
			return nil, err
		}
		out[i] = byte(v)
	}
	return out, nil
}
