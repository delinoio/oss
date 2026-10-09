// SPDX-License-Identifier: Apache-2.0
package outbound

import (
	"bytes"
	"encoding/json"
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
		g.feed(b, offset)
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
			var value string
			if json.Unmarshal(append(append([]byte{'"'}, g.escape...), '"'), &value) != nil {
				g.invalidEscape()
				return
			}
			if g.escape[1] == 'u' && g.escape[2] == 'D' || g.escape[1] == 'u' && g.escape[2] == 'd' {
				if g.escape[3] == '8' || g.escape[3] == '9' || g.escape[3] == 'a' || g.escape[3] == 'A' || g.escape[3] == 'b' || g.escape[3] == 'B' {
					return
				}
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
		g.escape = []byte{'\\'}
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
func (g *credentialJSONGuard) completeEscape(count int) {
	encoded := append(append([]byte{'"'}, g.escape[:count]...), '"')
	var value string
	if json.Unmarshal(encoded, &value) != nil {
		clear(encoded)
		g.invalidEscape()
		return
	}
	clear(encoded)
	tail := append([]byte(nil), g.escape[count:]...)
	start := g.escapeStart
	clear(g.escape)
	g.escape = nil
	g.appendDecoded([]byte(value), start)
	for i, b := range tail {
		g.feed(b, start+int64(count+i))
	}
	clear(tail)
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
	clear(g.escape)
	g.escape = nil
	g.resetString()
	for _, forms := range [][][]byte{g.patterns, g.short} {
		for _, p := range forms {
			clear(p)
		}
	}
}
