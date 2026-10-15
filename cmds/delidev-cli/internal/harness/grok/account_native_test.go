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
	// accountFixedBudget bounds every non-approval step. It must cover
	// productionBudgets().fixed() (setup 30s, version 30s, models 30s, the 3m prompt
	// turn, logout 30s, post-logout models 30s and cleanup 30s: 6m) plus a minute of
	// process-start slack. The package timeout must cover the approval wait plus this.
	accountFixedBudget = 7 * time.Minute
	maxAccountReply    = 16 << 10
	expectedReply      = "ok"
	maxAccountLine     = 16 << 10
	maxAccountFrame    = 4 << 20
	maxAccountOutput   = 64 << 10
	cachedTokenMethod  = "cached_token"
	// The agent may send the session/prompt response before the final
	// session/update text frames, so the client keeps reading after the response.
	// Once the target session has produced text, the reply is settled when its
	// length has not changed for accountReplyQuiet; until then, and in total, the
	// wait is bounded by accountReplyLimit. Both sit inside the Prompt budget.
	accountReplyQuiet = 2 * time.Second
	accountReplyLimit = 20 * time.Second
)

// accountBudgets is the deadline of every non-approval step. Each step creates
// its own context from context.Background() with exactly one of these budgets, so
// a slow step can never shorten the next one; fixed() is their sum and is what
// accountFixedBudget has to cover.
type accountBudgets struct {
	Setup            time.Duration // git init of the scratch repository
	Version          time.Duration // grok --version
	Models           time.Duration // grok models while logged in
	Prompt           time.Duration // ACP start, initialize, authenticate, session/new and session/prompt
	Logout           time.Duration // explicit grok logout
	PostLogoutModels time.Duration // grok models after the explicit logout
	Cleanup          time.Duration // the cleanup grok logout
}

func productionBudgets() accountBudgets {
	return accountBudgets{
		Setup: accountCommandTimeout, Version: accountCommandTimeout, Models: accountCommandTimeout,
		Prompt: accountPromptTimeout, Logout: accountCommandTimeout,
		PostLogoutModels: accountCommandTimeout, Cleanup: accountCleanupTimeout,
	}
}

func (b accountBudgets) fixed() time.Duration {
	return b.Setup + b.Version + b.Models + b.Prompt + b.Logout + b.PostLogoutModels + b.Cleanup
}

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

// nativeHandle is the part of *process.Handle that the ACP client uses. It exists
// so the self-tests can script ownership failures that a real process cannot be
// made to produce on demand.
type nativeHandle interface {
	Resume() error
	Write([]byte) (int, error)
	CloseInput() error
	Close() error
	Wait() error
	Done() <-chan struct{}
}

// startNative is process.Start without the typed-nil interface trap.
func startNative(ctx context.Context, config process.Config) (nativeHandle, error) {
	handle, err := process.Start(ctx, config)
	if err != nil {
		return nil, err
	}
	return handle, nil
}

// uncertainty is a suspicion that one owner's native process tree may still be
// alive. It is keyed by owner so a later successful reconciliation of that owner
// retires exactly its own suspicion.
type uncertainty struct {
	owner domain.ID
	err   error
}

// accountScratch is a private HOME/GROK_HOME tree for one acceptance run.
type accountScratch struct {
	executable string
	root       string
	grokHome   string
	repo       string
	processes  string
	env        []string
	budgets    accountBudgets

	// Seams for the harness self-tests; production uses internal/process directly.
	runner     func(context.Context, process.Config) error
	starter    func(context.Context, process.Config) (nativeHandle, error)
	reconciler func(root string, owner domain.ID) error

	mu        sync.Mutex
	uncertain []uncertainty

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

// noteUncertain records that the owner's native process tree may still be alive.
// The suspicion stands until reconcileNative proves that owner's tree is gone;
// while any suspicion stands, Cleanup retains the scratch tree instead of
// removing it from under a live process.
func (s *accountScratch) noteUncertain(owner domain.ID, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.uncertain = append(s.uncertain, uncertainty{owner, err})
}

// clearUncertain retires every suspicion held for the owner.
func (s *accountScratch) clearUncertain(owner domain.ID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.uncertain[:0]
	for _, entry := range s.uncertain {
		if entry.owner != owner {
			kept = append(kept, entry)
		}
	}
	s.uncertain = kept
}

func (s *accountScratch) uncertainErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	errs := make([]error, 0, len(s.uncertain))
	for _, entry := range s.uncertain {
		errs = append(errs, entry.err)
	}
	return errors.Join(errs...)
}

func recoveryRequired(err error) bool {
	return err != nil && domain.SafeError(err).Code == domain.RecoveryRequired
}

// observe flags an ownership-recovery failure from internal/process as a
// suspicion for the owner and passes the error through unchanged. Every call
// site is followed by reconcileNative for the same owner, which retires the
// suspicion when reconciliation proves the owned tree is gone, so a recovery
// error that reconciliation resolves does not make cleanup fail.
func (s *accountScratch) observe(owner domain.ID, err error) error {
	if recoveryRequired(err) {
		s.noteUncertain(owner, errors.New("native process ownership could not be confirmed"))
	}
	return err
}

// reconcileNative proves the owner's native process tree is gone. A failure is a
// standing suspicion (label names the process in the retained-path error); a
// success retires every earlier suspicion for that owner.
func (s *accountScratch) reconcileNative(config process.Config, label string) error {
	if err := s.reconciler(config.Directory, config.OwnerID); err != nil {
		s.noteUncertain(config.OwnerID, errors.New(label+" ownership reconciliation failed"))
		return err
	}
	s.clearUncertain(config.OwnerID)
	return nil
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
		budgets:    productionBudgets(),
		runner:     process.Run,
		starter:    startNative,
		reconciler: process.ReconcileOwner,
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
	ctx, cancel := context.WithTimeout(context.Background(), s.budgets.Setup)
	defer cancel()
	command := exec.CommandContext(ctx, "git", "init", "-q")
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
	err := s.observe(config.OwnerID, s.runner(ctx, config))
	if reconcile := s.reconcileNative(config, "native process"); reconcile != nil {
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

// LoginWithin runs Login under its own approval deadline.
func (s *accountScratch) LoginWithin(approval time.Duration, sink io.Writer, logf func(string, ...any)) error {
	ctx, cancel := context.WithTimeout(context.Background(), approval)
	defer cancel()
	return s.Login(ctx, sink, logf)
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
		ctx, cancel := context.WithTimeout(context.Background(), s.budgets.Cleanup)
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

// The verify* steps are the acceptance checks. Each owns its context: it is
// created here from context.Background() with that step's budget, never taken
// from an earlier step, so command N+1 always starts with its full budget.

// verifyVersion runs `grok --version`. The returned line carries no account data
// and is returned even when it is below the baseline.
func (s *accountScratch) verifyVersion() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.budgets.Version)
	defer cancel()
	output, err := s.capture(ctx, "--version")
	if err != nil {
		return "", fmt.Errorf("grok --version: %w", err)
	}
	return checkGrokVersion(output)
}

// verifyModels runs `grok models` for the logged-in account.
func (s *accountScratch) verifyModels() error {
	ctx, cancel := context.WithTimeout(context.Background(), s.budgets.Models)
	defer cancel()
	output, err := s.capture(ctx, "models")
	if err != nil {
		return fmt.Errorf("grok models: %w", err)
	}
	return checkModelsShape(output)
}

// verifyPromptTurn runs the whole ACP turn (start, initialize, authenticate,
// session/new, session/prompt, stop) under one prompt budget and returns the
// number of session updates.
func (s *accountScratch) verifyPromptTurn() (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.budgets.Prompt)
	defer cancel()
	client, closeACP, err := startACP(ctx, s)
	if err != nil {
		return 0, err
	}
	turnErr := runPromptTurn(ctx, client, s.repo)
	if err := closeACP(); err != nil {
		turnErr = errors.Join(turnErr, fmt.Errorf("ACP shutdown: %w", err))
	}
	return client.updates, turnErr
}

// verifyLogout runs the explicit `grok logout` and requires the scratch
// credential file to be gone.
func (s *accountScratch) verifyLogout() error {
	ctx, cancel := context.WithTimeout(context.Background(), s.budgets.Logout)
	defer cancel()
	if err := s.run(ctx, []string{"logout"}, nil, nil); err != nil {
		return fmt.Errorf("grok logout: %s", describeNativeError(err))
	}
	if _, err := os.Lstat(filepath.Join(s.grokHome, "auth.json")); !errors.Is(err, os.ErrNotExist) {
		return errors.New("logout left the scratch credential file in place")
	}
	return nil
}

// verifyLoggedOut runs `grok models` after the logout, under its own budget
// rather than whatever the logout left over.
func (s *accountScratch) verifyLoggedOut() error {
	ctx, cancel := context.WithTimeout(context.Background(), s.budgets.PostLogoutModels)
	defer cancel()
	output, err := s.capture(ctx, "models")
	if err != nil {
		return fmt.Errorf("grok models after logout: %w", err)
	}
	if !notSignedInOutput.MatchString(output) {
		return errors.New("grok models still reported an authenticated account after logout")
	}
	return nil
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
	handle  nativeHandle
	frames  *acpFrames
	nextID  int
	updates int
	// target is the session whose assistant text is collected into reply.
	target     string
	reply      strings.Builder
	replyBound bool
	// quiet and limit are the reply settling bounds (see settleReply).
	quiet time.Duration
	limit time.Duration
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
	handle, err := s.starter(ctx, config)
	if err != nil {
		if recoveryRequired(s.observe(config.OwnerID, err)) {
			// No handle exists, so reconciliation is the only proof that the
			// owner's partially launched scope is gone.
			_ = s.reconcileNative(config, "ACP process")
		}
		return nil, nil, errors.New("ACP process could not start: " + describeNativeError(err))
	}
	closer := func() error {
		var errs []error
		// The input may already be gone because the agent exited; the ownership
		// checks below decide whether anything is left running.
		_ = handle.CloseInput()
		if err := s.observe(config.OwnerID, handle.Close()); err != nil {
			errs = append(errs, errors.New("ACP process stop could not be confirmed"))
		}
		// Wait reports the stopped agent's own exit status; only a failure that
		// is not a plain exit status says anything about the process tree.
		if err := handle.Wait(); err != nil {
			if _, exited := process.CommandExitCode(err); !exited {
				s.noteUncertain(config.OwnerID, errors.New("ACP process wait failed"))
				errs = append(errs, errors.New("ACP process wait failed: "+string(domain.SafeError(err).Code)))
			}
		}
		if err := s.reconcileNative(config, "ACP process"); err != nil {
			errs = append(errs, errors.New("ACP process ownership reconciliation failed"))
		}
		return errors.Join(errs...)
	}
	if err := handle.Resume(); err != nil {
		_ = s.observe(config.OwnerID, err)
		if closeErr := closer(); closeErr != nil {
			return nil, nil, fmt.Errorf("ACP process could not resume (%s) and cleanup failed: %w", describeNativeError(err), closeErr)
		}
		return nil, nil, errors.New("ACP process could not resume: " + describeNativeError(err))
	}
	return &acpClient{handle: handle, frames: frames, quiet: accountReplyQuiet, limit: accountReplyLimit}, closer, nil
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
// unexpected permission or filesystem request cannot hang the turn. Frames that
// arrive before the response are handled in arrival order; frames that arrive
// after it stay queued for the next call or for settleReply.
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
		if message.Method != "" {
			if err := c.dispatch(method, message); err != nil {
				return nil, err
			}
			continue
		}
		if string(message.ID) == want {
			if message.Error != nil {
				return nil, fmt.Errorf("ACP %s returned JSON-RPC error code %d", method, message.Error.Code)
			}
			return message.Result, nil
		}
	}
}

// dispatch handles one agent-initiated frame (a request or a notification),
// whichever side of a response it arrived on: requests are refused, session
// updates are counted and their reply text collected.
func (c *acpClient) dispatch(during string, message acpMessage) error {
	if len(message.ID) > 0 {
		refusal := map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(message.ID), "error": map[string]any{"code": -32601, "message": "not supported by the acceptance client"}}
		if err := c.send(refusal); err != nil {
			return fmt.Errorf("ACP %s: could not refuse an agent request: %w", during, err)
		}
		return nil
	}
	if message.Method == "session/update" {
		c.updates++
		c.collectReply(message.Params)
	}
	return nil
}

// settleReply keeps reading frames after the session/prompt response, because
// the agent is free to send the final session/update text frames after it (the
// official xAI headless client example likewise waits for the accumulated text
// length to stabilize after the prompt result). The reply is settled when it is
// non-empty and has not changed for c.quiet, when the agent exits, or, with no
// text at all, when c.limit passes (checkPromptTurn then reports the empty
// reply). A reply that is still changing after c.limit is a failure. Late agent
// requests are refused exactly as call does. Only the length of the reply is
// ever reported.
func (c *acpClient) settleReply(ctx context.Context) error {
	limit := time.NewTimer(c.limit)
	defer limit.Stop()
	lastLen, lastBound := c.reply.Len(), c.replyBound
	lastChange := time.Now()
	for {
		for {
			message, ok, fault := c.frames.pop()
			if fault != nil {
				return fmt.Errorf("ACP reply settling: %w", fault)
			}
			if !ok {
				break
			}
			if message.Method != "" {
				if err := c.dispatch("reply settling", message); err != nil {
					return err
				}
			}
		}
		if c.reply.Len() != lastLen || c.replyBound != lastBound {
			lastLen, lastBound, lastChange = c.reply.Len(), c.replyBound, time.Now()
		}
		var quiet <-chan time.Time
		var quietTimer *time.Timer
		if lastLen > 0 || lastBound {
			quietTimer = time.NewTimer(c.quiet - time.Since(lastChange))
			quiet = quietTimer.C
		}
		stop := func() {
			if quietTimer != nil {
				quietTimer.Stop()
			}
		}
		select {
		case <-c.frames.notify:
			stop()
		case <-quiet:
			// A frame that raced with the timer restarts the check.
			if _, more, _ := c.frames.peek(); more {
				continue
			}
			return nil
		case <-limit.C:
			stop()
			if lastLen > 0 || lastBound {
				return fmt.Errorf("ACP assistant reply did not stabilize within %s", c.limit)
			}
			return nil
		case <-ctx.Done():
			stop()
			return errors.New("ACP reply settling: timed out or canceled")
		case <-c.handle.Done():
			stop()
			// Frames written before exit are still queued; take them and stop.
			for {
				message, ok, fault := c.frames.pop()
				if fault != nil {
					return fmt.Errorf("ACP reply settling: %w", fault)
				}
				if !ok {
					return nil
				}
				if message.Method != "" {
					_ = c.dispatch("reply settling", message)
				}
			}
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
	// The response may precede the last text frames; settle before judging.
	if err := client.settleReply(ctx); err != nil {
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

	// Every step below owns its context (see the verify* methods); none inherits
	// time from an earlier step.
	versionLine, err := scratch.verifyVersion()
	// The CLI version line carries no account data; record it in the PR.
	if versionLine != "" {
		t.Logf("Grok Build CLI under test: %s", versionLine)
	}
	if err != nil {
		t.Fatal(err)
	}

	if err := scratch.LoginWithin(timeout, terminal, t.Logf); err != nil {
		t.Fatal(err)
	}

	t.Run("models-shape", func(t *testing.T) {
		if err := scratch.verifyModels(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("acp-cached-token-prompt-turn", func(t *testing.T) {
		updates, err := scratch.verifyPromptTurn()
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("prompt turn completed with %d session updates", updates)
	})

	t.Run("logout-removes-credentials", func(t *testing.T) {
		if err := scratch.verifyLogout(); err != nil {
			t.Fatal(err)
		}
		if err := scratch.verifyLoggedOut(); err != nil {
			t.Fatal(err)
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
# CLI does; FAKE_GROK_TURN selects the agent's prompt-turn behavior, including
# whether session/update text frames arrive before or after the response.
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
--version)
  printf 'grok 1.0.52 (0920c8169e27) [stable]\n'
  ;;
models)
  if [ -f "$GROK_HOME/auth.json" ]; then
    printf 'You are logged in with grok.com.\nDefault model: grok-4.7\n\n  grok-4.7 (default)\n  grok-4.6\n'
  else
    printf 'You are not authenticated.\n'
  fi
  ;;
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
      respond() { printf '{"id":%s,"jsonrpc":"2.0","result":{"stopReason":"end_turn"}}\n' "$id"; }
      case "$FAKE_GROK_TURN" in
      empty) respond ;;
      unauthorized) update sess-1 agent_message_chunk "You are not authenticated. Run grok login."; respond ;;
      foreign) update sess-2 agent_message_chunk "ok"; respond ;;
      thought) update sess-1 agent_thought_chunk "ok"; respond ;;
      split) update sess-1 agent_message_chunk "O"; update sess-1 agent_message_chunk "k."; respond ;;
      late) respond; sleep 0.2; update sess-1 agent_message_chunk "ok" ;;
      latesplit) respond; sleep 0.1; update sess-1 agent_message_chunk "O"; sleep 0.1; update sess-1 agent_message_chunk "k"; sleep 0.1; update sess-1 agent_message_chunk "." ;;
      lateforeign) respond; sleep 0.2; update sess-2 agent_message_chunk "ok" ;;
      laterequest) respond; printf '{"jsonrpc":"2.0","id":"perm-1","method":"session/request_permission","params":{}}\n'; sleep 0.1; update sess-1 agent_message_chunk "ok" ;;
      never)
        respond
        i=0
        while [ "$i" -lt 200 ]; do update sess-1 agent_message_chunk "o"; sleep 0.05; i=$((i+1)); done ;;
      *) update sess-1 agent_message_chunk "ok"; respond ;;
      esac
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
	const (
		quiet = 600 * time.Millisecond
		limit = 4 * time.Second
		short = 500 * time.Millisecond // nothing more will arrive
	)
	cases := []struct {
		name  string
		turn  string
		limit time.Duration // zero means the default limit
		want  string        // substring of the expected error; empty means success
	}{
		{name: "update before response", turn: ""},
		{name: "reply split across chunks before the response", turn: "split"},
		{name: "update after response", turn: "late"},
		{name: "several late chunks", turn: "latesplit"},
		{name: "late agent request and late chunk", turn: "laterequest"},
		{name: "no update at all", turn: "empty", limit: short, want: "without any assistant message text"},
		{name: "unauthorized notice", turn: "unauthorized", want: "not the expected single word"},
		{name: "reply for another session", turn: "foreign", limit: short, want: "without any assistant message text"},
		{name: "late reply for another session", turn: "lateforeign", limit: short, want: "without any assistant message text"},
		{name: "non-message update only", turn: "thought", limit: short, want: "without any assistant message text"},
		{name: "stream that never stabilizes", turn: "never", limit: 1500 * time.Millisecond, want: "did not stabilize"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scratch, _ := fakeAccountScratch(t, "", "FAKE_GROK_TURN="+tc.turn)
			client, closeACP, err := startACP(ctx, scratch)
			if err != nil {
				t.Fatal(err)
			}
			client.quiet, client.limit = quiet, limit
			if tc.limit != 0 {
				client.limit = tc.limit
			}
			started := time.Now()
			turnErr := runPromptTurn(ctx, client, scratch.repo)
			elapsed := time.Since(started)
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
			// The wait after the response is bounded by the limit, plus the
			// process round trips before it.
			if elapsed > client.limit+20*time.Second {
				t.Fatalf("turn took %s, the reply wait is not bounded by %s", elapsed, client.limit)
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
	scratch.noteUncertain(domain.NewID(), errors.New("ACP process ownership reconciliation failed"))
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

// budgetCall is one native command (or ACP start) as seen by the self-test
// seams: its first argument after --no-auto-update, when the seam was entered and
// the deadline of the context it was given.
type budgetCall struct {
	name     string
	entered  time.Time
	deadline time.Time
	hasLimit bool
}

// recordBudgets wraps the scratch seams so each native command reports the
// deadline of its context.
func recordBudgets(s *accountScratch) func() []budgetCall {
	var mu sync.Mutex
	var calls []budgetCall
	note := func(ctx context.Context, config process.Config) {
		call := budgetCall{name: config.Args[1], entered: time.Now()}
		call.deadline, call.hasLimit = ctx.Deadline()
		mu.Lock()
		calls = append(calls, call)
		mu.Unlock()
	}
	s.runner = func(ctx context.Context, config process.Config) error {
		note(ctx, config)
		return process.Run(ctx, config)
	}
	s.starter = func(ctx context.Context, config process.Config) (nativeHandle, error) {
		note(ctx, config)
		return startNative(ctx, config)
	}
	return func() []budgetCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]budgetCall(nil), calls...)
	}
}

// TestNativeAccountHarnessEveryStepOwnsItsBudget is the budget table in code. It
// runs the acceptance steps in order against the fake grok and checks, for each
// native command, that its context was created for that command alone:
//
//	step                  command          budget
//	version               --version        Version
//	login                 login            approval wait
//	models-shape          models           Models
//	acp prompt turn       agent stdio      Prompt (initialize..prompt share it)
//	logout                logout           Logout
//	post-logout models    models           PostLogoutModels
//	cleanup               logout           Cleanup
//
// A context created after stepStart and before the command began has a deadline
// in [stepStart+budget, entered+budget]. A step that reused its predecessor's
// context would carry the predecessor's earlier deadline and fall below the
// lower bound. The check uses the monotonic clock only, with no sleeps.
func TestNativeAccountHarnessEveryStepOwnsItsBudget(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	scratch, _ := fakeAccountScratch(t, "")
	// Distinct budgets so a budget taken from the wrong step is also caught.
	budgets := accountBudgets{
		Setup: 31 * time.Second, Version: 32 * time.Second, Models: 33 * time.Second, Prompt: 34 * time.Second,
		Logout: 35 * time.Second, PostLogoutModels: 36 * time.Second, Cleanup: 37 * time.Second,
	}
	approval := 38 * time.Second
	scratch.budgets = budgets
	calls := recordBudgets(scratch)

	type stepCheck struct {
		name   string
		budget time.Duration
		start  time.Time
	}
	var steps []stepCheck
	begin := func(name string, budget time.Duration) { steps = append(steps, stepCheck{name, budget, time.Now()}) }

	if err := scratch.initRepo(); err != nil {
		t.Fatal(err)
	}
	begin("--version", budgets.Version)
	if _, err := scratch.verifyVersion(); err != nil {
		t.Fatalf("version: %v", err)
	}
	begin("login", approval)
	if err := scratch.LoginWithin(approval, &lockedBuffer{}, func(string, ...any) {}); err != nil {
		t.Fatalf("login: %v", err)
	}
	begin("models", budgets.Models)
	if err := scratch.verifyModels(); err != nil {
		t.Fatalf("models: %v", err)
	}
	begin("agent", budgets.Prompt)
	if _, err := scratch.verifyPromptTurn(); err != nil {
		t.Fatalf("prompt turn: %v", err)
	}
	begin("logout", budgets.Logout)
	if err := scratch.verifyLogout(); err != nil {
		t.Fatalf("logout: %v", err)
	}
	begin("models", budgets.PostLogoutModels)
	if err := scratch.verifyLoggedOut(); err != nil {
		t.Fatalf("post-logout models: %v", err)
	}
	begin("logout", budgets.Cleanup)
	if err := scratch.Cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	got := calls()
	if len(got) != len(steps) {
		t.Fatalf("native commands = %d, want %d", len(got), len(steps))
	}
	for i, step := range steps {
		call := got[i]
		if call.name != step.name || !call.hasLimit {
			t.Fatalf("command %d = %q (deadline %v), want %q with a deadline", i, call.name, call.hasLimit, step.name)
		}
		if call.deadline.Before(step.start.Add(step.budget)) || call.deadline.After(call.entered.Add(step.budget)) {
			t.Errorf("command %d (%s) did not get a context created for it with budget %s: deadline is outside [step start+budget, entry+budget]", i, step.name, step.budget)
		}
		for j := 0; j < i; j++ {
			if got[j].deadline.Equal(call.deadline) {
				t.Errorf("commands %d and %d share one deadline", j, i)
			}
		}
	}
}

func TestNativeAccountHarnessFixedBudgetCoversEveryStep(t *testing.T) {
	budgets := productionBudgets()
	if budgets.fixed() >= accountFixedBudget {
		t.Fatalf("steps need %s but accountFixedBudget is %s with no process-start slack", budgets.fixed(), accountFixedBudget)
	}
	for name, budget := range map[string]time.Duration{
		"setup": budgets.Setup, "version": budgets.Version, "models": budgets.Models, "prompt": budgets.Prompt,
		"logout": budgets.Logout, "post-logout models": budgets.PostLogoutModels, "cleanup": budgets.Cleanup,
	} {
		if budget <= 0 {
			t.Errorf("%s has no budget", name)
		}
	}
	// The runbook's 45m package timeout still covers the maximum approval wait.
	now := time.Now()
	if err := checkTimeBudget(now.Add(45*time.Minute), true, now, 30*time.Minute); err != nil {
		t.Fatal(err)
	}
}

func TestNativeAccountHarnessSetupHasABudget(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	scratch, _ := fakeAccountScratch(t, "")
	scratch.budgets.Setup = time.Nanosecond
	if err := scratch.initRepo(); err == nil {
		t.Fatal("git init ignored its setup budget")
	}
	scratch.budgets.Setup = 30 * time.Second
	if err := scratch.initRepo(); err != nil {
		t.Fatalf("git init within budget: %v", err)
	}
}

// ---- Ownership uncertainty (scripted process.Run/Start/Reconcile results) ----
//
// Every place that records "this native process tree may still be alive" is
// followed by an ownership reconciliation of the same owner. The record is a
// suspicion, not a verdict: it stands only when reconciliation fails, and a
// successful reconciliation retires it, because reconciliation is the proof that
// the owned tree is gone. The tests below cover every cell of this table, then
// check Cleanup against each outcome:
//
//	site                                  records on           retired by
//	run: process.Run                      RecoveryRequired     the reconcile run does next
//	run: reconcile                        reconcile failure    (stands)
//	startACP: process.Start               RecoveryRequired     the reconcile startACP does next
//	startACP: Handle.Resume               RecoveryRequired     closer's reconcile
//	closer: Handle.Close                  RecoveryRequired     closer's reconcile
//	closer: Handle.Wait                   non-exit failure     closer's reconcile
//	closer: reconcile                     reconcile failure    (stands)

var (
	errScriptedRecovery  = domain.Fail(domain.RecoveryRequired, "scripted ownership failure", "none")
	errScriptedReconcile = errors.New("scripted reconcile failure")
)

// scriptedScratch is a scratch whose native process calls are all scripted.
func scriptedScratch(t *testing.T) *accountScratch {
	t.Helper()
	scratch := buildAccountScratch(t, filepath.Join(t.TempDir(), "grok"), nil)
	t.Cleanup(func() { _ = os.RemoveAll(scratch.root) })
	return scratch
}

// assertCleanupMatches checks that Cleanup retains the scratch tree exactly when
// a suspicion still stands. Its own logout is scripted to succeed.
func assertCleanupMatches(t *testing.T, scratch *accountScratch, wantUncertain bool) {
	t.Helper()
	scratch.runner = func(context.Context, process.Config) error { return nil }
	scratch.reconciler = func(string, domain.ID) error { return nil }
	err := scratch.Cleanup()
	_, statErr := os.Lstat(scratch.root)
	switch {
	case wantUncertain && (err == nil || !strings.Contains(err.Error(), "retained at "+scratch.root) || statErr != nil):
		t.Fatalf("an uncertain run must retain the scratch tree and fail: err=%v stat=%v", err, statErr)
	case !wantUncertain && (err != nil || !errors.Is(statErr, os.ErrNotExist)):
		t.Fatalf("a reconciled run must clean up: err=%v stat=%v", err, statErr)
	}
}

func TestNativeAccountHarnessRunUncertaintyMatrix(t *testing.T) {
	plain := errors.New("exit status 1")
	cases := []struct {
		name          string
		runErr        error
		reconcileErr  error
		wantUncertain bool
	}{
		{"run ok, reconcile ok", nil, nil, false},
		{"run failed, reconcile ok", plain, nil, false},
		{"run recovery required, reconcile ok", errScriptedRecovery, nil, false},
		{"run recovery required, reconcile failed", errScriptedRecovery, errScriptedReconcile, true},
		{"run ok, reconcile failed", nil, errScriptedReconcile, true},
		{"run failed, reconcile failed", plain, errScriptedReconcile, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scratch := scriptedScratch(t)
			var reconciled int
			scratch.runner = func(context.Context, process.Config) error { return tc.runErr }
			scratch.reconciler = func(string, domain.ID) error { reconciled++; return tc.reconcileErr }
			err := scratch.run(context.Background(), []string{"models"}, nil, nil)
			if reconciled != 1 {
				t.Fatalf("run reconciled %d times, want exactly 1", reconciled)
			}
			if tc.runErr != nil && !errors.Is(err, tc.runErr) {
				t.Fatalf("run lost its own error: %v", err)
			}
			if tc.reconcileErr != nil && !errors.Is(err, tc.reconcileErr) {
				t.Fatalf("run lost the reconcile error: %v", err)
			}
			if tc.runErr == nil && tc.reconcileErr == nil && err != nil {
				t.Fatalf("clean run failed: %v", err)
			}
			if got := scratch.uncertainErr() != nil; got != tc.wantUncertain {
				t.Fatalf("uncertain = %v, want %v", got, tc.wantUncertain)
			}
			assertCleanupMatches(t, scratch, tc.wantUncertain)
		})
	}
}

// TestNativeAccountHarnessCleanupLogoutRecoveryIsReconciled covers the cleanup
// logout itself: process.Run reports RecoveryRequired, reconciliation proves the
// tree is gone, and the scratch credential tree is still removed.
func TestNativeAccountHarnessCleanupLogoutRecoveryIsReconciled(t *testing.T) {
	for _, tc := range []struct {
		name         string
		reconcileErr error
		wantRetained bool
	}{
		{"reconciled", nil, false},
		{"not reconciled", errScriptedReconcile, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scratch := scriptedScratch(t)
			scratch.runner = func(context.Context, process.Config) error { return errScriptedRecovery }
			scratch.reconciler = func(string, domain.ID) error { return tc.reconcileErr }
			err := scratch.Cleanup()
			_, statErr := os.Lstat(scratch.root)
			if tc.wantRetained && (err == nil || statErr != nil) {
				t.Fatalf("unreconciled cleanup must retain the tree: err=%v stat=%v", err, statErr)
			}
			if !tc.wantRetained && (err != nil || !errors.Is(statErr, os.ErrNotExist)) {
				t.Fatalf("reconciled cleanup must remove the tree: err=%v stat=%v", err, statErr)
			}
		})
	}
}

// TestNativeAccountHarnessReconcileRetiresOnlyItsOwner proves a successful
// reconciliation never forgives another owner's suspicion.
func TestNativeAccountHarnessReconcileRetiresOnlyItsOwner(t *testing.T) {
	scratch := scriptedScratch(t)
	first, second := scratch.config(nil, nil, nil), scratch.config(nil, nil, nil)
	scratch.noteUncertain(first.OwnerID, errors.New("first"))
	scratch.noteUncertain(second.OwnerID, errors.New("second"))
	scratch.reconciler = func(string, domain.ID) error { return nil }
	if err := scratch.reconcileNative(second, "native process"); err != nil {
		t.Fatal(err)
	}
	if err := scratch.uncertainErr(); err == nil || !strings.Contains(err.Error(), "first") || strings.Contains(err.Error(), "second") {
		t.Fatalf("reconciling one owner changed another owner's suspicion: %v", err)
	}
}

// scriptedHandle is a native handle whose lifecycle results are scripted.
type scriptedHandle struct {
	resume, closeErr, wait error
}

func (h scriptedHandle) Resume() error               { return h.resume }
func (h scriptedHandle) Write(p []byte) (int, error) { return len(p), nil }
func (h scriptedHandle) CloseInput() error           { return nil }
func (h scriptedHandle) Close() error                { return h.closeErr }
func (h scriptedHandle) Wait() error                 { return h.wait }
func (h scriptedHandle) Done() <-chan struct{}       { return nil }

func TestNativeAccountHarnessACPUncertaintyMatrix(t *testing.T) {
	notFound := domain.Fail(domain.NotFound, "scripted missing executable", "none")
	waitFailed := errors.New("scripted wait failure")
	cases := []struct {
		name          string
		startErr      error
		handle        scriptedHandle
		reconcileErr  error
		wantStartFail bool
		wantCloseFail bool
		wantReconcile int
		wantUncertain bool
	}{
		{name: "start recovery required, reconcile ok", startErr: errScriptedRecovery, wantStartFail: true, wantReconcile: 1},
		{name: "start recovery required, reconcile failed", startErr: errScriptedRecovery, reconcileErr: errScriptedReconcile, wantStartFail: true, wantReconcile: 1, wantUncertain: true},
		{name: "start refused without a process", startErr: notFound, reconcileErr: errScriptedReconcile, wantStartFail: true},
		{name: "resume recovery required, reconcile ok", handle: scriptedHandle{resume: errScriptedRecovery}, wantStartFail: true, wantReconcile: 1},
		{name: "resume recovery required, reconcile failed", handle: scriptedHandle{resume: errScriptedRecovery}, reconcileErr: errScriptedReconcile, wantStartFail: true, wantReconcile: 1, wantUncertain: true},
		{name: "close recovery required, reconcile ok", handle: scriptedHandle{closeErr: errScriptedRecovery}, wantCloseFail: true, wantReconcile: 1},
		{name: "close recovery required, reconcile failed", handle: scriptedHandle{closeErr: errScriptedRecovery}, reconcileErr: errScriptedReconcile, wantCloseFail: true, wantReconcile: 1, wantUncertain: true},
		{name: "wait failed, reconcile ok", handle: scriptedHandle{wait: waitFailed}, wantCloseFail: true, wantReconcile: 1},
		{name: "wait failed, reconcile failed", handle: scriptedHandle{wait: waitFailed}, reconcileErr: errScriptedReconcile, wantCloseFail: true, wantReconcile: 1, wantUncertain: true},
		{name: "stopped cleanly, reconcile ok", wantReconcile: 1},
		{name: "stopped cleanly, reconcile failed", reconcileErr: errScriptedReconcile, wantCloseFail: true, wantReconcile: 1, wantUncertain: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scratch := scriptedScratch(t)
			var reconciled int
			scratch.starter = func(context.Context, process.Config) (nativeHandle, error) {
				if tc.startErr != nil {
					return nil, tc.startErr
				}
				return tc.handle, nil
			}
			scratch.reconciler = func(string, domain.ID) error { reconciled++; return tc.reconcileErr }
			_, closeACP, err := startACP(context.Background(), scratch)
			if (err != nil) != tc.wantStartFail {
				t.Fatalf("startACP error = %v, want failure %v", err, tc.wantStartFail)
			}
			if err == nil {
				if closeErr := closeACP(); (closeErr != nil) != tc.wantCloseFail {
					t.Fatalf("closer error = %v, want failure %v", closeErr, tc.wantCloseFail)
				}
			}
			if reconciled != tc.wantReconcile {
				t.Fatalf("reconciled %d times, want %d", reconciled, tc.wantReconcile)
			}
			if got := scratch.uncertainErr() != nil; got != tc.wantUncertain {
				t.Fatalf("uncertain = %v (%v), want %v", got, scratch.uncertainErr(), tc.wantUncertain)
			}
			assertCleanupMatches(t, scratch, tc.wantUncertain)
		})
	}
}

// agentStep is one frame an in-process agent delivers, after a pause.
type agentStep struct {
	after time.Duration
	frame string
}

// orderedAgent is an in-process native handle that answers the four ACP
// requests of the prompt turn (initialize, authenticate, session/new,
// session/prompt) and lets each test place extra frames before or after the
// matching response. It records how the client answered agent requests.
type orderedAgent struct {
	frames *acpFrames
	// extra returns the frames to deliver before and after the response to one
	// request; it may return nil for both.
	extra func(method string) (before, after []agentStep)
	// exitAfter closes Done once every frame of the session/prompt request has
	// been delivered, as an agent that exits right after its last update does.
	exitAfter bool

	mu       sync.Mutex
	refusals []string
	stop     chan struct{}
	done     chan struct{}
	once     sync.Once
}

func newOrderedAgent(extra func(method string) (before, after []agentStep)) *orderedAgent {
	return &orderedAgent{
		frames: &acpFrames{notify: make(chan struct{}, 1)},
		extra:  extra,
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
}

func (a *orderedAgent) Resume() error     { return nil }
func (a *orderedAgent) CloseInput() error { return nil }
func (a *orderedAgent) Wait() error       { return nil }
func (a *orderedAgent) Done() <-chan struct{} {
	return a.done
}
func (a *orderedAgent) Close() error {
	a.once.Do(func() { close(a.stop) })
	return nil
}

func (a *orderedAgent) Write(p []byte) (int, error) {
	var request struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Error  *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(p, &request); err != nil {
		return 0, err
	}
	if request.Method == "" {
		// The client's answer to an agent request.
		a.mu.Lock()
		defer a.mu.Unlock()
		if request.Error == nil || request.Error.Code != -32601 {
			a.refusals = append(a.refusals, "unexpected answer")
		} else {
			a.refusals = append(a.refusals, string(request.ID))
		}
		return len(p), nil
	}
	results := map[string]string{
		"initialize":     `{"protocolVersion":1,"authMethods":[{"id":"cached_token"}]}`,
		"authenticate":   `{}`,
		"session/new":    `{"sessionId":"sess-1"}`,
		"session/prompt": `{"stopReason":"end_turn"}`,
	}
	result, ok := results[request.Method]
	if !ok {
		return 0, fmt.Errorf("unexpected request %s", request.Method)
	}
	before, after := a.extra(request.Method)
	steps := append([]agentStep{}, before...)
	steps = append(steps, agentStep{frame: fmt.Sprintf(`{"id":%s,"jsonrpc":"2.0","result":%s}`, request.ID, result)})
	steps = append(steps, after...)
	go func() {
		for _, step := range steps {
			if step.after > 0 {
				select {
				case <-time.After(step.after):
				case <-a.stop:
					return
				}
			}
			_, _ = a.frames.Write([]byte(step.frame + "\n"))
		}
		if a.exitAfter && request.Method == "session/prompt" {
			close(a.done)
		}
	}()
	return len(p), nil
}

func (a *orderedAgent) refused() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.refusals...)
}

func updateFrame(session, kind, text string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"%s","update":{"sessionUpdate":"%s","content":{"type":"text","text":"%s"}}}}`, session, kind, text)
}

func agentRequestFrame(id string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":"%s","method":"session/request_permission","params":{}}`, id)
}

// TestNativeAccountHarnessFrameOrderings pins that every request/response pair
// of the prompt turn tolerates notifications and agent requests on either side
// of its response. A notification or agent request that precedes a response is
// handled while waiting for it; one that follows is handled by the next call, or
// for the last call (session/prompt) by settleReply. Session/cancel is not part
// of the suite, and logout is a plain CLI command, not a JSON-RPC pair.
func TestNativeAccountHarnessFrameOrderings(t *testing.T) {
	const (
		quiet = 400 * time.Millisecond
		limit = 3 * time.Second
	)
	type outcome struct {
		updates  int      // session/update frames the client must have counted
		refusals []string // agent request ids the client must have refused, in order
		err      string   // substring of the turn error; empty means success
	}
	ok := updateFrame("sess-1", "agent_message_chunk", "ok")
	note := updateFrame("sess-1", "available_commands_update", "x")
	late := 40 * time.Millisecond
	type steps = func(method string) (before, after []agentStep)
	// onlyFor places frames around the response to one method; the prompt then
	// answers with its text before its response.
	onlyFor := func(method string, before, after []agentStep) steps {
		return func(got string) ([]agentStep, []agentStep) {
			switch got {
			case method:
				return before, after
			case "session/prompt":
				return []agentStep{{frame: ok}}, nil
			}
			return nil, nil
		}
	}
	// promptOnly places frames around the response to session/prompt.
	promptOnly := func(before, after []agentStep) steps {
		return func(got string) ([]agentStep, []agentStep) {
			if got == "session/prompt" {
				return before, after
			}
			return nil, nil
		}
	}
	type row struct {
		name      string
		extra     steps
		exitAfter bool
		want      outcome
	}
	var cases []row
	for _, method := range []string{"initialize", "authenticate", "session/new"} {
		cases = append(cases,
			row{method + ": notification before the response", onlyFor(method, []agentStep{{frame: note}}, nil), false, outcome{updates: 2}},
			row{method + ": notification after the response", onlyFor(method, nil, []agentStep{{after: late, frame: note}}), false, outcome{updates: 2}},
			row{method + ": agent request before the response", onlyFor(method, []agentStep{{frame: agentRequestFrame("r1")}}, nil), false, outcome{updates: 1, refusals: []string{`"r1"`}}},
			row{method + ": agent request after the response", onlyFor(method, nil, []agentStep{{after: late, frame: agentRequestFrame("r2")}}), false, outcome{updates: 1, refusals: []string{`"r2"`}}},
		)
	}
	cases = append(cases,
		row{"session/prompt: text before the response", promptOnly([]agentStep{{frame: ok}}, nil), false, outcome{updates: 1}},
		row{"session/prompt: text after the response", promptOnly(nil, []agentStep{{after: late, frame: ok}}), false, outcome{updates: 1}},
		row{"session/prompt: chunks on both sides of the response", promptOnly([]agentStep{{frame: updateFrame("sess-1", "agent_message_chunk", "O")}}, []agentStep{{after: late, frame: updateFrame("sess-1", "agent_message_chunk", "k")}}), false, outcome{updates: 2}},
		row{"session/prompt: several late chunks", promptOnly(nil, []agentStep{{after: late, frame: updateFrame("sess-1", "agent_message_chunk", "O")}, {after: late, frame: updateFrame("sess-1", "agent_message_chunk", "k")}, {after: late, frame: updateFrame("sess-1", "agent_message_chunk", ".")}}), false, outcome{updates: 3}},
		row{"session/prompt: agent request before the response", promptOnly([]agentStep{{frame: agentRequestFrame("r3")}, {frame: ok}}, nil), false, outcome{updates: 1, refusals: []string{`"r3"`}}},
		row{"session/prompt: agent request after the response", promptOnly(nil, []agentStep{{after: late, frame: agentRequestFrame("r4")}, {after: late, frame: ok}}), false, outcome{updates: 1, refusals: []string{`"r4"`}}},
		row{"session/prompt: notification after the text", promptOnly([]agentStep{{frame: ok}}, []agentStep{{after: late, frame: note}}), false, outcome{updates: 2}},
		row{"session/prompt: agent exits after its last late chunk", promptOnly(nil, []agentStep{{after: late, frame: ok}}), true, outcome{updates: 1}},
		row{"session/prompt: no text at all", promptOnly(nil, nil), false, outcome{err: "without any assistant message text"}},
		row{"session/prompt: only a late notification", promptOnly(nil, []agentStep{{after: late, frame: note}}), false, outcome{updates: 1, err: "without any assistant message text"}},
		row{"session/prompt: late text for another session", promptOnly(nil, []agentStep{{after: late, frame: updateFrame("sess-2", "agent_message_chunk", "ok")}}), false, outcome{updates: 1, err: "without any assistant message text"}},
		row{"session/prompt: late wrong text", promptOnly(nil, []agentStep{{after: late, frame: updateFrame("sess-1", "agent_message_chunk", "nope")}}), false, outcome{updates: 1, err: "not the expected single word"}},
	)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel() // each row owns its in-process agent and client
			agent := newOrderedAgent(tc.extra)
			agent.exitAfter = tc.exitAfter
			defer agent.Close()
			client := &acpClient{handle: agent, frames: agent.frames, quiet: quiet, limit: limit}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			err := runPromptTurn(ctx, client, t.TempDir())
			switch {
			case tc.want.err == "" && err != nil:
				t.Fatalf("turn rejected: %v", err)
			case tc.want.err != "" && (err == nil || !strings.Contains(err.Error(), tc.want.err)):
				t.Fatalf("turn error %v does not contain %q", err, tc.want.err)
			}
			if client.updates != tc.want.updates {
				t.Fatalf("counted %d session updates, want %d", client.updates, tc.want.updates)
			}
			if got := agent.refused(); strings.Join(got, ",") != strings.Join(tc.want.refusals, ",") {
				t.Fatalf("refused agent requests %v, want %v", got, tc.want.refusals)
			}
		})
	}
}
