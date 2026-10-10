// SPDX-License-Identifier: Apache-2.0
package outbound

import (
	"bytes"
	"encoding/json"
)

const credentialSSEProbeLimit = 256

type credentialFraming uint8

const (
	credentialUnknown credentialFraming = iota
	credentialOrdinary
	credentialSSE
)

// Select framing from the response media type when available. Otherwise retain
// only a bounded initial line while checking typeless SSE syntax. Metadata never
// enters the JSON decoder; the body literal guard still sees it.
func (g *credentialJSONGuard) frame(b byte, offset int64) {
	if g.framing == credentialOrdinary {
		g.feed(b, offset)
		return
	}
	// An optional initial UTF-8 BOM belongs to framing, not a field name.
	if offset == 0 && b == 0xef || len(g.probe) > 0 && g.probe[0] == 0xef {
		g.probe = append(g.probe, b)
		bom := []byte{0xef, 0xbb, 0xbf}
		if bytes.Equal(g.probe, bom[:len(g.probe)]) {
			if len(g.probe) == len(bom) {
				g.probe = g.probe[:0]
			}
			return
		}
		if g.framing == credentialUnknown {
			g.framing = credentialOrdinary
		}
		g.flushProbe(offset)
		return
	}
	if g.framing == credentialSSE {
		g.frameSSE(b, offset)
		return
	}
	if len(g.probe) == 0 && (b == '\r' || b == '\n') {
		g.sse.probeLeadingWhitespace = false
		return
	}
	if len(g.probe) == 0 && (b == ' ' || b == '\t') {
		g.sse.probeLeadingWhitespace = true
		return
	}
	// JSON objects and arrays are unambiguous before an SSE field delimiter.
	// Keep strings in the bounded probe because a colon inside a valid JSON
	// string is content, while a colon after a quoted SSE field name is a field
	// delimiter.
	if len(g.probe) == 0 && (b == '{' || b == '[') {
		g.framing = credentialOrdinary
		g.sse.probeLeadingWhitespace = false
		g.feed(b, offset)
		return
	}
	g.probe = append(g.probe, b)
	if b == ':' && credentialSSEProbeColonIsDelimiter(g.probe) {
		g.framing = credentialSSE
		g.flushProbe(offset)
		return
	}
	if b == '\r' || b == '\n' {
		line := g.probe[:len(g.probe)-1]
		if json.Valid(line) {
			g.framing = credentialOrdinary
		} else {
			g.framing = credentialSSE
		}
		g.flushProbe(offset)
		return
	}
	if len(g.probe) >= credentialSSEProbeLimit {
		// A long quoted JSON string remains on the ordinary path. Other long
		// prefixes cannot be a valid JSON object/array (handled above) and are
		// bounded as SSE field metadata instead of retaining an unbounded line.
		if g.probe[0] == '"' {
			g.framing = credentialOrdinary
		} else {
			g.framing = credentialSSE
		}
		g.flushProbe(offset)
	}
}

// A top-level JSON string may contain colons. For that one ambiguous start,
// treat only a colon outside the quoted string as an SSE field delimiter.
func credentialSSEProbeColonIsDelimiter(probe []byte) bool {
	if len(probe) == 0 || probe[0] != '"' {
		return true
	}
	inside, escaped := false, false
	for _, b := range probe {
		if !inside {
			if b == ':' {
				return true
			}
			if b == '"' {
				inside = true
			}
			continue
		}
		if escaped {
			escaped = false
			continue
		}
		switch b {
		case '\\':
			escaped = true
		case '"':
			inside = false
		case ':':
			// A colon inside a JSON string is content. Continue in case a
			// quoted SSE field name is followed by its actual delimiter.
		}
	}
	return false
}

// A typeless stream may end before an initial SSE field reaches a line ending.
// Finalize that bounded candidate before the body guard decides whether its
// retained wire suffix can be released.
func (g *credentialJSONGuard) finishProbe() {
	if g.framing != credentialUnknown {
		return
	}
	if len(g.probe) == 0 {
		g.framing = credentialOrdinary
		return
	}
	if json.Valid(g.probe) {
		g.framing = credentialOrdinary
	} else {
		g.framing = credentialSSE
	}
	g.flushProbe(g.position - 1)
}

func (g *credentialJSONGuard) flushProbe(offset int64) {
	start := offset - int64(len(g.probe)) + 1
	if g.framing == credentialSSE && g.sse.probeLeadingWhitespace {
		// Initial horizontal whitespace is valid JSON padding, but it is part of
		// an SSE field name. Such a field cannot be the standard "data" field.
		g.sse = credentialSSEFraming{field: credentialSSEIgnored, linePresent: true}
	}
	g.sse.probeLeadingWhitespace = false
	for i, b := range g.probe {
		if g.framing == credentialSSE {
			g.frameSSE(b, start+int64(i))
		} else {
			g.feed(b, start+int64(i))
		}
	}
	clear(g.probe)
	g.probe = g.probe[:0]
}

type credentialSSEField uint8

const (
	credentialSSEName credentialSSEField = iota
	credentialSSEData
	credentialSSEIgnored
)

type credentialSSEFraming struct {
	field                  credentialSSEField
	nameLength             int
	linePresent            bool
	firstValue             bool
	lastCR                 bool
	probeLeadingWhitespace bool
}

func (g *credentialJSONGuard) frameSSE(b byte, offset int64) {
	s := &g.sse
	if b == '\n' && s.lastCR {
		s.lastCR = false
		return
	}
	s.lastCR = false
	if b == '\r' || b == '\n' {
		if !s.linePresent {
			// Every record has independent JSON string/escape state. Finishing
			// also settles the right boundary of a retained short candidate.
			g.finish()
			clear(g.escape)
			g.escape = nil
			g.resetString()
		} else if s.field == credentialSSEData || s.field == credentialSSEName && s.nameLength == len("data") {
			// SSE joins data values with LF, irrespective of wire line endings.
			// Valid JSON cannot split a string across this literal newline.
			g.feed('\n', offset)
		}
		*s = credentialSSEFraming{lastCR: b == '\r'}
		return
	}
	s.linePresent = true
	switch s.field {
	case credentialSSEName:
		if b == ':' {
			if s.nameLength == len("data") {
				s.field, s.firstValue = credentialSSEData, true
			} else {
				s.field = credentialSSEIgnored
			}
		} else if s.nameLength < len("data") && b == "data"[s.nameLength] {
			s.nameLength++
		} else {
			s.field = credentialSSEIgnored
		}
	case credentialSSEData:
		if s.firstValue {
			s.firstValue = false
			if b == ' ' {
				return
			}
		}
		g.feed(b, offset)
	}
}
