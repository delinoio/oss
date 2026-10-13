// SPDX-License-Identifier: Apache-2.0
package outbound

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"strings"
	"sync"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Hold only unresolved protected prefixes across response reads without
// delaying unrelated stream data. This finite literal/JSON/Base64 guard cannot
// detect arbitrary transformations performed by a hostile proxy.
type credentialBody struct {
	body          io.ReadCloser
	jsonGuard     *credentialJSONGuard
	sseGuard      *credentialJSONGuard
	patterns      [][]byte
	shortPatterns [][]byte
	// Header values are independent complete values rather than framed bodies,
	// so their patterns always include JSON-escaped spellings.
	headerPatterns      [][]byte
	headerShortPatterns [][]byte
	previous            byte
	hasPrevious         bool
	pending             []byte
	mu                  sync.Mutex
	failed              bool
	eof                 bool
}

func newCredentialBody(body io.ReadCloser, c domain.ProxyCredential, contentType ...string) *credentialBody {
	return newExpectedCredentialBody(body, c, false, contentType...)
}

// A caller that asked for JSON will parse the body as JSON whatever media type
// the proxy or server labels it with. expectJSON keeps JSON escape decoding for
// such a body when the response label would otherwise make it opaque.
func newExpectedCredentialBody(body io.ReadCloser, c domain.ProxyCredential, expectJSON bool, contentType ...string) *credentialBody {
	g := &credentialBody{body: body, jsonGuard: &credentialJSONGuard{}}
	if len(contentType) > 0 {
		mediaType, _, err := mime.ParseMediaType(contentType[0])
		if err == nil && mediaType != "" {
			g.jsonGuard.framing = credentialFramingForMediaType(mediaType)
			if g.jsonGuard.framing == credentialOpaque && expectJSON {
				g.jsonGuard.framing = credentialOrdinary
			}
		}
	}
	for _, value := range []string{c.Username, c.Password, c.Username + ":" + c.Password} {
		candidates := []string{value, base64.StdEncoding.EncodeToString([]byte(value)), base64.RawStdEncoding.EncodeToString([]byte(value))}
		for _, candidate := range candidates {
			if len(candidate) >= 8 {
				g.jsonGuard.patterns = append(g.jsonGuard.patterns, []byte(candidate))
			} else {
				g.jsonGuard.short = append(g.jsonGuard.short, []byte(candidate))
			}
			encoded, _ := json.Marshal(candidate)
			escaped := encoded[1 : len(encoded)-1]
			headerForms := [][]byte{[]byte(candidate), escaped}
			g.headerPatterns = append(g.headerPatterns, bytes.Clone(encoded))
			bodyForms := [][]byte{[]byte(candidate)}
			if g.jsonGuard.framing != credentialOpaque {
				// JSON-escaped spellings (for example a quoted or backslash
				// escaped password) exist only in JSON-framed bodies. Opaque
				// bodies are matched on their literal and Base64 wire forms.
				g.patterns = append(g.patterns, bytes.Clone(encoded))
				bodyForms = append(bodyForms, escaped)
			}
			for _, form := range headerForms {
				if len(form) >= 8 {
					g.headerPatterns = append(g.headerPatterns, bytes.Clone(form))
				} else {
					g.headerShortPatterns = append(g.headerShortPatterns, bytes.Clone(form))
				}
			}
			for _, form := range bodyForms {
				if len(form) >= 8 {
					g.patterns = append(g.patterns, bytes.Clone(form))
				} else {
					// Short forms match complete byte tokens, including plaintext
					// SSE values. Substring matching would reject unrelated words.
					g.shortPatterns = append(g.shortPatterns, bytes.Clone(form))
				}
			}
		}
	}
	basic := []byte("Basic " + base64.StdEncoding.EncodeToString([]byte(c.Username+":"+c.Password)))
	g.patterns = append(g.patterns, basic)
	g.headerPatterns = append(g.headerPatterns, bytes.Clone(basic))
	if g.jsonGuard.framing == credentialUnknown {
		// Typeless input can be either JSON or SSE. Keep a streaming SSE view
		// alongside the bounded framing probe so a JSON-shaped unknown field name
		// cannot commit the response to the wrong decoder before a later data field.
		g.sseGuard = &credentialJSONGuard{framing: credentialSSE}
		for _, pattern := range g.jsonGuard.patterns {
			g.sseGuard.patterns = append(g.sseGuard.patterns, bytes.Clone(pattern))
		}
		for _, pattern := range g.jsonGuard.short {
			g.sseGuard.short = append(g.sseGuard.short, bytes.Clone(pattern))
		}
	}

	return g
}

// Choose the decoding framing for a parsed response media type. Only JSON media
// types decode JSON string escapes, and SSE decodes only joined data fields.
// Every other typed body is opaque: its source text is not JSON, so escape-like
// sequences are literal content and only wire-form matching applies.
func credentialFramingForMediaType(mediaType string) credentialFraming {
	mediaType = strings.ToLower(mediaType)
	switch {
	case mediaType == "text/event-stream":
		return credentialSSE
	case credentialJSONMediaType(mediaType):
		return credentialOrdinary
	default:
		return credentialOpaque
	}
}

// Report whether the request's Accept header asks for a JSON media type.
// Wildcards do not count: only an explicit JSON request makes the caller's
// parser JSON-based.
func credentialRequestExpectsJSON(accept []string) bool {
	for _, header := range accept {
		for _, part := range strings.Split(header, ",") {
			mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(part))
			if err == nil && credentialJSONMediaType(strings.ToLower(mediaType)) {
				return true
			}
		}
	}
	return false
}

func credentialJSONMediaType(mediaType string) bool {
	switch mediaType {
	case "application/json", "text/json", "application/x-ndjson", "application/jsonl", "application/x-jsonlines", "application/json-seq":
		return true
	}
	// RFC 7464 JSON text sequences use application/json-seq and +json-seq. Their
	// RS-separated texts are ordinary JSON strings for escape decoding.
	return strings.HasSuffix(mediaType, "+json") || strings.HasSuffix(mediaType, "+json-seq")
}

// Headers are complete independent values; response reads also carry the last
// emitted byte and withhold an unresolved right boundary until another read/EOF.
func (g *credentialBody) contains(raw []byte) bool {
	return containsCredentialForms(raw, true, 0, false, g.headerPatterns, g.headerShortPatterns)
}
func credentialTokenByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '-' || b >= 0x80
}
func (g *credentialBody) containsBounded(raw []byte, ended bool, previous byte, hasPrevious bool) bool {
	return containsCredentialForms(raw, ended, previous, hasPrevious, g.patterns, g.shortPatterns)
}

// HTTP field names are case-insensitive and net/http canonicalizes them before
// this guard sees them. Match their finite protected forms independently of
// case, while preserving the body's case-sensitive token boundary rules.
func (g *credentialBody) containsHeaderName(name string) bool {
	raw := []byte(strings.ToLower(name))
	patterns, short := make([][]byte, len(g.headerPatterns)), make([][]byte, len(g.headerShortPatterns))
	defer func() {
		clear(raw)
		for _, forms := range [][][]byte{patterns, short} {
			for _, form := range forms {
				clear(form)
			}
		}
	}()
	for i, form := range g.headerPatterns {
		patterns[i] = bytes.ToLower(form)
	}
	for i, form := range g.headerShortPatterns {
		short[i] = bytes.ToLower(form)
	}
	return containsCredentialForms(raw, true, 0, false, patterns, short)
}

func containsCredentialForms(raw []byte, ended bool, previous byte, hasPrevious bool, patterns, shortPatterns [][]byte) bool {
	for _, p := range patterns {
		if bytes.Contains(raw, p) {
			return true
		}
	}
	for _, pattern := range shortPatterns {
		for offset := 0; offset < len(raw); {
			index := bytes.Index(raw[offset:], pattern)
			if index < 0 {
				break
			}
			start := offset + index
			end := start + len(pattern)
			left := start == 0 && (!hasPrevious || !credentialTokenByte(previous)) || start > 0 && !credentialTokenByte(raw[start-1])
			right := end == len(raw) && ended || end < len(raw) && !credentialTokenByte(raw[end])
			if left && right {
				return true
			}
			offset = start + 1
		}
	}
	return false
}
func (g *credentialBody) retainedPrefix() int {
	retained := int(g.jsonGuard.position - g.jsonGuard.retainFrom())
	if g.sseGuard != nil {
		if candidate := int(g.sseGuard.position - g.sseGuard.retainFrom()); candidate > retained {
			retained = candidate
		}
	}
	if g.jsonGuard.framing == credentialUnknown && len(g.jsonGuard.probe) > retained {
		retained = len(g.jsonGuard.probe)
	}
	for _, pattern := range g.patterns {
		for n := min(len(pattern)-1, len(g.pending)); n > retained; n-- {
			if bytes.Equal(g.pending[len(g.pending)-n:], pattern[:n]) {
				retained = n
				break
			}
		}
	}
	// Retain even a complete short form until its right boundary is known.
	for _, pattern := range g.shortPatterns {
		for n := min(len(pattern), len(g.pending)); n > retained; n-- {
			if bytes.Equal(g.pending[len(g.pending)-n:], pattern[:n]) {
				retained = n
				break
			}
		}
	}
	return retained
}
func (g *credentialBody) Read(p []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.failed {
		return 0, unavailable()
	}
	if len(p) == 0 {
		return 0, nil
	}
	for !g.eof && len(g.pending) == g.retainedPrefix() {
		buf := make([]byte, 8192)
		n, err := g.body.Read(buf)
		g.pending = append(g.pending, buf[:n]...)
		decodedReflection := g.jsonGuard.scan(buf[:n])
		if g.sseGuard != nil && g.sseGuard.scan(buf[:n]) {
			decodedReflection = true
		}
		clear(buf)
		g.eof = err == io.EOF
		if g.eof {
			g.jsonGuard.finishProbe()
			if g.jsonGuard.finish() {
				decodedReflection = true
			}
			if g.sseGuard != nil && g.sseGuard.finish() {
				decodedReflection = true
			}
		}
		if decodedReflection || g.containsBounded(g.pending, g.eof, g.previous, g.hasPrevious) {
			clear(g.pending)
			g.pending = nil
			g.failed = true
			_ = g.body.Close()
			return 0, unavailable()
		}
		if err != nil {
			if err != io.EOF {
				g.failed = true
				return 0, err
			}
			g.eof = true
		}
	}
	available := len(g.pending)
	if !g.eof {
		available -= g.retainedPrefix()
	}
	if available == 0 && g.eof {
		return 0, io.EOF
	}
	n := copy(p, g.pending[:available])
	if n > 0 {
		g.previous, g.hasPrevious = p[n-1], true
	}
	copy(g.pending, g.pending[n:])
	clear(g.pending[len(g.pending)-n:])
	g.pending = g.pending[:len(g.pending)-n]
	return n, nil
}
func (g *credentialBody) Close() error {
	err := g.body.Close()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failed = true
	clear(g.pending)
	for _, p := range g.patterns {
		clear(p)
	}
	for _, p := range g.shortPatterns {
		clear(p)
	}
	for _, forms := range [][][]byte{g.headerPatterns, g.headerShortPatterns} {
		for _, p := range forms {
			clear(p)
		}
	}
	g.previous, g.hasPrevious = 0, false
	g.jsonGuard.clear()
	if g.sseGuard != nil {
		g.sseGuard.clear()
	}
	return err
}
