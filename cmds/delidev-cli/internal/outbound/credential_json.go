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
	escape          []byte
	escapeStart     int64
	decoded         []byte
	starts          []int64
	previous        byte
	hasPrevious     bool
	failed          bool
	framing         credentialFraming
	probe           []byte
	sse             credentialSSEFraming
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
			// A high surrogate may combine with the next \uXXXX. Delay only this
			// fixed-size escape until that choice is known, including split reads.
			value, valid := credentialUnicodeEscape(g.escape)
			if !valid {
				g.invalidEscape()
				return
			}
			if value >= 0xd800 && value <= 0xdbff {
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
		g.escape = append(g.escape[:0], '\\')
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
	g.escape = g.escape[:0]
	g.resetString()
}

// Decode the fixed JSON escape alphabet without allocating quoted fragments.
func credentialUnicodeEscape(raw []byte) (rune, bool) {
	if len(raw) != 6 || raw[0] != '\\' || raw[1] != 'u' {
		return 0, false
	}
	var value rune
	for _, b := range raw[2:] {
		value <<= 4
		switch {
		case b >= '0' && b <= '9':
			value += rune(b - '0')
		case b >= 'a' && b <= 'f':
			value += rune(b - 'a' + 10)
		case b >= 'A' && b <= 'F':
			value += rune(b - 'A' + 10)
		default:
			return 0, false
		}
	}
	return value, true
}

func (g *credentialJSONGuard) completeEscape(count int) {
	var buffer [8]byte
	value := buffer[:0]
	if count == 2 {
		var b byte
		switch g.escape[1] {
		case '"', '\\', '/':
			b = g.escape[1]
		case 'b':
			b = '\b'
		case 'f':
			b = '\f'
		case 'n':
			b = '\n'
		case 'r':
			b = '\r'
		case 't':
			b = '\t'
		default:
			g.invalidEscape()
			return
		}
		value = append(value, b)
	} else {
		first, valid := credentialUnicodeEscape(g.escape[:6])
		if !valid {
			g.invalidEscape()
			return
		}
		if count == 12 {
			second, valid := credentialUnicodeEscape(g.escape[6:12])
			if !valid {
				g.invalidEscape()
				return
			}
			if first >= 0xd800 && first <= 0xdbff && second >= 0xdc00 && second <= 0xdfff {
				value = utf8.AppendRune(value, utf16.DecodeRune(first, second))
			} else {
				// Match encoding/json's replacement of each unpaired surrogate.
				value = utf8.AppendRune(value, first)
				value = utf8.AppendRune(value, second)
			}
		} else {
			value = utf8.AppendRune(value, first)
		}
	}
	// A high surrogate may have consumed one or two lookahead bytes that
	// belong to the following input. Retain them on the stack with wire offsets.
	var tail [6]byte
	tailLength := copy(tail[:], g.escape[count:])
	start := g.escapeStart
	clear(g.escape)
	g.escape = g.escape[:0]
	g.appendDecoded(value, start)
	for i, b := range tail[:tailLength] {
		g.feed(b, start+int64(count+i))
	}
	clear(buffer[:])
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
