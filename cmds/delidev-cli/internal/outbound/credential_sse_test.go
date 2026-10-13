// SPDX-License-Identifier: Apache-2.0
package outbound

import (
	"encoding/base64"
	"io"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestCredentialSSEMetadataCannotChangeDataDecoding(t *testing.T) {
	credential := domainCredentialFixture()
	for _, form := range []string{`\u0066ixture-password`, escapedCredential(credential.Password, false)} {
		for _, wire := range []string{
			": \"\ndata: {\"value\":\"" + form + "\"}\n\n",
			"event: \"\nid: \\\"\nretry: 100\ndata: {\"value\":\"" + form + "\"}\n\n",
			"\n\n: \"\n\ndata: {\"value\":\"" + form + "\"}\n\n",
			"data: {\n: \"\nevent: \"\ndata: \"value\":\"" + form + "\"}\n\n",
			"data: \"unmatched\n\n: \"\ndata: {\"value\":\"" + form + "\"}\n\n",
			": \"\r\ndata:{\"value\":\"" + form + "\"}\r\n\r\n",
			"extension: \"\ndata: {\"value\":\"" + form + "\"}\n\n",
			": \"\rdata: {\"value\":\"" + form + "\"}\r\r",
			"\xef\xbb\xbfdata: {\"value\":\"" + form + "\"}\n\n",
			"data\n\n: \"\ndata: {\"value\":\"" + form + "\"}\n\n",
		} {
			for split := 0; split <= len(wire); split++ {
				for _, contentType := range []string{"", "text/event-stream; charset=utf-8"} {
					original := &trackedCredentialReader{Reader: io.MultiReader(strings.NewReader(wire[:split]), strings.NewReader(wire[split:]))}
					guard := newCredentialBody(original, credential, contentType)
					output, err := io.ReadAll(guard)
					if err == nil || err == io.EOF || !original.closed {
						t.Fatalf("SSE reflection accepted at split %d (%q)", split, contentType)
					}
					if strings.Contains(string(output), form) {
						t.Fatalf("credential-bearing wire bytes released at split %d", split)
					}
					guard.Close()
				}
			}
		}
	}
}

func TestCredentialTypelessUnknownSSEMetadataCannotChangeDataDecoding(t *testing.T) {
	credential := domainCredentialFixture()
	form := escapedCredential(credential.Password, false)
	for _, initialField := range []string{`extension: "`, `extension"`} {
		wire := initialField + "\n" + `data: {"value":"` + form + `"}` + "\n\n"
		for split := 0; split <= len(wire); split++ {
			original := &trackedCredentialReader{Reader: io.MultiReader(strings.NewReader(wire[:split]), strings.NewReader(wire[split:]))}
			guard := newCredentialBody(original, credential)
			output, err := io.ReadAll(guard)
			if err == nil || err == io.EOF || !original.closed {
				t.Fatalf("typeless SSE reflection accepted at split %d for %q", split, initialField)
			}
			if strings.Contains(string(output), form) {
				t.Fatalf("credential-bearing wire bytes released at split %d for %q", split, initialField)
			}
			guard.Close()
		}
	}
}

func TestCredentialTypelessBraceAndBracketSSEFieldsCannotChangeDataDecoding(t *testing.T) {
	credential := domainCredentialFixture()
	form := escapedCredential(credential.Password, false)
	for _, initialField := range []string{"{", "[", `{"extension"`} {
		wire := initialField + `: "` + "\n" + `data: {"value":"` + form + `"}` + "\n\n"
		for split := 0; split <= len(wire); split++ {
			original := &trackedCredentialReader{Reader: io.MultiReader(strings.NewReader(wire[:split]), strings.NewReader(wire[split:]))}
			guard := newCredentialBody(original, credential)
			output, err := io.ReadAll(guard)
			if err == nil || err == io.EOF || !original.closed {
				t.Fatalf("brace-prefixed typeless SSE reflection accepted at split %d for %q", split, initialField)
			}
			if strings.Contains(string(output), form) {
				t.Fatalf("credential-bearing wire bytes released at split %d for %q", split, initialField)
			}
			guard.Close()
		}
	}
}

func TestCredentialTypelessBraceSSEFieldBeyondProbeLimitCannotChangeDataDecoding(t *testing.T) {
	credential := domainCredentialFixture()
	form := escapedCredential(credential.Password, false)
	metadata := `{"extension":"` + strings.Repeat("a", credentialSSEProbeLimit*2)
	wire := metadata + "\n" + `data: {"value":"` + form + `"}` + "\n\n"
	for _, split := range []int{0, credentialSSEProbeLimit - 1, credentialSSEProbeLimit, credentialSSEProbeLimit + 1, len(metadata), len(wire) - 1, len(wire)} {
		original := &trackedCredentialReader{Reader: io.MultiReader(strings.NewReader(wire[:split]), strings.NewReader(wire[split:]))}
		guard := newCredentialBody(original, credential)
		output, err := io.ReadAll(guard)
		if err == nil || err == io.EOF || !original.closed || strings.Contains(string(output), form) {
			t.Fatalf("long brace-prefixed typeless SSE reflection accepted at split %d", split)
		}
		if len(guard.jsonGuard.probe) > credentialSSEProbeLimit || len(guard.sseGuard.decoded) > 32 || len(guard.sseGuard.escape) > 12 {
			t.Fatalf("typeless SSE scanner retained unbounded state at split %d", split)
		}
		guard.Close()
	}
}

func TestCredentialTypelessFramingPreservesOrdinaryJSONAndSafeWire(t *testing.T) {
	for _, wire := range []string{
		`{"value":"ordinary"}`,
		`{"value":"https://example.invalid/a:b"}`,
		`[{"value":"ordinary"}]`,
		"{\n\"value\":\"ordinary\"\n}",
		"[\n{\"value\":\"ordinary\"}\n]",
		`"extension: value"`,
		`"extension: \"value\""`,
		`true`,
		"extension: \"\ndata: {\"value\":\"ordinary\"}\n\n",
	} {
		for split := 0; split <= len(wire); split++ {
			original := &trackedCredentialReader{Reader: io.MultiReader(strings.NewReader(wire[:split]), strings.NewReader(wire[split:]))}
			guard := newCredentialBody(original, domainCredentialFixture())
			output, err := io.ReadAll(guard)
			if err != nil || string(output) != wire {
				t.Fatalf("safe typeless content changed at split %d for %q: %v", split, wire, err)
			}
			guard.Close()
		}
	}
}

func TestCredentialTypelessJSONStringWithColonStillDecodesEscapes(t *testing.T) {
	credential := domainCredentialFixture()
	form := escapedCredential(credential.Password, false)
	wire := `"extension: ` + form + `"`
	for split := 0; split <= len(wire); split++ {
		original := &trackedCredentialReader{Reader: io.MultiReader(strings.NewReader(wire[:split]), strings.NewReader(wire[split:]))}
		guard := newCredentialBody(original, credential)
		output, err := io.ReadAll(guard)
		if err == nil || !original.closed || strings.Contains(string(output), form) {
			t.Fatalf("JSON string reflection bypassed typeless probing at split %d", split)
		}
		guard.Close()
	}
}

func TestCredentialExplicitJSONContentTypeKeepsOrdinaryFraming(t *testing.T) {
	wire := `data: {"value":"ordinary"}`
	guard := newCredentialBody(io.NopCloser(strings.NewReader(wire)), domainCredentialFixture(), "application/json")
	output, err := io.ReadAll(guard)
	if err != nil || string(output) != wire || guard.jsonGuard.framing != credentialOrdinary {
		t.Fatal("explicit JSON content type did not select ordinary decoding", err)
	}
	guard.Close()
}

func TestCredentialSSEPreservesSafeFramingAtEverySplit(t *testing.T) {
	for _, wire := range []string{
		": \"\nevent: \"\nid: \\\"\nretry: 12\n\ndata: {\"value\":\"ordinary\"}\n\n",
		"data: {\n: \"\ndata: \"value\": \"Caf\\u00e9 \\uD83D\\uDD12\"}\n\n",
		"data: \"unmatched\n\ndata: {\"value\":\"ordinary\"}\n\n",
		"data:\ndata\n\n\ndata: [DONE]\n\ndata: harmless non-JSON data\n\n",
		"data: {\"value\":\"ordinary\"}\r\n\r\n: \"\rdata: [DONE]\r\r",
		"unknown-field: \"\n: \"\ndata: {\"value\":\"ordinary\"}\n\n",
		"\xef\xbb\xbfdata: {\"value\":\"ordinary\"}\n\n",
	} {
		for split := 0; split <= len(wire); split++ {
			original := &trackedCredentialReader{Reader: io.MultiReader(strings.NewReader(wire[:split]), strings.NewReader(wire[split:]))}
			guard := newCredentialBody(original, domainCredentialFixture(), "text/event-stream")
			output, err := io.ReadAll(guard)
			if err != nil || string(output) != wire {
				t.Fatalf("safe SSE changed at split %d: %v", split, err)
			}
			guard.Close()
		}
	}
}

func TestCredentialSSEIgnoredFieldsRetainLiteralProtection(t *testing.T) {
	credential := domainCredentialFixture()
	for _, field := range []string{": ", "event: ", "id: ", "unknown: "} {
		wire := field + credential.Password + "\n\ndata: [DONE]\n\n"
		original := &trackedCredentialReader{Reader: fragmentedReader{strings.NewReader(wire)}}
		guard := newCredentialBody(original, credential, "text/event-stream")
		output, err := io.ReadAll(guard)
		if err == nil || !original.closed || strings.Contains(string(output), credential.Password) {
			t.Fatal("ignored SSE metadata bypassed literal protection")
		}
		guard.Close()
	}
}

func TestCredentialSSEDecoderDoesNotReadIgnoredEscapes(t *testing.T) {
	// Escaped metadata is not interpreted as data JSON. Its exact wire bytes
	// remain subject to the independent literal guard.
	wire := ": \"\\u0066ixture-password\"\nevent: \"\\u0066ixture-password\"\ndata: [DONE]\n\n"
	guard := newCredentialBody(io.NopCloser(fragmentedReader{strings.NewReader(wire)}), domainCredentialFixture(), "text/event-stream")
	output, err := io.ReadAll(guard)
	if err != nil || string(output) != wire {
		t.Fatal("ignored SSE metadata entered JSON decoding", err)
	}
	guard.Close()
}

func domainCredentialFixture() domain.ProxyCredential {
	return domain.ProxyCredential{Username: "fixture-user", Password: "fixture-password"}
}

func TestCredentialSSEShortDecodedTokenBoundaries(t *testing.T) {
	credential := domain.ProxyCredential{Username: "x", Password: "pass123"}
	for _, test := range []struct {
		value  string
		reject bool
	}{
		{`\u0078`, true}, {`\u0070ass123`, true}, {`e\u0078tra`, false}, {`\u0070ass123more`, false},
	} {
		wire := ": \"\ndata: {\"value\":\"" + test.value + "\"}\n\n"
		for split := 0; split <= len(wire); split++ {
			original := &trackedCredentialReader{Reader: io.MultiReader(strings.NewReader(wire[:split]), strings.NewReader(wire[split:]))}
			guard := newCredentialBody(original, credential, "text/event-stream")
			output, err := io.ReadAll(guard)
			if test.reject {
				if err == nil || !original.closed || strings.Contains(string(output), test.value) {
					t.Fatalf("short SSE reflection released at split %d", split)
				}
			} else if err != nil || string(output) != wire {
				t.Fatalf("unrelated short token rejected at split %d: %v", split, err)
			}
			guard.Close()
		}
	}
}

func TestCredentialTypelessUnknownSSEShortDecodedTokenBoundaries(t *testing.T) {
	credential := domain.ProxyCredential{Username: "x", Password: "pass123"}
	for _, initialField := range []string{`extension: "`, `{: "`, `[: "`} {
		for _, test := range []struct {
			value  string
			reject bool
		}{
			{`\u0078`, true}, {`\u0070ass123`, true}, {`e\u0078tra`, false}, {`\u0070ass123more`, false},
		} {
			wire := initialField + "\n" + `data: {"value":"` + test.value + "\"}\n\n"
			for split := 0; split <= len(wire); split++ {
				original := &trackedCredentialReader{Reader: io.MultiReader(strings.NewReader(wire[:split]), strings.NewReader(wire[split:]))}
				guard := newCredentialBody(original, credential)
				output, err := io.ReadAll(guard)
				if test.reject {
					if err == nil || !original.closed || strings.Contains(string(output), test.value) {
						t.Fatalf("short typeless SSE reflection released at split %d", split)
					}
				} else if err != nil || string(output) != wire {
					t.Fatalf("unrelated short typeless token rejected at split %d: %v", split, err)
				}
				guard.Close()
			}
		}
	}
}

func TestCredentialSSEFramingRetainsBoundedState(t *testing.T) {
	wire := ": " + strings.Repeat("metadata", 20000) + "\n\ndata: {\"value\":\"" + strings.Repeat(`\u0061`, 20000) + "\"}\n\n"
	source := strings.NewReader(wire)
	guard := newCredentialBody(io.NopCloser(source), domainCredentialFixture(), "text/event-stream")
	buffer := make([]byte, 256)
	var output strings.Builder
	for {
		n, err := guard.Read(buffer)
		output.Write(buffer[:n])
		if len(guard.jsonGuard.probe) > credentialSSEProbeLimit || len(guard.jsonGuard.decoded) > 32 || len(guard.jsonGuard.escape) > 12 || len(guard.pending) > 8192+12 {
			t.Fatal("unbounded SSE framing or retained wire state")
		}
		if output.Len() == 256 && source.Len() == 0 {
			t.Fatal("framing buffered the entire safe stream")
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if output.String() != wire {
		t.Fatal("bounded SSE bytes changed")
	}
	guard.Close()
}

func TestCredentialTypelessUnknownSSECancellationJoinsWithoutReleasingPrefix(t *testing.T) {
	assertCredentialCancellationWithheld(
		t,
		"extension: \"\ndata: {\"value\":\"\\u0066",
		"extension: \"\ndata: {\"value\":\"",
	)
	assertCredentialCancellationWithheld(
		t,
		`{: "`+"\n"+`data: {"value":"\u0066`,
		`{: "`+"\n"+`data: {"value":"`,
	)
}

func TestCredentialTypelessUnknownFieldProbeRetainsBoundedState(t *testing.T) {
	wire := strings.Repeat("extension", 2000) + `: "` + "\n\ndata: [DONE]\n\n"
	source := strings.NewReader(wire)
	guard := newCredentialBody(io.NopCloser(source), domainCredentialFixture())
	buffer := make([]byte, 256)
	var output strings.Builder
	for {
		n, err := guard.Read(buffer)
		output.Write(buffer[:n])
		if len(guard.jsonGuard.probe) > credentialSSEProbeLimit {
			t.Fatal("typeless framing retained an unbounded initial field")
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	guard.Close()
	if output.String() != wire {
		t.Fatal("bounded typeless framing changed safe SSE bytes")
	}
}

func TestCredentialOpaqueMediaTypesDoNotDecodeJSONEscapes(t *testing.T) {
	credential := domainCredentialFixture()
	for _, mediaType := range []string{"application/vnd.github.diff", "application/vnd.github.v3.diff; charset=utf-8", "text/plain", "text/plain; charset=utf-8", "application/octet-stream"} {
		for _, form := range []string{`\u0066ixture-password`, escapedCredential(credential.Password, false), escapedCredential(credential.Password, true)} {
			wire := "diff --git a/f b/f\n+const s = \"" + form + "\"\n"
			for _, reader := range []io.Reader{strings.NewReader(wire), fragmentedReader{strings.NewReader(wire)}} {
				guard := newCredentialBody(io.NopCloser(reader), credential, mediaType)
				output, err := io.ReadAll(guard)
				if err != nil || string(output) != wire || guard.jsonGuard.framing != credentialOpaque {
					t.Fatal("opaque body was JSON-decoded", mediaType, err)
				}
				guard.Close()
			}
		}
	}
}

func TestCredentialOpaqueMediaTypesStillRejectLiteralAndBase64(t *testing.T) {
	credential := domainCredentialFixture()
	forms := []string{
		credential.Username,
		credential.Password,
		base64.StdEncoding.EncodeToString([]byte(credential.Username + ":" + credential.Password)),
		base64.RawStdEncoding.EncodeToString([]byte(credential.Password)),
		"Basic " + base64.StdEncoding.EncodeToString([]byte(credential.Username+":"+credential.Password)),
	}
	for _, mediaType := range []string{"application/vnd.github.diff", "text/plain"} {
		for _, form := range forms {
			wire := "context " + form + " tail"
			for _, reader := range []io.Reader{strings.NewReader(wire), fragmentedReader{strings.NewReader(wire)}} {
				guard := newCredentialBody(io.NopCloser(reader), credential, mediaType)
				output, err := io.ReadAll(guard)
				if err == nil || strings.Contains(string(output), form) {
					t.Fatal("opaque body reflected a protected value", mediaType, form)
				}
				guard.Close()
			}
		}
	}
}

func TestCredentialJSONMediaTypesStillRejectEscapedReflections(t *testing.T) {
	credential := domainCredentialFixture()
	for _, mediaType := range []string{"application/json", "application/json; charset=utf-8", "application/vnd.github+json", "application/problem+json", "Application/JSON"} {
		wire := `{"value":"\u0066ixture-password"}`
		for _, reader := range []io.Reader{strings.NewReader(wire), fragmentedReader{strings.NewReader(wire)}} {
			guard := newCredentialBody(io.NopCloser(reader), credential, mediaType)
			output, err := io.ReadAll(guard)
			if err == nil || strings.Contains(string(output), "fixture") {
				t.Fatal("JSON media type accepted escaped reflection", mediaType)
			}
			guard.Close()
		}
	}
}

func TestCredentialSSEDataStillRejectsEscapedReflections(t *testing.T) {
	credential := domainCredentialFixture()
	wire := "data: {\"value\":\"\\u0066ixture-password\"}\n\n"
	guard := newCredentialBody(io.NopCloser(fragmentedReader{strings.NewReader(wire)}), credential, "text/event-stream")
	if _, err := io.ReadAll(guard); err == nil {
		t.Fatal("SSE data accepted escaped reflection")
	}
	guard.Close()
}

func TestCredentialOpaqueMediaTypesKeepShortTokenBounds(t *testing.T) {
	credential := domain.ProxyCredential{Username: "x", Password: "pass123"}
	safe := "context passwords x-ray pass1234 tail"
	guard := newCredentialBody(io.NopCloser(fragmentedReader{strings.NewReader(safe)}), credential, "text/plain")
	output, err := io.ReadAll(guard)
	if err != nil || string(output) != safe {
		t.Fatal("opaque short token substring was rejected", err)
	}
	guard.Close()
	guard = newCredentialBody(io.NopCloser(fragmentedReader{strings.NewReader("context pass123 tail")}), credential, "text/plain")
	if _, err := io.ReadAll(guard); err == nil {
		t.Fatal("opaque short token was accepted")
	}
	guard.Close()
}

func TestCredentialJSONTextSequenceMediaTypesRejectEscapedReflections(t *testing.T) {
	credential := domainCredentialFixture()
	for _, mediaType := range []string{"application/json-seq", "application/json-seq; charset=utf-8", "Application/JSON-Seq", "application/vnd.fixture+json-seq"} {
		wire := "\x1e{\"value\":\"\\u0066ixture-password\"}\n\x1e{\"value\":\"safe\"}\n"
		for _, reader := range []io.Reader{strings.NewReader(wire), fragmentedReader{strings.NewReader(wire)}} {
			guard := newCredentialBody(io.NopCloser(reader), credential, mediaType)
			output, err := io.ReadAll(guard)
			if err == nil || strings.Contains(string(output), "fixture") || guard.jsonGuard.framing != credentialOrdinary {
				t.Fatal("JSON text sequence accepted escaped reflection", mediaType)
			}
			guard.Close()
		}
		safe := "\x1e{\"value\":\"safe\"}\n"
		guard := newCredentialBody(io.NopCloser(fragmentedReader{strings.NewReader(safe)}), credential, mediaType)
		output, err := io.ReadAll(guard)
		if err != nil || string(output) != safe {
			t.Fatal("JSON text sequence changed safe bytes", mediaType, err)
		}
		guard.Close()
	}
}

func TestCredentialOpaqueMediaTypesIgnoreJSONEscapedSpellings(t *testing.T) {
	credential := domain.ProxyCredential{Username: "fixture-user", Password: `fixture-"password\<tail>`}
	// Source literals that spell the password with JSON escapes are not wire
	// reflections of an opaque body.
	safe := "diff --git a/f b/f\n+const s = \"fixture-\\\"password\\\\\\u003ctail\\u003e\"\n"
	for _, mediaType := range []string{"application/vnd.github.diff", "text/plain"} {
		guard := newCredentialBody(io.NopCloser(fragmentedReader{strings.NewReader(safe)}), credential, mediaType)
		output, err := io.ReadAll(guard)
		if err != nil || string(output) != safe {
			t.Fatal("opaque body rejected a JSON-escaped spelling", mediaType, err)
		}
		guard.Close()
		literal := "context " + credential.Password + " tail"
		guard = newCredentialBody(io.NopCloser(fragmentedReader{strings.NewReader(literal)}), credential, mediaType)
		if _, err = io.ReadAll(guard); err == nil {
			t.Fatal("opaque body accepted a literal password", mediaType)
		}
		guard.Close()
	}
	// JSON framing keeps rejecting the JSON-escaped spelling on the wire.
	wire := `{"value":"fixture-\"password\\\u003ctail\u003e"}`
	guard := newCredentialBody(io.NopCloser(fragmentedReader{strings.NewReader(wire)}), credential, "application/json")
	if _, err := io.ReadAll(guard); err == nil {
		t.Fatal("JSON body accepted a JSON-escaped spelling")
	}
	guard.Close()
}
