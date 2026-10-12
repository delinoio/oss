// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/mattn/go-runewidth"
)

// This profile is independent of auth-login. The original CLI creates PKCE,
// validates state and exchanges the manual code; DeliDev only transports it.
func ValidateSetupTokenURL(raw string) error {
	if len(raw) > 8192 || !LoginCodeValid([]byte(raw)) {
		return authProblem()
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "claude.com" || u.Path != "/cai/oauth/authorize" || u.RawPath != "" || u.User != nil || u.Fragment != "" {
		return authProblem()
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q) != 8 {
		return authProblem()
	}
	expected := map[string]string{"code": "true", "client_id": nativeClaudeClientID, "response_type": "code", "redirect_uri": "https://platform.claude.com/oauth/code/callback", "scope": "user:inference", "code_challenge_method": "S256"}
	for key, values := range q {
		if len(values) != 1 {
			return authProblem()
		}
		if v, ok := expected[key]; ok {
			if values[0] != v {
				return authProblem()
			}
		} else if (key != "state" && key != "code_challenge") || len(values[0]) != 43 || !nativeOAuthValue.MatchString(values[0]) {
			return authProblem()
		}
	}
	return nil
}
func SetupTokenCodeValid(code []byte, rawURL string) bool {
	if !LoginCodeValid(code) || ValidateSetupTokenURL(rawURL) != nil {
		return false
	}
	parts := bytes.Split(code, []byte{'#'})
	u, _ := url.Parse(rawURL)
	return len(parts) == 2 && len(parts[0]) > 0 && string(parts[1]) == u.Query().Get("state")
}

const setupRows = 120
const setupColumns = 240
const maxSetupToken = 8192

// setupScreen reconstructs only the pinned Ink terminal grammar. It does not
// retain a raw transcript or interpret an executable terminal command. Unknown
// escapes fail closed. Remove this parser when verified structured output exists.
type setupScreen struct {
	mu              sync.Mutex
	cells           [setupRows][setupColumns]rune
	row, col, total int
	pending         []byte
	progress        AuthLoginProgress
	token           []byte
	problem         error
	prompt, success bool
}

func (s *setupScreen) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.problem != nil {
		return 0, s.problem
	}
	s.total += len(p)
	if s.total > 512<<10 {
		s.problem = authProblem()
		return 0, s.problem
	}
	s.pending = append(s.pending, p...)
	for len(s.pending) > 0 {
		n, err := s.consume()
		if err != nil {
			s.problem = err
			return 0, err
		}
		if n == 0 {
			break
		}
		clear(s.pending[:n])
		s.pending = s.pending[n:]
	}
	if len(s.pending) > 8192 {
		s.problem = authProblem()
		return 0, s.problem
	}
	if err := s.observe(); err != nil {
		s.problem = err
		return 0, err
	}
	return len(p), nil
}
func (s *setupScreen) consume() (int, error) {
	p := s.pending
	if p[0] == 27 {
		if len(p) < 2 {
			return 0, nil
		}
		if p[1] == ']' {
			end := bytes.IndexByte(p[2:], 7)
			n := 0
			if end >= 0 {
				end += 2
				n = end + 1
			} else if i := bytes.Index(p[2:], []byte{27, '\\'}); i >= 0 {
				end = i + 2
				n = end + 2
			} else {
				return 0, nil
			}
			payload := string(p[2:end])
			parts := strings.SplitN(payload, ";", 3)
			if len(parts) != 3 || parts[0] != "8" || parts[1] != "" || parts[2] != "" && ValidateSetupTokenURL(parts[2]) != nil {
				return 0, authProblem()
			}
			return n, nil
		}
		if p[1] != '[' {
			return 0, authProblem()
		}
		end := 2
		for end < len(p) && (p[end] < 0x40 || p[end] > 0x7e) {
			end++
		}
		if end == len(p) {
			return 0, nil
		}
		raw := string(p[2:end])
		cmd := p[end]
		if strings.HasPrefix(raw, "?") {
			if (raw != "?25" && raw != "?2026") || (cmd != 'h' && cmd != 'l') {
				return 0, authProblem()
			}
			return end + 1, nil
		}
		parts := strings.Split(raw, ";")
		v := make([]int, len(parts))
		for i, part := range parts {
			if part == "" {
				v[i] = 0
				continue
			}
			n, e := strconv.Atoi(part)
			if e != nil || n < 0 || n > 1000 {
				return 0, authProblem()
			}
			v[i] = n
		}
		amount := v[0]
		if amount == 0 {
			amount = 1
		}
		switch cmd {
		case 'm': // SGR changes presentation only.
		case 'A':
			s.row -= amount
		case 'B':
			s.row += amount
		case 'C':
			s.col += amount
		case 'D':
			s.col -= amount
		case 'G':
			s.col = amount - 1
		case 'H', 'f':
			s.row = amount - 1
			s.col = 0
			if len(v) > 1 {
				s.col = max(1, v[1]) - 1
			}
		case 'J':
			if v[0] == 2 {
				clear(s.cells[:])
			} else if v[0] == 0 {
				clear(s.cells[s.row][s.col:])
				for r := s.row + 1; r < setupRows; r++ {
					clear(s.cells[r][:])
				}
			} else {
				return 0, authProblem()
			}
		case 'K':
			switch v[0] {
			case 0:
				clear(s.cells[s.row][s.col:])
			case 1:
				clear(s.cells[s.row][:s.col+1])
			case 2:
				clear(s.cells[s.row][:])
			default:
				return 0, authProblem()
			}
		default:
			return 0, authProblem()
		}
		if s.row < 0 || s.row >= setupRows || s.col < 0 || s.col >= setupColumns {
			return 0, authProblem()
		}
		return end + 1, nil
	}
	switch p[0] {
	case '\r':
		s.col = 0
		return 1, nil
	case '\n':
		s.row++
		s.col = 0
		if s.row >= setupRows {
			return 0, authProblem()
		}
		return 1, nil
	case 8:
		if s.col > 0 {
			s.col--
		}
		return 1, nil
	case 7:
		return 1, nil
	}
	if !utf8.FullRune(p) {
		return 0, nil
	}
	r, n := utf8.DecodeRune(p)
	if r == utf8.RuneError || r < 32 || r == 127 {
		return 0, authProblem()
	}
	width := runewidth.RuneWidth(r)
	if width < 1 || width > 2 {
		return 0, authProblem()
	}
	if s.col+width > setupColumns {
		s.row++
		s.col = 0
	}
	if s.row >= setupRows {
		return 0, authProblem()
	}
	s.cells[s.row][s.col] = r
	if width == 2 {
		s.cells[s.row][s.col+1] = ' '
	}
	s.col += width
	if s.col == setupColumns {
		s.row++
		s.col = 0
		if s.row >= setupRows {
			return 0, authProblem()
		}
	}
	return n, nil
}
func (s *setupScreen) observe() error {
	lines := make([]string, setupRows)
	for r := range s.cells {
		var b strings.Builder
		for _, c := range s.cells[r] {
			if c == 0 {
				c = ' '
			}
			b.WriteRune(c)
		}
		lines[r] = strings.TrimRight(b.String(), " ")
	}
	screen := strings.Join(lines, "\n")
	// Wrapped URLs/tokens are joined only between their exact semantic markers.
	for _, bad := range []string{"Press Enter to retry", "OAuth error", "Authentication failed", "account_on_hold"} {
		if strings.Contains(screen, bad) {
			return authProblem()
		}
	}
	if i := strings.Index(screen, "https://claude.com/cai/oauth/authorize?"); i >= 0 {
		rest := screen[i:]
		end := strings.Index(rest, "Paste code here if prompted >")
		if end >= 0 {
			raw := strings.ReplaceAll(strings.TrimSpace(rest[:end]), "\n", "")
			if ValidateSetupTokenURL(raw) != nil {
				return authProblem()
			}
			if s.progress.URL != "" && s.progress.URL != raw || s.success {
				return authProblem()
			}
			s.progress = AuthLoginProgress{URL: raw, Method: domain.SubscriptionBrowserCode}
			s.prompt = true
		}
	}
	const heading = "✓ Long-lived authentication token created successfully!"
	const label = "Your OAuth token (valid for "
	const tail = "Store this token securely. You won't be able to see it again."
	if strings.Count(screen, heading) > 1 || strings.Count(screen, label) > 1 || strings.Count(screen, tail) > 1 {
		return authProblem()
	}
	if start := strings.Index(screen, label); start >= 0 {
		region := screen[start+len(label):]
		marker := strings.Index(region, "):")
		end := strings.Index(region, tail)
		if marker >= 0 && end > marker {
			if !strings.Contains(screen[:start], heading) || !strings.Contains(region[end:], "Use this token by setting: export CLAUDE_CODE_OAUTH_TOKEN=<token>") {
				return nil
			}
			value := strings.ReplaceAll(strings.TrimSpace(region[marker+2:end]), "\n", "")
			if len(value) == 0 || len(value) > maxSetupToken || !LoginCodeValid([]byte(value)) || len(s.token) > 0 && !bytes.Equal(s.token, []byte(value)) {
				return authProblem()
			}
			if len(s.token) == 0 {
				s.token = []byte(value)
			}
			s.success = true
		}
	}
	return nil
}
func (s *setupScreen) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.pending)
	clear(s.token)
	s.pending = nil
	s.token = nil
	clear(s.cells[:])
	s.progress = AuthLoginProgress{}
}

type SetupTokenLogin struct {
	handle    *process.Handle
	output    *setupScreen
	mu        sync.Mutex
	submitted bool
}

func StartSetupToken(ctx context.Context, config AuthConfig, opener string) (*SetupTokenLogin, error) {
	if config.Process.OwnerID.Validate() != nil || !filepath.IsAbs(config.Process.Executable) || !filepath.IsAbs(opener) {
		return nil, authProblem()
	}
	env, err := authEnvironment(config)
	if err != nil {
		return nil, err
	}
	output := &setupScreen{}
	prepared := config.Process
	prepared.Env = append(env, "BROWSER="+opener, "TERM=xterm-256color")
	prepared.Args = []string{"setup-token"}
	prepared.Terminal = &process.TerminalSize{Rows: setupRows, Columns: setupColumns}
	prepared.Stdout = output
	prepared.Stderr = io.Discard
	h, err := process.Start(ctx, prepared)
	if err != nil {
		output.close()
		return nil, err
	}
	if err = h.Resume(); err != nil {
		if h.Close() != nil {
			err = streamUncertain()
		}
		output.close()
		return nil, err
	}
	return &SetupTokenLogin{handle: h, output: output}, nil
}
func (l *SetupTokenLogin) Progress() (AuthLoginProgress, error) {
	l.output.mu.Lock()
	defer l.output.mu.Unlock()
	return l.output.progress, l.output.problem
}
func (l *SetupTokenLogin) Submit(code []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	defer clear(code)
	p, err := l.Progress()
	if err != nil || l.submitted || !SetupTokenCodeValid(code, p.URL) {
		return authProblem()
	}
	l.submitted = true
	input := append(slices.Clone(code), '\r')
	defer clear(input)
	n, err := l.handle.Write(input)
	if err != nil || n != len(input) {
		return authProblem()
	}
	return nil
}
func (l *SetupTokenLogin) Done() <-chan struct{} { return l.handle.Done() }

// Token is available only after native exit zero and joined descendants. The
// runtime owner must additionally remove the original private home before seal.
func (l *SetupTokenLogin) Token() ([]byte, error) {
	if l.handle.Wait() != nil || l.handle.Close() != nil {
		return nil, streamUncertain()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.output.mu.Lock()
	defer l.output.mu.Unlock()
	if l.output.problem != nil || len(l.output.pending) != 0 || !l.output.success || !l.submitted {
		return nil, authProblem()
	}
	return slices.Clone(l.output.token), nil
}
func (l *SetupTokenLogin) Close() error {
	err := l.handle.Close()
	l.output.close()
	if err != nil {
		return streamUncertain()
	}
	return nil
}
