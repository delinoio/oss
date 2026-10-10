// SPDX-License-Identifier: Apache-2.0
package outbound

import "bytes"

type credentialFraming uint8

const (
	credentialUnknown credentialFraming = iota
	credentialOrdinary
	credentialSSE
)

// Select framing from the response media type when available. Otherwise retain
// only a bounded initial field prefix, preserving typeless SSE compatibility.
// Metadata never enters the JSON decoder; the body literal guard still sees it.
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
		g.flushProbe(offset)
		return
	}
	if g.framing == credentialSSE {
		g.frameSSE(b, offset)
		return
	}
	if len(g.probe) == 0 && (b == '\r' || b == '\n') {
		return
	}
	g.probe = append(g.probe, b)
	if b == '\r' || b == '\n' {
		for _, field := range []string{"data", "event", "id", "retry"} {
			if bytes.Equal(g.probe[:len(g.probe)-1], []byte(field)) {
				g.framing = credentialSSE
				g.flushProbe(offset)
				return
			}
		}
	}
	for _, field := range []string{":", "data:", "event:", "id:", "retry:"} {
		if bytes.HasPrefix([]byte(field), g.probe) {
			if len(g.probe) == len(field) {
				g.framing = credentialSSE
				g.flushProbe(offset)
			}
			return
		}
	}
	g.framing = credentialOrdinary
	g.flushProbe(offset)
}

func (g *credentialJSONGuard) flushProbe(offset int64) {
	start := offset - int64(len(g.probe)) + 1
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
	field       credentialSSEField
	nameLength  int
	linePresent bool
	firstValue  bool
	lastCR      bool
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
