// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

type terminalFrame struct {
	event                  string
	data                   []byte
	boundary, partial, eof bool
	err                    error
}
type terminalPolicyBody struct {
	reader *io.PipeReader
	source io.ReadCloser
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func (b *terminalPolicyBody) Read(p []byte) (int, error) { return b.reader.Read(p) }
func (b *terminalPolicyBody) Close() error {
	b.once.Do(func() { b.cancel(); b.reader.Close(); b.source.Close(); <-b.done })
	return nil
}

// nextTerminalBlock accepts CR, LF and CRLF but never promotes an unframed tail.
func nextTerminalBlock(b []byte, eof bool) (int, int, bool) {
	line := 0
	for i := 0; i < len(b); i++ {
		if b[i] != '\r' && b[i] != '\n' {
			continue
		}
		end := i + 1
		if b[i] == '\r' {
			if end == len(b) && !eof {
				return 0, 0, false
			}
			if end < len(b) && b[end] == '\n' {
				end++
			}
		}
		if i == line {
			return line, end, true
		}
		line = end
		i = end - 1
	}
	return 0, 0, false
}
func terminalPayload(block []byte) (string, []byte) {
	text := strings.ReplaceAll(strings.ReplaceAll(string(block), "\r\n", "\n"), "\r", "\n")
	var data []string
	event := ""
	for _, line := range strings.Split(text, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimPrefix(value, " ")
		if key == "data" {
			data = append(data, value)
		} else if key == "event" {
			event = value
		}
	}
	return event, []byte(strings.Join(data, "\n"))
}
func produceTerminalFrames(ctx context.Context, source io.ReadCloser, out chan<- terminalFrame) {
	defer close(out)
	send := func(f terminalFrame) bool {
		select {
		case out <- f:
			return true
		case <-ctx.Done():
			return false
		}
	}
	pending := []byte{}
	chunk := make([]byte, 4096)
	for {
		n, e := source.Read(chunk)
		if n > 0 {
			if !send(terminalFrame{boundary: true, partial: true}) {
				return
			}
			pending = append(pending, chunk[:n]...)
		}
		for {
			end, next, ok := nextTerminalBlock(pending, e == io.EOF)
			if !ok {
				break
			}
			event, data := terminalPayload(pending[:end])
			pending = append(pending[:0], pending[next:]...)
			if len(data) > 2<<20 {
				send(terminalFrame{err: &Error{502, "stream_event_too_large", 0}})
				return
			}
			if len(data) > 0 && !send(terminalFrame{event: event, data: data}) {
				return
			}
		}
		if len(pending) > 2<<20 {
			send(terminalFrame{err: &Error{502, "stream_event_too_large", 0}})
			return
		}
		if e != nil {
			if e == io.EOF {
				send(terminalFrame{eof: true, partial: len(pending) > 0})
			} else {
				send(terminalFrame{err: e})
			}
			return
		}
		if !send(terminalFrame{boundary: true, partial: len(pending) > 0}) {
			return
		}
	}
}

type responsesGraph struct {
	created           object
	added, done       map[int]object
	sequence          int
	bytes             int
	tainted, terminal bool
}

func graphIndex(v any) (int, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, e := n.Int64()
	return int(i), e == nil && i >= 0 && i <= 4095
}
func (g *responsesGraph) observe(m object, size int) {
	if n, ok := m["sequence_number"].(json.Number); ok {
		if value, e := n.Int64(); e == nil && value > int64(g.sequence) && value < 1<<53 {
			g.sequence = int(value)
		}
	}
	switch str(m["type"]) {
	case "response.completed", "response.incomplete", "response.failed", "error":
		g.terminal = true
	case "response.created":
		if response := obj(m["response"]); response != nil {
			g.created = response
			g.bytes += size
		} else {
			g.tainted = true
		}
	case "response.output_item.added", "response.output_item.done":
		i, ok := graphIndex(m["output_index"])
		item := obj(m["item"])
		if !ok || item == nil {
			g.tainted = true
			return
		}
		if str(m["type"]) == "response.output_item.added" {
			if g.added[i] != nil || g.done[i] != nil {
				g.tainted = true
			} else {
				g.added[i] = object{"type": item["type"], "id": item["id"]}
			}
		} else {
			opened := g.added[i]
			if opened == nil || g.done[i] != nil || str(opened["type"]) != str(item["type"]) || str(opened["id"]) != str(item["id"]) {
				g.tainted = true
			}
			g.done[i] = item
		}
		g.bytes += size
	}
	if g.bytes > 32<<20 {
		g.tainted = true
	}
}
func completeGraphItem(item object) bool {
	if item["status"] != "completed" || str(item["id"]) == "" {
		return false
	}
	switch str(item["type"]) {
	case "function_call":
		if str(item["call_id"]) == "" || str(item["name"]) == "" {
			return false
		}
		_, e := decode([]byte(str(item["arguments"])))
		return e == nil
	case "message", "reasoning":
		if item["type"] == "message" && item["role"] != "assistant" {
			return false
		}
		content, ok := item["content"].([]any)
		if !ok {
			return false
		}
		kind := "output_text"
		if item["type"] == "reasoning" {
			kind = "reasoning_text"
		}
		for _, v := range content {
			part := obj(v)
			if part["type"] != kind {
				return false
			}
			if _, ok := part["text"].(string); !ok {
				return false
			}
		}
		return true
	}
	return false
}
func (g *responsesGraph) complete() bool {
	if g.tainted || g.terminal || g.created == nil || len(g.done) == 0 || len(g.done) != len(g.added) {
		return false
	}
	for i, item := range g.done {
		if g.added[i] == nil || !completeGraphItem(item) {
			return false
		}
	}
	return true
}
func (g *responsesGraph) synthetic() object {
	kind := "incomplete"
	if g.complete() {
		kind = "completed"
	}
	out := clone(g.created)
	out["status"] = kind
	out["completed_at"] = time.Now().Unix()
	indices := []int{}
	for i := range g.done {
		indices = append(indices, i)
	}
	sort.Ints(indices)
	items := []any{}
	for _, i := range indices {
		items = append(items, g.done[i])
	}
	out["output"] = items
	if kind == "incomplete" {
		out["incomplete_details"] = object{"reason": "missing_terminal_event"}
	}
	return object{"type": "response." + kind, "response": out, "sequence_number": g.sequence + 1}
}

type eofChatCalls struct {
	calls                 map[string]object
	bytes                 int
	finish, text, tainted bool
}

func (c *eofChatCalls) observe(m object) {
	if m["error"] != nil {
		c.tainted = true
	}
	for ci, v := range arr(m["choices"]) {
		choice := obj(v)
		if str(choice["finish_reason"]) != "" {
			c.finish = true
		}
		d := obj(choice["delta"])
		if str(d["content"]) != "" {
			c.text = true
		}
		for _, raw := range arr(d["tool_calls"]) {
			call := obj(raw)
			i, ok := graphIndex(call["index"])
			if !ok {
				c.tainted = true
				continue
			}
			key := fmt.Sprintf("%d/%d", ci, i)
			current := c.calls[key]
			if current == nil {
				if len(c.calls) >= 4096 {
					c.tainted = true
					c.bytes = 2<<20 + 1
					return
				}
				current = object{}
				c.calls[key] = current
			}
			fn := obj(call["function"])
			for field, value := range map[string]string{"id": str(call["id"]), "name": str(fn["name"]), "arguments": str(fn["arguments"])} {
				c.bytes += len(value)
				if c.bytes > 2<<20 {
					c.tainted = true
					return
				}
				current[field] = str(current[field]) + value
			}
		}
	}
}
func (c *eofChatCalls) complete() bool {
	if c.tainted {
		return false
	}
	for _, call := range c.calls {
		if str(call["id"]) == "" || str(call["name"]) == "" {
			return false
		}
		if _, e := decode([]byte(str(call["arguments"]))); e != nil {
			return false
		}
	}
	return c.finish || c.text || len(c.calls) > 0
}
func wrapTerminalPolicy(parent context.Context, source io.ReadCloser, t Target) io.ReadCloser {
	if t.Policy == nil {
		return source
	}
	grace := time.Duration(0)
	tolerate := false
	if t.Wire == Responses {
		if p := t.Policy.ResponsesTerminalRepair(); p != nil && p.GraceMS > 0 && p.GraceMS <= 60000 {
			grace = time.Duration(p.GraceMS) * time.Millisecond
		}
	}
	if t.Wire == Chat {
		if p := t.Policy.ChatEOFTolerance(); p != nil {
			tolerate = *p
		}
	}
	if grace == 0 && !tolerate {
		return source
	}
	ctx, cancel := context.WithCancel(parent)
	reader, writer := io.Pipe()
	result := &terminalPolicyBody{reader: reader, source: source, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(result.done)
		defer writer.Close()
		defer source.Close()
		defer cancel()
		frames := make(chan terminalFrame)
		producerDone := make(chan struct{})
		go func() { defer close(producerDone); produceTerminalFrames(ctx, source, frames) }()
		defer func() { cancel(); source.Close(); <-producerDone }()
		graph := responsesGraph{added: map[int]object{}, done: map[int]object{}, sequence: -1}
		chat := eofChatCalls{calls: map[string]object{}}
		realTerminal := false
		var timer *time.Timer
		var quiet <-chan time.Time
		stop := func() {
			if timer != nil {
				timer.Stop()
			}
			quiet = nil
		}
		defer stop()
		emit := func(event string, data []byte) error {
			if event != "" {
				if _, e := fmt.Fprintf(writer, "event: %s\n", safeEventName(event)); e != nil {
					return e
				}
			}
			_, e := fmt.Fprintf(writer, "data: %s\n\n", strings.ReplaceAll(string(data), "\n", "\ndata: "))
			return e
		}
		synthetic := func() error { m := graph.synthetic(); data, _ := json.Marshal(m); return emit(str(m["type"]), data) }
		for {
			select {
			case <-ctx.Done():
				writer.CloseWithError(ctx.Err())
				return
			case <-quiet:
				if graph.complete() {
					if e := synthetic(); e != nil {
						writer.CloseWithError(e)
					}
					return
				}
				stop()
			case f, ok := <-frames:
				if !ok {
					return
				}
				if f.err != nil {
					writer.CloseWithError(f.err)
					return
				}
				if f.boundary {
					stop()
					if grace > 0 && !f.partial && graph.complete() {
						timer = time.NewTimer(grace)
						quiet = timer.C
					}
					continue
				}
				if f.eof {
					if f.partial {
						graph.tainted = true
						chat.tainted = true
					}
					if !realTerminal && grace > 0 {
						if e := synthetic(); e != nil {
							writer.CloseWithError(e)
						}
					} else if !realTerminal && tolerate && chat.complete() {
						if e := emit("", []byte("[DONE]")); e != nil {
							writer.CloseWithError(e)
						}
					}
					return
				}
				stop()
				if string(f.data) == "[DONE]" {
					if grace > 0 && !realTerminal {
						if e := synthetic(); e != nil {
							writer.CloseWithError(e)
							return
						}
					}
					if e := emit(f.event, f.data); e != nil {
						writer.CloseWithError(e)
					}
					return
				}
				m, e := decode(f.data)
				if e != nil {
					graph.tainted = true
					chat.tainted = true
				} else {
					if grace > 0 {
						graph.observe(m, len(f.data))
						realTerminal = graph.terminal
					}
					if tolerate {
						chat.observe(m)
					}
				}
				if graph.bytes > 32<<20 || chat.bytes > 2<<20 {
					writer.CloseWithError(&Error{502, "terminal_repair_limit", 0})
					return
				}
				if e := emit(f.event, f.data); e != nil {
					writer.CloseWithError(e)
					return
				}
				if graph.terminal {
					return
				}
			}
		}
	}()
	return result
}
