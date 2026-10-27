// SPDX-License-Identifier: Apache-2.0
// Root selection/truncation adapted from pinned protobuf-request.ts; see LICENSE.upstream.
package cursor

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

type rootCandidate struct {
	role, wireRole, text, fullText string
	clippedLine, fullLine          string
	index                          int
	data                           []byte
	elided                         bool
}

func (r rootCandidate) encode() rootCandidate {
	var content any = []any{map[string]any{"type": "text", "text": r.text}}
	if r.role == "system" {
		content = r.text
	}
	r.data, _ = json.Marshal(map[string]any{"role": r.wireRole, "content": content})
	return r
}
func utf8Prefix(s string, n int) string {
	n = min(len(s), max(0, n))
	for n > 0 && n < len(s) && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

const replayTruncation = "\n…[truncated for Cursor external replay budget]"

func shrinkRoot(r rootCandidate, budget int) (rootCandidate, bool) {
	if len(r.data) <= budget {
		return r, true
	}
	if r.role != "tool" {
		return r, false
	}
	keep := min(len(r.text), max(0, budget-len(replayTruncation)-96))
	for attempt := 0; attempt < 8; attempt++ {
		prefix := utf8Prefix(r.text, keep)
		next := r
		next.text = prefix + replayTruncation
		next = next.encode()
		if len(next.data) <= budget {
			at := strings.Index(prefix, "\noutput:\n")
			next.elided = at < 0 || len(prefix) <= at+len("\noutput:\n")
			return next, true
		}
		if len(prefix) == 0 {
			break
		}
		keep = max(0, len(prefix)-(len(next.data)-budget)-16)
	}
	next := r
	next.text = strings.TrimLeft(replayTruncation, "\n")
	next.elided = true
	next = next.encode()
	return next, len(next.data) <= budget
}
func rootBytes(entries []rootCandidate) int {
	total := 0
	for _, r := range entries {
		total += len(r.data)
	}
	return total
}
func rootCandidates(history []historyMessage, active, start int, model string) ([]rootCandidate, []rootCandidate, error) {
	var systems, prior []rootCandidate
	known := map[string]historyCall{}
	type run struct {
		text         string
		index, count int
	}
	runs := map[string]run{}
	calls := map[string]int{}
	maxRun, maxCalls := 1, 1
	for i, h := range history {
		for _, c := range h.Calls {
			known[c.ID] = c
		}
		if i == active {
			break
		}
		role, text := h.Role, h.Text
		if role == "system" {
			if text != "" {
				systems = append(systems, rootCandidate{role: "system", wireRole: "system", text: text, index: i}.encode())
			}
			continue
		}
		if i < start {
			continue
		}
		if role == "user" || role == "developer" {
			runs = map[string]run{}
			calls = map[string]int{}
			maxRun, maxCalls = 1, 1
		}
		if role == "assistant" && !nativeModel(model) {
			seen := map[string]bool{}
			for _, call := range h.Calls {
				key := call.Name + "\x00" + call.Arguments
				if !seen[key] {
					calls[key]++
					maxCalls = max(maxCalls, calls[key])
					seen[key] = true
				}
			}
		}
		if role == "developer" {
			role = "user"
		}
		if role == "assistant" {
			text = stripHistoryEnvelope(text)
		}
		full := ""
		clippedLine, fullLine := "", ""
		if role == "tool" {
			call, ok := known[h.CallID]
			if !ok {
				return nil, nil, &inference.Error{Status: 400, Code: "cursor_unpaired_tool_result"}
			}
			if !externalToolContinuation(model) {
				continue
			}
			prefix := "[Tool Result]"
			if h.IsError {
				prefix = "[Tool Error]"
			}
			render := func(args string) string {
				return fmt.Sprintf("%s\n[tool_result]\ncall_id: %s\nname: %s\ninvoked: %s with %s\nis_error: %t\noutput:\n%s", prefix, call.ID, call.Name, call.Name, args, h.IsError, text)
			}
			full = render(call.Arguments)
			args := call.Arguments
			if len(args) > 2048 {
				marker := "…[arguments truncated]"
				args = utf8Prefix(args, 2048-len(marker)) + marker
			}
			text = render(args)
			clippedLine = "\ninvoked: " + call.Name + " with " + args
			fullLine = "\ninvoked: " + call.Name + " with " + call.Arguments
		} else {
			text = strings.TrimSpace(text)
		}
		if text == "" {
			continue
		}
		wireRole := role
		if role == "tool" {
			wireRole = "assistant"
		}
		candidate := rootCandidate{role: role, wireRole: wireRole, text: text, fullText: full, clippedLine: clippedLine, fullLine: fullLine, index: i}.encode()
		if !nativeModel(model) && (role == "assistant" || role == "tool") {
			previous, exists := runs[role]
			if exists && previous.text == text {
				previous.count++
				maxRun = max(maxRun, previous.count)
				candidate.text = fmt.Sprintf("%s\n[note: this exact output was produced %d times in a row]", text, previous.count)
				candidate.index = prior[previous.index].index
				prior[previous.index] = candidate.encode()
				runs[role] = previous
				continue
			}
			runs[role] = run{text, len(prior), 1}
		}
		prior = append(prior, candidate)
	}
	if !nativeModel(model) && active < 0 {
		note := ""
		if maxCalls >= 3 {
			note = fmt.Sprintf("[context note] The transcript above contains the same tool call repeated %d times in this user turn. Requested polling or changed observations can justify repetition. If nothing changed and no new evidence requires another check, use the existing result. Take a DIFFERENT action now only when the repeated check cannot advance the current request. Do not repeat a completed side effect merely to recover missing output.", maxCalls)
		} else if maxRun >= 3 {
			note = fmt.Sprintf("[context note] The transcript above contains the same output repeated %d times in a row. Use completed observations to advance the current request. Take a DIFFERENT action now if there is no new evidence to check; requested polling remains valid. Do not repeat a completed side effect merely to recover missing output.", maxRun)
		}
		if note != "" {
			prior = append(prior, rootCandidate{role: "user", wireRole: "user", text: note, index: -1}.encode())
		}
	}
	return systems, prior, nil
}

// Prune only the external replay representation. System instructions are never
// truncated. Tool-result truncation is explicit and tracks whether output survives.
func pruneRoots(systems, history []rootCandidate, carriedCount, carriedBytes int, suffix bool) ([]rootCandidate, []int) {
	systemCount := len(systems)
	if suffix {
		systemCount = 0
	}
	limit := max(0, MaxRoots-systemCount-carriedCount)
	budget := max(0, MaxRootBytes-rootBytes(systems)-carriedBytes)
	end := len(history)
	for end > 0 && history[end-1].index < 0 {
		end--
	}
	var synthetic []rootCandidate
	if end < len(history) && len(history)-end < limit && rootBytes(history[end:]) <= budget {
		synthetic = history[end:]
		limit -= len(synthetic)
		budget -= rootBytes(synthetic)
	}
	history = history[:end]
	start := len(history)
	for start > 0 && history[start-1].role == "tool" {
		start--
	}
	var active []rootCandidate
	var required []int
	for _, r := range history[start:] {
		required = append(required, r.index)
		if shrunk, ok := shrinkRoot(r, budget); ok {
			active = append(active, shrunk)
		}
	}
	if len(active) > 1 && rootBytes(active) > budget {
		share := budget / len(active)
		for i, r := range active {
			if shrunk, ok := shrinkRoot(r, share); ok {
				active[i] = shrunk
			}
		}
	}
	for len(active) > 1 && (rootBytes(active) > budget || len(active) > limit) {
		active = active[1:]
	}
	if len(active) == 1 && rootBytes(active) > budget {
		shrunk, ok := shrinkRoot(active[0], budget)
		if ok {
			active[0] = shrunk
		} else {
			active = nil
		}
	}
	prior := history[:start]
	var kept []rootCandidate
	for i := len(prior) - 1; i >= 0 && len(kept)+len(active) < limit; {
		begin := i
		if !suffix {
			for begin > 0 && prior[begin].role != "user" {
				begin--
			}
		}
		turn := prior[begin : i+1]
		if len(kept)+len(active)+len(turn) > limit || rootBytes(kept)+rootBytes(active)+rootBytes(turn) > budget {
			break
		}
		kept = append(append([]rootCandidate(nil), turn...), kept...)
		i = begin - 1
	}
	selected := append(kept, active...)
	if !suffix {
		for len(selected) > len(active) && (selected[0].role == "assistant" || selected[0].role == "tool") {
			selected = selected[1:]
		}
	}
	if len(selected) > 0 && selected[0].role == "tool" && start > 0 {
		initiator := start - 1
		for initiator >= 0 && history[initiator].role != "user" {
			initiator--
		}
		if initiator >= 0 {
			user := history[initiator]
			reduced := append([]rootCandidate(nil), selected...)
			for len(reduced) > 1 && len(reduced)+1 > limit {
				reduced = reduced[1:]
			}
			for len(reduced) > 1 && len(user.data)+rootBytes(reduced) > budget {
				reduced = reduced[1:]
			}
			if len(reduced) == 1 && len(user.data)+rootBytes(reduced) > budget {
				if shrunk, ok := shrinkRoot(reduced[0], budget-len(user.data)); ok {
					reduced[0] = shrunk
				}
			}
			if len(reduced)+1 <= limit && len(user.data)+rootBytes(reduced) <= budget {
				selected = append([]rootCandidate{user}, reduced...)
			}
		}
	}
	return append(append([]rootCandidate(nil), systems...), append(selected, synthetic...)...), required
}
func refundInvocationSpace(entries []rootCandidate, carriedBytes int) []rootCandidate {
	spare := MaxRootBytes - carriedBytes - rootBytes(entries)
	for i := len(entries) - 1; i >= 0 && spare > 0; i-- {
		r := entries[i]
		if r.elided || r.fullLine == r.clippedLine || r.clippedLine == "" || !strings.Contains(r.text, r.clippedLine) {
			continue
		}
		if len(r.fullLine)-len(r.clippedLine) > spare {
			continue
		}
		full := r
		full.text = strings.Replace(r.text, r.clippedLine, r.fullLine, 1)
		full = full.encode()
		if cost := len(full.data) - len(r.data); cost > 0 && cost <= spare {
			entries[i] = full
			spare -= cost
		}
	}
	return entries
}

func (s *Session) buildRoots(history []historyMessage, active int, saved *Continuation, state []byte, model string) ([]byte, error) {
	start := 0
	suffix := saved != nil && !saved.ReplayOnly
	carriedCount, carriedBytes := 0, 0
	if suffix {
		start = len(saved.History)
		m, err := parse(state)
		if err != nil {
			return nil, inference.ErrContinuation
		}
		for _, f := range m {
			if f.number == 1 {
				blob, ok := s.blobs[hex.EncodeToString(f.data)]
				if !ok {
					return nil, inference.ErrContinuation
				}
				carriedCount++
				carriedBytes += len(blob)
			}
		}
	}
	systems, historyRoots, err := rootCandidates(history, active, start, model)
	if err != nil {
		return nil, err
	}
	selected := append(append([]rootCandidate(nil), systems...), historyRoots...)
	var required []int
	if !nativeModel(model) {
		selected, required = pruneRoots(systems, historyRoots, carriedCount, carriedBytes, suffix)
	}
	abandon := false
	if suffix && !nativeModel(model) {
		kept := map[int]bool{}
		for _, r := range selected {
			if r.role != "system" && !r.elided {
				kept[r.index] = true
			}
		}
		abandon = carriedCount+len(systems) >= MaxRoots || len(selected) <= len(systems)
		for _, index := range required {
			if active < 0 && len(history) > 0 && history[len(history)-1].Role == "tool" && !kept[index] {
				abandon = true
			}
		}
	}
	if abandon {
		// The input prefix was already validated against authenticated saved history.
		// Rebuild its complete replay instead of sending a checkpoint without answers.
		state = nil
		suffix = false
		carriedCount = 0
		carriedBytes = 0
		systems, historyRoots, err = rootCandidates(history, active, 0, model)
		if err != nil {
			return nil, err
		}
		selected, _ = pruneRoots(systems, historyRoots, 0, 0, false)
		s.blobs = map[string][]byte{}
		s.blobBytes = 0
		s.checkpoint = nil
	}
	selected = refundInvocationSpace(selected, carriedBytes)
	if suffix {
		withoutSystems := selected[:0]
		for _, r := range selected {
			if r.role != "system" {
				withoutSystems = append(withoutSystems, r)
			}
		}
		selected = withoutSystems
	}
	if carriedCount+len(selected) > MaxRoots || carriedBytes+rootBytes(selected) > MaxRootBytes {
		return nil, &inference.Error{Status: 400, Code: "cursor_root_envelope_limit"}
	}
	for _, r := range selected {
		id, err := s.replayBlob(r.data)
		if err != nil {
			return nil, err
		}
		state = append(state, pb(1, id)...)
	}
	return state, nil
}
