// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

// The usage profile is the exclusive, DeliDev-owned native profile in which the
// official `claude auth login --claudeai` runs for usage tracking. It is created
// for exactly one server-token Claude account and is independent of the
// execution setup-token profile, any personal profile and any legacy profile.
// The adapter below never enumerates, adopts or reads any other profile: it
// reads and removes only the native credential storage derived from the
// profile's own configuration directory.

const (
	// UsageCredentialFile is the native credential file name below the profile's
	// Claude configuration directory.
	UsageCredentialFile = ".credentials.json"
	// UsageClientID is the pinned OAuth client of the original CLI. The
	// authorization address was validated against it before any code is accepted.
	UsageClientID = nativeClaudeClientID

	usageKeychainBase       = "Claude Code-credentials"
	maxUsageCredentialBytes = 64 << 10
	usageRequiredScopeA     = "user:inference"
	usageRequiredScopeB     = "user:profile"
)

func usageLoginUnsupported() *domain.Error {
	return domain.Fail(domain.Unsupported, "The Claude usage login must complete with a manual approval code.", "Keep the original native operation; do not substitute a client localhost callback.")
}

func usageStorageProblem(message string) *domain.Error {
	return domain.Fail(domain.RecoveryRequired, message, "Inspect the exclusive usage profile without exposing its contents; never adopt another profile or credential.")
}

// UsageEnvironment returns the isolated child environment of the usage login.
// Both CLAUDE_CONFIG_DIR and CLAUDE_SECURESTORAGE_CONFIG_DIR name the exact
// private profile directory, and the server never opens a browser: a verified
// no-op BROWSER opener fails closed on a host that has none.
func UsageEnvironment(config AuthConfig) ([]string, error) {
	env, err := authEnvironment(config)
	if err != nil {
		return nil, err
	}
	opener, err := setupTokenBrowserBlocker(runtime.GOOS, setupTokenNoopOpeners)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if key, _, _ := strings.Cut(entry, "="); strings.EqualFold(key, "BROWSER") {
			continue
		}
		result = append(result, entry)
	}
	return append(result, "BROWSER="+opener), nil
}

// StartUsageLogin starts the original `claude auth login --claudeai` child in
// the exclusive usage profile. The login address is validated by the shared
// original-CLI grammar, and only the manual approval-code variant is accepted.
func StartUsageLogin(ctx context.Context, config AuthConfig) (*AuthLogin, error) {
	env, err := UsageEnvironment(config)
	if err != nil {
		return nil, err
	}
	return startAuthLogin(ctx, config, env, true)
}

// UsageIdentity is the validated OAuth identity reported by the native CLI for
// the usage profile. It is not credential material, but it is personal data and
// is never logged.
type UsageIdentity struct {
	Email            string
	OrganizationID   string
	OrganizationName string
	Plan             string
}

// ReadUsageIdentity asks the original CLI for the identity of the profile's
// newly created login. Anything but a first-party claude.ai subscription fails.
func ReadUsageIdentity(ctx context.Context, config AuthConfig) (UsageIdentity, error) {
	status, err := ReadAuthStatus(ctx, config)
	if err != nil {
		return UsageIdentity{}, err
	}
	if !status.LoggedIn || !status.Subscription() {
		return UsageIdentity{}, authProblem()
	}
	identity := UsageIdentity{Email: *status.Email, OrganizationID: *status.OrgID, Plan: *status.SubscriptionType}
	if status.OrgName != nil {
		identity.OrganizationName = *status.OrgName
	}
	return identity, nil
}

// UsageKeychain is the macOS native secure-storage seam. The production
// implementation shells out to the operating system tool only for the exact item
// derived from the profile; tests inject a fake.
type UsageKeychain interface {
	// Find returns the stored secret of exactly one service and account.
	Find(ctx context.Context, service, account string) (secret []byte, found bool, err error)
	// Delete removes exactly one item. An absent item is success.
	Delete(ctx context.Context, service, account string) error
}

// UsageKeychainService derives the Keychain service the original CLI uses for a
// profile whose configuration directory is configDir: the fixed base name plus
// the first eight hex digits of the SHA-256 of the exact directory string. The
// derivation is the single place that knows it; a profile whose item cannot be
// found under this name simply has no usable credential and fails closed.
func UsageKeychainService(configDir string) string {
	sum := sha256.Sum256([]byte(configDir))
	return usageKeychainBase + "-" + hex.EncodeToString(sum[:])[:8]
}

// UsageKeychainAccount is the Keychain account the original CLI uses: the
// operating system user the process runs as. The profile's child environment
// never carries USER, so the CLI resolves the same identity from the operating
// system and a stale or overridden USER of this process must not choose the
// account label. An identity that cannot be resolved yields the empty account,
// which makes the macOS adapter fail closed.
func UsageKeychainAccount() string {
	current, err := user.LookupId(strconv.Itoa(os.Geteuid()))
	if err != nil {
		return ""
	}
	return current.Username
}

// UsageStorage addresses the native credential storage of exactly one usage
// profile. ConfigDir is the profile's `claude` directory, which is both
// CLAUDE_CONFIG_DIR and CLAUDE_SECURESTORAGE_CONFIG_DIR of the login.
type UsageStorage struct {
	// GOOS selects the platform adapter; empty selects the running platform.
	GOOS      string
	ConfigDir string
	// Account is the Keychain account (macOS only).
	Account  string
	Keychain UsageKeychain
	// Now supplies the clock used to reject an already expired bundle.
	Now func() time.Time
}

func (s UsageStorage) goos() string {
	if s.GOOS != "" {
		return s.GOOS
	}
	return runtime.GOOS
}

func (s UsageStorage) clock() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s UsageStorage) validate() error {
	if !filepath.IsAbs(s.ConfigDir) || filepath.Base(s.ConfigDir) != "claude" || filepath.Clean(s.ConfigDir) != s.ConfigDir {
		return usageStorageProblem("The Claude usage profile is not a canonical private profile.")
	}
	switch s.goos() {
	case "linux", "windows":
		return nil
	case "darwin":
		if s.Keychain == nil || s.Account == "" {
			return domain.Fail(domain.Unsupported, "The macOS Keychain is unavailable for the Claude usage profile.", "Run Claude usage tracking on a supported server host.")
		}
		return nil
	}
	return domain.Fail(domain.Unsupported, "This server platform does not support Claude usage tracking.", "Run Claude usage tracking on a Linux, macOS or Windows server host.")
}

// File is the profile-owned credential file path.
func (s UsageStorage) File() string { return filepath.Join(s.ConfigDir, UsageCredentialFile) }

func (s UsageStorage) root() string { return filepath.Dir(s.ConfigDir) }

// readFile reads the profile-owned credential file. A symlink, a non-regular
// entry, unsafe permissions, a foreign owner or an oversized file fails closed.
func (s UsageStorage) readFile() (raw []byte, found bool, err error) {
	info, err := os.Lstat(s.File())
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, usageStorageProblem("The Claude usage credential file could not be inspected.")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, false, usageStorageProblem("The Claude usage credential file is not a regular profile-owned file.")
	}
	raw, err = security.ReadPrivate(s.File(), maxUsageCredentialBytes)
	if err != nil {
		return nil, false, usageStorageProblem("The Claude usage credential file is unsafe or unreadable.")
	}
	return raw, true, nil
}

// Present reports whether any native credential storage exists in the profile.
// An unexpected entry (symlink, non-regular file, unsafe permissions) is
// reported as an error because it cannot be proven to be removable usage state.
func (s UsageStorage) Present(ctx context.Context) (bool, error) {
	if err := s.validate(); err != nil {
		return true, err
	}
	if _, err := subscription.InspectRetainedCredentials(s.root()); err != nil {
		return true, usageStorageProblem("The Claude usage profile holds unsafe credential storage.")
	}
	if _, err := os.Lstat(s.File()); err == nil {
		return true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return true, usageStorageProblem("The Claude usage credential file could not be inspected.")
	}
	if s.goos() == "darwin" {
		secret, found, err := s.Keychain.Find(ctx, UsageKeychainService(s.ConfigDir), s.Account)
		clear(secret)
		if err != nil {
			return true, usageStorageProblem("The Claude usage Keychain item could not be inspected.")
		}
		return found, nil
	}
	return false, nil
}

// Remove deletes only this profile's native credential storage: the credential
// file with its attributable temporaries and, on macOS, the exact derived
// Keychain item. A foreign or unsafe entry is never deleted; it fails closed.
func (s UsageStorage) Remove(ctx context.Context) error {
	if err := s.validate(); err != nil {
		return err
	}
	if s.goos() == "darwin" {
		if err := s.Keychain.Delete(ctx, UsageKeychainService(s.ConfigDir), s.Account); err != nil {
			return usageStorageProblem("The Claude usage Keychain item could not be removed.")
		}
	}
	if err := subscription.RetireCredentialFiles(s.root()); err != nil {
		return usageStorageProblem("The Claude usage credential file could not be removed.")
	}
	return nil
}

type usageNativeCredentials struct {
	ClaudeAiOauth *struct {
		AccessToken  json.RawMessage `json:"accessToken"`
		RefreshToken json.RawMessage `json:"refreshToken"`
		ExpiresAt    json.RawMessage `json:"expiresAt"`
		Scopes       []string        `json:"scopes"`
	} `json:"claudeAiOauth"`
}

func unquoteUsageToken(raw json.RawMessage) []byte {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' || bytes.ContainsAny(raw[1:len(raw)-1], "\"\\") {
		return nil
	}
	return append([]byte(nil), raw[1:len(raw)-1]...)
}

// Read returns the newly created usage-owned credential bundle. It consults
// only this profile: the profile credential file and, on macOS, the exact
// profile-derived Keychain item. Both present at once is ambiguous and fails
// closed; neither present means the login produced no credential. Callers must
// call Clear on the returned bundle.
func (s UsageStorage) Read(ctx context.Context) (subscription.ClaudeUsageBundle, error) {
	if err := s.validate(); err != nil {
		return subscription.ClaudeUsageBundle{}, err
	}
	fileRaw, fileFound, err := s.readFile()
	defer clear(fileRaw)
	if err != nil {
		return subscription.ClaudeUsageBundle{}, err
	}
	raw := fileRaw
	if s.goos() == "darwin" {
		secret, found, e := s.Keychain.Find(ctx, UsageKeychainService(s.ConfigDir), s.Account)
		defer clear(secret)
		if e != nil {
			return subscription.ClaudeUsageBundle{}, usageStorageProblem("The Claude usage Keychain item could not be read.")
		}
		if found && fileFound {
			return subscription.ClaudeUsageBundle{}, usageStorageProblem("The Claude usage credential storage is ambiguous.")
		}
		if found {
			raw, fileFound = secret, true
		}
	}
	if !fileFound {
		return subscription.ClaudeUsageBundle{}, usageStorageProblem("The Claude usage login produced no credential in its profile.")
	}
	return parseUsageCredentials(raw, s.clock())
}

func parseUsageCredentials(raw []byte, now time.Time) (subscription.ClaudeUsageBundle, error) {
	invalid := func() (subscription.ClaudeUsageBundle, error) {
		return subscription.ClaudeUsageBundle{}, usageStorageProblem("The Claude usage credential is invalid.")
	}
	var native usageNativeCredentials
	if len(raw) == 0 || len(raw) > maxUsageCredentialBytes || json.Unmarshal(raw, &native) != nil || native.ClaudeAiOauth == nil {
		return invalid()
	}
	oauth := native.ClaudeAiOauth
	defer clear(oauth.AccessToken)
	defer clear(oauth.RefreshToken)
	bundle := subscription.ClaudeUsageBundle{AccessToken: unquoteUsageToken(oauth.AccessToken), Scopes: slices.Clone(oauth.Scopes), ClientID: UsageClientID}
	if len(oauth.RefreshToken) != 0 && !bytes.Equal(bytes.TrimSpace(oauth.RefreshToken), []byte("null")) {
		bundle.RefreshToken = unquoteUsageToken(oauth.RefreshToken)
		if bundle.RefreshToken == nil {
			bundle.Clear()
			return invalid()
		}
	}
	var expiresMillis int64
	if bundle.AccessToken == nil || json.Unmarshal(oauth.ExpiresAt, &expiresMillis) != nil || expiresMillis <= 0 {
		bundle.Clear()
		return invalid()
	}
	bundle.ExpiresAt = time.UnixMilli(expiresMillis).UTC()
	if !bundle.ExpiresAt.After(now) || !slices.Contains(bundle.Scopes, usageRequiredScopeA) || !slices.Contains(bundle.Scopes, usageRequiredScopeB) {
		bundle.Clear()
		return invalid()
	}
	// Seal validates the exact token, scope and client shape once more, and binds
	// nothing: the shape check alone is wanted here.
	probe, err := subscription.SealClaudeUsageBundle(domain.NewID(), domain.NewID(), domain.NewID(), strings.Repeat("0", 64), bundle)
	clear(probe)
	if err != nil {
		bundle.Clear()
		return invalid()
	}
	return bundle, nil
}
