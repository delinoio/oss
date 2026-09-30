// SPDX-License-Identifier: Apache-2.0
package outbound

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"sync"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Hold only unresolved protected prefixes across response reads without
// delaying unrelated stream data. This finite literal/JSON/Base64 guard cannot
// detect arbitrary transformations performed by a hostile proxy.
type credentialBody struct {
	body     io.ReadCloser
	patterns [][]byte
	pending  []byte
	mu       sync.Mutex
	failed   bool
	eof      bool
}

func newCredentialBody(body io.ReadCloser, c domain.ProxyCredential) *credentialBody {
	g := &credentialBody{body: body}
	for _, value := range []string{c.Username, c.Password, c.Username + ":" + c.Password} {
		candidates := []string{value, base64.StdEncoding.EncodeToString([]byte(value)), base64.RawStdEncoding.EncodeToString([]byte(value))}
		for _, candidate := range candidates {
			encoded, _ := json.Marshal(candidate)
			// Short credentials still match exact JSON values. Arbitrary one-byte
			// substring rejection would make valid usernames unusable in any JSON.
			g.patterns = append(g.patterns, bytes.Clone(encoded))
			if len(candidate) >= 8 {
				g.patterns = append(g.patterns, []byte(candidate), bytes.Clone(encoded[1:len(encoded)-1]))
			}
		}
	}
	g.patterns = append(g.patterns, []byte("Basic "+base64.StdEncoding.EncodeToString([]byte(c.Username+":"+c.Password))))

	return g
}
func (g *credentialBody) contains(raw []byte) bool {
	for _, p := range g.patterns {
		if bytes.Contains(raw, p) {
			return true
		}
	}
	return false
}
func (g *credentialBody) retainedPrefix() int {
	retained := 0
	for _, pattern := range g.patterns {
		for n := min(len(pattern)-1, len(g.pending)); n > retained; n-- {
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
		clear(buf)
		if g.contains(g.pending) {
			clear(g.pending)
			g.pending = nil
			g.failed = true
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
	return err
}
