// SPDX-License-Identifier: Apache-2.0
package zed

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// Namespace groups are declarations, not callable tools. The pinned delegates
// flatten their wire names with __ and resolve an unqualified historical name
// only when the current catalog gives it a unique meaning.
func flattenToolGroups(b object) error {
	declared := map[string]bool{}
	byBare := map[string][]string{}
	var flatten func([]any, string, int) ([]any, error)
	flatten = func(tools []any, namespace string, depth int) ([]any, error) {
		if depth > 32 {
			return nil, requestError("zed_tool_group_depth")
		}
		out := []any{}
		for _, v := range tools {
			t := objectOf(v)
			if t["type"] == "namespace" {
				ns := stringOf(t["name"])
				if namespace != "" {
					ns = namespace + "__" + ns
				}
				children, err := flatten(arrayOf(t["tools"]), ns, depth+1)
				if err != nil {
					return nil, err
				}
				out = append(out, children...)
				continue
			}
			f := objectOf(t["function"])
			if f == nil {
				f = t
			}
			name := stringOf(f["name"])
			wire := name
			if namespace != "" {
				wire = namespace + "__" + name
				f["name"] = wire
			}
			if name != "" {
				if declared[wire] {
					return nil, requestError("ambiguous_zed_tool_catalog")
				}
				declared[wire] = true
				byBare[name] = append(byBare[name], wire)
			}
			out = append(out, t)
		}
		return out, nil
	}
	if tools, ok := b["tools"].([]any); ok {
		flat, err := flatten(tools, "", 0)
		if err != nil {
			return err
		}
		b["tools"] = flat
	}
	visitToolNames(b, func(m object, k string) {
		name := stringOf(m[k])
		if name == "" {
			return
		}
		if ns := stringOf(m["namespace"]); ns != "" {
			m[k] = ns + "__" + name
			delete(m, "namespace")
			return
		}
		if !declared[name] && !strings.Contains(name, "__") && len(byBare[name]) == 1 {
			m[k] = byBare[name][0]
		}
	})
	return nil
}

// RequestOptions comes from authenticated routing, never from request JSON or
// arbitrary inbound headers. The executor owns thread/replay authorization.
type RequestOptions struct {
	ThreadID string
	// Nil uses the pinned direct-mode fallback; empty explicitly disables effort.
	ReasoningEfforts      []string
	ReasoningEffortMap    map[string]string
	ShowThinkingSummaries bool
}
type optionsKey struct{}
type stateKey struct{}
type requestState struct {
	mu           sync.Mutex
	aliases      map[string]string
	custom       map[string]bool
	built        bool
	showThinking bool
	threadID     string
	binding      [32]byte
}

func WithRequestOptions(ctx context.Context, options RequestOptions) context.Context {
	if options.ReasoningEfforts != nil {
		options.ReasoningEfforts = append([]string{}, options.ReasoningEfforts...)
	}
	m := make(map[string]string, len(options.ReasoningEffortMap))
	for k, v := range options.ReasoningEffortMap {
		m[k] = v
	}
	options.ReasoningEffortMap = m
	return context.WithValue(ctx, optionsKey{}, options)
}
func (*Adapter) PrepareContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, stateKey{}, &requestState{})
}
func requestOptions(ctx context.Context) RequestOptions {
	o, _ := ctx.Value(optionsKey{}).(RequestOptions)
	return o
}

var safeToolName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]{0,63}$`)

// Only visit protocol-owned tool locations. Never rewrite arbitrary tool arguments.
func visitToolNames(b object, visit func(object, string)) {
	for _, v := range arrayOf(b["tools"]) {
		t := objectOf(v)
		if f := objectOf(t["function"]); f != nil {
			visit(f, "name")
		} else {
			visit(t, "name")
		}
	}
	if c := objectOf(b["tool_choice"]); c != nil {
		if f := objectOf(c["function"]); f != nil {
			visit(f, "name")
		} else if custom := objectOf(c["custom"]); custom != nil {
			visit(custom, "name")
		} else {
			visit(c, "name")
		}
		for _, v := range arrayOf(c["tools"]) {
			visit(objectOf(v), "name")
		}
	}
	for _, v := range arrayOf(b["input"]) {
		m := objectOf(v)
		if m["type"] == "function_call" || m["type"] == "custom_tool_call" {
			visit(m, "name")
		}
	}
	for _, v := range arrayOf(b["messages"]) {
		m := objectOf(v)
		for _, v := range arrayOf(m["tool_calls"]) {
			c := objectOf(v)
			visit(objectOf(c["function"]), "name")
			visit(objectOf(c["custom"]), "name")
		}
		for _, v := range arrayOf(m["content"]) {
			p := objectOf(v)
			if p["type"] == "tool_use" {
				visit(p, "name")
			}
		}
	}
}

func aliasTools(b object) (map[string]string, map[string]bool) {
	aliases, custom := map[string]string{}, map[string]bool{}
	used := map[string]bool{}
	visitToolNames(b, func(m object, k string) {
		if name := stringOf(m[k]); name != "" {
			used[name] = true
		}
	})
	forward := map[string]string{}
	visitToolNames(b, func(m object, k string) {
		name := stringOf(m[k])
		if name == "" || safeToolName.MatchString(name) {
			return
		}
		wire := forward[name]
		if wire == "" {
			h := sha256.Sum256([]byte(name))
			wire = fmt.Sprintf("zed_%x", h[:20])
			for n := 0; used[wire]; n++ {
				wire = fmt.Sprintf("zed_%x_%d", h[:20], n)
			}
			used[wire] = true
			forward[name] = wire
			aliases[wire] = name
		}
		m[k] = wire
	})
	for _, v := range arrayOf(b["tools"]) {
		t := objectOf(v)
		if t["type"] == "custom" {
			custom[stringOf(t["name"])] = true
		}
	}
	return aliases, custom
}

func restoreNames(event object, aliases map[string]string, custom map[string]bool) {
	restore := func(m object) {
		name := stringOf(m["name"])
		if name == "" {
			return
		}
		if custom[name] {
			extra := objectOf(m["extra_content"])
			if extra == nil {
				extra = object{}
				m["extra_content"] = extra
			}
			extra["aiprouter"] = object{"custom_tool": true}
		}
		if original := aliases[name]; original != "" {
			m["name"] = original
		}
	}
	restore(objectOf(event["content_block"]))
	restore(objectOf(event["item"]))
	for _, v := range arrayOf(objectOf(event["response"])["output"]) {
		restore(objectOf(v))
	}
	for _, v := range arrayOf(event["choices"]) {
		for _, call := range arrayOf(objectOf(objectOf(v)["delta"])["tool_calls"]) {
			restore(objectOf(objectOf(call)["function"]))
		}
	}
}
