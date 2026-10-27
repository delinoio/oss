// SPDX-License-Identifier: Apache-2.0
// MiMo markup restoration adapted from opencodex; see LICENSE.upstream.
package commandcode

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

const heldLimit = 64 << 10

var envelope = regexp.MustCompile(`(?s)^<tool_call>\s*<function=([^>\s]+)>(.*)</tool_call>$`)
var parameter = regexp.MustCompile(`(?s)<parameter=([^>]+)>(.*?)</parameter>`)

type blockState uint8

const (
	probing blockState = iota
	held
	queued
	dropped
	streaming
)

type textBlock struct {
	id          string
	text        strings.Builder
	probe       string
	state       blockState
	interrupted bool
	candidates  map[string]bool
	late        bool
}
type pendingEvent struct {
	block *textBlock
	event object
}
type toolFilter struct {
	enabled    bool
	freeform   map[string]bool
	tools      map[string]object
	blocks     map[string]*textBlock
	openInputs map[string]string
	held       []*textBlock
	pending    []pendingEvent
	bytes      int
}

func newToolFilter(in inference.AdapterInput) *toolFilter {
	f := &toolFilter{enabled: strings.HasPrefix(strings.ToLower(CanonicalModel(in.Target.Model)), "xiaomi/mimo-"), tools: map[string]object{}, freeform: map[string]bool{}, blocks: map[string]*textBlock{}, openInputs: map[string]string{}}
	if !f.enabled {
		return f
	}
	m, err := chatInput(in)
	if err != nil {
		return f
	}
	for _, raw := range arr(m["tools"]) {
		fn := obj(obj(raw)["function"])
		if obj(obj(fn["extra_content"])["aiprouter"])["custom_tool"] == true {
			f.freeform[str(fn["name"])] = true
		}
	}
	tools, _ := wireTools(m)
	for _, v := range tools {
		t := obj(v)
		// Schema validation uses finite JSON numbers; original request schema bytes
		// remain untouched in Build.
		raw, _ := json.Marshal(t["input_schema"])
		var schema object
		_ = json.Unmarshal(raw, &schema)
		f.tools[str(t["name"])] = schema
	}
	return f
}
func (f *toolFilter) newBlock(id string, interrupted bool) *textBlock {
	b := &textBlock{id: id, state: probing, interrupted: interrupted, candidates: map[string]bool{}}
	for id := range f.openInputs {
		b.candidates[id] = true
	}
	f.blocks[id] = b
	return b
}
func (f *toolFilter) drain() []object {
	var out []object
	index := 0
	for _, p := range f.pending {
		if p.block != nil && (p.block.state == held || p.block.state == probing) {
			break
		}
		index++
		if p.block == nil || p.block.state != dropped {
			out = append(out, p.event)
		}
	}
	f.pending = append([]pendingEvent(nil), f.pending[index:]...)
	if len(f.pending) == 0 {
		f.bytes = 0
	}
	return out
}
func (f *toolFilter) boundary(except string) []object {
	for id, b := range f.blocks {
		if id != except && b.state == probing {
			b.state = queued
			b.interrupted = true
			b.text.Reset()
		}
	}
	return f.drain()
}
func (f *toolFilter) append(b *textBlock, e object) []object {
	raw, _ := json.Marshal(e)
	size := len(raw)
	var out []object
	if f.bytes+size > heldLimit {
		out, _ = f.finish(false)
		if b != nil {
			b.state = queued
			b.interrupted = true
			b.text.Reset()
		}
	}
	f.pending = append(f.pending, pendingEvent{b, e})
	f.bytes += size
	return append(out, f.drain()...)
}
func probe(b *textBlock, text string) {
	if b.state != probing {
		return
	}
	for _, c := range text {
		if b.probe == "" && strings.TrimSpace(string(c)) == "" {
			continue
		}
		b.probe += string(c)
		if !strings.HasPrefix("<tool_call>", b.probe) {
			b.state = queued
			b.text.Reset()
			return
		}
		if b.probe == "<tool_call>" {
			b.state = held
			b.probe = ""
			return
		}
	}
}
func marker(text string) int {
	if i := strings.Index(text, "<tool_call>"); i >= 0 {
		return i
	}
	for n := min(len(text), len("<tool_call>")-1); n > 0; n-- {
		if strings.HasSuffix(text, "<tool_call>"[:n]) {
			return len(text) - n
		}
	}
	return -1
}
func (f *toolFilter) textDelta(id, text string) []object {
	out := f.boundary(id)
	b := f.blocks[id]
	if b == nil {
		b = f.newBlock(id, false)
	}
	old := b.state
	probe(b, text)
	if b.state == held && old != held {
		f.held = append(f.held, b)
	}
	if b.state == streaming || b.state == queued {
		if i := marker(text); i >= 0 {
			if i > 0 {
				out = append(out, f.append(b, object{"type": "text-delta", "text": text[:i]})...)
			}
			b = f.newBlock(id, true)
			text = text[i:]
			probe(b, text)
			if b.state == held {
				f.held = append(f.held, b)
			}
		}
	}
	if b.state == dropped {
		b = f.newBlock(id, true)
		b.state = queued
	}
	if b.state == probing || b.state == held {
		b.text.WriteString(text)
	}
	out = append(out, f.append(b, object{"type": "text-delta", "text": text})...)
	if b.state == queued && len(f.pending) == 0 {
		b.state = streaming
	}
	return out
}
func (f *toolFilter) end(id string) []object {
	b := f.blocks[id]
	delete(f.blocks, id)
	if b != nil && b.state == probing {
		b.state = queued
		b.text.Reset()
	}
	return f.drain()
}
func (f *toolFilter) event(e object) []object {
	kind := str(e["type"])
	if !f.enabled {
		if kind == "text-delta" || kind == "reasoning-delta" || kind == "tool-call" {
			return []object{e}
		}
		return nil
	}
	id := str(e["id"])
	// Metadata-only frames must not grow the block/input registries without the
	// held-text byte accounting. Fall back to presentation text at this bound.
	if len(id) > 1024 || len(str(e["toolName"])) > 1024 || len(f.blocks)+len(f.openInputs) >= 1024 {
		released, _ := f.finish(false)
		f.enabled = false
		return append(released, f.event(e)...)
	}
	switch kind {
	case "text-start":
		out := f.end(id)
		out = append(out, f.boundary(id)...)
		f.newBlock(id, false)
		return out
	case "text-end":
		return f.end(id)
	case "text-delta":
		return f.textDelta(id, str(e["text"]))
	case "tool-input-start":
		out := f.boundary("\x00boundary")
		name := str(e["toolName"])
		f.openInputs[id] = name
		for _, b := range f.held {
			if b.interrupted && len(b.candidates) > 0 && !b.late && looseName(b.text.String()) == name {
				b.candidates[id] = true
				b.late = true
			}
		}
		return out
	case "tool-call":
		out := f.boundary("\x00boundary")
		callID, name := str(e["toolCallId"]), str(e["toolName"])
		delete(f.openInputs, callID)
		input := e["input"]
		if input == nil {
			input = e["args"]
		}
		remaining := []*textBlock{}
		for _, b := range f.held {
			if b.state != held {
				continue
			}
			if len(b.candidates) > 0 && !b.candidates[callID] {
				remaining = append(remaining, b)
				continue
			}
			markup, valid := parseMarkup(b.text.String())
			if valid && markup.name == name && matchesMarkup(markup, input) {
				b.state = dropped
				b.text.Reset()
				continue
			}
			delete(b.candidates, callID)
			if len(b.candidates) == 0 {
				if !b.interrupted && !valid && f.tools[name] != nil && looseName(b.text.String()) == name {
					b.state = dropped
				} else {
					b.state = queued
				}
				b.text.Reset()
			} else {
				remaining = append(remaining, b)
			}
		}
		f.held = remaining
		return append(out, f.append(nil, e)...)
	case "reasoning-delta":
		out := f.boundary("\x00boundary")
		return append(out, f.append(nil, e)...)
	default:
		return f.boundary("\x00boundary")
	}
}
func (f *toolFilter) finish(clean bool) ([]object, bool) {
	out := []object{}
	salvaged := false
	for _, p := range f.pending {
		b := p.block
		if b == nil {
			out = append(out, p.event)
			continue
		}
		if b.state == held {
			markup, valid := parseMarkup(b.text.String())
			schema := f.tools[markup.name]
			if clean && !b.interrupted && valid && schema != nil {
				if args, ok := restoreMarkup(markup.body, schema, f.freeform[markup.name]); ok {
					out = append(out, object{"type": "tool-call", "toolCallId": uuid(), "toolName": markup.name, "input": args})
					b.state = dropped
					salvaged = true
				}
			}
			if b.state == held {
				if clean && !b.interrupted && !valid && f.tools[looseName(b.text.String())] != nil {
					b.state = dropped
				} else {
					b.state = queued
				}
			}
		}
		if b.state != dropped {
			out = append(out, p.event)
		}
	}
	f.pending = nil
	f.held = nil
	f.bytes = 0
	f.blocks = map[string]*textBlock{}
	f.openInputs = map[string]string{}
	return out, salvaged
}

type markup struct {
	name, body, raw string
	params          map[string]string
}

func looseName(text string) string {
	m := envelope.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil || strings.Contains(m[2], "<tool_call>") {
		return ""
	}
	return m[1]
}
func parseMarkup(text string) (markup, bool) {
	m := envelope.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return markup{}, false
	}
	v := markup{name: m[1], body: m[2]}
	body := m[2]
	if strings.Contains(body, "<tool_call>") || strings.Contains(body, "</tool_call>") || strings.Contains(body, "<function=") {
		return markup{}, false
	}
	closed := strings.HasSuffix(strings.TrimSpace(body), "</function>")
	if closed {
		body = strings.TrimSuffix(strings.TrimRight(body, " \t\r\n"), "</function>")
	}
	if !strings.Contains(body, "<parameter=") {
		v.raw = trimNewlines(strings.TrimSuffix(body, "</parameter>"))
		return v, true
	}
	if !closed || strings.TrimSpace(parameter.ReplaceAllString(body, "")) != "" {
		return markup{}, false
	}
	v.params = map[string]string{}
	for _, p := range parameter.FindAllStringSubmatch(body, -1) {
		key := strings.TrimSpace(p[1])
		if key == "" {
			return markup{}, false
		}
		if _, exists := v.params[key]; exists {
			return markup{}, false
		}
		v.params[key] = trimNewlines(p[2])
	}
	return v, len(v.params) > 0
}
func matchesMarkup(m markup, input any) bool {
	if m.params == nil {
		if s, ok := input.(string); ok {
			return strings.TrimSpace(s) == strings.TrimSpace(m.raw)
		}
		o := obj(input)
		if len(o) != 1 {
			return false
		}
		for _, v := range o {
			s, ok := v.(string)
			return ok && strings.TrimSpace(s) == strings.TrimSpace(m.raw)
		}
		return false
	}
	if s, ok := input.(string); ok {
		if json.Unmarshal([]byte(s), &input) != nil {
			return false
		}
	}
	o := obj(input)
	if len(o) != len(m.params) {
		return false
	}
	for key, raw := range m.params {
		expected, ok := o[key]
		if !ok {
			return false
		}
		if s, ok := expected.(string); ok {
			if s != raw {
				return false
			}
		} else {
			var v any
			if json.Unmarshal([]byte(raw), &v) != nil || !reflect.DeepEqual(v, expected) {
				return false
			}
		}
	}
	return true
}
func trimNewlines(s string) string {
	s = strings.TrimPrefix(s, "\r\n")
	s = strings.TrimPrefix(s, "\n")
	s = strings.TrimSuffix(s, "\r\n")
	return strings.TrimSuffix(s, "\n")
}
func restoreMarkup(body string, schema object, freeform bool) (object, bool) {
	closed := strings.HasSuffix(strings.TrimSpace(body), "</function>")
	if closed {
		body = strings.TrimSuffix(strings.TrimRight(body, " \t\r\n"), "</function>")
	}
	props := obj(schema["properties"])
	out := object{}
	if !strings.Contains(body, "<parameter=") {
		// Custom tools are lowered by the shared translator to a single input string.
		// Restrict raw restoration to that explicit shape, never an arbitrary function.
		p := obj(props["input"])
		if !freeform || len(props) != 1 || p["type"] != "string" {
			return nil, false
		}
		out["input"] = trimNewlines(strings.TrimSuffix(body, "</parameter>"))
	} else {
		if freeform || !closed || strings.TrimSpace(parameter.ReplaceAllString(body, "")) != "" {
			return nil, false
		}
		matches := parameter.FindAllStringSubmatch(body, -1)
		if len(matches) == 0 {
			return nil, false
		}
		for _, m := range matches {
			key := strings.TrimSpace(m[1])
			if _, exists := out[key]; exists {
				return nil, false
			}
			rule, ok := props[key]
			if !ok {
				return nil, false
			}
			raw := trimNewlines(m[2])
			var value any
			if obj(rule)["type"] == "string" || json.Unmarshal([]byte(raw), &value) != nil || !validSchema(value, rule, 0) {
				value = raw
			}
			if !validSchema(value, rule, 0) {
				return nil, false
			}
			out[key] = value
		}
	}
	return out, supportedSchema(schema, 0) && validSchema(out, schema, 0)
}

// Unknown schema assertions fail closed: text is never promoted to an executable
// call merely because the adapter does not understand a constraint.
func validSchema(v, raw any, depth int) bool {
	if depth > 16 {
		return false
	}
	if b, ok := raw.(bool); ok {
		return b
	}
	s := obj(raw)
	if s == nil {
		return false
	}
	for k := range s {
		switch k {
		case "$schema", "$id", "title", "description", "default", "examples", "deprecated", "readOnly", "writeOnly", "type", "enum", "const", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf", "minLength", "maxLength", "minItems", "maxItems", "uniqueItems", "items", "minProperties", "maxProperties", "properties", "required", "additionalProperties", "anyOf", "oneOf", "allOf":
		default:
			return false
		}
	}
	if t, ok := s["type"]; ok {
		types := arr(t)
		if text, ok := t.(string); ok {
			types = []any{text}
		}
		matched := false
		for _, typ := range types {
			if matchesType(v, str(typ)) {
				matched = true
			}
		}
		if !matched {
			return false
		}
	}
	if e, ok := s["enum"]; ok {
		matched := false
		for _, item := range arr(e) {
			if reflect.DeepEqual(v, item) {
				matched = true
			}
		}
		if !matched {
			return false
		}
	}
	if c, ok := s["const"]; ok && !reflect.DeepEqual(v, c) {
		return false
	}
	for _, key := range []string{"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf", "minLength", "maxLength", "minItems", "maxItems", "minProperties", "maxProperties"} {
		limit, exists := s[key]
		if !exists {
			continue
		}
		n, ok := limit.(float64)
		if !ok {
			return false
		}
		actual, numeric := v.(float64)
		switch key {
		case "minLength", "maxLength":
			text, ok := v.(string)
			if !ok {
				return false
			}
			actual = float64(len([]rune(text)))
			numeric = true
		case "minItems", "maxItems":
			a, ok := v.([]any)
			if !ok {
				return false
			}
			actual = float64(len(a))
			numeric = true
		case "minProperties", "maxProperties":
			o := obj(v)
			if o == nil {
				return false
			}
			actual = float64(len(o))
			numeric = true
		}
		if !numeric {
			return false
		}
		switch key {
		case "minimum", "minLength", "minItems", "minProperties":
			if actual < n {
				return false
			}
		case "maximum", "maxLength", "maxItems", "maxProperties":
			if actual > n {
				return false
			}
		case "exclusiveMinimum":
			if actual <= n {
				return false
			}
		case "exclusiveMaximum":
			if actual >= n {
				return false
			}
		case "multipleOf":
			if n <= 0 || actual/n != float64(int64(actual/n)) {
				return false
			}
		}
	}
	if rule, ok := s["items"]; ok {
		a, ok := v.([]any)
		if !ok {
			return false
		}
		for _, item := range a {
			if !validSchema(item, rule, depth+1) {
				return false
			}
		}
	}
	if unique, ok := s["uniqueItems"]; ok {
		b, ok := unique.(bool)
		a, isArray := v.([]any)
		if !ok || !isArray {
			return false
		}
		if b {
			for i, x := range a {
				for _, y := range a[i+1:] {
					if reflect.DeepEqual(x, y) {
						return false
					}
				}
			}
		}
	}
	o := obj(v)
	if required, ok := s["required"]; ok {
		a, valid := required.([]any)
		if !valid || o == nil {
			return false
		}
		for _, key := range a {
			if _, ok := o[str(key)]; !ok {
				return false
			}
		}
	}
	properties := obj(s["properties"])
	if _, ok := s["properties"]; ok && (o == nil || properties == nil) {
		return false
	}
	for key, item := range o {
		if rule, ok := properties[key]; ok {
			if !validSchema(item, rule, depth+1) {
				return false
			}
		} else if rule, ok := s["additionalProperties"]; ok && !validSchema(item, rule, depth+1) {
			return false
		}
	}
	for _, key := range []string{"anyOf", "oneOf", "allOf"} {
		if raw, ok := s[key]; ok {
			branches := arr(raw)
			if len(branches) == 0 {
				return false
			}
			count := 0
			for _, b := range branches {
				if validSchema(v, b, depth+1) {
					count++
				}
			}
			if key == "anyOf" && count < 1 || key == "oneOf" && count != 1 || key == "allOf" && count != len(branches) {
				return false
			}
		}
	}
	return true
}
func matchesType(v any, t string) bool {
	switch t {
	case "null":
		return v == nil
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "object":
		return obj(v) != nil
	case "array":
		_, ok := v.([]any)
		return ok
	case "number":
		_, ok := v.(float64)
		return ok
	case "integer":
		n, ok := v.(float64)
		return ok && n >= -9007199254740991 && n <= 9007199254740991 && n == float64(int64(n))
	}
	return false
}

func supportedSchema(raw any, depth int) bool {
	if depth > 16 {
		return false
	}
	if _, ok := raw.(bool); ok {
		return true
	}
	s := obj(raw)
	if s == nil {
		return false
	}
	for key, v := range s {
		switch key {
		case "$schema", "$id", "title", "description", "default", "examples", "deprecated", "readOnly", "writeOnly", "type", "enum", "const", "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf", "minLength", "maxLength", "minItems", "maxItems", "uniqueItems", "minProperties", "maxProperties", "required":
		case "properties":
			p := obj(v)
			if p == nil {
				return false
			}
			for _, child := range p {
				if !supportedSchema(child, depth+1) {
					return false
				}
			}
		case "items", "additionalProperties":
			if !supportedSchema(v, depth+1) {
				return false
			}
		case "anyOf", "oneOf", "allOf":
			a := arr(v)
			if len(a) == 0 {
				return false
			}
			for _, child := range a {
				if !supportedSchema(child, depth+1) {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}
