// SPDX-License-Identifier: Apache-2.0
package security

import (
	"encoding/base64"
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
