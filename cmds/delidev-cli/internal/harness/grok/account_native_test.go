// SPDX-License-Identifier: Apache-2.0
package grok

// Real-account acceptance for the Grok Build subscription path.
//
// TestAccountAcceptance needs a person: the account owner must approve a
// device-code login in their own browser. It is skipped unless both
// DELIDEV_NATIVE_GROK_ACCOUNT=1 and an absolute DELIDEV_NATIVE_GROK_EXECUTABLE
// are set, so CI and `go test ./...` never run it. The login URL and code are
// written only to the controlling terminal, never to test output. The runbook is
// in docs/cmds-delidev-grok-subscription-contract.md ("Build and Test").
//
// The TestNativeAccountHarness* tests exercise the same helpers against a fake
// `grok` shell script. They need no account, network or credential.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

const (
	accountGateEnv         = "DELIDEV_NATIVE_GROK_ACCOUNT"
	accountExecutableEnv   = "DELIDEV_NATIVE_GROK_EXECUTABLE"
	approvalTimeoutEnv     = "DELIDEV_NATIVE_GROK_APPROVAL_TIMEOUT"
	defaultApprovalTimeout = 5 * time.Minute
	// approvalNotice is the only login-related text a test may print.
	approvalNotice = "waiting for account owner approval"

	accountCommandTimeout = 30 * time.Second
	accountPromptTimeout  = 3 * time.Minute
	accountCleanupTimeout = 30 * time.Second
	// accountFixedBudget bounds every non-approval step: version 30s, models 30s,
	// the 3m prompt turn, logout 30s, post-logout models 30s and the cleanup logout
	// 30s, rounded up. The package timeout must cover the approval wait plus this.
	accountFixedBudget = 6 * time.Minute
	maxAccountReply    = 16 << 10
	expectedReply      = "ok"
	maxAccountLine     = 16 << 10
	maxAccountFrame    = 4 << 20
	maxAccountOutput   = 64 << 10
	cachedTokenMethod  = "cached_token"
)

var (
	ansiPattern       = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	urlPattern        = regexp.MustCompile(`https://\S+`)
	userCodePattern   = regexp.MustCompile(`^[A-Z0-9]{4}-[A-Z0-9]{4}$`)
	versionPattern    = regexp.MustCompile(`^grok (\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)\b`)
	defaultModelLine  = regexp.MustCompile(`(?m)^\s*Default model:\s*(\S+)\s*$`)
	modelEntryLine    = regexp.MustCompile(`(?m)^\s*(?:[-*]\s*)?grok-[A-Za-z0-9._-]+(?:\s+\(default\))?\s*$`)
	loggedInLine      = regexp.MustCompile(`(?m)^\s*You are logged in with\b`)
	notSignedInOutput = regexp.MustCompile(`(?i)not authenticated`)
)

// requireAccountGate skips unless the explicit real-account gates are set and
// returns the absolute native executable.
func requireAccountGate(t *testing.T) string {
	t.Helper()
	if os.Getenv(accountGateEnv) != "1" {
		t.Skip("explicit real Grok account acceptance; set " + accountGateEnv + "=1")
	}
	executable := os.Getenv(accountExecutableEnv)
	if executable == "" {
		t.Skip("explicit private native Grok Build binary required; set " + accountExecutableEnv)
	}
	if !filepath.IsAbs(executable) {
		t.Fatal("select an absolute native executable")
	}
	return executable
}

// approvalTimeout returns the account-owner approval deadline: five minutes
// unless DELIDEV_NATIVE_GROK_APPROVAL_TIMEOUT holds a Go duration of 10s..30m.
func approvalTimeout(raw string) (time.Duration, error) {
	if raw == "" {
		return defaultApprovalTimeout, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value < 10*time.Second || value > 30*time.Minute {
		return 0, fmt.Errorf("%s must be a Go duration between 10s and 30m", approvalTimeoutEnv)
	}
	return value, nil
}

// checkTimeBudget fails fast when the go test deadline cannot cover the approval
// wait plus the fixed steps, so a documented approval override is never cut off
// by the package timeout in the middle of a login.
func checkTimeBudget(deadline time.Time, hasDeadline bool, now time.Time, approval time.Duration) error {
	if !hasDeadline {
		return nil
	}
	need := approval + accountFixedBudget
	if remaining := deadline.Sub(now); remaining < need {
		return fmt.Errorf("the go test -timeout leaves %s but an approval wait of %s needs at least %s; raise -timeout (the runbook uses 45m)", remaining.Round(time.Second), approval, need)
	}
	return nil
}

// authFileDigest hashes a file without returning or printing any of its content.
// A missing file has the stable digest "absent".
func authFileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "absent", nil
	}
	if err != nil {
		return "", err
	}
	defer file.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// realAuthPath names the invoking user's credential file. It is only hashed.
func realAuthPath() (string, error) {
	if home := os.Getenv("GROK_HOME"); home != "" {
		return filepath.Join(home, "auth.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".grok", "auth.json"), nil
}

// guardRealAuth records the real credential digest and fails the test if it
// changes. The digest is never printed.
func guardRealAuth(t *testing.T) func() {
	t.Helper()
	path, err := realAuthPath()
	if err != nil {
		t.Fatalf("locate the real Grok credential file: %v", err)
	}
	before, err := authFileDigest(path)
	if err != nil {
		t.Fatalf("hash the real Grok credential file: %v", err)
	}
	check := func() {
		t.Helper()
		after, err := authFileDigest(path)
		if err != nil {
			t.Errorf("hash the real Grok credential file: %v", err)
			return
		}
		if after != before {
			t.Error("the real Grok credential file changed during the acceptance run")
		}
	}
	t.Cleanup(check)
	return check
}

// lockedBuffer is a concurrency-safe bounded capture used for sinks and output
// that must never reach test logs.
type lockedBuffer struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	limit := b.limit
	if limit == 0 {
		limit = maxAccountOutput
	}
	if b.buffer.Len()+len(p) > limit {
		b.overflow = true
		return len(p), nil
	}
	return b.buffer.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func (b *lockedBuffer) Overflowed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.overflow
}

// approvalFilter consumes the `grok login --device-auth` output. The real CLI
// prints its prompt on stderr while other builds print it on stdout, so both
// streams (Stdout and Stderr) feed one verification state. It validates only the
// verification URL host and the user-code shape, forwards exactly those two
// lines to the operator sink, and drops everything else (including the account
// identity the CLI prints after success).
type approvalFilter struct {
	mu      sync.Mutex
	sink    io.Writer
	stdout  lineAssembler
	stderr  lineAssembler
	host    string
	badHost bool
	code    bool
	sinkErr error
}

// lineAssembler splits one stream into bounded lines.
type lineAssembler struct {
	pending []byte
	overrun bool
}

// approvalStream is one stream's view of the shared filter.
type approvalStream struct {
	filter    *approvalFilter
	assembler *lineAssembler
}

func (a approvalStream) Write(p []byte) (int, error) {
	a.filter.mu.Lock()
	defer a.filter.mu.Unlock()
	for _, b := range p {
		if b == '\n' {
			a.filter.line(string(a.assembler.pending))
			a.assembler.pending = a.assembler.pending[:0]
			a.assembler.overrun = false
			continue
		}
		if a.assembler.overrun {
			continue
		}
		if len(a.assembler.pending) >= maxAccountLine {
			a.assembler.pending, a.assembler.overrun = a.assembler.pending[:0], true
			continue
		}
		a.assembler.pending = append(a.assembler.pending, b)
	}
	return len(p), nil
}

// Write consumes stdout.
func (f *approvalFilter) Write(p []byte) (int, error) { return f.Stdout().Write(p) }

// Stdout returns the writer for the CLI's standard output.
func (f *approvalFilter) Stdout() io.Writer { return approvalStream{f, &f.stdout} }

// Stderr returns the writer for the CLI's standard error.
func (f *approvalFilter) Stderr() io.Writer { return approvalStream{f, &f.stderr} }

func (f *approvalFilter) line(raw string) {
	text := strings.TrimSpace(ansiPattern.ReplaceAllString(raw, ""))
	if match := urlPattern.FindString(text); match != "" {
		parsed, err := url.Parse(match)
		if err != nil || parsed.Scheme != "https" || !trustedLoginHost(parsed.Hostname()) {
			f.badHost = true
			return
		}
		f.host = parsed.Hostname()
		f.forward("Open this URL in your browser: " + match + "\n")
		return
	}
	if userCodePattern.MatchString(text) {
		f.code = true
		f.forward("Confirm this code: " + text + "\n")
	}
}

func (f *approvalFilter) forward(text string) {
	if f.sink == nil {
		return
	}
	if _, err := io.WriteString(f.sink, text); err != nil && f.sinkErr == nil {
		f.sinkErr = err
	}
}

// flush handles a final line that had no trailing newline.
func (f *approvalFilter) flush() {
	for _, assembler := range []*lineAssembler{&f.stdout, &f.stderr} {
		if len(assembler.pending) > 0 && !assembler.overrun {
			f.line(string(assembler.pending))
		}
		assembler.pending, assembler.overrun = assembler.pending[:0], false
	}
}

// Verified reports whether the expected device-auth shape was observed. The
// error text never includes the URL or code.
func (f *approvalFilter) Verified() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.flush()
	switch {
	case f.badHost:
		return errors.New("device login printed a verification URL outside the trusted x.ai hosts")
	case f.host == "":
		return errors.New("device login did not print an https verification URL")
	case !f.code:
		return errors.New("device login did not print a user code of the form XXXX-XXXX")
	case f.sinkErr != nil:
		return errors.New("the approval details could not be written to the terminal")
	}
	return nil
}

func trustedLoginHost(host string) bool {
	return host == "x.ai" || strings.HasSuffix(host, ".x.ai")
}

// approvalTerminal opens the operator's controlling terminal. Writing there
// keeps the device code and URL out of `go test` output, CI logs and -json.
func approvalTerminal() (io.WriteCloser, error) {
	device := "/dev/tty"
	if runtime.GOOS == "windows" {
		device = "CONOUT$"
	}
	file, err := os.OpenFile(device, os.O_WRONLY, 0)
	if err != nil {
		return nil, errors.New("an interactive terminal is required to show the account owner the login approval details")
	}
	return file, nil
}

// accountScratch is a private HOME/GROK_HOME tree for one acceptance run.
type accountScratch struct {
	executable string
	root       string
	grokHome   string
	repo       string
	processes  string
	env        []string

	mu        sync.Mutex
	uncertain []error

	once       sync.Once
	cleanupErr error
}

// newAccountScratch builds a scratch tree whose cleanup failure fails the test.
func newAccountScratch(t *testing.T, executable string, extraEnv []string) *accountScratch {
	t.Helper()
	s := buildAccountScratch(t, executable, extraEnv)
	// Registered before any further setup so every failure still cleans up.
	t.Cleanup(func() {
		if err := s.Cleanup(); err != nil {
			t.Errorf("scratch cleanup: %v", err)
		}
	})
	return s
}

// noteUncertain records that a native process tree may still be alive. The
// scratch tree is then retained instead of removed from under it.
func (s *accountScratch) noteUncertain(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.uncertain = append(s.uncertain, err)
}

func (s *accountScratch) uncertainErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return errors.Join(s.uncertain...)
}

// observe flags an ownership-recovery failure from internal/process and passes
// the error through unchanged.
func (s *accountScratch) observe(err error) error {
	if err != nil && domain.SafeError(err).Code == domain.RecoveryRequired {
		s.noteUncertain(errors.New("native process ownership could not be confirmed"))
	}
	return err
}

func buildAccountScratch(t *testing.T, executable string, extraEnv []string) *accountScratch {
	t.Helper()
	// A short root keeps native unix-socket paths below platform limits.
	root, err := os.MkdirTemp("", "ddgrok")
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	s := &accountScratch{
		executable: executable,
		root:       root,
		grokHome:   filepath.Join(root, "grok"),
		repo:       filepath.Join(root, "repo"),
		processes:  filepath.Join(root, "proc"),
	}
	t.Cleanup(func() {
		// Backstop for a scratch built without newAccountScratch's reporting
		// cleanup: never leave a tree behind unless ownership is uncertain.
		if s.uncertainErr() == nil {
			_ = os.RemoveAll(s.root)
		}
	})
	for _, dir := range []string{"home", "grok", "repo", "config", "cache", "data", "state", "tmp"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	var path []string
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if filepath.IsAbs(entry) {
			path = append(path, entry)
		}
	}
	s.env = []string{
		"PATH=" + strings.Join(path, string(os.PathListSeparator)),
		"HOME=" + filepath.Join(root, "home"), "USERPROFILE=" + filepath.Join(root, "home"),
		"APPDATA=" + filepath.Join(root, "config"), "LOCALAPPDATA=" + filepath.Join(root, "data"),
		"XDG_CONFIG_HOME=" + filepath.Join(root, "config"), "XDG_CACHE_HOME=" + filepath.Join(root, "cache"),
		"XDG_DATA_HOME=" + filepath.Join(root, "data"), "XDG_STATE_HOME=" + filepath.Join(root, "state"),
		"TMPDIR=" + filepath.Join(root, "tmp"), "TMP=" + filepath.Join(root, "tmp"), "TEMP=" + filepath.Join(root, "tmp"),
		"GROK_HOME=" + s.grokHome, "GROK_DISABLE_AUTOUPDATER=1", "CI=1", "NO_COLOR=1",
	}
	for _, vendor := range []string{"CLAUDE", "CURSOR"} {
		for _, surface := range []string{"SKILLS", "RULES", "AGENTS", "MCPS", "HOOKS"} {
			s.env = append(s.env, "GROK_"+vendor+"_"+surface+"_ENABLED=0")
		}
	}
	if runtime.GOOS == "windows" {
		for _, key := range []string{"SystemRoot", "WINDIR"} {
			if value := os.Getenv(key); value != "" {
				s.env = append(s.env, key+"="+value)
			}
		}
	}
	s.env = append(s.env, extraEnv...)
	return s
}

// initRepo makes the scratch workspace a git repository with one file.
func (s *accountScratch) initRepo() error {
	if err := os.WriteFile(filepath.Join(s.repo, "README.md"), []byte("# scratch\n"), 0o600); err != nil {
		return err
	}
	command := exec.Command("git", "init", "-q")
	command.Dir, command.Env = s.repo, s.env
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("git init in the scratch repository failed (%d bytes of output): %w", len(output), err)
	}
	return nil
}

// config builds one native command. A nil stream is discarded; stderr is
// delivered only to a caller that needs it (login prompt, bounded captures).
func (s *accountScratch) config(args []string, stdout, stderr io.Writer) process.Config {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	return process.Config{
		Directory: s.processes, OwnerID: domain.NewID(), Executable: s.executable,
		Args: append([]string{"--no-auto-update"}, args...), Env: s.env, Cwd: s.repo,
		Stdout: stdout, Stderr: stderr,
	}
}

// run executes one native command to completion through internal/process and
// proves the owned process tree is gone.
func (s *accountScratch) run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	config := s.config(args, stdout, stderr)
	err := s.observe(process.Run(ctx, config))
	if reconcile := process.ReconcileOwner(config.Directory, config.OwnerID); reconcile != nil {
		s.noteUncertain(errors.New("native process ownership reconciliation failed"))
		err = errors.Join(err, reconcile)
	}
	return err
}

func describeNativeError(err error) string {
	if code, ok := process.CommandExitCode(err); ok {
		return fmt.Sprintf("native command exited with status %d", code)
	}
	return fmt.Sprintf("native command failed with code %s", domain.SafeError(err).Code)
}

// Login runs the device-code login and reports success only for exit 0 plus the
// expected device-auth output shape and a created credential file.
func (s *accountScratch) Login(ctx context.Context, sink io.Writer, logf func(string, ...any)) error {
	filter := &approvalFilter{sink: sink}
	logf("%s", approvalNotice)
	err := s.run(ctx, []string{"login", "--device-auth"}, filter.Stdout(), filter.Stderr())
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("device login was not approved before it was canceled or its deadline passed: %s", describeNativeError(err))
		}
		return errors.New("device login failed: " + describeNativeError(err))
	}
	if err := filter.Verified(); err != nil {
		return err
	}
	info, err := os.Lstat(filepath.Join(s.grokHome, "auth.json"))
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("device login exited 0 but did not create the scratch credential file")
	}
	return nil
}

// Cleanup always attempts `grok logout` on a fresh context, then removes the
// whole scratch tree. It is idempotent and safe after cancellation. The tree is
// removed only when every owned native process is confirmed gone: if ownership
// reconciliation failed anywhere, the tree is retained (its path is reported,
// never its content) and the returned error fails the run.
func (s *accountScratch) Cleanup() error {
	s.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), accountCleanupTimeout)
		defer cancel()
		var errs []error
		if _, err := os.Lstat(s.grokHome); err == nil {
			// A plain logout failure matters only if the credential file survives
			// (a repeated logout may legitimately report nothing to do). An
			// ownership failure is recorded by run and handled below.
			logoutErr := s.run(ctx, []string{"logout"}, nil, nil)
			if _, err := os.Lstat(filepath.Join(s.grokHome, "auth.json")); err == nil {
				if logoutErr != nil {
					errs = append(errs, errors.New("scratch credential file survived logout: "+describeNativeError(logoutErr)))
				} else {
					errs = append(errs, errors.New("scratch credential file survived logout"))
				}
			}
		}
		if uncertain := s.uncertainErr(); uncertain != nil {
			errs = append(errs, fmt.Errorf("cleanup is uncertain (%w); the scratch tree is retained at %s and must be removed manually after confirming no grok process still uses it", uncertain, s.root))
			s.cleanupErr = errors.Join(errs...)
			return
		}
		if err := os.RemoveAll(s.root); err != nil {
			errs = append(errs, err)
		}
		if _, err := os.Lstat(s.root); !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("scratch directory was not removed; it remains at %s", s.root))
		}
		s.cleanupErr = errors.Join(errs...)
	})
	return s.cleanupErr
}

// capture runs a short native command and returns its bounded output: stdout,
// then stderr. Neither stream is ever logged.
func (s *accountScratch) capture(ctx context.Context, args ...string) (string, error) {
	out, errOut := &lockedBuffer{}, &lockedBuffer{}
	err := s.run(ctx, args, out, errOut)
	if err != nil {
		return "", errors.New(describeNativeError(err))
	}
	if out.Overflowed() || errOut.Overflowed() {
		return "", errors.New("native output exceeded its bound")
	}
	return out.String() + "\n" + errOut.String(), nil
}

func checkGrokVersion(output string) (string, error) {
	line := strings.TrimSpace(ansiPattern.ReplaceAllString(strings.SplitN(output, "\n", 2)[0], ""))
	match := versionPattern.FindStringSubmatch(line)
	if match == nil {
		return "", errors.New("grok --version output did not match 'grok <semver> ...'")
	}
	if !domain.VersionAtLeast(match[1], domain.GrokSubscriptionBaselineVersion) {
		return line, fmt.Errorf("Grok Build %s is below the subscription baseline %s", match[1], domain.GrokSubscriptionBaselineVersion)
	}
	return line, nil
}

// checkModelsShape verifies `grok models` for a logged-in account without
// echoing the output.
func checkModelsShape(output string) error {
	if !loggedInLine.MatchString(output) {
		return errors.New("grok models did not report a logged-in account ('You are logged in with ...')")
	}
	if !defaultModelLine.MatchString(output) {
		return errors.New("grok models did not print a 'Default model: <id>' line")
	}
	if !modelEntryLine.MatchString(output) {
		return errors.New("grok models listed no 'grok-*' model entries")
	}
	return nil
}

// acpClient is a minimal raw JSON-RPC 2.0 client for `grok agent stdio`. The
// product ACP code only knows API-key authentication, so the subscription
// (cached_token) path is spoken directly here.
type acpClient struct {
	handle  *process.Handle
	frames  *acpFrames
	nextID  int
	updates int
	// target is the session whose assistant text is collected into reply.
	target     string
	reply      strings.Builder
	replyBound bool
}

type acpMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code int `json:"code"`
	} `json:"error,omitempty"`
}

type acpFrames struct {
	mu      sync.Mutex
	pending []byte
	queue   []acpMessage
	fault   error
	notify  chan struct{}
}

func (f *acpFrames) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, b := range p {
		if b != '\n' {
			if len(f.pending) >= maxAccountFrame {
				f.fault = errors.New("ACP frame exceeded its bound")
				f.pending = nil
				continue
			}
			f.pending = append(f.pending, b)
			continue
		}
		line := bytes.TrimSpace(f.pending)
		f.pending = f.pending[:0]
		if len(line) == 0 {
			continue
		}
		var message acpMessage
		if err := json.Unmarshal(line, &message); err != nil {
			f.fault = errors.New("ACP stdout carried a frame that is not JSON")
			continue
		}
		f.queue = append(f.queue, message)
	}
	select {
	case f.notify <- struct{}{}:
	default:
	}
	return len(p), nil
}

func (f *acpFrames) pop() (acpMessage, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fault != nil {
		return acpMessage{}, false, f.fault
	}
	if len(f.queue) == 0 {
		return acpMessage{}, false, nil
	}
	message := f.queue[0]
	f.queue = f.queue[1:]
	return message, true, nil
}

// startACP launches `grok agent stdio`. The returned closer stops the process
// and must have its error checked: a non-nil result means native ownership could
// not be confirmed, and the scratch tree is then retained by Cleanup. The agent's
// stderr is diagnostic text that can carry account details, so it is discarded
// on purpose; every failure is reported from the JSON-RPC stream instead.
func startACP(ctx context.Context, s *accountScratch) (*acpClient, func() error, error) {
	frames := &acpFrames{notify: make(chan struct{}, 1)}
	config := s.config([]string{"agent", "stdio"}, frames, nil)
	handle, err := process.Start(ctx, config)
	if err != nil {
		_ = s.observe(err)
		return nil, nil, errors.New("ACP process could not start: " + describeNativeError(err))
	}
	closer := func() error {
		var errs []error
		// The input may already be gone because the agent exited; the ownership
		// checks below decide whether anything is left running.
		_ = handle.CloseInput()
		if err := s.observe(handle.Close()); err != nil {
			errs = append(errs, errors.New("ACP process stop could not be confirmed"))
		}
		// Wait reports the stopped agent's own exit status; only a failure that
		// is not a plain exit status says anything about the process tree.
		if err := handle.Wait(); err != nil {
			if _, exited := process.CommandExitCode(err); !exited {
				s.noteUncertain(errors.New("ACP process wait failed"))
				errs = append(errs, errors.New("ACP process wait failed: "+string(domain.SafeError(err).Code)))
			}
		}
		if err := process.ReconcileOwner(config.Directory, config.OwnerID); err != nil {
			s.noteUncertain(errors.New("ACP process ownership reconciliation failed"))
			errs = append(errs, errors.New("ACP process ownership reconciliation failed"))
		}
		return errors.Join(errs...)
	}
	if err := handle.Resume(); err != nil {
		_ = s.observe(err)
		if closeErr := closer(); closeErr != nil {
			return nil, nil, fmt.Errorf("ACP process could not resume (%s) and cleanup failed: %w", describeNativeError(err), closeErr)
		}
		return nil, nil, errors.New("ACP process could not resume: " + describeNativeError(err))
	}
	return &acpClient{handle: handle, frames: frames}, closer, nil
}

func (c *acpClient) send(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if n, err := c.handle.Write(raw); err != nil || n != len(raw) {
		return errors.New("ACP stdin write failed")
	}
	return nil
}

// call sends one request and waits for its response. Notifications are
// counted, and any agent-initiated request is refused with -32601 so an
// unexpected permission or filesystem request cannot hang the turn.
func (c *acpClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.nextID++
	id := c.nextID
	if err := c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, fmt.Errorf("ACP %s: %w", method, err)
	}
	want := fmt.Sprint(id)
	for {
		message, ok, fault := c.frames.pop()
		if fault != nil {
			return nil, fmt.Errorf("ACP %s: %w", method, fault)
		}
		if !ok {
			select {
			case <-c.frames.notify:
			case <-ctx.Done():
				return nil, fmt.Errorf("ACP %s: timed out or canceled", method)
			case <-c.handle.Done():
				// Drain frames that raced with exit before giving up.
				if _, more, _ := c.frames.peek(); !more {
					return nil, fmt.Errorf("ACP %s: the agent exited before responding", method)
				}
			}
			continue
		}
		switch {
		case message.Method != "" && len(message.ID) > 0:
			refusal := map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(message.ID), "error": map[string]any{"code": -32601, "message": "not supported by the acceptance client"}}
			if err := c.send(refusal); err != nil {
				return nil, fmt.Errorf("ACP %s: could not refuse an agent request: %w", method, err)
			}
		case message.Method != "":
			if message.Method == "session/update" {
				c.updates++
				c.collectReply(message.Params)
			}
		case string(message.ID) == want:
			if message.Error != nil {
				return nil, fmt.Errorf("ACP %s returned JSON-RPC error code %d", method, message.Error.Code)
			}
			return message.Result, nil
		}
	}
}

// collectReply appends the text of an agent_message_chunk update that belongs to
// the target session. Updates for other sessions, thoughts, tool calls and
// non-text content are counted by the caller but never become reply text.
func (c *acpClient) collectReply(params json.RawMessage) {
	var decoded struct {
		SessionID string `json:"sessionId"`
		Update    struct {
			Kind    string `json:"sessionUpdate"`
			Content struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"update"`
	}
	if json.Unmarshal(params, &decoded) != nil || c.target == "" || decoded.SessionID != c.target {
		return
	}
	if decoded.Update.Kind != "agent_message_chunk" || decoded.Update.Content.Type != "text" {
		return
	}
	if c.reply.Len()+len(decoded.Update.Content.Text) > maxAccountReply {
		c.replyBound = true
		return
	}
	c.reply.WriteString(decoded.Update.Content.Text)
}

func (f *acpFrames) peek() (acpMessage, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.queue) == 0 {
		return acpMessage{}, false, f.fault
	}
	return f.queue[0], true, f.fault
}

// checkInitializeOffers verifies that the initialize result advertises the
// cached_token authentication method.
func checkInitializeOffers(result json.RawMessage) error {
	var decoded struct {
		AuthMethods []struct {
			ID string `json:"id"`
		} `json:"authMethods"`
	}
	if err := json.Unmarshal(result, &decoded); err != nil {
		return errors.New("ACP initialize result is not the expected object")
	}
	for _, method := range decoded.AuthMethods {
		if method.ID == cachedTokenMethod {
			return nil
		}
	}
	return errors.New("ACP initialize did not offer the " + cachedTokenMethod + " authentication method")
}

// checkPromptTurn requires the target session to finish with stopReason
// end_turn and to have answered with the expected single word. A turn that
// completes without assistant text, or answers something else (for example an
// authentication notice), is a failure. The reply is never echoed.
func checkPromptTurn(result json.RawMessage, reply string, overflowed bool) error {
	var outcome struct {
		StopReason string `json:"stopReason"`
	}
	if json.Unmarshal(result, &outcome) != nil || outcome.StopReason != "end_turn" {
		return errors.New("ACP session/prompt did not finish with stopReason end_turn")
	}
	if overflowed {
		return errors.New("ACP assistant reply exceeded its bound")
	}
	normalized := strings.ToLower(strings.Trim(strings.TrimSpace(reply), ".!\"'`"))
	if normalized == "" {
		return errors.New("ACP session/prompt finished without any assistant message text")
	}
	if normalized != expectedReply {
		return fmt.Errorf("ACP assistant reply (%d bytes) was not the expected single word %q", len(reply), expectedReply)
	}
	return nil
}

// runPromptTurn drives initialize, authenticate, session/new and one prompt over
// an already started client and validates the target session's reply.
func runPromptTurn(ctx context.Context, client *acpClient, cwd string) error {
	initialized, err := client.call(ctx, "initialize", map[string]any{
		"protocolVersion": 1, "clientCapabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "delidev-account-acceptance", "version": "0.1.0"},
	})
	if err != nil {
		return err
	}
	if err := checkInitializeOffers(initialized); err != nil {
		return err
	}
	if _, err := client.call(ctx, "authenticate", map[string]any{"methodId": cachedTokenMethod}); err != nil {
		return err
	}
	created, err := client.call(ctx, "session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}})
	if err != nil {
		return err
	}
	var session struct {
		SessionID string `json:"sessionId"`
	}
	if json.Unmarshal(created, &session) != nil || session.SessionID == "" {
		return errors.New("ACP session/new did not return a sessionId")
	}
	client.target = session.SessionID
	turn, err := client.call(ctx, "session/prompt", map[string]any{
		"sessionId": session.SessionID,
		"prompt":    []any{map[string]any{"type": "text", "text": "Reply with the single word " + expectedReply + ". Do not use any tools."}},
	})
	if err != nil {
		return err
	}
	return checkPromptTurn(turn, client.reply.String(), client.replyBound)
}

func TestAccountAcceptance(t *testing.T) {
	executable := requireAccountGate(t)
	timeout, err := approvalTimeout(os.Getenv(approvalTimeoutEnv))
	if err != nil {
		t.Fatal(err)
	}
	deadline, hasDeadline := t.Deadline()
	if err := checkTimeBudget(deadline, hasDeadline, time.Now(), timeout); err != nil {
		t.Fatal(err)
	}
	// Registered first so it runs last, after the scratch cleanup.
	checkRealAuth := guardRealAuth(t)
	terminal, err := approvalTerminal()
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()

	scratch := newAccountScratch(t, executable, nil)
	if err := scratch.initRepo(); err != nil {
		t.Fatal(err)
	}

	versionCtx, cancelVersion := context.WithTimeout(context.Background(), accountCommandTimeout)
	versionOutput, err := scratch.capture(versionCtx, "--version")
	cancelVersion()
	if err != nil {
		t.Fatalf("grok --version: %v", err)
	}
	// The CLI version line carries no account data; record it in the PR.
	versionLine, err := checkGrokVersion(versionOutput)
	if versionLine != "" {
		t.Logf("Grok Build CLI under test: %s", versionLine)
	}
	if err != nil {
		t.Fatal(err)
	}

	loginCtx, cancelLogin := context.WithTimeout(context.Background(), timeout)
	err = scratch.Login(loginCtx, terminal, t.Logf)
	cancelLogin()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("models-shape", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), accountCommandTimeout)
		defer cancel()
		output, err := scratch.capture(ctx, "models")
		if err != nil {
			t.Fatalf("grok models: %v", err)
		}
		if err := checkModelsShape(output); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("acp-cached-token-prompt-turn", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), accountPromptTimeout)
		defer cancel()
		client, closeACP, err := startACP(ctx, scratch)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := closeACP(); err != nil {
				t.Errorf("ACP shutdown: %v", err)
			}
		}()
		if err := runPromptTurn(ctx, client, scratch.repo); err != nil {
			t.Fatal(err)
		}
		t.Logf("prompt turn completed with %d session updates", client.updates)
	})

	t.Run("logout-removes-credentials", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), accountCommandTimeout)
		defer cancel()
		if err := scratch.run(ctx, []string{"logout"}, nil, nil); err != nil {
			t.Fatalf("grok logout: %s", describeNativeError(err))
		}
		if _, err := os.Lstat(filepath.Join(scratch.grokHome, "auth.json")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("logout left the scratch credential file in place")
		}
		output, err := scratch.capture(ctx, "models")
		if err != nil {
			t.Fatalf("grok models after logout: %v", err)
		}
		if !notSignedInOutput.MatchString(output) {
			t.Fatal("grok models still reported an authenticated account after logout")
		}
	})

	t.Run("real-home-unchanged", func(t *testing.T) {
		checkRealAuth()
	})
}

// ---- Harness self-tests (fake grok; no account, network or credential) ----

const fakeGrokScript = `#!/bin/sh
# Fake grok for harness tests. Usage: fake --no-auto-update COMMAND [args]
# FAKE_GROK_PROMPT=stderr prints the device-login prompt on stderr, as the real
# CLI does; FAKE_GROK_TURN selects the agent's prompt-turn behavior.
cmd="$2"
printf '%s\n' "$cmd" >> "$FAKE_GROK_LOG"
prompt() {
  printf 'To sign in, open this URL in your browser:\n\n  https://accounts.x.ai/oauth2/device?user_code=ABCD-1234\n\nConfirm this code in your browser:\n\n  ABCD-1234\n\nWaiting for authorization...\n'
  if [ "$FAKE_GROK_LOGIN" = "evil" ]; then printf '  https://evil.example/device?user_code=ABCD-1234\n'; fi
}
update() {
  # update SESSION KIND TEXT
  printf '{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"%s","update":{"sessionUpdate":"%s","content":{"type":"text","text":"%s"}}}}\n' "$1" "$2" "$3"
}
case "$cmd" in
login)
  if [ "$FAKE_GROK_PROMPT" = "stderr" ]; then prompt >&2; else prompt; fi
  if [ "$FAKE_GROK_LOGIN" = "hang" ]; then sleep 60; fi
  mkdir -p "$GROK_HOME"
  printf '{}' > "$GROK_HOME/auth.json"
  printf 'Signed in as person@example.com\n'
  ;;
logout)
  rm -f "$GROK_HOME/auth.json"
  printf 'Logged out (was signed in as person@example.com)\n'
  ;;
agent)
  while IFS= read -r line; do
    id=$(printf '%s' "$line" | sed -n 's/^{"id":\([0-9][0-9]*\),.*/\1/p')
    case "$line" in
    *'"method":"initialize"'*)
      printf '{"id":%s,"jsonrpc":"2.0","result":{"protocolVersion":1,"authMethods":[{"id":"cached_token"}]}}\n' "$id" ;;
    *'"method":"authenticate"'*)
      printf '{"id":%s,"jsonrpc":"2.0","result":{}}\n' "$id" ;;
    *'"method":"session/new"'*)
      printf '{"id":%s,"jsonrpc":"2.0","result":{"sessionId":"sess-1"}}\n' "$id" ;;
    *'"method":"session/prompt"'*)
      case "$FAKE_GROK_TURN" in
      empty) ;;
      unauthorized) update sess-1 agent_message_chunk "You are not authenticated. Run grok login." ;;
      foreign) update sess-2 agent_message_chunk "ok" ;;
      thought) update sess-1 agent_thought_chunk "ok" ;;
      split) update sess-1 agent_message_chunk "O"; update sess-1 agent_message_chunk "k." ;;
      *) update sess-1 agent_message_chunk "ok" ;;
      esac
      printf '{"id":%s,"jsonrpc":"2.0","result":{"stopReason":"end_turn"}}\n' "$id" ;;
    esac
  done
  ;;
esac
`

// fakeGrokExecutable writes the fake grok and returns it with its call log.
func fakeGrokExecutable(t *testing.T) (string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake grok is a POSIX shell script")
	}
	dir := t.TempDir()
	executable := filepath.Join(dir, "grok")
	if err := os.WriteFile(executable, []byte(fakeGrokScript), 0o700); err != nil {
		t.Fatal(err)
	}
	return executable, filepath.Join(dir, "calls.log")
}

func fakeAccountScratch(t *testing.T, mode string, extraEnv ...string) (*accountScratch, string) {
	t.Helper()
	executable, record := fakeGrokExecutable(t)
	env := append([]string{"FAKE_GROK_LOG=" + record, "FAKE_GROK_LOGIN=" + mode}, extraEnv...)
	return newAccountScratch(t, executable, env), record
}

func fakeCalls(t *testing.T, record string) []string {
	t.Helper()
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(raw))
}

func TestNativeAccountHarnessLoginFiltersOutput(t *testing.T) {
	scratch, record := fakeAccountScratch(t, "")
	sink := &lockedBuffer{}
	var logged []string
	logf := func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := scratch.Login(ctx, sink, logf); err != nil {
		t.Fatalf("login: %v", err)
	}
	if got := sink.String(); !strings.Contains(got, "user_code=ABCD-1234") || !strings.Contains(got, "Confirm this code: ABCD-1234") || strings.Contains(got, "person@example.com") {
		t.Fatalf("operator sink received unexpected content: %q", got)
	}
	if len(logged) != 1 || logged[0] != approvalNotice {
		t.Fatalf("test log must hold only the fixed notice, got %q", logged)
	}
	if err := scratch.Cleanup(); err != nil {
		t.Fatal(err)
	}
	if calls := fakeCalls(t, record); strings.Join(calls, ",") != "login,logout" {
		t.Fatalf("unexpected native calls: %v", calls)
	}
	if _, err := os.Lstat(scratch.root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("scratch directory survived cleanup")
	}
}

// TestNativeAccountHarnessCancelledLoginStillCleansUp simulates a login that
// never completes: the context is canceled mid-login, and cleanup must still run
// logout and remove the scratch directory.
func TestNativeAccountHarnessCancelledLoginStillCleansUp(t *testing.T) {
	scratch, record := fakeAccountScratch(t, "hang")
	sink := &lockedBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		deadline := time.Now().Add(time.Minute)
		for time.Now().Before(deadline) && !strings.Contains(sink.String(), "Confirm this code") {
			time.Sleep(20 * time.Millisecond)
		}
		cancel()
	}()
	err := scratch.Login(ctx, sink, func(string, ...any) {})
	if err == nil {
		t.Fatal("canceled login reported success")
	}
	if strings.Contains(err.Error(), "ABCD") || strings.Contains(err.Error(), "x.ai") {
		t.Fatal("login error leaked approval details")
	}
	if err := scratch.Cleanup(); err != nil {
		t.Fatal(err)
	}
	calls := fakeCalls(t, record)
	if len(calls) < 2 || calls[0] != "login" || calls[len(calls)-1] != "logout" {
		t.Fatalf("cleanup did not run logout after the canceled login: %v", calls)
	}
	if _, err := os.Lstat(scratch.root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("scratch directory survived cleanup")
	}
}

func TestNativeAccountHarnessRejectsUntrustedVerificationHost(t *testing.T) {
	scratch, _ := fakeAccountScratch(t, "evil")
	sink := &lockedBuffer{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	err := scratch.Login(ctx, sink, func(string, ...any) {})
	if err == nil || !strings.Contains(err.Error(), "trusted x.ai hosts") {
		t.Fatalf("untrusted host accepted: %v", err)
	}
	if strings.Contains(sink.String(), "evil.example") {
		t.Fatal("untrusted URL was shown to the account owner")
	}
}

func TestNativeAccountHarnessApprovalFilterChunksAndShapes(t *testing.T) {
	sink := &lockedBuffer{}
	filter := &approvalFilter{sink: sink}
	stream := "\x1b[1mTo sign in\x1b[0m\n  https://accounts.x.ai/oauth2/device?user_code=WXYZ-9876\n\n  WXYZ-9876\nSigned in as person@example.com\n"
	for i := 0; i < len(stream); i += 3 {
		end := min(i+3, len(stream))
		if _, err := filter.Write([]byte(stream[i:end])); err != nil {
			t.Fatal(err)
		}
	}
	if err := filter.Verified(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sink.String(), "person@example.com") || strings.Contains(sink.String(), "\x1b") {
		t.Fatalf("filter forwarded unrelated or raw terminal content: %q", sink.String())
	}
	for _, stream := range []string{"WXYZ-9876\n", "https://accounts.x.ai/device\n", "https://x.ai.evil.example/device\nABCD-1234\n", "http://accounts.x.ai/device\nABCD-1234\n", "https://accounts.x.ai/device\nabcd-1234\n"} {
		bad := &approvalFilter{}
		_, _ = bad.Write([]byte(stream))
		if bad.Verified() == nil {
			t.Fatalf("filter accepted %q", stream)
		}
	}
}

func TestNativeAccountHarnessApprovalTimeout(t *testing.T) {
	if got, err := approvalTimeout(""); err != nil || got != 5*time.Minute {
		t.Fatalf("default approval deadline: %v %v", got, err)
	}
	if got, err := approvalTimeout("90s"); err != nil || got != 90*time.Second {
		t.Fatalf("override: %v %v", got, err)
	}
	for _, raw := range []string{"soon", "1s", "2h", "-5m"} {
		if _, err := approvalTimeout(raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}

func TestNativeAccountHarnessRealAuthDigest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	absent, err := authFileDigest(path)
	if err != nil || absent != "absent" {
		t.Fatalf("missing file digest: %q %v", absent, err)
	}
	if err := os.WriteFile(path, []byte("secret-one"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, _ := authFileDigest(path)
	again, _ := authFileDigest(path)
	if first == "absent" || first != again || strings.Contains(first, "secret") {
		t.Fatal("digest is not stable or leaks content")
	}
	if err := os.WriteFile(path, []byte("secret-two"), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, _ := authFileDigest(path); changed == first {
		t.Fatal("digest did not detect a change")
	}
}

func TestNativeAccountHarnessProfileShapes(t *testing.T) {
	if line, err := checkGrokVersion("grok 1.0.52 (0920c8169e27) [stable]\n"); err != nil || !strings.HasPrefix(line, "grok 1.0.52") {
		t.Fatalf("current stable rejected: %q %v", line, err)
	}
	for _, output := range []string{"grok 1.0.45 (abc) [stable]", "grok dev", ""} {
		if _, err := checkGrokVersion(output); err == nil {
			t.Fatalf("accepted version output %q", output)
		}
	}
	good := "You are logged in with grok.com.\nDefault model: grok-4.7\n\n  grok-4.7 (default)\n  grok-4.6\n"
	if err := checkModelsShape(good); err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{"You are not authenticated.\nDefault model: grok-4.6\n", "You are logged in with grok.com.\n", "You are logged in with grok.com.\nDefault model: grok-4.7\n"} {
		if checkModelsShape(output) == nil {
			t.Fatalf("accepted models output %q", output)
		}
	}
	offers := json.RawMessage(`{"protocolVersion":1,"authMethods":[{"id":"cached_token"},{"id":"grok.com"}]}`)
	if err := checkInitializeOffers(offers); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"authMethods":[{"id":"grok.com"}]}`, `{}`, `[]`} {
		if checkInitializeOffers(json.RawMessage(raw)) == nil {
			t.Fatalf("accepted initialize result %s", raw)
		}
	}
}

func TestNativeAccountHarnessACPFrames(t *testing.T) {
	frames := &acpFrames{notify: make(chan struct{}, 1)}
	_, _ = frames.Write([]byte(`{"jsonrpc":"2.0","method":"session/update","params":{}}` + "\n" + `{"jsonrpc":"2.0","id":2,"re`))
	_, _ = frames.Write([]byte(`sult":{"ok":true}}` + "\n"))
	first, ok, err := frames.pop()
	if err != nil || !ok || first.Method != "session/update" {
		t.Fatalf("notification: %+v %v %v", first, ok, err)
	}
	second, ok, err := frames.pop()
	if err != nil || !ok || string(second.ID) != "2" || string(second.Result) != `{"ok":true}` {
		t.Fatalf("response: %+v %v %v", second, ok, err)
	}
	_, _ = frames.Write([]byte("not json\n"))
	if _, _, err := frames.pop(); err == nil {
		t.Fatal("non-JSON frame accepted")
	}
}

func TestNativeAccountHarnessLoginPromptOnStderr(t *testing.T) {
	scratch, _ := fakeAccountScratch(t, "", "FAKE_GROK_PROMPT=stderr")
	sink := &lockedBuffer{}
	var logged []string
	logf := func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := scratch.Login(ctx, sink, logf); err != nil {
		t.Fatalf("login with a stderr prompt: %v", err)
	}
	if got := sink.String(); !strings.Contains(got, "user_code=ABCD-1234") || !strings.Contains(got, "Confirm this code: ABCD-1234") || strings.Contains(got, "person@example.com") {
		t.Fatalf("operator sink did not receive the stderr prompt: %q", got)
	}
	if len(logged) != 1 || logged[0] != approvalNotice {
		t.Fatalf("test log must hold only the fixed notice, got %q", logged)
	}
}

func TestNativeAccountHarnessRejectsUntrustedHostOnStderr(t *testing.T) {
	scratch, _ := fakeAccountScratch(t, "evil", "FAKE_GROK_PROMPT=stderr")
	sink := &lockedBuffer{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	err := scratch.Login(ctx, sink, func(string, ...any) {})
	if err == nil || !strings.Contains(err.Error(), "trusted x.ai hosts") {
		t.Fatalf("untrusted stderr host accepted: %v", err)
	}
	if strings.Contains(sink.String(), "evil.example") || strings.Contains(err.Error(), "ABCD") {
		t.Fatal("untrusted stderr URL leaked")
	}
}

// failingWriter simulates an unwritable operator terminal.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("terminal closed") }

func TestNativeAccountHarnessApprovalFilterStreams(t *testing.T) {
	// Chunks from the two streams interleave without corrupting each other.
	sink := &lockedBuffer{}
	filter := &approvalFilter{sink: sink}
	out, errOut := filter.Stdout(), filter.Stderr()
	_, _ = errOut.Write([]byte("  https://accounts.x.ai/oauth2/de"))
	_, _ = out.Write([]byte("Signed in as person@exa"))
	_, _ = errOut.Write([]byte("vice?user_code=ABCD-1234\n  ABCD-"))
	_, _ = out.Write([]byte("mple.com\n"))
	_, _ = errOut.Write([]byte("1234"))
	// The final code line has no trailing newline and is still honored.
	if err := filter.Verified(); err != nil {
		t.Fatalf("interleaved streams: %v", err)
	}
	if got := sink.String(); !strings.Contains(got, "Confirm this code: ABCD-1234") || strings.Contains(got, "person@") {
		t.Fatalf("unexpected sink content %q", got)
	}

	// A terminal that cannot show the approval details fails the login.
	broken := &approvalFilter{sink: failingWriter{}}
	_, _ = broken.Write([]byte("https://accounts.x.ai/device\nABCD-1234\n"))
	if err := broken.Verified(); err == nil || strings.Contains(err.Error(), "ABCD") {
		t.Fatalf("terminal write failure was not reported safely: %v", err)
	}
}

func TestNativeAccountHarnessPromptTurnValidation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cases := []struct {
		name string
		turn string
		want string // substring of the expected error; empty means success
	}{
		{"expected reply", "", ""},
		{"reply split across chunks", "split", ""},
		{"empty successful turn", "empty", "without any assistant message text"},
		{"unauthorized notice", "unauthorized", "not the expected single word"},
		{"reply for another session", "foreign", "without any assistant message text"},
		{"non-message update only", "thought", "without any assistant message text"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scratch, _ := fakeAccountScratch(t, "", "FAKE_GROK_TURN="+tc.turn)
			client, closeACP, err := startACP(ctx, scratch)
			if err != nil {
				t.Fatal(err)
			}
			turnErr := runPromptTurn(ctx, client, scratch.repo)
			if err := closeACP(); err != nil {
				t.Fatalf("ACP shutdown: %v", err)
			}
			switch {
			case tc.want == "" && turnErr != nil:
				t.Fatalf("turn rejected: %v", turnErr)
			case tc.want != "" && (turnErr == nil || !strings.Contains(turnErr.Error(), tc.want)):
				t.Fatalf("turn error %v does not contain %q", turnErr, tc.want)
			}
			if turnErr != nil && strings.Contains(turnErr.Error(), "authenticated") {
				t.Fatal("turn error echoed the assistant text")
			}
		})
	}
}

func TestNativeAccountHarnessCheckPromptTurn(t *testing.T) {
	endTurn := json.RawMessage(`{"stopReason":"end_turn"}`)
	for _, reply := range []string{"ok", "Ok.", " OK\n", "\"ok\""} {
		if err := checkPromptTurn(endTurn, reply, false); err != nil {
			t.Fatalf("rejected %q: %v", reply, err)
		}
	}
	for name, tc := range map[string]struct {
		result json.RawMessage
		reply  string
		over   bool
	}{
		"no reply":        {endTurn, "", false},
		"whitespace only": {endTurn, " \n", false},
		"other reply":     {endTurn, "Please sign in", false},
		"longer reply":    {endTurn, "ok, done", false},
		"overflow":        {endTurn, "ok", true},
		"refusal":         {json.RawMessage(`{"stopReason":"refusal"}`), "ok", false},
		"cancelled":       {json.RawMessage(`{"stopReason":"cancelled"}`), "ok", false},
		"no stop reason":  {json.RawMessage(`{}`), "ok", false},
		"not an object":   {json.RawMessage(`[]`), "ok", false},
	} {
		if checkPromptTurn(tc.result, tc.reply, tc.over) == nil {
			t.Fatalf("accepted %s", name)
		}
	}
}

func TestNativeAccountHarnessTimeBudget(t *testing.T) {
	now := time.Now()
	if err := checkTimeBudget(time.Time{}, false, now, 30*time.Minute); err != nil {
		t.Fatalf("no deadline must pass: %v", err)
	}
	// The runbook's 45m package timeout covers the maximum 30m approval wait.
	if err := checkTimeBudget(now.Add(45*time.Minute), true, now, 30*time.Minute); err != nil {
		t.Fatalf("45m must cover a 30m approval: %v", err)
	}
	if err := checkTimeBudget(now.Add(10*time.Minute), true, now, defaultApprovalTimeout); err == nil {
		t.Fatal("a 10m package timeout cannot cover the default approval plus the fixed steps")
	}
	if err := checkTimeBudget(now.Add(10*time.Minute), true, now, 30*time.Minute); err == nil {
		t.Fatal("a 10m package timeout cannot cover a 30m approval override")
	}
}

// TestNativeAccountHarnessUncertainCleanupRetainsScratch proves that an
// unconfirmed process ownership leaves the scratch tree in place and fails.
func TestNativeAccountHarnessUncertainCleanupRetainsScratch(t *testing.T) {
	executable, record := fakeGrokExecutable(t)
	scratch := buildAccountScratch(t, executable, []string{"FAKE_GROK_LOG=" + record, "FAKE_GROK_LOGIN="})
	t.Cleanup(func() { _ = os.RemoveAll(scratch.root) })
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := scratch.Login(ctx, &lockedBuffer{}, func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	scratch.noteUncertain(errors.New("ACP process ownership reconciliation failed"))
	err := scratch.Cleanup()
	if err == nil || !strings.Contains(err.Error(), "retained at "+scratch.root) {
		t.Fatalf("uncertain cleanup did not fail with the retained path: %v", err)
	}
	if _, statErr := os.Lstat(scratch.root); statErr != nil {
		t.Fatalf("scratch tree was removed despite uncertain ownership: %v", statErr)
	}
	if again := scratch.Cleanup(); again == nil {
		t.Fatal("a repeated cleanup must keep reporting the failure")
	}
}
