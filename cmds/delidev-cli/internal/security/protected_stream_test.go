// SPDX-License-Identifier: Apache-2.0
package security

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const streamSecret = "fixture-stream-secret/+é"

func streamForms() []string {
	secret := []byte(streamSecret)
	return []string{streamSecret, base64.StdEncoding.EncodeToString(secret), base64.RawStdEncoding.EncodeToString(secret), base64.URLEncoding.EncodeToString(secret), base64.RawURLEncoding.EncodeToString(secret)}
}

func TestProtectedStreamNeverReleasesCompletedFormAtAnySplit(t *testing.T) {
	guard := NewProtectedJSON([]string{streamSecret})
	for _, form := range streamForms() {
		body := "before " + form + " after"
		for first := 0; first <= len(body); first++ {
			for second := first; second <= len(body); second++ {
				stream := guard.NewStream(StreamPlainText)
				var released strings.Builder
				failed := false
				for _, part := range []string{body[:first], body[first:second], body[second:]} {
					out, err := stream.Push(part)
					if err != nil {
						if !errors.Is(err, ErrProtectedStreamValue) || out != "" || stream.Held() != 0 {
							t.Fatal("refusal leaked or retained text", err)
						}
						failed = true
						break
					}
					released.WriteString(out)
				}
				if !failed {
					t.Fatalf("split %d/%d of %q completed without refusal", first, second, form)
				}
				if strings.Contains(released.String(), form) || !strings.HasPrefix(body, released.String()) {
					t.Fatalf("released text reconstructs or reorders the form: %q", released.String())
				}
			}
		}
	}
}

func TestProtectedStreamKeepsSafeTextOrderedAndBounded(t *testing.T) {
	stream := NewProtectedJSON([]string{streamSecret}).NewStream(StreamPlainText)
	body := strings.Repeat("harmless fixture-stream and fixture text ", 200) + "fixture-stream-"
	var released strings.Builder
	for _, chunk := range strings.SplitAfter(body, " ") {
		out, err := stream.Push(chunk)
		if err != nil {
			t.Fatal(err)
		}
		released.WriteString(out)
		if stream.Held() >= len(streamSecret) {
			t.Fatalf("holdback exceeded the longest protected form: %d", stream.Held())
		}
	}
	if stream.Held() == 0 || released.String()+stream.Flush() != body || stream.Held() != 0 {
		t.Fatal("safe stream was reordered, truncated or flushed twice")
	}
}

func TestProtectedStreamSplitsOnRuneBoundariesAndDiscards(t *testing.T) {
	stream := NewProtectedJSON([]string{"éa-secret-value"}).NewStream(StreamPlainText)
	out, err := stream.Push("日本é")
	if err != nil || out != "日本" || stream.Held() != len("é") {
		t.Fatalf("unexpected release %q %d %v", out, stream.Held(), err)
	}
	stream.Discard()
	if stream.Held() != 0 {
		t.Fatal("discard kept withheld text")
	}
	if out, err = stream.Push("a-secret-value"); err != nil || out != "a-secret-value" {
		t.Fatal("discard must clear matching state", out, err)
	}
}

func TestProtectedStreamObserveReportsUnresolvedSuffixForWholeItems(t *testing.T) {
	stream := NewProtectedJSON([]string{"abcdef"}).NewStream(StreamPlainText)
	hold, err := stream.Observe("xxab")
	if err != nil || hold != 2 {
		t.Fatal(hold, err)
	}
	if hold, err = stream.Observe("cd"); err != nil || hold != 4 {
		t.Fatal(hold, err)
	}
	if _, err = stream.Observe("ef"); !errors.Is(err, ErrProtectedStreamValue) {
		t.Fatal("completing item was accepted", err)
	}
	if hold, err = stream.Observe("ab"); err != nil || hold != 2 {
		t.Fatal("matching state survived refusal", hold, err)
	}
}

func TestProtectedStreamPrimeCountsPublishedTextWithoutRepublishing(t *testing.T) {
	stream := NewProtectedJSON([]string{"abcdef"}).NewStream(StreamPlainText)
	if err := stream.Prime("zzabc"); err != nil {
		t.Fatal(err)
	}
	if out, err := stream.Push("de"); err != nil || out != "" || stream.Held() != 2 {
		t.Fatal("already published prefix must not be republished", out, err)
	}
	if _, err := stream.Push("f"); !errors.Is(err, ErrProtectedStreamValue) {
		t.Fatal("primed prefix did not participate in matching", err)
	}
}

func TestProtectedStreamJSONProfileMatchesEscapedSpelling(t *testing.T) {
	guard := NewProtectedJSON([]string{`pw"with\slash`})
	escaped := `pw\"with\\slash`
	if _, err := guard.NewStream(StreamPlainText).Push(escaped); err != nil {
		t.Fatal("plain profile must not invent JSON escapes", err)
	}
	stream := guard.NewStream(StreamJSONText)
	if _, err := stream.Push(escaped[:6]); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Push(escaped[6:]); !errors.Is(err, ErrProtectedStreamValue) {
		t.Fatal("escaped JSON spelling was accepted", err)
	}
}

func TestProtectedStreamJSONProfileRejectsEquivalentSpellingsAtEverySplit(t *testing.T) {
	cases := []struct {
		name, secret, spelling string
	}{
		{"unicode-escape", "abc", `\u0061bc`},
		{"mixed-escapes", "abc", `a\u0062\u0063`},
		{"all-escaped", "abc", `\u0061\u0062\u0063`},
		{"hex-upper", "é", `\u00E9`},
		{"hex-lower", "é", `\u00e9`},
		{"short-escapes", "a\n\t\"\\/\b\f\rz", `a\n\t\"\\\/\b\f\rz`},
		{"surrogate-pair", "A😀B", `A\uD83D\uDE00B`},
		{"lower-surrogate-pair", "A😀B", `\u0041\ud83d\ude00\u0042`},
		{"base64-escape", "abc", `\u0059WJj`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			protected := tc.secret
			if tc.name == "base64-escape" {
				protected = base64.StdEncoding.EncodeToString([]byte(tc.secret))
			}
			body := `{"value":"` + tc.spelling + `"}`
			var decoded map[string]string
			if err := json.Unmarshal([]byte(body), &decoded); err != nil || decoded["value"] != protected {
				t.Fatalf("fixture is not an equivalent JSON spelling: %v", err)
			}
			complete := len(`{"value":"`) + len(tc.spelling)
			for first := 0; first <= len(body); first++ {
				for second := first; second <= len(body); second++ {
					stream := NewProtectedJSON([]string{tc.secret}).NewStream(StreamJSONText)
					var released strings.Builder
					refused := false
					for _, part := range []string{body[:first], body[first:second], body[second:]} {
						out, err := stream.Push(part)
						if err != nil {
							if !errors.Is(err, ErrProtectedStreamValue) || out != "" || stream.Held() != 0 {
								t.Fatal("refusal released or retained a fragment", err)
							}
							refused = true
							break
						}
						released.WriteString(out)
					}
					if !refused || len(released.String()) >= complete || !strings.HasPrefix(body, released.String()) {
						t.Fatalf("split %d/%d released a complete spelling: %q", first, second, released.String())
					}
				}
			}
		})
	}
}

func TestProtectedStreamJSONProfilePreservesSafeBytesAndBoundsState(t *testing.T) {
	guard := NewProtectedJSON([]string{"abc", "A😀B"})
	body := strings.Repeat(`{"value":"\u0061bd","next":"A\uD83D\uDE00C"} `, 100)
	stream := guard.NewStream(StreamJSONText)
	var released strings.Builder
	for i := 0; i < len(body); i++ {
		out, err := stream.Push(body[i : i+1])
		if err != nil {
			t.Fatal(err)
		}
		released.WriteString(out)
		if stream.Held() > 12*stream.jsonMax+12 || len(stream.jsonTail) >= stream.jsonMax || len(stream.jsonStarts) != len(stream.jsonTail) {
			t.Fatal("JSON matcher retained an unbounded prefix")
		}
	}
	if released.String()+stream.Flush() != body || stream.Held() != 0 {
		t.Fatal("safe JSON bytes changed order or spelling")
	}
}

func TestProtectedStreamJSONProfileRejectsEscapedBase64Variants(t *testing.T) {
	const digits = "0123456789ABCDEF"
	for _, form := range streamForms()[1:] {
		spelling := `\u00` + string(digits[form[0]>>4]) + string(digits[form[0]&15]) + form[1:]
		body := `{"value":"` + spelling + `"}`
		var decoded map[string]string
		if err := json.Unmarshal([]byte(body), &decoded); err != nil || decoded["value"] != form {
			t.Fatal("Base64 fixture changed during JSON decoding", err)
		}
		complete := len(`{"value":"`) + len(spelling)
		for split := 0; split <= len(body); split++ {
			stream := NewProtectedJSON([]string{streamSecret}).NewStream(StreamJSONText)
			var released strings.Builder
			refused := false
			for _, part := range []string{body[:split], body[split:]} {
				out, err := stream.Push(part)
				if err != nil {
					refused = errors.Is(err, ErrProtectedStreamValue) && out == ""
					break
				}
				released.WriteString(out)
			}
			if !refused || len(released.String()) >= complete {
				t.Fatal("escaped Base64 form reached publication", split)
			}
		}
	}
}

func TestProtectedStreamJSONProfileKeepsIncompleteEscapesPrivate(t *testing.T) {
	guard := NewProtectedJSON([]string{"abc"})
	stream := guard.NewStream(StreamJSONText)
	if out, err := stream.Push(`{"value":"\u00`); err != nil || out != `{"value":"` || stream.Held() != len(`\u00`) {
		t.Fatal("incomplete escape was released", out, err)
	}
	clone := stream.Clone()
	if out, err := clone.Push(`61bc"}`); !errors.Is(err, ErrProtectedStreamValue) || out != "" || clone.Held() != 0 {
		t.Fatal("cloned escape did not protect its completion", out, err)
	}
	if out, err := stream.Push(`61bd"}`); err != nil || out != `\u0061bd"}` || stream.Held() != 0 {
		t.Fatal("safe clone branch changed bytes", out, err)
	}
	stream.Discard()
	if out, err := stream.Push(`{"value":"abd"}`); err != nil || out != `{"value":"abd"}` {
		t.Fatal("discard retained another JSON stream", out, err)
	}
	malformed := guard.NewStream(StreamJSONText)
	if _, err := malformed.Push(`{"value":"\u00`); err != nil {
		t.Fatal(err)
	}
	if out, err := malformed.Push(`G1bc"}`); !errors.Is(err, errMalformedJSONStream) || out != "" || malformed.Held() != 0 {
		t.Fatal("malformed escape escaped private state", out, err)
	}
}

func TestProtectedStreamJSONProfileSettleAndIndependentStreams(t *testing.T) {
	guard := NewProtectedJSON([]string{"abc"})
	first := guard.NewStream(StreamJSONText)
	second := guard.NewStream(StreamJSONText)
	if _, err := first.Push(`{"value":"\u0061b`); err != nil {
		t.Fatal(err)
	}
	first.Settle()
	if first.Held() != 0 {
		t.Fatal("settle kept already published bytes")
	}
	if out, err := first.Push(`c"}`); !errors.Is(err, ErrProtectedStreamValue) || out != "" {
		t.Fatal("settle cleared the decoded matching window", out, err)
	}
	if out, err := second.Push(`c"}`); err != nil || out != `c"}` {
		t.Fatal("independent stream inherited another value", out, err)
	}
	primed := guard.NewStream(StreamJSONText)
	if err := primed.Prime(`{"value":"\u0061b`); err != nil {
		t.Fatal(err)
	}
	if out, err := primed.Push(`c"}`); !errors.Is(err, ErrProtectedStreamValue) || out != "" {
		t.Fatal("primed JSON prefix did not participate in matching", out, err)
	}
}

func TestProtectedStreamNilIsPassThrough(t *testing.T) {
	stream := NewProtectedJSON(nil).NewStream(StreamPlainText)
	if stream != nil {
		t.Fatal("unconfigured guard allocated a stream")
	}
	out, err := stream.Push("anything")
	hold, observeErr := stream.Observe("anything")
	if out != "anything" || err != nil || hold != 0 || observeErr != nil || stream.Flush() != "" || stream.Held() != 0 || stream.Prime("x") != nil {
		t.Fatal("nil stream changed unconfigured behavior")
	}
	stream.Discard()
}
