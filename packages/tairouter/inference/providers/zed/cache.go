// SPDX-License-Identifier: Apache-2.0
package zed

// Zed does not document Anthropic's one-hour default. Generated breakpoints use
// ephemeral without ttl (five minutes), matching the pinned hosted delegate.
func applyShortCache(body object) {
	if s, ok := body["system"].(string); ok {
		body["system"] = []any{object{"type": "text", "text": s}}
	}
	tools, system := arrayOf(body["tools"]), arrayOf(body["system"])
	var users, messageBlocks []any
	external := false
	for _, v := range arrayOf(body["messages"]) {
		m := objectOf(v)
		blocks := arrayOf(m["content"])
		messageBlocks = append(messageBlocks, blocks...)
		if m["role"] == "user" {
			users = append(users, m)
		}
		for _, b := range blocks {
			if objectOf(b)["cache_control"] != nil {
				external = true
			}
		}
	}
	mark := func(blocks []any, preferText bool) {
		if len(blocks) == 0 {
			return
		}
		selected := objectOf(blocks[len(blocks)-1])
		if preferText {
			for i := len(blocks) - 1; i >= 0; i-- {
				if m := objectOf(blocks[i]); m["type"] == "text" {
					selected = m
					break
				}
			}
		}
		if selected != nil {
			selected["cache_control"] = object{"type": "ephemeral"}
		}
	}
	if !external {
		mark(tools, false)
		mark(system, false)
		start := len(users) - 2
		if start < 0 {
			start = 0
		}
		for _, v := range users[start:] {
			mark(arrayOf(objectOf(v)["content"]), true)
		}
	}
	// Preserve at most four explicit breakpoints, preferring stable tools/system.
	ordered := append(append(append([]any{}, messageBlocks...), system...), tools...)
	count := 0
	for _, b := range ordered {
		if objectOf(b)["cache_control"] != nil {
			count++
		}
	}
	for _, b := range ordered {
		if count <= 4 {
			break
		}
		if m := objectOf(b); m["cache_control"] != nil {
			delete(m, "cache_control")
			count--
		}
	}
}
