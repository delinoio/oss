// SPDX-License-Identifier: Apache-2.0
package outbound

import (
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

func TestCredentialSSEFramingRetainsBoundedState(t *testing.T) {
	wire := ": " + strings.Repeat("metadata", 20000) + "\n\ndata: {\"value\":\"" + strings.Repeat(`\u0061`, 20000) + "\"}\n\n"
	source := strings.NewReader(wire)
	guard := newCredentialBody(io.NopCloser(source), domainCredentialFixture(), "text/event-stream")
	buffer := make([]byte, 256)
	var output strings.Builder
	for {
		n, err := guard.Read(buffer)
		output.Write(buffer[:n])
		if len(guard.jsonGuard.probe) > 6 || len(guard.jsonGuard.decoded) > 32 || len(guard.jsonGuard.escape) > 12 || len(guard.pending) > 8192+12 {
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
