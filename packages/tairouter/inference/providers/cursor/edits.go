// SPDX-License-Identifier: Apache-2.0
// Structured-edit conversion adapted from pinned MIT source; see LICENSE.upstream.
package cursor

import (
	"encoding/json"
	"reflect"
	"strings"
)

type editPair struct{ old, new string }

func stringArg(m map[string]any, keys ...string) (string, bool) {
	for _, key := range keys {
		if s, ok := m[key].(string); ok {
			return s, true
		}
		if a, ok := m[key].([]any); ok && len(a) > 0 {
			parts := make([]string, 0, len(a))
			for _, v := range a {
				s, ok := v.(string)
				if !ok {
					return "", false
				}
				parts = append(parts, s)
			}
			return strings.Join(parts, "\n"), true
		}
	}
	return "", false
}
func editLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}
func lineBlock(hay, needle string) int {
	h, n := editLines(hay), editLines(needle)
	if len(n) == 0 {
		return -1
	}
	for i := 0; i+len(n) <= len(h); i++ {
		if reflect.DeepEqual(h[i:i+len(n)], n) {
			return i
		}
	}
	return -1
}
func replaceBlock(hay, needle, replacement string) string {
	at := lineBlock(hay, needle)
	if at < 0 {
		return hay
	}
	h, n := editLines(hay), editLines(needle)
	out := append(append(append([]string(nil), h[:at]...), editLines(replacement)...), h[at+len(n):]...)
	return strings.Join(out, "\n")
}
func structuredPatch(name string, args map[string]any) (string, error) {
	fail := func() (string, error) { return "", protocolError("cursor_invalid_structured_edit") }
	path, _ := stringArg(args, "file_path", "filePath", "path", "filepath", "filename", "file", "target_file", "targetFile", "target_path", "targetPath")
	path = strings.ReplaceAll(strings.TrimSpace(path), "\\", "/")
	for strings.HasPrefix(path, "./") {
		path = strings.TrimPrefix(path, "./")
	}
	if path == "" || strings.ContainsAny(path, "\r\n\x00") || args["replace_all"] == true || args["replaceAll"] == true {
		return fail()
	}
	if args["delete_file"] == true || args["deleteFile"] == true {
		return "*** Begin Patch\n*** Delete File: " + path + "\n*** End Patch", nil
	}
	records := []any{args}
	if name == "multi_edit" {
		edits := args["edits"]
		if text, ok := edits.(string); ok {
			if json.Unmarshal([]byte(text), &edits) != nil {
				return fail()
			}
		}
		records = array(edits)
	}
	if len(records) == 0 || len(records) > 500 {
		return fail()
	}
	var folded []editPair
	for _, record := range records {
		m := object(record)
		if m == nil || m["replace_all"] == true || m["replaceAll"] == true {
			return fail()
		}
		old, ok := stringArg(m, "old_string", "oldString", "oldtext", "old_text", "old_content", "oldContent", "before", "search")
		if !ok {
			return fail()
		}
		next, ok := stringArg(m, "new_string", "newString", "newtext", "new_text", "contents", "content", "new_contents", "newContents", "after", "replace")
		if !ok {
			return fail()
		}
		absorbed := false
		for i := len(folded) - 1; i >= 0; i-- {
			prior := &folded[i]
			if lineBlock(prior.new, old) >= 0 {
				prior.new = replaceBlock(prior.new, old, next)
				absorbed = true
				break
			}
			if lineBlock(old, prior.new) >= 0 {
				prior.old = replaceBlock(old, prior.new, prior.old)
				prior.new = next
				absorbed = true
				break
			}
		}
		if !absorbed {
			folded = append(folded, editPair{old, next})
		}
	}
	lines := []string{"*** Begin Patch"}
	if len(folded) == 1 && folded[0].old == "" {
		added := editLines(folded[0].new)
		if len(added) == 0 {
			return fail()
		}
		lines = append(lines, "*** Add File: "+path)
		for _, line := range added {
			lines = append(lines, "+"+line)
		}
	} else {
		lines = append(lines, "*** Update File: "+path)
		for i, pair := range folded {
			if pair.old == "" {
				return fail()
			}
			for j, other := range folded {
				if i != j && lineBlock(pair.old, other.old) >= 0 {
					return fail()
				}
			}
			old, next := editLines(pair.old), editLines(pair.new)
			if reflect.DeepEqual(old, next) {
				return fail()
			}
			// Restore accidental stripped indentation, but retain intentional changes
			// where all non-whitespace content remains identical.
			if len(old) == len(next) {
				same := true
				for k := range old {
					if strings.TrimLeft(old[k], " \t") != strings.TrimLeft(next[k], " \t") {
						same = false
					}
				}
				if !same {
					for k := range next {
						lead := old[k][:len(old[k])-len(strings.TrimLeft(old[k], " \t"))]
						if next[k] != "" && len(next[k]) == len(strings.TrimLeft(next[k], " \t")) {
							next[k] = lead + next[k]
						}
					}
				}
			}
			lines = append(lines, "@@")
			for _, line := range old {
				lines = append(lines, "-"+line)
			}
			for _, line := range next {
				lines = append(lines, "+"+line)
			}
		}
	}
	lines = append(lines, "*** End Patch")
	return strings.Join(lines, "\n"), nil
}

func syntheticEdits(tools []tool, choice any) ([]tool, error) {
	selection := str(choice)
	if choice != nil && selection != "auto" && selection != "required" {
		return nil, nil
	}
	found := false
	existing := map[string]bool{}
	for _, t := range tools {
		existing[t.Name] = true
		if t.Name == "apply_patch" && t.Freeform {
			found = true
		}
	}
	if !found {
		return nil, nil
	}
	var out []tool
	for _, name := range []string{"edit_file", "multi_edit"} {
		if existing[name] {
			continue
		}
		props := map[string]any{"file_path": map[string]any{"type": "string"}, "old_string": map[string]any{"type": "string"}, "new_string": map[string]any{"type": "string"}}
		required := []any{"file_path", "old_string", "new_string"}
		if name == "multi_edit" {
			props = map[string]any{"file_path": map[string]any{"type": "string"}, "edits": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"old_string": map[string]any{"type": "string"}, "new_string": map[string]any{"type": "string"}}, "required": []any{"old_string", "new_string"}}}}
			required = []any{"file_path", "edits"}
		}
		schema := map[string]any{"type": "object", "properties": props, "required": required}
		value, err := encodeValue(schema, 0)
		if err != nil {
			return nil, err
		}
		def := join(ps(1, name), ps(2, "Replace exact text, preserving whitespace. Converted to the client's apply_patch tool; never executed by the router."), pb(3, value), ps(4, toolProvider), ps(5, name))
		out = append(out, tool{Name: "apply_patch", WireName: name, Synthetic: true, Definition: def})
	}
	return out, nil
}
