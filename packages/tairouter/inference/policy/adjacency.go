// SPDX-License-Identifier: Apache-2.0
package policy

import "sort"

// Ambiguous histories remain unchanged: adjacency must not invent tool attribution.
func adjacent(in []any) []any {
	calls, outputs := map[string][]int{}, map[string][]int{}
	for i, x := range in {
		m := obj(x)
		id := str(m["call_id"])
		if id == "" {
			continue
		}
		switch m["type"] {
		case "function_call", "local_shell_call":
			calls["f:"+id] = append(calls["f:"+id], i)
		case "custom_tool_call":
			calls["c:"+id] = append(calls["c:"+id], i)
		case "function_call_output":
			outputs["f:"+id] = append(outputs["f:"+id], i)
		case "custom_tool_call_output":
			outputs["c:"+id] = append(outputs["c:"+id], i)
		}
	}
	type pair struct{ call, output int }
	pairs := []pair{}
	for k, c := range calls {
		o := outputs[k]
		if len(c) != 1 || len(o) != 1 || o[0] <= c[0] {
			return in
		}
		pairs = append(pairs, pair{c[0], o[0]})
	}
	for k, o := range outputs {
		if len(calls[k]) != 1 || len(o) != 1 {
			return in
		}
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].call < pairs[j].call })
	batches := map[int][]any{}
	moved := map[int]bool{}
	for cursor := 0; cursor < len(pairs); {
		end := cursor + 1
		first := pairs[cursor].output
		for end < len(pairs) && pairs[end].call < first {
			if pairs[end].output < first {
				first = pairs[end].output
			}
			end++
		}
		batch := []any{}
		for i := cursor; i < end; i++ {
			if i > cursor && pairs[i].output < pairs[i-1].output {
				return in
			}
			batch = append(batch, in[pairs[i].call])
			moved[pairs[i].call] = true
		}
		for i := cursor; i < end; i++ {
			batch = append(batch, in[pairs[i].output])
			moved[pairs[i].output] = true
		}
		batches[pairs[cursor].call] = batch
		cursor = end
	}
	out := []any{}
	for i, x := range in {
		out = append(out, batches[i]...)
		if !moved[i] {
			out = append(out, x)
		}
	}
	return out
}
