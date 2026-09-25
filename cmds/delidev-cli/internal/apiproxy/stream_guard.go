package apiproxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type guardedPattern struct {
	value   string
	failure []int
}
type streamGuard struct {
	patterns []guardedPattern
	states   map[string][]int
	pending  [][]byte
	size     int
}

func newStreamGuard(guard secretGuard) *streamGuard {
	s := &streamGuard{states: map[string][]int{}}
	seen := map[string]bool{}
	for _, value := range guard.values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		p := guardedPattern{value: value, failure: make([]int, len(value))}
		for i, j := 1, 0; i < len(value); i++ {
			for j > 0 && value[i] != value[j] {
				j = p.failure[j-1]
			}
			if value[i] == value[j] {
				j++
			}
			p.failure[i] = j
		}
		s.patterns = append(s.patterns, p)
	}
	return s
}

// Track decoded strings at stable JSON paths across native delta events. KMP
// bounds work linearly even for long keys and adversarial repeated prefixes.
// Frames containing an unresolved key prefix wait until it is disproved or the
// native stream ends, so a later rejection cannot leak an already-sent prefix.
func (s *streamGuard) check(path, value string) error {
	if len(path) > 4096 {
		return errInvalidDocument
	}
	state := s.states[path]
	if state == nil {
		state = make([]int, len(s.patterns))
	}
	active := false
	for i, p := range s.patterns {
		position := state[i]
		for offset := 0; offset < len(value); offset++ {
			for position > 0 && value[offset] != p.value[position] {
				position = p.failure[position-1]
			}
			if value[offset] == p.value[position] {
				position++
			}
			if position == len(p.value) {
				return errSecret
			}
		}
		state[i] = position
		active = active || position > 0
	}
	if active {
		s.states[path] = state
	} else {
		delete(s.states, path)
	}
	if len(s.states) > 1024 {
		return errInvalidDocument
	}
	return nil
}
func (s *streamGuard) inspect(raw []byte) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var walk func(string, int) error
	walk = func(path string, depth int) error {
		if depth > 64 || len(path) > 4096 {
			return errInvalidDocument
		}
		token, err := d.Token()
		if err != nil {
			return errInvalidDocument
		}
		if value, ok := token.(string); ok {
			return s.check(path, value)
		}
		if value, ok := token.(json.Number); ok {
			// Keep the native spelling, including precision and exponent syntax.
			return s.check(path, value.String())
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return errInvalidDocument
				}
				name, ok := key.(string)
				if !ok {
					return errInvalidDocument
				}
				// Keys are forwarded content too. Match decoded names before JSON
				// pointer escaping, both globally and at each original parent path;
				// repeated enclosing keys must not reset a nested fragment match.
				if err := s.check("@json-field-names"+path, name); err != nil {
					return err
				}
				if err := s.check("@json-field-sequence", name); err != nil {
					return err
				}
				name = strings.ReplaceAll(strings.ReplaceAll(name, "~", "~0"), "/", "~1")
				if err = walk(path+"/"+name, depth+1); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		case '[':
			for index := 0; d.More(); index++ {
				if err := walk(fmt.Sprintf("%s/%d", path, index), depth+1); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		default:
			return errInvalidDocument
		}
	}
	if err := walk("", 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errInvalidDocument
	}
	return nil
}
func (s *streamGuard) deliver(frame []byte, terminal bool, write func([]byte) error) error {
	if len(s.pending) > 0 || len(s.states) > 0 {
		if s.size+len(frame) > maxBody {
			return errInvalidDocument
		}
		s.pending = append(s.pending, frame)
		s.size += len(frame)
		if len(s.states) > 0 && !terminal {
			return nil
		}
		for _, item := range s.pending {
			if err := write(item); err != nil {
				return err
			}
			clear(item)
		}
		s.pending = nil
		s.size = 0
		return nil
	}
	return write(frame)
}
func (s *streamGuard) clear() {
	for _, item := range s.pending {
		clear(item)
	}
	s.pending = nil
}

// SSE comments and metadata can also reflect protected values across frames.
// Track both each field and the metadata sequence, independently of JSON paths,
// before any original frame bytes reach the client (including data-less frames).
func (s *streamGuard) inspectMetadata(frame []byte) error {
	for _, raw := range bytes.Split(frame, []byte("\n")) {
		line := bytes.TrimSuffix(raw, []byte("\r"))
		if len(line) == 0 {
			continue
		}
		field, value, _ := bytes.Cut(line, []byte(":"))
		if string(field) == "data" {
			continue
		}
		// Unknown field names are forwarded verbatim too. Keep their byte
		// sequence separate from values so changing names cannot evade matching.
		if err := s.check("@sse-field-names", string(field)); err != nil {
			return err
		}
		value = bytes.TrimPrefix(value, []byte(" "))
		if err := s.check("@sse/"+string(field), string(value)); err != nil {
			return err
		}
		if err := s.check("@sse-metadata", string(value)); err != nil {
			return err
		}
	}
	return nil
}
