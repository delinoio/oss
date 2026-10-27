// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	json5 "github.com/titanous/json5"
	"sort"
	"strings"
)

func semanticEqual(a, b any) bool {
	x, e := json.Marshal(a)
	y, f := json.Marshal(b)
	return e == nil && f == nil && bytes.Equal(x, y)
}
func mapAt(doc object, keys ...string) object {
	var v any = doc
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	m, _ := v.(map[string]any)
	return m
}
func jsonChild(b []byte, n *jsonSpan, segment string) (*jsonSpan, error) {
	if !strings.HasPrefix(segment, "[") {
		return n.members[segment], nil
	}
	if !strings.HasSuffix(segment, "]") || b[n.start] != '[' {
		return nil, errors.New("invalid ownership selector")
	}
	criteria := strings.Split(segment[1:len(segment)-1], ",")
	var found *jsonSpan
	for _, row := range n.items {
		matches := true
		for _, criterion := range criteria {
			pair := strings.SplitN(criterion, "=", 2)
			if len(pair) != 2 {
				return nil, errors.New("invalid ownership selector")
			}
			field := row.members[pair[0]]
			if field == nil {
				matches = false
				break
			}
			var v any
			if json5.Unmarshal(b[field.start:field.end], &v) != nil || fmt.Sprint(v) != pair[1] {
				matches = false
				break
			}
		}
		if matches {
			if found != nil {
				return nil, ErrConflict
			}
			found = row
		}
	}
	return found, nil
}
func jsonNodeAt(b []byte, path []string) (*jsonSpan, error) {
	var v any
	if json5.Unmarshal(b, &v) != nil {
		return nil, errors.New("invalid JSON source")
	}
	scanner := scanner{b: b}
	node, e := scanner.value()
	if e != nil {
		return nil, e
	}
	for _, part := range path {
		node, e = jsonChild(b, node, part)
		if e != nil || node == nil {
			return node, e
		}
	}
	return node, nil
}

// Remove one owned member/row and only its separating comma. Comments and
// unrelated source bytes stay in place, including client-reformatted documents.
func jsonRemove(b []byte, path []string) ([]byte, error) {
	if len(path) == 0 {
		return nil, errors.New("cannot remove the JSON root")
	}
	parent, e := jsonNodeAt(b, path[:len(path)-1])
	if e != nil || parent == nil {
		return nil, ErrConflict
	}
	last := path[len(path)-1]
	node, e := jsonChild(b, parent, last)
	if e != nil || node == nil {
		return nil, ErrConflict
	}
	start := node.start
	if parent.members != nil {
		start = parent.entries[last]
	}
	type span struct{ start, end int }
	cuts := []span{{start, node.end}}
	next := scanner{b: b, i: node.end}
	next.space()
	if next.i < len(b) && b[next.i] == ',' {
		cuts = append(cuts, span{next.i, next.i + 1})
	} else {
		var previous *jsonSpan
		children := parent.items
		if parent.members != nil {
			children = nil
			for _, child := range parent.members {
				children = append(children, child)
			}
		}
		for _, child := range children {
			if child.end < start && (previous == nil || previous.end < child.end) {
				previous = child
			}
		}
		if previous != nil {
			prior := scanner{b: b, i: previous.end}
			prior.space()
			if prior.i >= len(b) || b[prior.i] != ',' {
				return nil, ErrConflict
			}
			cuts = append(cuts, span{prior.i, prior.i + 1})
		}
	}
	sort.Slice(cuts, func(i, j int) bool { return cuts[i].start > cuts[j].start })
	result := append([]byte{}, b...)
	for _, cut := range cuts {
		result = append(result[:cut.start], result[cut.end:]...)
	}
	var valid any
	if json5.Unmarshal(result, &valid) != nil {
		return nil, ErrConflict
	}
	return result, nil
}
func removeIntroducedJSON(b, original []byte, path []string) ([]byte, error) {
	out, e := jsonRemove(b, path)
	if e != nil {
		return nil, e
	}
	if len(bytes.TrimSpace(original)) == 0 {
		original = []byte("{}")
	} // Empty originals have no owned ancestors.
	for depth := len(path) - 1; depth > 0; depth-- {
		prefix := path[:depth]
		old, e := jsonNodeAt(original, prefix)
		if e != nil {
			return nil, e
		}
		if old != nil {
			break
		}
		current, e := jsonNodeAt(out, prefix)
		if e != nil {
			return nil, e
		}
		if current == nil {
			continue
		}
		if !((out[current.start] == '{' && len(current.members) == 0) || (out[current.start] == '[' && len(current.items) == 0)) {
			break
		}
		out, e = jsonRemove(out, prefix)
		if e != nil {
			return nil, e
		}
	}
	return out, nil
}
func projectedCline(row object) object {
	data, _ := json.Marshal(row)
	var result object
	_ = json.Unmarshal(data, &result)
	if settings, ok := result["settings"].(map[string]any); ok {
		delete(result, "updatedAt")
		delete(settings, "model")
	}
	return result
}
func projectedDroid(row object) object {
	data, _ := json.Marshal(row)
	var result object
	_ = json.Unmarshal(data, &result)
	delete(result, "id")
	delete(result, "index")
	return result
}

// Only client-documented derived fields are ignored. Endpoint/authentication,
// catalog membership, context and unknown fields remain protected.
func restoreOwned(client string, b Backup, current []byte) ([]byte, bool, error) {
	if b.Removed {
		if current != nil {
			return nil, false, ErrConflict
		}
		return b.Before, !b.Existed, nil
	}
	if result, e := restoreBytes(b, current); e == nil && (b.Existed || bytes.Equal(current, b.After)) {
		return result, !b.Existed, nil
	}
	if client == "codex" {
		return restoreCodexNative(b, current)
	}
	if client != "cline" && client != "droid" {
		return nil, false, ErrConflict
	}
	expected, e := parseDocument(b.After, JSON)
	if e != nil {
		return nil, false, e
	}
	observed, e := parseDocument(current, JSON)
	if e != nil {
		return nil, false, e
	}
	var paths [][]string
	switch client {
	case "cline":
		want := mapAt(expected, "providers", providerID)
		actual := mapAt(observed, "providers", providerID)
		if want == nil || actual == nil {
			return nil, false, ErrConflict
		}
		if !semanticEqual(projectedCline(want), projectedCline(actual)) {
			return nil, false, ErrConflict
		}
		paths = append(paths, []string{"providers", providerID})
	case "droid":
		rows, ok := expected["customModels"].([]any)
		if !ok {
			return nil, false, ErrConflict
		}
		for _, raw := range rows {
			row, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			label, ok := row["displayName"].(string)
			if !ok || !strings.HasPrefix(label, "AIPRouter: ") {
				continue
			}
			model, ok := row["model"].(string)
			if !ok {
				return nil, false, ErrConflict
			}
			s, e := selector("model", model)
			if e != nil {
				return nil, false, e
			}
			s = strings.TrimSuffix(s, "]") + ",displayName=" + label + "]"
			path := []string{"customModels", s}
			if len(bytes.TrimSpace(b.Before)) > 0 {
				prior, e := jsonNodeAt(b.Before, path)
				if e != nil {
					return nil, false, e
				}
				if prior != nil {
					continue
				}
			}
			node, e := jsonNodeAt(current, path)
			if e != nil || node == nil {
				return nil, false, ErrConflict
			}
			var actual object
			if json5.Unmarshal(current[node.start:node.end], &actual) != nil || !semanticEqual(projectedDroid(row), projectedDroid(actual)) {
				return nil, false, ErrConflict
			}
			paths = append(paths, path)
		}
		if len(paths) == 0 {
			return nil, false, ErrConflict
		}
	}
	result := current
	for _, path := range paths {
		var original *jsonSpan
		if len(bytes.TrimSpace(b.Before)) > 0 {
			original, e = jsonNodeAt(b.Before, path)
			if e != nil {
				return nil, false, e
			}
		}
		if original != nil {
			var value any
			if json5.Unmarshal(b.Before[original.start:original.end], &value) != nil {
				return nil, false, ErrConflict
			}
			result, e = jsonPatch(result, path, value, true)
		} else {
			result, e = removeIntroducedJSON(result, b.Before, path)
		}
		if e != nil {
			return nil, false, e
		}
	}
	// New files may now contain foreign data. Remove them only if the document
	// left after removing owned contributions matches the original empty envelope.
	if !b.Existed {
		remainder, e := parseDocument(result, JSON)
		if e != nil {
			return nil, false, e
		}
		envelope := object{}
		if client == "cline" {
			envelope["version"] = float64(1)
			if _, ok := expected["modes"]; ok {
				envelope["modes"] = object{}
			}
		}
		if semanticEqual(remainder, envelope) || len(remainder) == 0 {
			return nil, true, nil
		}
	}
	return result, false, nil
}
func preserveClineSelection(before []byte, fragment *Fragment, models []ClientModel) {
	doc, e := parseDocument(before, JSON)
	if e != nil {
		return
	}
	row := mapAt(doc, "providers", providerID)
	if row == nil {
		return
	}
	value, ok := fragment.Value.(map[string]any)
	if !ok {
		return
	}
	if stamp, ok := row["updatedAt"]; ok {
		value["updatedAt"] = stamp
	}
	old := mapAt(row, "settings")
	chosen, ok := old["model"].(string)
	if !ok {
		return
	}
	for _, m := range models {
		if m.ID == chosen {
			value["settings"].(map[string]any)["model"] = chosen
			break
		}
	}
}
