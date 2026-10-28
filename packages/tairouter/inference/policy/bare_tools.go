// SPDX-License-Identifier: Apache-2.0
package policy

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
)

const maxBareBytes = 4 * 1024 * 1024

var mimoModel = regexp.MustCompile(`(?i)(?:^|[/-])mimo-v2(?:\.|$)`)
var bareHeader = regexp.MustCompile(`^<tool_call>\s*<function=([^>\r\n]+)>`)
var parameterHeader = regexp.MustCompile(`^<parameter=[A-Za-z_$][\w$.-]*>`)

type bareCall struct {
	name, input string
	start, end  int
}

// recoverBare authorizes only declared freeform tools. Fenced examples, nested
// wrappers, identified wrappers and ordinary function tools never authorize calls.
func recoverBare(text string, tools map[string]string) []bareCall {
	if len(text) > maxBareBytes {
		return nil
	}
	calls := []bareCall{}
	fence := ""
	lineStart := true
	for i := 0; i < len(text); {
		if lineStart {
			end := strings.IndexByte(text[i:], '\n')
			if end < 0 {
				end = len(text) - i
			}
			line := strings.TrimLeft(text[i:i+end], " \t")
			if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
				mark := line[:3]
				if fence == "" {
					fence = mark
				} else if fence == mark {
					fence = ""
				}
				i += end
				if i < len(text) {
					i++
				}
				lineStart = true
				continue
			}
		}
		if fence != "" {
			lineStart = text[i] == '\n'
			i++
			continue
		}
		if text[i] == '`' {
			end := strings.IndexByte(text[i+1:], '`')
			if end >= 0 {
				i += end + 2
				lineStart = false
				continue
			}
		}
		if strings.HasPrefix(text[i:], "<tool_call_") {
			return nil
		}
		if !strings.HasPrefix(text[i:], "<tool_call>") {
			lineStart = text[i] == '\n'
			i++
			continue
		}
		match := bareHeader.FindStringSubmatchIndex(text[i:])
		if match == nil {
			return nil
		}
		bodyStart := i + match[1]
		name := strings.TrimSpace(text[i+match[2] : i+match[3]])
		close := strings.Index(text[bodyStart:], "</tool_call>")
		if close < 0 {
			return nil
		}
		end := bodyStart + close
		body := strings.TrimRight(text[bodyStart:end], " \r\n\t")
		body = strings.TrimSuffix(body, "</function>")
		body = strings.TrimRight(body, " \r\n\t")
		body = strings.TrimSuffix(body, "</parameter>")
		if strings.Contains(body, "<tool_call") || strings.Contains(body, "<function=") {
			return nil
		}
		body = freeformBody(body)
		if strings.HasPrefix(body, "<parameter=") {
			if parameterHeader.MatchString(body) {
				i = end + len("</tool_call>")
				continue
			}
			body = freeformBody(strings.TrimPrefix(body, "<parameter="))
		}
		if _, ok := tools[name]; ok && strings.TrimSpace(body) != "" && !strings.Contains(body, "<parameter=") && !strings.Contains(body, "</parameter>") {
			calls = append(calls, bareCall{name, body, i, end + len("</tool_call>")})
			if len(calls) > 128 {
				return nil
			}
		}
		i = end + len("</tool_call>")
		lineStart = false
	}
	return calls
}
func freeformBody(s string) string {
	s = strings.TrimPrefix(s, "\r\n")
	s = strings.TrimPrefix(s, "\n")
	return strings.TrimRight(s, " \t\r\n")
}
func repairedText(s string, calls []bareCall) string {
	var b strings.Builder
	at := 0
	for _, c := range calls {
		b.WriteString(s[at:c.start])
		at = c.end
	}
	b.WriteString(s[at:])
	return b.String()
}
func callJSON(c bareCall, tools map[string]string, index int) map[string]any {
	args, _ := json.Marshal(map[string]any{"input": c.input})
	return map[string]any{"id": "call_" + rand.Text(), "type": "function", "function": map[string]any{"name": tools[c.name], "arguments": string(args)}}
}

// RepairChatResponse repairs only MiMo bare calls when that choice emitted no
// structured call. freeformTools maps declared wire names to restored names.
func RepairChatResponse(body []byte, model string, freeformTools map[string]string) ([]byte, error) {
	if !mimoModel.MatchString(model) || len(freeformTools) == 0 {
		return body, nil
	}
	b, e := decode(body)
	if e != nil {
		return nil, e
	}
	changed := false
	for _, x := range list(b["choices"]) {
		choice := obj(x)
		m := obj(choice["message"])
		if len(list(m["tool_calls"])) > 0 {
			continue
		}
		text := str(m["content"])
		calls := recoverBare(text, freeformTools)
		if len(calls) == 0 {
			continue
		}
		out := []any{}
		for i, c := range calls {
			out = append(out, callJSON(c, freeformTools, i))
		}
		m["content"] = repairedText(text, calls)
		m["tool_calls"] = out
		choice["finish_reason"] = "tool_calls"
		changed = true
	}
	if !changed {
		return body, nil
	}
	return json.Marshal(b)
}

type repairReadCloser struct {
	io.ReadCloser
	source io.Closer
	once   sync.Once
	done   chan struct{}
}

func (r *repairReadCloser) closeSource() { r.once.Do(func() { _ = r.source.Close() }) }
func (r *repairReadCloser) Close() error {
	e := r.ReadCloser.Close()
	r.closeSource()
	<-r.done
	return e
}

// WrapChatResponse preserves status and headers. Streaming retains a bounded
// candidate batch until its terminal event; overflow flushes unchanged and disables
// recovery. Closing the returned body closes the upstream and stops the worker.
func WrapChatResponse(resp *http.Response, model string, freeformTools map[string]string) *http.Response {
	if resp == nil || resp.Body == nil || resp.StatusCode < 200 || resp.StatusCode >= 300 || !mimoModel.MatchString(model) || len(freeformTools) == 0 {
		return resp
	}
	tools := map[string]string{}
	for k, v := range freeformTools {
		tools[k] = v
	}
	out := new(http.Response)
	*out = *resp
	out.Header = resp.Header.Clone()
	out.Header.Del("Content-Length")
	out.ContentLength = -1
	reader, writer := io.Pipe()
	body := &repairReadCloser{ReadCloser: reader, source: resp.Body, done: make(chan struct{})}
	out.Body = body
	go func() {
		defer close(body.done)
		defer body.closeSource()
		var err error
		if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
			err = repairSSE(resp.Body, writer, tools)
		} else {
			var raw []byte
			raw, err = io.ReadAll(io.LimitReader(resp.Body, maxBareBytes+1))
			if err == nil {
				if len(raw) > maxBareBytes {
					_, err = writer.Write(raw)
					if err == nil {
						_, err = io.Copy(writer, resp.Body)
					}
				} else {
					raw, err = RepairChatResponse(raw, model, tools)
					if err == nil {
						_, err = writer.Write(raw)
					}
				}
			}
		}
		_ = writer.CloseWithError(err)
	}()
	return out
}
func repairSSE(src io.Reader, dst io.Writer, tools map[string]string) error {
	scan := bufio.NewScanner(src)
	scan.Buffer(make([]byte, 4096), maxBareBytes+1)
	held := []string{}
	size := 0
	disabled := false
	flush := func() error {
		for _, line := range held {
			if _, e := io.WriteString(dst, line+"\n"); e != nil {
				return e
			}
		}
		held = nil
		size = 0
		return nil
	}
	for scan.Scan() {
		line := scan.Text()
		if disabled {
			if _, e := io.WriteString(dst, line+"\n"); e != nil {
				return e
			}
			continue
		}
		held = append(held, line)
		size += len(line) + 1
		if size > maxBareBytes {
			if e := flush(); e != nil {
				return e
			}
			disabled = true
			continue
		}
		if strings.HasPrefix(line, "data:") {
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			terminal := payload == "[DONE]"
			if !terminal {
				b, _ := decode([]byte(payload))
				for _, c := range list(b["choices"]) {
					if obj(c)["finish_reason"] != nil {
						terminal = true
					}
				}
			}
			if terminal {
				repaired := repairSSEBatch(held, tools)
				for _, l := range repaired {
					if _, e := io.WriteString(dst, l+"\n"); e != nil {
						return e
					}
				}
				held = nil
				size = 0
				disabled = true
			}
		}
	}
	if e := scan.Err(); e != nil {
		return e
	}
	return flush()
}
func repairSSEBatch(lines []string, tools map[string]string) []string {
	var text strings.Builder
	structured := false
	choiceIndex := ""
	last := -1
	parsed := map[int]map[string]any{}
	for i, line := range lines {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		b, e := decode([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))))
		if e != nil {
			continue
		}
		parsed[i] = b
		for _, x := range list(b["choices"]) {
			c := obj(x)
			id := fmt.Sprint(c["index"])
			if choiceIndex != "" && choiceIndex != id {
				return lines
			}
			choiceIndex = id
			d := obj(c["delta"])
			if len(list(d["tool_calls"])) > 0 {
				structured = true
			}
			text.WriteString(str(d["content"]))
			if c["finish_reason"] != nil {
				last = i
			}
		}
	}
	if structured || last < 0 {
		return lines
	}
	calls := recoverBare(text.String(), tools)
	if len(calls) == 0 {
		return lines
	}
	// Keep all reasoning/usage events in order; replace only held text fields.
	first := true
	for i := range lines {
		b := parsed[i]
		if b == nil {
			continue
		}
		for _, x := range list(b["choices"]) {
			c := obj(x)
			d := obj(c["delta"])
			if d == nil {
				d = map[string]any{}
				c["delta"] = d
			}
			if _, ok := d["content"]; ok {
				delete(d, "content")
				if first {
					d["content"] = repairedText(text.String(), calls)
					first = false
				}
			}
			if i == last {
				a := []any{}
				for j, call := range calls {
					t := callJSON(call, tools, j)
					t["index"] = j
					a = append(a, t)
				}
				d["tool_calls"] = a
				c["finish_reason"] = "tool_calls"
			}
		}
		raw, _ := json.Marshal(b)
		lines[i] = "data: " + string(raw)
	}
	return lines
}
