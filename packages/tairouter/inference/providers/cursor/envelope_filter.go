// SPDX-License-Identifier: Apache-2.0
// Adapted from the pinned MIT tool-envelope-echo-filter.ts; see LICENSE.upstream.
package cursor

import "strings"

var envelopeMarkers = []string{"[Tool Result]", "[Tool Error]", "[tool_result]", "[Tool Call]", "[Tool call:"}
var wholeEnvelopeMarkers = []string{"[Tool Result]", "[Tool Error]", "[tool_result]", "[Tool Call]", "[Tool call:", "[Tool Result", "[Tool Error", "[tool_result", "[Tool Call"}

func envelopeMarker(line string) bool {
	line = strings.TrimSpace(line)
	for _, marker := range wholeEnvelopeMarkers {
		if line == marker {
			return true
		}
	}
	return strings.HasPrefix(line, "[Tool call:")
}

type fenceLine struct {
	char   byte
	length int
	rest   string
}

func parseFence(line string) (fenceLine, bool) {
	probe := strings.TrimLeft(line, " \t")
	if len(line)-len(probe) > 3 {
		return fenceLine{}, false
	}
	probe = strings.TrimSuffix(strings.TrimSuffix(probe, "\n"), "\r")
	if len(probe) < 3 || probe[0] != '`' && probe[0] != '~' {
		return fenceLine{}, false
	}
	n := 0
	for n < len(probe) && probe[n] == probe[0] {
		n++
	}
	return fenceLine{probe[0], n, probe[n:]}, n >= 3
}
func opensFence(f fenceLine) bool { return f.char != '`' || !strings.ContainsRune(f.rest, '`') }
func closesFence(f, open fenceLine) bool {
	return f.char == open.char && f.length >= open.length && strings.TrimSpace(f.rest) == ""
}

type envelopeFilter struct {
	pending                           string
	safeLine                          bool
	fence                             *fenceLine
	held                              strings.Builder
	holding, holdDisabled, overflowed bool
	matched, unverified               bool
}

func (f *envelopeFilter) emit(text string) string {
	if !f.holding {
		return text
	}
	f.held.WriteString(text)
	if f.held.Len() <= 64<<10 {
		return ""
	}
	released := f.held.String()
	f.held.Reset()
	f.holding = false
	f.holdDisabled = true
	f.overflowed = true
	f.unverified = true
	return released
}
func (f *envelopeFilter) take() string { text := f.pending; f.pending = ""; return text }
func (f *envelopeFilter) hold() {
	if f.fence != nil && !f.holdDisabled && !f.holding {
		f.holding = true
	}
}
func (f *envelopeFilter) completeLine() string {
	raw := f.take()
	if fence, ok := parseFence(raw); ok {
		if f.fence == nil {
			if opensFence(fence) {
				f.fence = &fence
				f.holdDisabled = false
				f.overflowed = false
			}
			return f.emit(raw)
		}
		if closesFence(fence, *f.fence) {
			out := f.emit(raw)
			if f.overflowed {
				f.unverified = false
			}
			released := f.held.String()
			f.held.Reset()
			f.holding = false
			f.fence = nil
			return out + released
		}
		return f.emit(raw)
	}
	probe := strings.TrimLeft(raw, " \t")
	if len(raw)-len(probe) <= 128 && envelopeMarker(probe) {
		if f.fence == nil {
			f.matched = true
			return ""
		}
		f.hold()
	}
	return f.emit(raw)
}
func (f *envelopeFilter) feed(delta string) string {
	if f.matched {
		return ""
	}
	var out strings.Builder
	for _, r := range delta {
		if f.matched {
			break
		}
		char := string(r)
		if f.safeLine {
			out.WriteString(f.emit(char))
			if r == '\n' {
				f.safeLine = false
			}
			continue
		}
		f.pending += char
		if r == '\n' {
			out.WriteString(f.completeLine())
			continue
		}
		probe := strings.TrimLeft(f.pending, " \t")
		indent := len(f.pending) - len(probe)
		if indent <= 128 && probe == "[Tool call:" {
			if f.fence == nil {
				f.pending = ""
				f.matched = true
				break
			}
			f.hold()
			f.safeLine = true
			out.WriteString(f.emit(f.take()))
			continue
		}
		fenceCandidate := false
		if indent <= 3 && len(f.pending) <= 1024 && len(probe) > 0 && (probe[0] == '`' || probe[0] == '~') {
			n := 0
			for n < len(probe) && probe[n] == probe[0] {
				n++
			}
			fenceCandidate = n >= 3 || n == len(probe)
		}
		markerCandidate := probe == ""
		for _, m := range envelopeMarkers {
			markerCandidate = markerCandidate || strings.HasPrefix(m, probe)
		}
		for _, m := range wholeEnvelopeMarkers {
			markerCandidate = markerCandidate || strings.TrimRight(probe, " \t\r\n") == m
		}
		// A marker followed by arbitrary whitespace can otherwise retain an
		// unbounded line. Release once the quarantine bound is exhausted;
		// never retract visible text or retry this turn on uncertain evidence.
		if len(f.pending) > 64<<10 {
			f.unverified = true
			if f.fence != nil {
				f.overflowed = true
			}
			f.safeLine = true
			out.WriteString(f.emit(f.take()))
			continue
		}
		if fenceCandidate || indent <= 128 && markerCandidate {
			continue
		}
		f.safeLine = true
		out.WriteString(f.emit(f.take()))
	}
	return out.String()
}
func (f *envelopeFilter) finish() string {
	if f.matched {
		return ""
	}
	settled := ""
	if _, ok := parseFence(f.pending); f.pending != "" && ok {
		settled = f.completeLine()
	}
	pending := f.take()
	if f.holding {
		f.holding = false
		f.held.Reset()
		f.matched = true
		return ""
	}
	if envelopeMarker(pending) {
		f.matched = true
		return settled
	}
	return settled + pending
}

// History cleanup preserves closed code fences and resumes after the blank line
// that terminates an echoed envelope, matching the pinned replay boundary.
func stripHistoryEnvelope(text string) string {
	newline := "\n"
	if strings.Contains(text, "\r\n") {
		newline = "\r\n"
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	shield := make([]bool, len(lines))
	var open *fenceLine
	start := 0
	for i, line := range lines {
		fence, ok := parseFence(line)
		if open == nil {
			if ok && opensFence(fence) {
				open = &fence
				start = i
			}
			continue
		}
		if ok && closesFence(fence, *open) {
			for j := start; j <= i; j++ {
				shield[j] = true
			}
			open = nil
		}
	}
	kept := make([]string, 0, len(lines))
	dropped := false
	for i := 0; i < len(lines); i++ {
		if shield[i] || !envelopeMarker(lines[i]) {
			kept = append(kept, lines[i])
			continue
		}
		dropped = true
		for i+1 < len(lines) && strings.TrimSpace(lines[i+1]) != "" {
			i++
		}
	}
	if !dropped {
		return text
	}
	return strings.TrimRight(strings.Join(kept, newline), " \t\r\n")
}
