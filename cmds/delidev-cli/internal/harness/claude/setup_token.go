// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

// ValidateSetupTokenURL accepts only the pinned original `claude setup-token`
// authorization address: the shared host, path and client, one inference-only
// scope, the manual code callback and the code=true marker. The original CLI
// alone creates PKCE and exchanges the code. The address is neither normalized
// nor repaired, and the auth-login profile is never reused by assumption.
func ValidateSetupTokenURL(raw string) error { return validateAuthorizationURL(raw, true) }

const (
	setupTokenRows         = 50
	setupTokenColumns      = 1000
	setupTokenOutputLimit  = 256 << 10
	setupTokenMaxEmissions = 8
	setupTokenURLPrefix    = "https://claude.com/cai/oauth/authorize?"
	setupTokenTokenPrefix  = "sk-ant-oat01-"
	// The two writes keep the code and the Enter key separate so the native
	// text input sees a typed/pasted value followed by one submit key.
	setupTokenSubmitDelay = 150 * time.Millisecond
)

var (
	setupTokenCandidate = regexp.MustCompile(`sk-ant-oat01-[A-Za-z0-9_-]*`)
	setupTokenFailures  = []string{"Invalid code", "Failed to exchange", "Token exchange failed", "OAuth error"}
)

// SetupTokenConfig describes one isolated private runtime. Home must be the
// runtime's `claude` directory and Process.Cwd its parent, exactly like auth.
type SetupTokenConfig struct {
	Process       process.Config
	Version, Home string
}

// SetupTokenProgress is the only presentation state derived from the native
// screen. It never contains the approval code or the emitted token.
type SetupTokenProgress struct {
	URL string
}

func setupTokenProblem() *domain.Error {
	return domain.Fail(domain.Unsupported, "The Claude setup-token output is unsupported.", "Keep the original operation; do not substitute another authentication flow or resend the approval code.")
}

// setupTokenNoopOpeners are the absolute paths of executables that ignore their
// argument and exit successfully. Only Unix-like hosts provide them.
var setupTokenNoopOpeners = []string{"/usr/bin/true", "/bin/true"}

// setupTokenBrowserBlocker returns a verified no-op browser opener. Windows has
// no equivalent executable that the native child is known to honor through
// BROWSER, so it and any host without a regular executable candidate fail closed.
func setupTokenBrowserBlocker(goos string, candidates []string) (string, error) {
	if goos != "windows" {
		for _, candidate := range candidates {
			if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
				return candidate, nil
			}
		}
	}
	return "", domain.Fail(domain.Unsupported, "This server cannot prevent the Claude login from opening a browser.", "Run Claude account login on a Linux or macOS server host.")
}

func setupTokenEnvironment(config SetupTokenConfig) ([]string, error) {
	env, err := authEnvironment(AuthConfig{Process: config.Process, Version: config.Version, Home: config.Home})
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(env)+2)
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(key) {
		// The native interface renders every intermediate screen only when it is
		// not in a CI environment. Inherited credentials never reach this map.
		case "CI", "TERM", "BROWSER":
			continue
		}
		result = append(result, entry)
	}
	result = append(result, "TERM=xterm-256color")
	// The server host owns no user browser. This blocks the native auto-open of
	// the callback variant; the trusted desktop binding opens the original URL.
	// A host without a verified no-op opener fails closed instead of letting the
	// child open the server user's normal browser.
	opener, err := setupTokenBrowserBlocker(runtime.GOOS, setupTokenNoopOpeners)
	if err != nil {
		return nil, err
	}
	result = append(result, "BROWSER="+opener)
	return result, nil
}

type setupTokenOutput struct {
	mu     sync.Mutex
	data   []byte
	failed bool
}

func (w *setupTokenOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failed || len(w.data)+len(p) > setupTokenOutputLimit {
		w.failed = true
		clear(w.data)
		w.data = nil
		return 0, authProblem()
	}
	w.data = append(w.data, p...)
	return len(p), nil
}

func (w *setupTokenOutput) snapshot() ([]byte, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.data), w.failed
}

func (w *setupTokenOutput) wipe() {
	w.mu.Lock()
	defer w.mu.Unlock()
	clear(w.data)
	w.data = nil
}

// sanitizeSetupTokenScreen reduces terminal output to separated plain text.
// Styling is removed without a gap; every other escape, carriage return and
// control byte becomes a separator so adjacent screen rows can never merge.
func sanitizeSetupTokenScreen(raw []byte) []byte {
	out := make([]byte, 0, len(raw))
	separate := func() {
		if len(out) != 0 && out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
	}
	for i := 0; i < len(raw); i++ {
		b := raw[i]
		switch {
		case b == 0x1b:
			if i+1 >= len(raw) {
				separate()
				continue
			}
			switch raw[i+1] {
			case '[':
				j := i + 2
				for j < len(raw) && (raw[j] < 0x40 || raw[j] > 0x7e) {
					j++
				}
				if j >= len(raw) {
					i = len(raw)
					separate()
					continue
				}
				if raw[j] != 'm' {
					separate()
				}
				i = j
			case ']':
				j := i + 2
				for j < len(raw) && raw[j] != 0x07 && !(raw[j] == 0x1b && j+1 < len(raw) && raw[j+1] == '\\') {
					j++
				}
				if j < len(raw) && raw[j] == 0x1b {
					j++
				}
				i = j
				separate()
			default:
				i++
				separate()
			}
		case b == '\n' || b == '\r':
			separate()
		case b < 0x20 || b == 0x7f:
			separate()
		default:
			out = append(out, b)
		}
	}
	return out
}

type setupTokenScreen struct {
	URL       string
	Token     []byte
	Completed bool
	Failed    bool
	Problem   error
}

// parseSetupTokenScreen is deliberately stateless and bounded. It accepts only
// complete (delimited) URL and token candidates, requires every URL occurrence
// to be identical and every token occurrence to be the same single value.
func parseSetupTokenScreen(raw []byte) setupTokenScreen {
	text := sanitizeSetupTokenScreen(raw)
	defer clear(text)
	var screen setupTokenScreen
	for rest := text; ; {
		index := bytes.Index(rest, []byte(setupTokenURLPrefix))
		if index < 0 {
			break
		}
		rest = rest[index:]
		end := bytes.IndexAny(rest, " \t\n")
		if end < 0 {
			// The address is still being written; wait for its terminator.
			break
		}
		candidate := string(rest[:end])
		if err := ValidateSetupTokenURL(candidate); err != nil {
			screen.Problem = err
			return screen
		}
		if screen.URL != "" && screen.URL != candidate {
			screen.Problem = setupTokenProblem()
			return screen
		}
		screen.URL = candidate
		rest = rest[end:]
	}
	var found []byte
	count := 0
	for _, loc := range setupTokenCandidate.FindAllIndex(text, -1) {
		if loc[1] == len(text) {
			// A trailing run may still be receiving bytes.
			continue
		}
		value := text[loc[0]:loc[1]]
		count++
		if !domain.ClaudeSetupTokenValid(value) || count > setupTokenMaxEmissions || found != nil && !bytes.Equal(found, value) {
			clear(found)
			screen.Problem = setupTokenProblem()
			return screen
		}
		if found == nil {
			found = slices.Clone(value)
		}
	}
	screen.Token = found
	screen.Completed = bytes.Contains(text, []byte("created successfully"))
	for _, marker := range setupTokenFailures {
		if bytes.Contains(text, []byte(marker)) {
			screen.Failed = true
		}
	}
	return screen
}

// SetupTokenLogin owns one original `claude setup-token` child on a PTY.
type SetupTokenLogin struct {
	handle    *process.Handle
	output    *setupTokenOutput
	logger    *slog.Logger
	mu        sync.Mutex
	submitted bool
	taken     bool
	waited    bool
}

// StartSetupToken launches the pinned CLI. It never passes --bare or any
// credential. The caller must Close the login to join the child and wipe buffers.
func StartSetupToken(ctx context.Context, config SetupTokenConfig) (*SetupTokenLogin, error) {
	if config.Process.OwnerID.Validate() != nil || !filepath.IsAbs(config.Process.Executable) {
		return nil, authProblem()
	}
	env, err := setupTokenEnvironment(config)
	if err != nil {
		return nil, err
	}
	output := &setupTokenOutput{}
	prepared := config.Process
	prepared.Env = env
	prepared.Args = []string{"setup-token"}
	prepared.Stdout = output
	prepared.Stderr = nil
	prepared.Terminal = &process.TerminalSize{Rows: setupTokenRows, Columns: setupTokenColumns}
	h, err := process.Start(ctx, prepared)
	if err != nil {
		return nil, err
	}
	if err := h.Resume(); err != nil {
		if cleanup := h.Close(); cleanup != nil {
			return nil, streamUncertain()
		}
		return nil, err
	}
	return &SetupTokenLogin{handle: h, output: output, logger: config.Process.Logger}, nil
}

// Progress returns the original authorization address once complete.
func (l *SetupTokenLogin) Progress() (SetupTokenProgress, error) {
	data, failed := l.output.snapshot()
	defer clear(data)
	if failed {
		return SetupTokenProgress{}, authProblem()
	}
	screen := parseSetupTokenScreen(data)
	defer clear(screen.Token)
	if screen.Problem != nil {
		return SetupTokenProgress{}, screen.Problem
	}
	if screen.Failed {
		return SetupTokenProgress{}, authProblem()
	}
	return SetupTokenProgress{URL: screen.URL}, nil
}

// Submit sends the approval code once. The code buffer is always cleared.
func (l *SetupTokenLogin) Submit(code []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	defer clear(code)
	progress, err := l.Progress()
	if err != nil {
		return err
	}
	if l.submitted || progress.URL == "" || !LoginCodeValid(code) {
		return authProblem()
	}
	l.submitted = true
	input := slices.Clone(code)
	defer clear(input)
	if n, err := l.handle.Write(input); err != nil || n != len(input) {
		return authProblem()
	}
	time.Sleep(setupTokenSubmitDelay)
	if n, err := l.handle.Write([]byte{'\r'}); err != nil || n != 1 {
		return authProblem()
	}
	return nil
}

func (l *SetupTokenLogin) Done() <-chan struct{} { return l.handle.Done() }

// Wait joins the original child result. A non-zero exit never yields a token.
func (l *SetupTokenLogin) Wait() error {
	err := l.handle.Wait()
	l.mu.Lock()
	l.waited = err == nil
	l.mu.Unlock()
	if err != nil {
		return domain.SafeError(err)
	}
	return nil
}

// Token returns the one emitted token after a successful exit. The native
// success heading is not a reliable part of the final PTY capture, so the
// joined zero exit and one valid token are the completion proof. It succeeds
// at most once and leaves clearing the returned bytes to the caller.
func (l *SetupTokenLogin) Token() ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.taken || !l.waited {
		return nil, authProblem()
	}
	data, failed := l.output.snapshot()
	defer clear(data)
	if failed {
		return nil, authProblem()
	}
	// Re-parse with a delimiter so a token at the final byte is complete once
	// the child has exited.
	screen := parseSetupTokenScreen(append(data, '\n'))
	if screen.Problem != nil || screen.Failed || len(screen.Token) == 0 {
		if l.logger != nil {
			plain := sanitizeSetupTokenScreen(data)
			l.logger.Warn("claude_setup_token_output_rejected",
				"output_limited", failed,
				"completion_marker", screen.Completed,
				"failure_marker", screen.Failed,
				"parser_problem", screen.Problem != nil,
				"token_candidates", len(setupTokenCandidate.FindAll(plain, -1)),
				"token_like", bytes.Contains(plain, []byte("sk-ant-")))
			clear(plain)
		}
		clear(screen.Token)
		return nil, setupTokenProblem()
	}
	l.taken = true
	return screen.Token, nil
}

// Close stops and joins the child, then wipes the private screen buffer.
func (l *SetupTokenLogin) Close() error {
	err := l.handle.Close()
	l.output.wipe()
	if err != nil {
		return streamUncertain()
	}
	return nil
}
