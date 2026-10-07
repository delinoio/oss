// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

const nativeClaudeClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"

var nativeOAuthValue = regexp.MustCompile(`^[A-Za-z0-9_-]{16,512}$`)

// ValidateLoginURL accepts only the pinned original CLI's subscription profile.
// It validates presentation; the original CLI alone creates PKCE and exchanges
// the code. No URL is normalized, repaired or retained outside the operation.
func ValidateLoginURL(raw string) error {
	invalid := func() error {
		return domain.Fail(domain.Unsupported, "The original Claude login address is unsupported.", "Keep the original native operation; do not substitute another authentication flow.")
	}
	for _, b := range []byte(raw) {
		if b < 33 || b > 126 {
			return invalid()
		}
	}
	if len(raw) > 8192 || strings.ContainsAny(raw, "\r\n\t ") {
		return invalid()
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "claude.com" || u.Path != "/cai/oauth/authorize" || u.RawPath != "" || u.User != nil || u.Fragment != "" {
		return invalid()
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return invalid()
	}
	allowed := []string{"code", "client_id", "response_type", "redirect_uri", "scope", "code_challenge", "code_challenge_method", "state", "orgUUID", "login_hint", "login_method"}
	for key, values := range q {
		if !slices.Contains(allowed, key) || len(values) != 1 || len(values[0]) > 2048 || strings.ContainsAny(values[0], "\x00\r\n") {
			return invalid()
		}
	}
	if q.Get("client_id") != nativeClaudeClientID || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || !nativeOAuthValue.MatchString(q.Get("code_challenge")) || len(q.Get("code_challenge")) != 43 || !nativeOAuthValue.MatchString(q.Get("state")) {
		return invalid()
	}
	if q.Get("code") != "" && q.Get("code") != "true" {
		return invalid()
	}
	redirect := q.Get("redirect_uri")
	if redirect != "https://platform.claude.com/oauth/code/callback" {
		callback, err := url.Parse(redirect)
		if err != nil || callback.Scheme != "http" || callback.Hostname() != "localhost" || callback.Port() == "" || callback.User != nil || callback.Path != "/callback" || callback.RawPath != "" || callback.RawQuery != "" || callback.Fragment != "" || callback.ForceQuery {
			return invalid()
		}
		port, err := strconv.ParseUint(callback.Port(), 10, 16)
		if err != nil || port == 0 || callback.Host != "localhost:"+strconv.FormatUint(port, 10) {
			return invalid()
		}
	}
	scopes := strings.Fields(q.Get("scope"))
	allowedScopes := []string{"org:create_api_key", "user:profile", "user:inference", "user:sessions:claude_code", "user:mcp_servers", "user:file_upload"}
	if len(scopes) == 0 || !slices.Contains(scopes, "user:inference") || !slices.Contains(scopes, "user:profile") {
		return invalid()
	}
	seen := map[string]bool{}
	for _, scope := range scopes {
		if !slices.Contains(allowedScopes, scope) || seen[scope] {
			return invalid()
		}
		seen[scope] = true
	}
	return nil
}
func LoginCodeValid(code []byte) bool {
	if len(code) == 0 || len(code) > 16384 {
		return false
	}
	// One original readline input, never a second line, terminal escape or shell.
	for _, b := range code {
		if b < 33 || b > 126 {
			return false
		}
	}
	return true
}

type AuthConfig struct {
	Process       process.Config
	Version, Home string
}
type AuthStatus struct {
	LoggedIn          bool    `json:"loggedIn"`
	AuthMethod        string  `json:"authMethod"`
	APIProvider       string  `json:"apiProvider"`
	ForcedLoginMethod *string `json:"forcedLoginMethod,omitempty"`
	APIKeySource      *string `json:"apiKeySource,omitempty"`
	Email             *string `json:"email,omitempty"`
	OrgID             *string `json:"orgId,omitempty"`
	OrgName           *string `json:"orgName,omitempty"`
	SubscriptionType  *string `json:"subscriptionType,omitempty"`
}

func (s AuthStatus) Subscription() bool {
	return s.LoggedIn && s.AuthMethod == "claude.ai" && s.APIProvider == "firstParty" && s.APIKeySource == nil && s.SubscriptionType != nil && slices.Contains([]string{"pro", "max", "team", "enterprise"}, *s.SubscriptionType) && s.Email != nil && domain.Text(*s.Email, "native identity", 512, true) == nil && s.OrgID != nil && domain.Text(*s.OrgID, "native organization", 512, true) == nil && (s.ForcedLoginMethod == nil || *s.ForcedLoginMethod == "claudeai")
}
func authProblem() *domain.Error {
	return domain.Fail(domain.Unauthenticated, "Claude subscription authentication could not be confirmed.", "Inspect the original Runner Device and login operation; do not resend the approval code.")
}
func authEnvironment(config AuthConfig) ([]string, error) {
	if config.Version != SupportedVersion {
		return nil, incompatible()
	}
	env, err := nativeEnvironment(ProbeConfig{Process: config.Process, Version: config.Version, Home: config.Home}, false)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(env))
	for _, entry := range env {
		if !strings.HasPrefix(entry, "ANTHROPIC_BASE_URL=") {
			result = append(result, entry)
		}
	}
	return append(result, "CLAUDE_SECURESTORAGE_CONFIG_DIR="+config.Home), nil
}

type authOutput struct {
	mu     sync.Mutex
	data   []byte
	limit  int
	failed bool
}

func (w *authOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failed || len(w.data)+len(p) > w.limit {
		w.failed = true
		return 0, io.ErrShortBuffer
	}
	w.data = append(w.data, p...)
	return len(p), nil
}
func (w *authOutput) close() { w.mu.Lock(); defer w.mu.Unlock(); clear(w.data); w.data = nil }
func authCommand(ctx context.Context, config AuthConfig, args ...string) ([]byte, error) {
	env, err := authEnvironment(config)
	if err != nil {
		return nil, err
	}
	output := &authOutput{limit: 32 << 10}
	defer output.close()
	prepared := config.Process
	prepared.Env = env
	prepared.Args = args
	prepared.Stdout = output
	stderr := &authOutput{limit: 32 << 10}
	defer stderr.close()
	prepared.Stderr = stderr
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err = process.Run(bounded, prepared)
	output.mu.Lock()
	defer output.mu.Unlock()
	if output.failed {
		return nil, authProblem()
	}
	return slices.Clone(output.data), err
}
func VerifyAuthVersion(ctx context.Context, config AuthConfig) error {
	raw, err := authCommand(ctx, config, "--version")
	defer clear(raw)
	if err != nil {
		return domain.SafeError(err)
	}
	if string(bytes.TrimSpace(raw)) != SupportedVersion+" (Claude Code)" {
		return incompatible()
	}
	return nil
}
func ReadAuthStatus(ctx context.Context, config AuthConfig) (AuthStatus, error) {
	var status AuthStatus
	raw, err := authCommand(ctx, config, "auth", "status", "--json")
	defer clear(raw)
	if domain.Decode(raw, &status) != nil || status.APIProvider != "firstParty" {
		return status, authProblem()
	}
	if err != nil {
		var exit interface{ ExitCode() int }
		if !status.LoggedIn && errors.As(err, &exit) && exit.ExitCode() == 1 && status.AuthMethod == "none" {
			return status, nil
		}
		return status, domain.SafeError(err)
	}
	if !status.Subscription() {
		return status, authProblem()
	}
	return status, nil
}
func LogoutSubscription(ctx context.Context, config AuthConfig) error {
	raw, err := authCommand(ctx, config, "auth", "logout")
	clear(raw)
	if err != nil {
		return domain.SafeError(err)
	}
	status, err := ReadAuthStatus(ctx, config)
	if err != nil || status.LoggedIn {
		return authProblem()
	}
	return nil
}

type AuthLoginProgress struct {
	URL    string
	Method domain.SubscriptionLoginMethod
}
type loginOutput struct {
	mu       sync.Mutex
	data     []byte
	progress AuthLoginProgress
	problem  error
}

func (w *loginOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.problem != nil {
		return 0, w.problem
	}
	if len(w.data)+len(p) > 32<<10 {
		w.problem = authProblem()
		return 0, w.problem
	}
	w.data = append(w.data, p...)
	// Claude 2.1.236 has no machine-readable auth-login output. Pipes use its
	// original plaintext URL line. Parse only a complete bounded line and this
	// pinned profile; remove this adapter when a verified structured CLI auth
	// interface replaces it. Never use a PTY or interpret terminal styling.
	for _, line := range bytes.Split(w.data, []byte{'\n'})[:bytes.Count(w.data, []byte{'\n'})] {
		raw := bytes.TrimSpace(line)
		if !bytes.HasPrefix(raw, []byte("https://claude.com/cai/oauth/authorize?")) {
			continue
		}
		value := string(raw)
		if err := ValidateLoginURL(value); err != nil {
			w.problem = err
			return 0, err
		}
		if w.progress.URL != "" && w.progress.URL != value {
			w.problem = authProblem()
			return 0, w.problem
		}
		method := domain.SubscriptionBrowserCallback
		u, _ := url.Parse(value)
		if u.Query().Get("redirect_uri") == "https://platform.claude.com/oauth/code/callback" {
			method = domain.SubscriptionBrowserCode
		}
		w.progress = AuthLoginProgress{URL: value, Method: method}
	}
	return len(p), nil
}

type AuthLogin struct {
	handle    *process.Handle
	output    *loginOutput
	mu        sync.Mutex
	submitted bool
}

func StartSubscriptionLogin(ctx context.Context, config AuthConfig) (*AuthLogin, error) {
	if config.Process.OwnerID.Validate() != nil || !filepath.IsAbs(config.Process.Executable) {
		return nil, authProblem()
	}
	env, err := authEnvironment(config)
	if err != nil {
		return nil, err
	}
	output := &loginOutput{}
	prepared := config.Process
	prepared.Env = env
	prepared.Args = []string{"auth", "login", "--claudeai"}
	prepared.Stdout = output
	stderr := &authOutput{limit: 32 << 10}
	defer stderr.close()
	prepared.Stderr = stderr
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
	return &AuthLogin{handle: h, output: output}, nil
}
func (l *AuthLogin) Progress() (AuthLoginProgress, error) {
	l.output.mu.Lock()
	defer l.output.mu.Unlock()
	return l.output.progress, l.output.problem
}
func (l *AuthLogin) Submit(code []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	defer clear(code)
	progress, err := l.Progress()
	if err != nil {
		return err
	}
	if l.submitted || progress.Method != domain.SubscriptionBrowserCode || !LoginCodeValid(code) {
		return authProblem()
	}
	l.submitted = true
	input := append(slices.Clone(code), '\n')
	defer clear(input)
	n, err := l.handle.Write(input)
	if err != nil || n != len(input) {
		return authProblem()
	}
	return nil
}
func (l *AuthLogin) Done() <-chan struct{} { return l.handle.Done() }
func (l *AuthLogin) Wait() error {
	err := l.handle.Wait()
	if err != nil {
		return domain.SafeError(err)
	}
	return nil
}
func (l *AuthLogin) Close() error {
	err := l.handle.Close()
	l.output.mu.Lock()
	clear(l.output.data)
	l.output.data = nil
	l.output.progress = AuthLoginProgress{}
	l.output.mu.Unlock()
	if err != nil {
		return streamUncertain()
	}
	return nil
}

// The native status JSON is decoded only here. This helper returns bounded
// identity bytes to the Worker-local keyed comparison, never an RPC account.
func (s AuthStatus) Identity() ([]byte, error) {
	if !s.Subscription() {
		return nil, authProblem()
	}
	return json.Marshal(struct{ Email, Organization string }{*s.Email, *s.OrgID})
}
