// SPDX-License-Identifier: Apache-2.0
package outbound

import (
	"bytes"
	"unicode/utf16"
	"unicode/utf8"
)

// Decode only one JSON escape and a bounded matching suffix at a time. Offsets
// refer to the original wire bytes, so a possible reflection stays withheld
// without buffering a whole JSON string or changing allowed response content.
type credentialJSONGuard struct {
	patterns, short [][]byte
	inside          bool
	position        int64
	escapeBuffer    [12]byte
	escape          []byte
	escapeStart     int64
	decoded         []byte
	starts          []int64
	previous        byte
	hasPrevious     bool
	failed          bool
	framing         credentialFraming
	// sequence marks an RFC 7464 JSON text sequence whose RS bytes separate
	// independently decoded records.
	sequence bool
	probe    []byte
	sse      credentialSSEFraming
}

func (g *credentialJSONGuard) retainFrom() int64 {
	start := g.position
	if len(g.starts) > 0 {
		start = g.starts[0]
	}
	if len(g.escape) > 0 && g.escapeStart < start {
		start = g.escapeStart
	}
	return start
}
func (g *credentialJSONGuard) scan(raw []byte) bool {
	for _, b := range raw {
		offset := g.position
		g.position++
		g.frame(b, offset)
		if g.failed {
			return true
		}
	}
	return false
}
func (g *credentialJSONGuard) feed(b byte, offset int64) {
	if len(g.escape) > 0 {
		g.escape = append(g.escape, b)
		if len(g.escape) == 2 && b != 'u' {
			g.completeEscape(2)
			return
		}
		if len(g.escape) < 6 {
			return
		}
		if len(g.escape) == 6 {
			unit, valid := credentialJSONCodeUnit(g.escape)
			if !valid {
				g.invalidEscape()
				return
			}
			// A high surrogate may combine with the next \uXXXX. Delay only this
			// fixed-size escape until that choice is known, including split reads.
			if unit >= 0xd800 && unit <= 0xdbff {
				return
			}
			g.completeEscape(6)
			return
		}
		if len(g.escape) == 7 && b != '\\' || len(g.escape) == 8 && b != 'u' {
			g.completeEscape(6)
			return
		}
		if len(g.escape) == 12 {
			g.completeEscape(12)
		}
		return
	}
	if !g.inside {
		if b == '"' {
			g.inside = true
			g.previous = 0
			g.hasPrevious = false
		}
		return
	}
	switch b {
	case '\\':
		g.escape = g.escapeBuffer[:1]
		g.escape[0] = '\\'
		g.escapeStart = offset
	case '"':
		g.failed = containsCredentialForms(g.decoded, true, g.previous, g.hasPrevious, g.patterns, g.short)
		g.resetString()
	default:
		g.appendDecoded([]byte{b}, offset)
	}
}
func (g *credentialJSONGuard) invalidEscape() {
	// Malformed JSON is left unchanged for the caller's parser. It cannot grant
	// a decoded reflection; the independent literal guard still applies.
	clear(g.escape)
	g.escape = nil
	g.resetString()
}

// credentialJSONCodeUnit accepts exactly the JSON \uXXXX spelling. It reads
// the reusable escape buffer without allocating a temporary JSON string.
func credentialJSONCodeUnit(escape []byte) (rune, bool) {
	if len(escape) != 6 || escape[0] != '\\' || escape[1] != 'u' {
		return 0, false
	}
	var unit rune
	for _, b := range escape[2:] {
		unit <<= 4
		switch {
		case b >= '0' && b <= '9':
			unit |= rune(b - '0')
		case b >= 'a' && b <= 'f':
			unit |= rune(b - 'a' + 10)
		case b >= 'A' && b <= 'F':
			unit |= rune(b - 'A' + 10)
		default:
			return 0, false
		}
	}
	return unit, true
}
func (g *credentialJSONGuard) completeEscape(count int) {
	var value rune
	if count == 2 {
		switch g.escape[1] {
		case '"', '\\', '/':
			value = rune(g.escape[1])
		case 'b':
			value = '\b'
		case 'f':
			value = '\f'
		case 'n':
			value = '\n'
		case 'r':
			value = '\r'
		case 't':
			value = '\t'
		default:
			g.invalidEscape()
			return
		}
	} else {
		var valid bool
		value, valid = credentialJSONCodeUnit(g.escape[:6])
		if !valid {
			g.invalidEscape()
			return
		}
		if count == 12 {
			next, valid := credentialJSONCodeUnit(g.escape[6:12])
			if !valid {
				g.invalidEscape()
				return
			}
			if paired := utf16.DecodeRune(value, next); paired != utf8.RuneError {
				value = paired
			} else {
				// An unmatched high surrogate consumes only its own escape.
				// Replay the next one so a later low surrogate can still pair.
				count = 6
			}
		}
	}
	var decoded [utf8.UTFMax]byte
	n := utf8.EncodeRune(decoded[:], value)
	var tail [6]byte
	tailLen := copy(tail[:], g.escape[count:])
	start := g.escapeStart
	clear(g.escape)
	g.escape = nil
	g.appendDecoded(decoded[:n], start)
	clear(decoded[:])
	for i := 0; i < tailLen && !g.failed; i++ {
		g.feed(tail[i], start+int64(count+i))
	}
	clear(tail[:])
}
func (g *credentialJSONGuard) appendDecoded(value []byte, offset int64) {
	for _, b := range value {
		g.decoded = append(g.decoded, b)
		g.starts = append(g.starts, offset)
		if containsCredentialForms(g.decoded, false, g.previous, g.hasPrevious, g.patterns, g.short) {
			g.failed = true
			return
		}
		retained := 0
		for _, forms := range [][][]byte{g.patterns, g.short} {
			for _, pattern := range forms {
				bound := len(pattern) - 1
				if len(pattern) < 8 {
					bound = len(pattern)
				}
				for n := min(bound, len(g.decoded)); n > retained; n-- {
					if bytes.Equal(g.decoded[len(g.decoded)-n:], pattern[:n]) {
						retained = n
						break
					}
				}
			}
		}
		release := len(g.decoded) - retained
		if release > 0 {
			g.previous, g.hasPrevious = g.decoded[release-1], true
			copy(g.decoded, g.decoded[release:])
			clear(g.decoded[retained:])
			g.decoded = g.decoded[:retained]
			copy(g.starts, g.starts[release:])
			g.starts = g.starts[:retained]
		}
	}
}
func (g *credentialJSONGuard) finish() bool {
	if len(g.escape) >= 6 {
		g.completeEscape(6)
	}
	if !g.failed {
		g.failed = containsCredentialForms(g.decoded, true, g.previous, g.hasPrevious, g.patterns, g.short)
	}
	return g.failed
}
func (g *credentialJSONGuard) resetString() {
	clear(g.decoded)
	g.decoded = g.decoded[:0]
	g.starts = g.starts[:0]
	g.previous = 0
	g.hasPrevious = false
	g.inside = false
}
func (g *credentialJSONGuard) clear() {
	clear(g.probe)
	g.probe = nil
	clear(g.escape)
	g.escape = nil
	g.resetString()
	for _, forms := range [][][]byte{g.patterns, g.short} {
		for _, p := range forms {
			clear(p)
		}
	}
}
