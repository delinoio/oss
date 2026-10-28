// SPDX-License-Identifier: Apache-2.0
package cursor

import (
	"strings"
	"testing"
)

func TestEnvelopeFilterPinnedChunkBoundaries(t *testing.T) {
	cases := []struct {
		name, input, want string
		matched           bool
	}{
		{"plain", "answer\n[Tool Result]\nsecret\n\nmore", "answer\n", true},
		{"marker prose", "answer\n[Tool Result] shows success\n", "answer\n[Tool Result] shows success\n", false},
		{"inline", "the string [Tool Result] is a marker", "the string [Tool Result] is a marker", false},
		{"closed fence", "before\n~~~text\n[Tool Result]\nexample\n~~~\nafter", "before\n~~~text\n[Tool Result]\nexample\n~~~\nafter", false},
		{"unclosed fence", "before\n~~~text\n[Tool Result]\nsecret", "before\n~~~text\n", true},
		{"long closer", "~~~\n[Tool Error]\nexample\n~~~~", "~~~\n[Tool Error]\nexample\n~~~~", false},
		{"short closer", "~~~~\n[Tool Error]\nsecret\n~~~", "~~~~\n", true},
		{"wrong closer", "~~~\n[tool_result]\nsecret\n```", "~~~\n", true},
		{"closer info", "~~~\n[Tool Result]\nsecret\n~~~info", "~~~\n", true},
		{"invalid opener", "```bad`info\n[Tool Result]\nsecret", "```bad`info\n", true},
		{"four space fence", "    ~~~\n[Tool Result]\nsecret", "    ~~~\n", true},
		{"CRLF", "ok\r\n~~~\r\n[Tool Result]\r\ncode\r\n~~~\r\n", "ok\r\n~~~\r\n[Tool Result]\r\ncode\r\n~~~\r\n", false},
		{"unterminated marker", "ok\n[Tool Result", "ok\n", true},
		{"call prefix", "ok\n[Tool call: run (args)", "ok\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for split := 0; split <= len(tc.input); split++ {
				var f envelopeFilter
				got := f.feed(tc.input[:split]) + f.feed(tc.input[split:]) + f.finish()
				if got != tc.want || f.matched != tc.matched {
					t.Fatalf("split=%d got=%q matched=%t want=%q", split, got, f.matched, tc.want)
				}
			}
			var f envelopeFilter
			var out strings.Builder
			for _, r := range tc.input {
				out.WriteString(f.feed(string(r)))
			}
			out.WriteString(f.finish())
			if out.String() != tc.want {
				t.Fatal("single-character framing", out.String())
			}
		})
	}
}
func TestEnvelopeFenceHoldBound(t *testing.T) {
	text := "~~~\n[Tool Result]\n" + strings.Repeat("x", 70<<10)
	var f envelopeFilter
	got := f.feed(text)
	if got != text || f.held.Len() != 0 || !f.unverified {
		t.Fatal("hold did not release at bound")
	}
	if tail := f.feed("\n~~~\n") + f.finish(); tail != "\n~~~\n" || f.unverified || f.matched {
		t.Fatal("closed overflow fence remained unverified")
	}
}
func TestSessionFenceDoesNotRotateOrReplay(t *testing.T) {
	s := newSession("gpt-5.4", nil)
	s.observeReplay = true
	s.guardEcho = true
	var text strings.Builder
	for _, chunk := range []string{"answer\n~~~\n[Tool ", "Result]\nexample\n", "~~~\nend"} {
		events, err := s.textEvents(chunk)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range events {
			text.WriteString(e.Text)
		}
	}
	s.ended = true
	events, err := s.finish(true)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Kind == Text {
			text.WriteString(e.Text)
		}
	}
	if text.String() != "answer\n~~~\n[Tool Result]\nexample\n~~~\nend" || s.midstreamEcho {
		t.Fatalf("legitimate fence changed: %q", text.String())
	}
}
func TestHistoryEnvelopeFenceAndProse(t *testing.T) {
	input := "real\n[Tool Result]\nsecret\n\n~~~\n[Tool Result]\nexample\n~~~\n[Tool Result] describes a marker"
	want := "real\n\n~~~\n[Tool Result]\nexample\n~~~\n[Tool Result] describes a marker"
	if got := stripHistoryEnvelope(input); got != want {
		t.Fatalf("%q", got)
	}
}

func TestEnvelopeWhitespaceProbeIsBounded(t *testing.T) {
	var f envelopeFilter
	input := "[Tool Result]" + strings.Repeat(" ", 70<<10) + "legitimate suffix\n"
	got := f.feed(input) + f.finish()
	if got != input || len(f.pending) != 0 || !f.unverified || f.matched {
		t.Fatal("oversized ambiguous line must be released without retraction")
	}
}
