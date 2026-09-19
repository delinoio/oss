package core

import (
	"strings"
	"unicode/utf8"
)

const failureFieldBytes = 4096
const runFailureBytes = 1024 * 1024

func failureText(s string) (string, bool) {
	clean := strings.ToValidUTF8(s, "�")
	if len(clean) <= failureFieldBytes {
		return clean, clean != s
	}
	const suffix = "… [truncated; see report evidence]"
	end := failureFieldBytes - len(suffix)
	for !utf8.RuneStart(clean[end]) {
		end--
	}
	return clean[:end] + suffix, true
}

// The shared JSON budget includes escaping and field overhead, keeping both
// JSON and protobuf summaries below the 8 MiB transport cap. Evidence is intact.
func boundFailures(in []Failure, budget int) (out []Failure, used int, truncated bool) {
	out = []Failure{}
	for _, f := range in {
		for _, field := range []*string{&f.Check, &f.Test, &f.Command, &f.Message, &f.File} {
			value, cut := failureText(*field)
			*field = value
			truncated = truncated || cut
		}
		cost := len(Encode(f)) + 1
		if used+cost > budget {
			truncated = true
			break
		}
		used += cost
		out = append(out, f)
	}
	return
}
func boundCheckFailures(c *Check, budget int) int {
	failures, used, truncated := boundFailures(c.Failures, budget)
	c.Failures = failures
	if truncated {
		for _, d := range c.Diagnostics {
			if d.Code == "failure-summaries-truncated" {
				return used
			}
		}
		c.Diagnostics = append(c.Diagnostics, Diagnostic{Code: "failure-summaries-truncated", Message: "Structured failure summaries were truncated; inspect the paginated report evidence for full details."})
	}
	return used
}

// Consume the complete field so a secret crossing the retained prefix is still
// masked, but never allocate the potentially much larger redacted output.
func redactedFailureText(value string, secrets []string) (string, bool) {
	w := &failurePrefix{}
	r := NewRedactor(w, secrets)
	_, _ = r.Write([]byte(value))
	_ = r.Close()
	return failureText(string(w.bytes))
}

type failurePrefix struct{ bytes []byte }

func (w *failurePrefix) Write(b []byte) (int, error) {
	n := min(len(b), failureFieldBytes+utf8.UTFMax-len(w.bytes))
	w.bytes = append(w.bytes, b[:n]...)
	return len(b), nil
}
