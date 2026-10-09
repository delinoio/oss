// SPDX-License-Identifier: Apache-2.0
package outbound

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
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
	patterns      [][]byte
	shortPatterns [][]byte
	previous      byte
	hasPrevious   bool
	pending       []byte
	mu            sync.Mutex
	failed        bool
	eof           bool
}

func newCredentialBody(body io.ReadCloser, c domain.ProxyCredential) *credentialBody {
	g := &credentialBody{body: body, jsonGuard: &credentialJSONGuard{}}
	for _, value := range []string{c.Username, c.Password, c.Username + ":" + c.Password} {
		candidates := []string{value, base64.StdEncoding.EncodeToString([]byte(value)), base64.RawStdEncoding.EncodeToString([]byte(value))}
		for _, candidate := range candidates {
			if len(candidate) >= 8 {
				g.jsonGuard.patterns = append(g.jsonGuard.patterns, []byte(candidate))
			} else {
				g.jsonGuard.short = append(g.jsonGuard.short, []byte(candidate))
			}
			encoded, _ := json.Marshal(candidate)
			g.patterns = append(g.patterns, bytes.Clone(encoded))
			for _, form := range [][]byte{[]byte(candidate), encoded[1 : len(encoded)-1]} {
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
	g.patterns = append(g.patterns, []byte("Basic "+base64.StdEncoding.EncodeToString([]byte(c.Username+":"+c.Password))))

	return g
}

// Headers are complete independent values; response reads also carry the last
// emitted byte and withhold an unresolved right boundary until another read/EOF.
func (g *credentialBody) contains(raw []byte) bool {
	return g.containsBounded(raw, true, 0, false)
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
	patterns, short := make([][]byte, len(g.patterns)), make([][]byte, len(g.shortPatterns))
	defer func() {
		clear(raw)
		for _, forms := range [][][]byte{patterns, short} {
			for _, form := range forms {
				clear(form)
			}
		}
	}()
	for i, form := range g.patterns {
		patterns[i] = bytes.ToLower(form)
	}
	for i, form := range g.shortPatterns {
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
		clear(buf)
		g.eof = err == io.EOF
		if g.eof && g.jsonGuard.finish() {
			decodedReflection = true
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
	g.previous, g.hasPrevious = 0, false
	g.jsonGuard.clear()
	return err
}
