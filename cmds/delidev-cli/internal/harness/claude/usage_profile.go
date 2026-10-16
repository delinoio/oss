// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

// The Claude usage login is a separate, server-owned, read-only sign-in that
// runs the original `claude auth login --claudeai` in one exclusive persistent
// usage profile. Its native credentials are read only from that newly created
// profile; personal, legacy execution and external profiles are never
// enumerated, adopted or read.

// usageManualCallback is the only redirect the usage login accepts: a remote
// server cannot receive a callback on the client's localhost, so the original
// manual approval-code method is required.
const usageManualCallback = "https://platform.claude.com/oauth/code/callback"

const (
	// usageCredentialMax bounds one native credential record.
	usageCredentialMax = 64 << 10
	// usageKeychainServicePrefix is the native Keychain service of a profile
	// selected through CLAUDE_CONFIG_DIR; the suffix is derived from that exact
	// directory.
	usageKeychainServicePrefix = "Claude Code-credentials"
	usageKeychainNotFoundExit  = 44
)

// ValidateUsageLoginURL accepts the original auth-login address only with the
// manual approval-code callback. The ordinary localhost callback variant is
// refused because it would deliver the code to the server's loopback, which the
// user's browser cannot reach, and DeliDev never substitutes another callback.
func ValidateUsageLoginURL(raw string) error {
	if err := ValidateLoginURL(raw); err != nil {
		return err
	}
	u, err := url.Parse(raw)
	if err != nil || u.Query().Get("redirect_uri") != usageManualCallback {
		return domain.NewClaudeLimitFailure(domain.ClaudeLimitFailureLoginNotInteractive, 0)
	}
	return nil
}

// UsageConfig describes one usage-profile native launch. Process.Cwd is the
// profile root and Home its `claude` directory, exactly like every auth launch.
type UsageConfig struct {
	Process process.Config
	Version string
	Home    string
}

func (c UsageConfig) auth() AuthConfig {
	return AuthConfig{Process: c.Process, Version: c.Version, Home: c.Home}
}

// usageEnvironment extends the private auth environment so the native child can
// never open a browser on the server host. CLAUDE_CONFIG_DIR and
// CLAUDE_SECURESTORAGE_CONFIG_DIR both name the exact private profile.
func usageEnvironment(config UsageConfig) ([]string, error) {
	env, err := authEnvironment(config.auth())
	if err != nil {
		return nil, err
	}
	opener, err := setupTokenBrowserBlocker(runtime.GOOS, setupTokenNoopOpeners)
	if err != nil {
		return nil, domain.NewClaudeLimitFailure(domain.ClaudeLimitFailureLoginUnsupported, 0)
	}
	result := make([]string, 0, len(env)+1)
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "BROWSER") {
			continue
		}
		result = append(result, entry)
	}
	return append(result, "BROWSER="+opener), nil
}

// StartUsageLogin launches the original login. The caller must Close it to join
// the child and clear its buffers.
func StartUsageLogin(ctx context.Context, config UsageConfig) (*AuthLogin, error) {
	env, err := usageEnvironment(config)
	if err != nil {
		return nil, err
	}
	return launchAuthLogin(ctx, config.auth(), env, &loginOutput{validate: ValidateUsageLoginURL})
}

// UsageRefreshInput is the only credential material a refresh-login child
// receives, and only through its own environment.
type UsageRefreshInput struct {
	RefreshToken []byte
	Scopes       []string
	ClientID     string
}

// StartUsageRefresh launches one supervised native refresh-login in the same
// profile. The refresh token, scopes and optional client identifier exist only
// in the child's environment. An interactive authorization address in the
// output is refused by the usage URL grammar and surfaces as an error.
func StartUsageRefresh(ctx context.Context, config UsageConfig, input UsageRefreshInput) (*AuthLogin, error) {
	if len(input.RefreshToken) == 0 || len(input.Scopes) == 0 {
		return nil, authProblem()
	}
	env, err := usageEnvironment(config)
	if err != nil {
		return nil, err
	}
	filtered := make([]string, 0, len(env)+3)
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(strings.ToUpper(key), "CLAUDE_CODE_OAUTH_") {
			continue
		}
		filtered = append(filtered, entry)
	}
	filtered = append(filtered, "CLAUDE_CODE_OAUTH_REFRESH_TOKEN="+string(input.RefreshToken), "CLAUDE_CODE_OAUTH_SCOPES="+strings.Join(input.Scopes, " "))
	if input.ClientID != "" && input.ClientID != subscription.ClaudeUsageClientID {
		filtered = append(filtered, "CLAUDE_CODE_OAUTH_CLIENT_ID="+input.ClientID)
	}
	return launchAuthLogin(ctx, config.auth(), filtered, &loginOutput{validate: func(string) error {
		return domain.NewClaudeLimitFailure(domain.ClaudeLimitFailureRefreshUnsupported, 0)
	}})
}

// UsageIdentity is the validated OAuth identity of the usage profile.
type UsageIdentity struct {
	Email        string
	Organization string
	OrgID        string
	Plan         string
}

// StableKey is the bounded identity material the server commits to; the plan is
// excluded so a plan change alone never reads as a different account.
func (i UsageIdentity) StableKey() []byte { return []byte(i.Email + "\x00" + i.OrgID) }

// ReadUsageIdentity asks the original CLI which first-party subscription
// account the usage profile holds. The result is bounded and validated.
func ReadUsageIdentity(ctx context.Context, config UsageConfig) (UsageIdentity, error) {
	status, err := ReadAuthStatus(ctx, config.auth())
	if err != nil {
		return UsageIdentity{}, domain.NewClaudeLimitFailure(domain.ClaudeLimitFailureLoginRejected, 0)
	}
	if !status.Subscription() {
		return UsageIdentity{}, domain.NewClaudeLimitFailure(domain.ClaudeLimitFailureLoginRejected, 0)
	}
	identity := UsageIdentity{Email: *status.Email, OrgID: *status.OrgID, Plan: *status.SubscriptionType}
	if status.OrgName != nil && domain.Text(*status.OrgName, "usage organization", 512, false) == nil {
		identity.Organization = *status.OrgName
	}
	return identity, nil
}

// UsageStore reads and removes the native credential record of exactly one
// usage profile. It never resolves another profile's storage.
type UsageStore struct {
	root string
	home string
	goos string
	// user and keychain are replaceable seams for fake native stores.
	user     func() (string, error)
	keychain func(ctx context.Context, args ...string) ([]byte, int, error)
}

// NewUsageStore binds the store to the profile root (the directory that holds
// the `claude` configuration directory).
func NewUsageStore(root string) UsageStore {
	return UsageStore{root: root, home: filepath.Join(root, "claude"), goos: runtime.GOOS, user: currentUsageAccount, keychain: runSecurity}
}

func currentUsageAccount() (string, error) {
	current, err := user.Current()
	if err != nil || current.Username == "" {
		return "", errors.New("account")
	}
	return current.Username, nil
}

func runSecurity(ctx context.Context, args ...string) ([]byte, int, error) {
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(bounded, "/usr/bin/security", args...)
	var stdout bytes.Buffer
	command.Stdout = &limitedWriter{buffer: &stdout, limit: usageCredentialMax}
	err := command.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return stdout.Bytes(), exit.ExitCode(), err
	}
	return stdout.Bytes(), 0, err
}

type limitedWriter struct {
	buffer *bytes.Buffer
	limit  int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if w.buffer.Len()+len(p) > w.limit {
		return 0, errors.New("limit")
	}
	return w.buffer.Write(p)
}

// KeychainService is the exact native item service of this profile: the fixed
// prefix plus the first eight hexadecimal digits of the SHA-256 of the exact
// configuration directory. It is derived, never enumerated.
func (s UsageStore) KeychainService() string {
	digest := sha256.Sum256([]byte(s.home))
	return usageKeychainServicePrefix + "-" + hex.EncodeToString(digest[:])[:8]
}

func (s UsageStore) credentialPath() string { return filepath.Join(s.home, ".credentials.json") }

func storageFailure(f domain.ClaudeLimitFailure) error { return domain.NewClaudeLimitFailure(f, 0) }

// filePresent inspects the profile-owned credential file and its interrupted
// writes. A symlink, foreign owner, unsafe permission or other non-regular entry
// is an integrity failure, never an admission.
func (s UsageStore) filePresent() (bool, error) {
	present, err := subscription.InspectRetainedCredentials(s.root)
	if err != nil {
		return true, storageFailure(domain.ClaudeLimitFailureProfileUnsafe)
	}
	return present, nil
}

func (s UsageStore) keychainArgs(verb string, extra ...string) ([]string, error) {
	account, err := s.user()
	if err != nil {
		return nil, storageFailure(domain.ClaudeLimitFailureStorage)
	}
	return append([]string{verb, "-a", account, "-s", s.KeychainService()}, extra...), nil
}

// keychainRecord returns the exact Keychain item when present.
func (s UsageStore) keychainRecord(ctx context.Context) ([]byte, bool, error) {
	args, err := s.keychainArgs("find-generic-password", "-w")
	if err != nil {
		return nil, false, err
	}
	out, code, runErr := s.keychain(ctx, args...)
	if runErr != nil {
		clear(out)
		if code == usageKeychainNotFoundExit {
			return nil, false, nil
		}
		return nil, false, storageFailure(domain.ClaudeLimitFailureStorage)
	}
	return bytes.TrimSpace(out), true, nil
}

// Present reports whether any native credential storage of the profile holds a
// record. It is used to prove that a first admission is empty.
func (s UsageStore) Present(ctx context.Context) (bool, error) {
	file, err := s.filePresent()
	if err != nil {
		return true, err
	}
	if s.goos != "darwin" {
		return file, nil
	}
	record, found, err := s.keychainRecord(ctx)
	clear(record)
	if err != nil {
		return true, err
	}
	return file || found, nil
}

// Read returns the native credential record of the profile. On macOS the exact
// Keychain item is preferred and the documented file fallback is used only when
// that item is absent; Linux and Windows read the profile-owned file. Windows
// storage that is not the profile file fails explicitly. Callers must clear the
// returned bytes.
func (s UsageStore) Read(ctx context.Context) ([]byte, error) {
	if s.goos == "darwin" {
		record, found, err := s.keychainRecord(ctx)
		if err != nil {
			return nil, err
		}
		if found {
			return record, nil
		}
	}
	present, err := s.filePresent()
	if err != nil {
		return nil, err
	}
	if !present {
		if s.goos == "windows" {
			return nil, storageFailure(domain.ClaudeLimitFailureStorage)
		}
		return nil, storageFailure(domain.ClaudeLimitFailureCredentials)
	}
	raw, err := security.ReadPrivate(s.credentialPath(), usageCredentialMax)
	if err != nil {
		return nil, storageFailure(domain.ClaudeLimitFailureProfileUnsafe)
	}
	return raw, nil
}

// Remove deletes only this profile's native credential storage: the exact
// Keychain item (macOS) and the profile-owned credential file with its
// interrupted-write remnants. Unrelated records are never touched. Absence is
// success so a repeated removal is idempotent.
func (s UsageStore) Remove(ctx context.Context) error {
	if s.goos == "darwin" {
		args, err := s.keychainArgs("delete-generic-password")
		if err != nil {
			return err
		}
		out, code, runErr := s.keychain(ctx, args...)
		clear(out)
		if runErr != nil && code != usageKeychainNotFoundExit {
			return storageFailure(domain.ClaudeLimitFailureCleanupUncertain)
		}
	}
	if err := subscription.RetireCredentialFiles(s.root); err != nil {
		return storageFailure(domain.ClaudeLimitFailureCleanupUncertain)
	}
	if _, err := os.Lstat(s.credentialPath()); err == nil {
		return storageFailure(domain.ClaudeLimitFailureCleanupUncertain)
	}
	return nil
}
