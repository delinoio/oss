// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/pelletier/go-toml/v2"
	json5 "github.com/titanous/json5"
	"gopkg.in/yaml.v3"
	"strings"
	"unicode/utf8"
)

type Format string

const (
	JSON  Format = "json"
	JSON5 Format = "json5"
	YAML  Format = "yaml"
	TOML  Format = "toml"
)

type Fragment struct {
	Path  []string
	Value any
}

// JSON/JSON5 source spans let us preserve every unrelated byte, including
// comments, numeric spelling and whitespace. The decoder validates semantics.
type jsonSpan struct {
	start, end int
	members    map[string]*jsonSpan
	entries    map[string]int
	items      []*jsonSpan
}
type scanner struct {
	b        []byte
	i, depth int
}

func (s *scanner) space() {
	for s.i < len(s.b) {
		if strings.ContainsRune(" \t\r\n", rune(s.b[s.i])) {
			s.i++
			continue
		}
		if s.i+1 < len(s.b) && s.b[s.i] == '/' {
			if s.b[s.i+1] == '/' {
				for s.i < len(s.b) && s.b[s.i] != '\n' {
					s.i++
				}
				continue
			}
			if s.b[s.i+1] == '*' {
				s.i += 2
				for s.i+1 < len(s.b) && !(s.b[s.i] == '*' && s.b[s.i+1] == '/') {
					s.i++
				}
				s.i += 2
				continue
			}
		}
		break
	}
}
func (s *scanner) quoted() error {
	q := s.b[s.i]
	s.i++
	for s.i < len(s.b) {
		c := s.b[s.i]
		s.i++
		if c == '\\' {
			s.i++
			continue
		}
		if c == q {
			return nil
		}
	}
	return errors.New("unterminated string")
}
func (s *scanner) value() (*jsonSpan, error) {
	s.space()
	if s.i >= len(s.b) || s.depth > 256 {
		return nil, errors.New("invalid or deeply nested JSON")
	}
	s.depth++
	defer func() { s.depth-- }()
	n := &jsonSpan{start: s.i}
	c := s.b[s.i]
	switch c {
	case '{':
		n.members = map[string]*jsonSpan{}
		n.entries = map[string]int{}
		s.i++
		s.space()
		for s.i < len(s.b) && s.b[s.i] != '}' {
			start := s.i
			if s.b[s.i] == '"' || s.b[s.i] == '\'' {
				if e := s.quoted(); e != nil {
					return nil, e
				}
			} else {
				for s.i < len(s.b) && !strings.ContainsRune(": \t\n\r", rune(s.b[s.i])) {
					s.i++
				}
			}
			raw := s.b[start:s.i]
			var key string
			if len(raw) > 0 && (raw[0] == '"' || raw[0] == '\'') {
				if e := json5.Unmarshal(raw, &key); e != nil {
					return nil, e
				}
			} else {
				key = string(raw)
			}
			if _, ok := n.members[key]; ok {
				return nil, errors.New("duplicate JSON key")
			}
			s.space()
			if s.i >= len(s.b) || s.b[s.i] != ':' {
				return nil, errors.New("expected colon")
			}
			s.i++
			child, e := s.value()
			if e != nil {
				return nil, e
			}
			n.members[key] = child
			n.entries[key] = start
			s.space()
			if s.i < len(s.b) && s.b[s.i] == ',' {
				s.i++
				s.space()
			} else {
				break
			}
		}
		if s.i >= len(s.b) || s.b[s.i] != '}' {
			return nil, errors.New("expected object end")
		}
		s.i++
	case '[':
		s.i++
		s.space()
		for s.i < len(s.b) && s.b[s.i] != ']' {
			v, e := s.value()
			if e != nil {
				return nil, e
			}
			n.items = append(n.items, v)
			s.space()
			if s.i < len(s.b) && s.b[s.i] == ',' {
				s.i++
				s.space()
			} else {
				break
			}
		}
		if s.i >= len(s.b) || s.b[s.i] != ']' {
			return nil, errors.New("expected array end")
		}
		s.i++
	case '"', '\'':
		if e := s.quoted(); e != nil {
			return nil, e
		}
	default:
		for s.i < len(s.b) && !strings.ContainsRune(",]} \n\r\t", rune(s.b[s.i])) {
			s.i++
		}
	}
	n.end = s.i
	return n, nil
}
func parseDocument(b []byte, f Format) (map[string]any, error) {
	var doc map[string]any
	var e error
	switch f {
	case JSON, JSON5:
		var value any
		e = json5.Unmarshal(b, &value)
		if e == nil {
			if m, ok := value.(map[string]any); ok {
				doc = m
			} else if rows, ok := value.([]any); ok {
				doc = map[string]any{"$sequence": rows}
			} else if value != nil {
				e = errors.New("invalid JSON root")
			}
		}
	case YAML:
		var value any
		e = yaml.Unmarshal(b, &value)
		if e == nil {
			if mapping, ok := value.(map[string]any); ok {
				doc = mapping
			} else if sequence, ok := value.([]any); ok {
				doc = map[string]any{"$sequence": sequence}
			} else if value != nil {
				e = errors.New("invalid YAML root")
			}
		}
	case TOML:
		e = toml.Unmarshal(b, &doc)
	default:
		e = errors.New("unknown config format")
	}
	if e != nil {
		return nil, errors.New("configuration cannot be parsed safely")
	}
	if doc == nil {
		doc = map[string]any{}
	}
	return doc, nil
}
func wrap(path []string, v any) any {
	for i := len(path) - 1; i >= 0; i-- {
		if strings.HasPrefix(path[i], "[") {
			v = []any{v}
		} else {
			v = map[string]any{path[i]: v}
		}
	}
	return v
}
func jsonPatch(b []byte, path []string, value any, replace bool) ([]byte, error) {
	var valid any
	if json5.Unmarshal(b, &valid) != nil {
		return nil, errors.New("invalid JSON configuration")
	}
	s := scanner{b: b}
	n, e := s.value()
	if e != nil {
		return nil, e
	}
	for i, k := range path {
		if strings.HasPrefix(k, "[") && strings.HasSuffix(k, "]") {
			if b[n.start] != '[' {
				return nil, errors.New("managed selector parent is not an array")
			}
			criteria := strings.Split(k[1:len(k)-1], ",")
			var matches []*jsonSpan
			for _, item := range n.items {
				match := true
				for _, criterion := range criteria {
					pair := strings.SplitN(criterion, "=", 2)
					if len(pair) != 2 {
						return nil, errors.New("invalid array selector")
					}
					field := item.members[pair[0]]
					if field == nil {
						match = false
						break
					}
					var actual any
					if json5.Unmarshal(b[field.start:field.end], &actual) != nil || fmt.Sprint(actual) != pair[1] {
						match = false
						break
					}
				}
				if match {
					matches = append(matches, item)
				}
			}
			if len(matches) > 1 {
				return nil, errors.New("ambiguous client row")
			}
			if len(matches) == 1 {
				if i == len(path)-1 {
					if !replace {
						return nil, errors.New("client row already exists without ownership")
					}
					encoded, e := json.Marshal(value)
					if e != nil {
						return nil, e
					}
					child := matches[0]
					return append(append(append([]byte{}, b[:child.start]...), encoded...), b[child.end:]...), nil
				}
				n = matches[0]
				continue
			}
			if i != len(path)-1 {
				return nil, errors.New("missing array selector parent")
			}
			encoded, e := json.Marshal(value)
			if e != nil {
				return nil, e
			}
			if len(n.items) > 0 {
				encoded = append(encoded, ',')
			}
			insert := n.start + 1
			return append(append(append([]byte{}, b[:insert]...), encoded...), b[insert:]...), nil
		}
		if n.members == nil {
			return nil, errors.New("managed JSON parent is not an object")
		}
		child, ok := n.members[k]
		if ok {
			if i == len(path)-1 {
				if !replace {
					return nil, errors.New("client contribution already exists without ownership")
				}
				encoded, e := json.Marshal(value)
				if e != nil {
					return nil, e
				}
				return append(append(append([]byte{}, b[:child.start]...), encoded...), b[child.end:]...), nil
			}
			n = child
			continue
		}
		encoded, e := json.Marshal(wrap(path[i+1:], value))
		if e != nil {
			return nil, e
		}
		key, _ := json.Marshal(k)
		insert := n.end - 1
		// Inserting before the first member avoids mistaking a trailing comment
		// for the last comma, and leaves all existing member bytes untouched.
		insert = n.start + 1
		addition := append(append([]byte("\n  "), key...), []byte(": ")...)
		addition = append(addition, encoded...)
		if len(n.members) > 0 {
			addition = append(addition, ',')
		}
		addition = append(addition, '\n')
		return append(append(append([]byte{}, b[:insert]...), addition...), b[insert:]...), nil
	}
	return nil, errors.New("empty managed path")
}
func lineOffsets(b []byte) []int {
	r := []int{0}
	for i, c := range b {
		if c == '\n' {
			r = append(r, i+1)
		}
	}
	return r
}

// yaml.v3 reports columns in Unicode characters, while source splices use bytes.
func yamlSourceOffset(b []byte, line, column int) (int, error) {
	offsets := lineOffsets(b)
	if line < 1 || line > len(offsets) || column < 1 {
		return 0, errors.New("invalid YAML source position")
	}
	start := offsets[line-1]
	end := len(b)
	if line < len(offsets) {
		end = offsets[line] - 1
	}
	for current := 1; start < end; current++ {
		if current == column {
			return start, nil
		}
		r, size := utf8.DecodeRune(b[start:end])
		if r == utf8.RuneError && size == 1 {
			return 0, errors.New("invalid YAML source encoding")
		}
		start += size
	}
	return 0, errors.New("invalid YAML source position")
}

func yamlPatch(b []byte, path []string, value any, replace bool) ([]byte, error) {
	var root yaml.Node
	if yaml.Unmarshal(b, &root) != nil || len(root.Content) != 1 {
		return nil, errors.New("invalid YAML configuration")
	}
	n := root.Content[0]
	offsets := lineOffsets(b)
	for i, k := range path {
		if n.Anchor != "" {
			return nil, errors.New("managed YAML container owns a shared anchor; select an unshared contribution")
		}
		if n.Kind == yaml.AliasNode {
			// Detach only this alias occurrence. Editing its anchor would change
			// every other consumer of the shared user configuration.
			if n.Alias == nil {
				return nil, errors.New("invalid YAML alias")
			}
			var resolved any
			if e := n.Alias.Decode(&resolved); e != nil {
				return nil, errors.New("cannot resolve managed YAML alias")
			}
			encoded, e := json.Marshal(resolved)
			if e != nil {
				return nil, errors.New("managed YAML alias cannot be represented safely")
			}
			replacement, e := yamlPatch(encoded, path[i:], value, replace)
			if e != nil {
				return nil, e
			}
			start, e := yamlSourceOffset(b, n.Line, n.Column)
			if e != nil {
				return nil, e
			}
			token := []byte("*" + n.Value)
			if start+len(token) > len(b) || !bytes.Equal(b[start:start+len(token)], token) {
				return nil, errors.New("managed YAML alias source changed")
			}
			out := append([]byte{}, b[:start]...)
			out = append(out, replacement...)
			out = append(out, b[start+len(token):]...)
			var check yaml.Node
			if yaml.Unmarshal(out, &check) != nil {
				return nil, errors.New("invalid detached YAML alias")
			}
			return out, nil
		}
		if strings.HasPrefix(k, "[") && strings.HasSuffix(k, "]") {
			if n.Kind != yaml.SequenceNode {
				return nil, errors.New("managed YAML selector requires a block sequence")
			}
			pair := strings.SplitN(k[1:len(k)-1], "=", 2)
			if len(pair) != 2 {
				return nil, errors.New("invalid YAML selector")
			}
			var found *yaml.Node
			for _, item := range n.Content {
				if item.Kind != yaml.MappingNode {
					return nil, errors.New("invalid YAML sequence entry")
				}
				for j := 0; j < len(item.Content); j += 2 {
					if item.Content[j].Value == pair[0] && item.Content[j+1].Value == pair[1] {
						if found != nil {
							return nil, errors.New("ambiguous YAML selector")
						}
						found = item
					}
				}
			}
			if found != nil {
				if i < len(path)-1 {
					n = found
					continue
				}
				if !replace {
					return nil, errors.New("client row already exists without ownership")
				}
				return nil, errors.New("restore the owned YAML row before replacement")
			}
			var row any = value
			if i != len(path)-1 {
				row = wrap(path[i+1:], value)
				mapping, ok := row.(map[string]any)
				if !ok {
					return nil, errors.New("unsupported YAML selector nesting")
				}
				mapping[pair[0]] = pair[1]
			}
			encoded, e := yaml.Marshal([]any{row})
			if e != nil {
				return nil, e
			}
			if n.Style&yaml.FlowStyle != 0 {
				return yamlFlowInsert(b, n, row, len(n.Content) > 0)
			}
			encoded = indentLines(encoded, n.Column-1)
			start := offsets[n.Line-1]
			return append(append(append([]byte{}, b[:start]...), encoded...), b[start:]...), nil
		}
		if n.Kind == yaml.MappingNode && n.Style&yaml.FlowStyle != 0 {
			found := -1
			for j := 0; j < len(n.Content); j += 2 {
				if n.Content[j].Value == k {
					found = j
					break
				}
			}
			if found >= 0 {
				if i < len(path)-1 {
					n = n.Content[found+1]
					continue
				}
				return nil, errors.New("client contribution already exists without ownership")
			}
			return yamlFlowInsert(b, n, wrap(path[i:], value), len(n.Content) > 0)
		}
		if n.Kind != yaml.MappingNode || n.Style&yaml.FlowStyle != 0 {
			return nil, errors.New("managed YAML parent must use a block mapping")
		}
		found := -1
		for j := 0; j < len(n.Content); j += 2 {
			if n.Content[j].Value == k {
				found = j
				break
			}
		}
		if found >= 0 {
			if i < len(path)-1 {
				n = n.Content[found+1]
				continue
			}
			if !replace {
				return nil, errors.New("client contribution already exists without ownership")
			}
			start := offsets[n.Content[found].Line-1]
			end := len(b)
			indent := n.Content[found].Column - 1
			for line := n.Content[found].Line; line < len(offsets); line++ {
				text := strings.TrimRight(string(b[offsets[line]:]), "\r\n")
				if ix := strings.IndexByte(text, '\n'); ix >= 0 {
					text = text[:ix]
				}
				trim := strings.TrimSpace(text)
				if trim != "" && !strings.HasPrefix(trim, "#") && len(text)-len(strings.TrimLeft(text, " ")) <= indent {
					end = offsets[line]
					break
				}
			}
			encoded, e := yaml.Marshal(map[string]any{k: value})
			if e != nil {
				return nil, e
			}
			encoded = indentLines(encoded, indent)
			return append(append(append([]byte{}, b[:start]...), encoded...), b[end:]...), nil
		}
		encoded, e := yaml.Marshal(wrap(path[i:], value))
		if e != nil {
			return nil, e
		}
		indent := n.Column - 1
		encoded = indentLines(encoded, indent)
		end := len(b)
		// Find the end of this mapping by the next non-comment line at a lower indent.
		for line := n.Line; line < len(offsets); line++ {
			row := b[offsets[line]:]
			if ix := bytes.IndexByte(row, '\n'); ix >= 0 {
				row = row[:ix]
			}
			trim := bytes.TrimSpace(row)
			if len(trim) > 0 && trim[0] != '#' && len(row)-len(bytes.TrimLeft(row, " ")) < indent {
				end = offsets[line]
				break
			}
		}
		if end > 0 && b[end-1] != '\n' {
			encoded = append([]byte{'\n'}, encoded...)
		}
		return append(append(append([]byte{}, b[:end]...), encoded...), b[end:]...), nil
	}
	return nil, errors.New("empty YAML path")
}
func indentLines(b []byte, n int) []byte {
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	for i := range lines {
		lines[i] = strings.Repeat(" ", n) + lines[i]
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

// TOML insertion uses complete new tables; changing an existing owned table is
// performed after restoring its saved source contribution. No whole-file render.
func tomlPatch(b []byte, path []string, value any, replace bool) ([]byte, error) {
	doc, e := parseDocument(b, TOML)
	if e != nil {
		return nil, e
	}
	var v any = doc
	for _, k := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, errors.New("TOML parent is not a table")
		}
		v = m[k]
		if v == nil {
			break
		}
	}
	if v != nil {
		return nil, errors.New("TOML contribution already exists; restore the owned contribution before replacing")
	}
	encoded, e := toml.Marshal(wrap(path, value))
	if e != nil {
		return nil, e
	}
	var out []byte
	if len(path) == 1 {
		if _, isMap := value.(map[string]any); !isMap {
			out = append(append([]byte{}, encoded...), b...)
		}
	}
	if out == nil {
		// Marshal emits parent table headers. Existing parent tables must not
		// be declared twice; retain only newly introduced child headers.
		lines := strings.Split(string(encoded), "\n")
		var kept []string
		for _, line := range lines {
			skip := false
			for i := 1; i < len(path); i++ {
				plain := "[" + strings.Join(path[:i], ".") + "]"
				if line == plain {
					var cur any = doc
					for _, k := range path[:i] {
						m, ok := cur.(map[string]any)
						if !ok {
							cur = nil
							break
						}
						cur = m[k]
					}
					skip = cur != nil
				}
			}
			if !skip {
				kept = append(kept, line)
			}
		}
		out = append(append(append([]byte{}, b...), '\n'), []byte(strings.Join(kept, "\n"))...)
	}
	if _, e = parseDocument(out, TOML); e != nil {
		return nil, fmt.Errorf("TOML source-preserving insertion refused: %w", e)
	}
	return out, nil
}
func Patch(b []byte, f Format, fragments []Fragment, replace bool) ([]byte, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		switch f {
		case JSON, JSON5:
			b = []byte("{}\n")
		case YAML:
			b = []byte("{}\n")
		case TOML:
			b = []byte{}
		}
	}
	var e error
	for _, p := range fragments {
		switch f {
		case JSON, JSON5:
			b, e = jsonPatch(b, p.Path, p.Value, replace)
		case YAML:
			if string(bytes.TrimSpace(b)) == "{}" {
				b, e = yaml.Marshal(wrap(p.Path, p.Value))
			} else {
				b, e = yamlPatch(b, p.Path, p.Value, replace)
			}
		case TOML:
			b, e = tomlPatch(b, p.Path, p.Value, replace)
		}
		if e != nil {
			return nil, e
		}
	}
	if _, e = parseDocument(b, f); e != nil {
		return nil, e
	}
	return b, nil
}

// JSON fragments are valid YAML flow values. Insert only the new member/row;
// retain every byte of existing flow content, including comments and quotes.
func yamlFlowInsert(b []byte, n *yaml.Node, value any, comma bool) ([]byte, error) {
	if n.Anchor != "" {
		return nil, errors.New("managed YAML flow container is shared by an anchor")
	}
	encoded, e := json.Marshal(value)
	if e != nil {
		return nil, e
	}
	if n.Kind == yaml.MappingNode {
		encoded = encoded[1 : len(encoded)-1]
	}
	if comma {
		encoded = append(encoded, ',')
	}
	start, e := yamlSourceOffset(b, n.Line, n.Column)
	if e != nil {
		return nil, e
	}
	if start >= len(b) || (b[start] != '{' && b[start] != '[') {
		return nil, errors.New("managed YAML flow source changed")
	}
	start++
	out := append([]byte{}, b[:start]...)
	out = append(out, encoded...)
	out = append(out, b[start:]...)
	var check yaml.Node
	if yaml.Unmarshal(out, &check) != nil {
		return nil, errors.New("invalid YAML flow insertion")
	}
	return out, nil
}
