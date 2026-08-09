// Copyright (c) the go-freedesktop/mime authors
//
// SPDX-License-Identifier: BSD-3-Clause

package mime

import (
	"errors"
	"fmt"
	"sort"
)

// magicMatch is one node in a magic rule tree. A node matches when its value
// (optionally masked) is found somewhere in the offset range and, if it has
// children, at least one child also matches. Offsets are absolute file
// positions, as in the spec.
type magicMatch struct {
	rangeStart int
	rangeLen   int // number of successive start offsets to try (>= 1)
	value      []byte
	mask       []byte // nil, or the same length as value
	children   []*magicMatch
}

// magicRule associates a type with its top-level magic matches and a priority.
type magicRule struct {
	typ      string
	priority int
	matches  []*magicMatch
	// canStart is a first-byte reject filter: it holds every byte value that
	// data[0] could take for any top-level match of this rule to possibly
	// succeed. A rule whose filter excludes data[0] cannot match, so the
	// expensive tree walk is skipped. Rules with a top-level match that is not
	// anchored at offset 0 (an offset range or a non-zero start) accept every
	// byte, so the filter never rejects a rule that could genuinely match.
	canStart byteSet
}

// byteSet is a 256-bit set of byte values.
type byteSet [4]uint64

func (s *byteSet) set(b byte)      { s[b>>6] |= 1 << (b & 63) }
func (s *byteSet) has(b byte) bool { return s[b>>6]&(1<<(b&63)) != 0 }
func (s *byteSet) setAll() {
	s[0], s[1], s[2], s[3] = ^uint64(0), ^uint64(0), ^uint64(0), ^uint64(0)
}

// buildCanStart computes the rule's first-byte reject filter from its top-level
// matches. A match anchored at offset 0 with a single start position constrains
// data[0] to its (optionally masked) first value byte; any other match leaves
// data[0] unconstrained.
func (r *magicRule) buildCanStart() {
	for _, m := range r.matches {
		if m.rangeStart != 0 || m.rangeLen != 1 || len(m.value) == 0 {
			r.canStart.setAll()
			return
		}
		if m.mask == nil {
			r.canStart.set(m.value[0])
			continue
		}
		want := m.value[0] & m.mask[0]
		for b := 0; b < 256; b++ {
			if byte(b)&m.mask[0] == want {
				r.canStart.set(byte(b))
			}
		}
	}
}

// selfMatch reports whether the node's value matches within its offset range,
// ignoring children.
func (m *magicMatch) selfMatch(data []byte) bool {
	n := len(m.value)
	if n == 0 {
		return false
	}
	for off := m.rangeStart; off < m.rangeStart+m.rangeLen; off++ {
		if off < 0 || off+n > len(data) {
			break // larger offsets only get worse
		}
		if equalMasked(data[off:off+n], m.value, m.mask) {
			return true
		}
	}
	return false
}

// match reports whether the node and (if present) at least one descendant path
// match data.
func (m *magicMatch) match(data []byte) bool {
	if !m.selfMatch(data) {
		return false
	}
	if len(m.children) == 0 {
		return true
	}
	for _, c := range m.children {
		if c.match(data) {
			return true
		}
	}
	return false
}

// equalMasked compares region against value, applying mask when non-nil.
func equalMasked(region, value, mask []byte) bool {
	if mask == nil {
		for i := range value {
			if region[i] != value[i] {
				return false
			}
		}
		return true
	}
	for i := range value {
		if region[i]&mask[i] != value[i]&mask[i] {
			return false
		}
	}
	return true
}

// magicLookup returns the highest-priority type whose magic matches data, and
// that priority. On a priority tie the earliest-registered rule wins.
func (db *Database) magicLookup(data []byte) (string, int) {
	if len(data) == 0 {
		return "", -1
	}
	first := data[0]
	best := ""
	bestPrio := -1
	for i := range db.magics {
		r := &db.magics[i]
		if r.priority <= bestPrio {
			continue
		}
		if !r.canStart.has(first) {
			continue // no top-level match can start with this byte
		}
		for _, m := range r.matches {
			if m.match(data) {
				best = r.typ
				bestPrio = r.priority
				break
			}
		}
	}
	return best, bestPrio
}

// sortMagics orders rules by descending priority so magicLookup's early-out on
// r.priority <= bestPrio is sound, keeping registration order among ties, and
// builds each rule's first-byte reject filter.
func (db *Database) sortMagics() {
	sort.SliceStable(db.magics, func(i, j int) bool {
		return db.magics[i].priority > db.magics[j].priority
	})
	for i := range db.magics {
		db.magics[i].buildCanStart()
	}
}

// ---- binary "magic" file parser ----

var magicHeader = []byte("MIME-Magic\x00\n")

// ErrBadMagic reports a malformed binary magic file.
var ErrBadMagic = errors.New("mime: malformed magic database")

// parseMagic parses the generated binary magic file and appends its rules.
func (db *Database) parseMagic(b []byte) error {
	if len(b) < len(magicHeader) || string(b[:len(magicHeader)]) != string(magicHeader) {
		return fmt.Errorf("%w: bad header", ErrBadMagic)
	}
	p := &magicParser{b: b, pos: len(magicHeader)}
	for p.pos < len(p.b) {
		rule, err := p.section()
		if err != nil {
			return err
		}
		db.magics = append(db.magics, rule)
	}
	db.sortMagics()
	return nil
}

type magicParser struct {
	b   []byte
	pos int
}

// section parses "[priority:type]\n" and the indented rule lines that follow.
func (p *magicParser) section() (magicRule, error) {
	if p.pos >= len(p.b) || p.b[p.pos] != '[' {
		return magicRule{}, fmt.Errorf("%w: expected section header", ErrBadMagic)
	}
	p.pos++ // consume '['
	end := indexByteFrom(p.b, p.pos, ']')
	if end < 0 {
		return magicRule{}, fmt.Errorf("%w: unterminated section header", ErrBadMagic)
	}
	head := string(p.b[p.pos:end])
	p.pos = end + 1
	if p.pos >= len(p.b) || p.b[p.pos] != '\n' {
		return magicRule{}, fmt.Errorf("%w: section header not newline-terminated", ErrBadMagic)
	}
	p.pos++ // consume '\n'

	colon := indexByteFrom([]byte(head), 0, ':')
	if colon < 0 {
		return magicRule{}, fmt.Errorf("%w: section header %q missing ':'", ErrBadMagic, head)
	}
	prio, err := atoiStrict(head[:colon])
	if err != nil {
		return magicRule{}, fmt.Errorf("%w: bad priority %q", ErrBadMagic, head[:colon])
	}
	rule := magicRule{typ: head[colon+1:], priority: prio}

	// Parse rule lines until the next section or EOF, building the indent tree.
	var stack []*magicMatch // stack[i] is the last node at indent i
	for p.pos < len(p.b) && p.b[p.pos] != '[' {
		indent, m, err := p.ruleLine()
		if err != nil {
			return magicRule{}, err
		}
		if indent > len(stack) {
			return magicRule{}, fmt.Errorf("%w: indent %d jumps past %d", ErrBadMagic, indent, len(stack))
		}
		stack = stack[:indent]
		if indent == 0 {
			rule.matches = append(rule.matches, m)
		} else {
			parent := stack[indent-1]
			parent.children = append(parent.children, m)
		}
		stack = append(stack, m)
	}
	if len(rule.matches) == 0 {
		return magicRule{}, fmt.Errorf("%w: section %q has no rules", ErrBadMagic, rule.typ)
	}
	return rule, nil
}

// ruleLine parses one "[indent]>offset=<len><value>[&<mask>][~word][+range]\n".
func (p *magicParser) ruleLine() (int, *magicMatch, error) {
	indent, err := p.decimalUntil('>')
	if err != nil {
		return 0, nil, err
	}
	// decimalUntil left pos at '>'; consume it.
	p.pos++
	offset, err := p.decimalUntil('=')
	if err != nil {
		return 0, nil, err
	}
	p.pos++ // consume '='

	value, err := p.lengthPrefixed()
	if err != nil {
		return 0, nil, err
	}
	m := &magicMatch{rangeStart: offset, rangeLen: 1, value: value}

	if p.peek() == '&' {
		p.pos++
		mask := make([]byte, len(value))
		if !p.readN(mask) {
			return 0, nil, fmt.Errorf("%w: truncated mask", ErrBadMagic)
		}
		m.mask = mask
	}
	if p.peek() == '~' {
		p.pos++
		word, err := p.decimalUntilAny("+\n")
		if err != nil {
			return 0, nil, err
		}
		byteSwap(m.value, word)
		byteSwap(m.mask, word)
	}
	if p.peek() == '+' {
		p.pos++
		r, err := p.decimalUntilAny("\n")
		if err != nil {
			return 0, nil, err
		}
		m.rangeLen = r + 1
	}
	if p.peek() != '\n' {
		return 0, nil, fmt.Errorf("%w: rule not newline-terminated", ErrBadMagic)
	}
	p.pos++ // consume '\n'
	return indent, m, nil
}

// decimalUntil reads optional decimal digits terminated by sep, returning the
// value (0 if no digits). pos is left at sep.
func (p *magicParser) decimalUntil(sep byte) (int, error) {
	start := p.pos
	for p.pos < len(p.b) && p.b[p.pos] >= '0' && p.b[p.pos] <= '9' {
		p.pos++
	}
	if p.pos >= len(p.b) || p.b[p.pos] != sep {
		return 0, fmt.Errorf("%w: expected %q after number", ErrBadMagic, string(sep))
	}
	if start == p.pos {
		return 0, nil
	}
	return atoiStrict(string(p.b[start:p.pos]))
}

// decimalUntilAny reads decimal digits terminated by any byte in seps; pos is
// left at that terminator.
func (p *magicParser) decimalUntilAny(seps string) (int, error) {
	start := p.pos
	for p.pos < len(p.b) && p.b[p.pos] >= '0' && p.b[p.pos] <= '9' {
		p.pos++
	}
	if start == p.pos {
		return 0, fmt.Errorf("%w: expected number", ErrBadMagic)
	}
	if p.pos >= len(p.b) || indexByteStr(seps, p.b[p.pos]) < 0 {
		return 0, fmt.Errorf("%w: unterminated number", ErrBadMagic)
	}
	return atoiStrict(string(p.b[start:p.pos]))
}

// lengthPrefixed reads a 2-byte big-endian length and that many value bytes.
func (p *magicParser) lengthPrefixed() ([]byte, error) {
	if p.pos+2 > len(p.b) {
		return nil, fmt.Errorf("%w: truncated value length", ErrBadMagic)
	}
	n := int(p.b[p.pos])<<8 | int(p.b[p.pos+1])
	p.pos += 2
	if n == 0 {
		return nil, fmt.Errorf("%w: zero-length value", ErrBadMagic)
	}
	out := make([]byte, n)
	if !p.readN(out) {
		return nil, fmt.Errorf("%w: truncated value", ErrBadMagic)
	}
	return out, nil
}

// readN copies len(dst) bytes from the stream, advancing pos; false if short.
func (p *magicParser) readN(dst []byte) bool {
	if p.pos+len(dst) > len(p.b) {
		return false
	}
	copy(dst, p.b[p.pos:p.pos+len(dst)])
	p.pos += len(dst)
	return true
}

// peek returns the current byte, or 0 at EOF.
func (p *magicParser) peek() byte {
	if p.pos >= len(p.b) {
		return 0
	}
	return p.b[p.pos]
}

// byteSwap reverses each group of size bytes in b (for word-size endian
// conversion). Sizes <= 1, or that do not evenly divide len(b), leave b
// unchanged.
func byteSwap(b []byte, size int) {
	if b == nil || size <= 1 || len(b)%size != 0 {
		return
	}
	for i := 0; i < len(b); i += size {
		for l, r := i, i+size-1; l < r; l, r = l+1, r-1 {
			b[l], b[r] = b[r], b[l]
		}
	}
}

// ---- small byte helpers (avoid bytes/strconv churn on hot paths) ----

func indexByteFrom(b []byte, from int, c byte) int {
	for i := from; i < len(b); i++ {
		if b[i] == c {
			return i
		}
	}
	return -1
}

func indexByteStr(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// atoiStrict parses a non-negative decimal integer, rejecting empty input and
// non-digit bytes.
func atoiStrict(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("%w: empty number", ErrBadMagic)
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, fmt.Errorf("%w: %q is not a number", ErrBadMagic, s)
		}
		n = n*10 + int(s[i]-'0')
	}
	return n, nil
}
