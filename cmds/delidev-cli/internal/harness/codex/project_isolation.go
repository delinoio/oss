// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// Ordinary Codex execution selects no DeliDev MCP server. Codex activates a
// repository's own .codex/config.toml layer once it records the project as
// trusted, and MCP tables merge across layers, so a workspace-write thread
// would otherwise launch the repository's MCP servers. The adapter therefore
// keeps every thread project (and workspace root) marked untrusted in the
// private CODEX_HOME before the thread starts, pins the native defaults that
// the untrusted marker would otherwise change, removes every MCP definition and
// plugin source the private configuration could contribute, and verifies the
// effective MCP inventory twice: before the native thread call, so that a
// definition the private home cannot remove (system or managed policy) never
// reaches a Codex that launches servers while a thread starts, and again after
// the thread exists and before any input to catch drift.

// isolationStage names the closed stage of a rejected isolation step.
type isolationStage string

const (
	isolationStageProjectTrust isolationStage = "project-trust-isolation"
	isolationStageMCPInventory isolationStage = "mcp-inventory-verification"
	// isolationStageMCPPreflight is the inventory check that runs before the
	// native thread call.
	isolationStageMCPPreflight isolationStage = "mcp-inventory-preflight"
	projectTrustUntrusted                     = "untrusted"
	maxProjectIsolationConfig                 = 1 << 20
	maxProjectIsolationGitFile                = 4096
)

// isolationReason is the closed reason published with a rejected step.
type isolationReason string

const (
	isolationInventoryNonEmpty   isolationReason = "inventory-nonempty"
	isolationPluginsEnabled      isolationReason = "plugins-enabled"
	isolationInventoryUnreadable isolationReason = "inventory-unreadable"
	isolationTrustUnwritable     isolationReason = "trust-unwritable"
)

// isolationKind is the closed set of native thread profile kinds that
// decide whether project isolation applies.
type isolationKind string

const (
	// isolationAPI is an ordinary execution through the DeliDev API provider.
	isolationAPI isolationKind = "api"
	// isolationManaged is an ordinary execution authenticated by the ChatGPT
	// subscription, including members of a shared account owner.
	isolationManaged isolationKind = "managed"
	// isolationSidechat is the read-only Sidechat profile (API or managed).
	isolationSidechat isolationKind = "sidechat"
	// isolationTitle is an automatic-title profile (API or managed).
	isolationTitle isolationKind = "title"
	// isolationReview is the dedicated native review profile.
	isolationReview isolationKind = "review"
	// isolationOther is any profile without an authenticated execution
	// provider; it never binds an ordinary thread.
	isolationOther isolationKind = "other"
)

// isolationPolicy states which isolation steps run for one profile kind.
type isolationPolicy struct {
	// SeedTrust marks the project untrusted and pins the defaults it changes.
	SeedTrust bool
	// VerifyInventory requires an empty effective MCP inventory after binding.
	VerifyInventory bool
}

// policyFor is the single source of the isolation table. Sidechat, title and
// review profiles pin and verify an empty inventory through their own private
// configuration; they use a read-only sandbox, so Codex never records trust.
func policyFor(profile isolationKind) isolationPolicy {
	switch profile {
	case isolationAPI, isolationManaged:
		return isolationPolicy{SeedTrust: true, VerifyInventory: true}
	}
	return isolationPolicy{}
}

func (c *Client) isolationProfile() isolationKind {
	switch {
	case c.codeReviewModel != "":
		return isolationReview
	case c.sidechat != "":
		return isolationSidechat
	case c.managedTitle || c.api != nil && c.api.title:
		return isolationTitle
	case c.api != nil:
		return isolationAPI
	case c.managedHome != "":
		return isolationManaged
	}
	return isolationOther
}

// pinIsolatedDefaults keeps the effective approval and sandbox identical to a
// project whose trust Codex has never recorded: an untrusted marker would
// otherwise derive approval "untrusted" and a workspace-write sandbox from an
// absent selection. Explicit selections are never changed.
func pinIsolatedDefaults(p *threadParams) {
	if p.ApprovalPolicy == "" {
		p.ApprovalPolicy = ApprovalOnRequest
	}
	if p.Sandbox == "" && p.Permissions == "" {
		p.Sandbox = "read-only"
	}
}

func isolationFailure(reason isolationReason) *domain.Error {
	var e *domain.Error
	switch reason {
	case isolationTrustUnwritable:
		e = domain.Fail(domain.Unsupported, "Codex native project isolation could not be prepared.", "Check that the private native runtime is writable and its configuration is valid TOML; no thread or input was started.")
	case isolationPluginsEnabled:
		e = domain.Fail(domain.Unsupported, "Codex native plugins are enabled, and a plugin can contribute an MCP server that this execution did not select.", "Disable native plugins in the managed policy; no input was sent.")
	default:
		e = domain.Fail(domain.Unsupported, "Codex native configuration contains an MCP server that this execution did not select.", "Remove the MCP server from the native configuration or the managed policy; no input was sent.")
	}
	e.Cause = string(reason)
	return e
}

// rejectIsolation logs the closed stage and reason only, then reports the
// failure as an unsupported native profile. The shared diagnostic scheme
// reconstructs its presentation locally, so no native content can reach it.
func (c *Client) rejectIsolation(ctx context.Context, stage isolationStage, reason isolationReason) error {
	if c.logger != nil {
		c.logger.WarnContext(ctx, "codex_native_isolation_rejected", "owner_id", c.ownerID, "stage", stage, "reason", reason)
	}
	return domain.WithCodexDiagnostic(c.version, domain.CodexProfile, isolationFailure(reason))
}

// isolateProject marks every project key of the thread untrusted in the
// private home, pins the defaults that marker changes and then requires the
// effective inventory for the thread directory to be empty before the caller
// issues thread/start, thread/resume or thread/fork. A definition from a layer
// the private home cannot edit (system or managed policy) survives the rewrite
// and Codex may launch it while the thread is created, so only this probe can
// keep it from starting; the post-bind check cannot.
func (c *Client) isolateProject(ctx context.Context, settings ThreadSettings, params *threadParams) error {
	if !policyFor(c.isolationProfile()).SeedTrust {
		return nil
	}
	keys := projectTrustKeys(append([]string{settings.Cwd}, settings.WorkspaceRoots...))
	if err := isolateHomeConfig(c.home, keys); err != nil {
		return c.rejectIsolation(ctx, isolationStageProjectTrust, isolationTrustUnwritable)
	}
	pinIsolatedDefaults(params)
	return c.verifyMCPInventoryAt(ctx, settings.Cwd, isolationStageMCPPreflight)
}

// verifyMCPInventory is the post-bind check: the thread exists, no input has
// been sent, and the effective inventory must still be empty.
func (c *Client) verifyMCPInventory(ctx context.Context, cwd string) error {
	return c.verifyMCPInventoryAt(ctx, cwd, isolationStageMCPInventory)
}

// verifyMCPInventoryAt reads the effective configuration of the thread
// directory and requires an mcp_servers table without an enabled entry (absent,
// null, empty or only entries with enabled = false) and an explicit false
// plugin feature. The stage names the timing in the closed log.
func (c *Client) verifyMCPInventoryAt(ctx context.Context, cwd string, stage isolationStage) error {
	if !policyFor(c.isolationProfile()).VerifyInventory {
		return nil
	}
	response, err := c.call(ctx, domain.NewID(), "config/read", struct {
		Cwd           string `json:"cwd"`
		IncludeLayers bool   `json:"includeLayers"`
	}{Cwd: cwd})
	if err != nil {
		if ctx.Err() != nil {
			return domain.SafeError(ctx.Err())
		}
		return c.rejectIsolation(ctx, stage, isolationInventoryUnreadable)
	}
	var result struct {
		Config  map[string]json.RawMessage `json:"config"`
		Origins json.RawMessage            `json:"origins"`
		Layers  json.RawMessage            `json:"layers"`
	}
	if response.ErrorCode != nil || domain.Decode(response.Result, &result) != nil || result.Config == nil {
		return c.rejectIsolation(ctx, stage, isolationInventoryUnreadable)
	}
	// config/read keeps an entry whose enabled value is false in the effective
	// table although Codex never launches it, so only an enabled entry is an
	// inventory. The startup inventory classifier is the single source of that
	// rule; a malformed table or a non-boolean enabled value is unreadable.
	enabled, err := configuredMCPStartupServers(result.Config["mcp_servers"])
	if err != nil {
		return c.rejectIsolation(ctx, stage, isolationInventoryUnreadable)
	}
	if len(enabled) != 0 {
		return c.rejectIsolation(ctx, stage, isolationInventoryNonEmpty)
	}
	// A plugin contributes MCP servers through its own .mcp.json, which
	// config/read never lists. The private configuration disables the plugin
	// feature before the thread starts, so the effective value must still be
	// the explicit false; anything else lets a plugin server launch.
	var features map[string]json.RawMessage
	if raw := result.Config["features"]; len(raw) != 0 && string(raw) != "null" && json.Unmarshal(raw, &features) != nil {
		return c.rejectIsolation(ctx, stage, isolationInventoryUnreadable)
	}
	if string(features[pluginsFeature]) != "false" {
		return c.rejectIsolation(ctx, stage, isolationPluginsEnabled)
	}
	return nil
}

// projectKeysFoldCase is true where the platform compares paths without case.
var projectKeysFoldCase = runtime.GOOS == "windows"

// normalizeProjectKey is the comparison form of a project directory: cleaned
// and, on a case-insensitive platform, case-folded. Two spellings of one
// directory (a trailing separator, dot segments, a different case on Windows)
// have the same comparison form; Codex resolves trust through a normalized
// lookup, so every spelling that normalizes alike must carry the same trust.
func normalizeProjectKey(path string, foldCase bool) string {
	if path == "" {
		return ""
	}
	path = filepath.Clean(path)
	if foldCase {
		path = strings.ToLower(path)
	}
	return path
}

// projectTrustKeys returns the deduplicated keys Codex may use to record trust
// for the directories: each directory in its cleaned and symlink-resolved
// spelling, the nearest Git repository root and, for a linked worktree, the
// main repository root of each spelling. The caller's raw spelling is never
// written.
func projectTrustKeys(directories []string) []string {
	var keys []string
	add := func(path string) {
		if path != "" && !slices.Contains(keys, path) {
			keys = append(keys, path)
		}
	}
	for _, directory := range directories {
		if directory == "" {
			continue
		}
		cleaned := filepath.Clean(directory)
		spellings := []string{cleaned}
		if resolved, err := filepath.EvalSymlinks(cleaned); err == nil && resolved != cleaned {
			spellings = append(spellings, filepath.Clean(resolved))
		}
		for _, spelling := range spellings {
			add(spelling)
			root, main := gitRoots(spelling)
			add(root)
			add(main)
		}
	}
	return keys
}

// gitRoots finds the nearest ancestor with a .git entry. A .git file points a
// linked worktree to its common directory, whose parent is the main root.
func gitRoots(directory string) (root, main string) {
	for current := filepath.Clean(directory); ; {
		info, err := os.Lstat(filepath.Join(current, ".git"))
		if err == nil {
			root = current
			if info.Mode().IsRegular() && info.Size() <= maxProjectIsolationGitFile {
				main = linkedWorktreeMain(current)
			}
			return root, main
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", ""
		}
		current = parent
	}
}

func linkedWorktreeMain(root string) string {
	raw, err := os.ReadFile(filepath.Join(root, ".git"))
	if err != nil || len(raw) > maxProjectIsolationGitFile {
		return ""
	}
	target, found := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir:")
	if !found {
		return ""
	}
	target = strings.TrimSpace(target)
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	target = filepath.Clean(target)
	// <common>/worktrees/<name>: the main working tree owns <common>/..
	worktrees := filepath.Dir(target)
	if filepath.Base(worktrees) != "worktrees" {
		return ""
	}
	common := filepath.Dir(worktrees)
	if filepath.Base(common) != ".git" {
		return ""
	}
	return filepath.Dir(common)
}

// homeConfigLocks serializes in-process writers per private home. An entry
// exists only while a caller holds or waits for the lock, so a long-lived
// Worker that creates a fresh private CODEX_HOME per execution retains no
// state once isolation completes.
var homeConfigLocks = struct {
	sync.Mutex
	entries map[string]*homeConfigLock
}{entries: map[string]*homeConfigLock{}}

type homeConfigLock struct {
	sync.Mutex
	holders int
}

// lockHomeConfig acquires the lock of one home and returns its release.
func lockHomeConfig(home string) (release func()) {
	key := filepath.Clean(home)
	homeConfigLocks.Lock()
	entry := homeConfigLocks.entries[key]
	if entry == nil {
		entry = &homeConfigLock{}
		homeConfigLocks.entries[key] = entry
	}
	entry.holders++
	homeConfigLocks.Unlock()
	entry.Lock()
	return func() {
		entry.Unlock()
		homeConfigLocks.Lock()
		entry.holders--
		if entry.holders == 0 {
			delete(homeConfigLocks.entries, key)
		}
		homeConfigLocks.Unlock()
	}
}

// homeConfigLockCount reports the retained lock entries.
func homeConfigLockCount() int {
	homeConfigLocks.Lock()
	defer homeConfigLocks.Unlock()
	return len(homeConfigLocks.entries)
}

const pluginsFeature = "plugins"

// isolateHomeDocument applies the private-home isolation cells to a decoded
// config.toml and reports whether it changed:
//
//   - the top-level mcp_servers table is removed;
//   - every profile table (selected or not) loses its mcp_servers table, because
//     the selected profile merges over the top-level configuration;
//   - every profile loses a plugins feature override and features.plugins is
//     false, which disables plugin loading and with it every MCP server a plugin
//     contributes through its own .mcp.json;
//   - every equivalent spelling of a requested project key, and the requested
//     key itself, is untrusted.
//
// Any other shape of a table this function must edit is invalid and rejected.
func isolateHomeDocument(document map[string]any, keys []string) (bool, error) {
	changed := false
	if _, present := document["mcp_servers"]; present {
		delete(document, "mcp_servers")
		changed = true
	}
	if raw, present := document["profiles"]; present {
		profiles, ok := raw.(map[string]any)
		if !ok {
			return false, fmt.Errorf("invalid native profiles table")
		}
		for _, name := range sortedKeys(profiles) {
			profile, ok := profiles[name].(map[string]any)
			if !ok {
				return false, fmt.Errorf("invalid native profile entry")
			}
			if _, present := profile["mcp_servers"]; present {
				delete(profile, "mcp_servers")
				changed = true
			}
			if raw, present := profile["features"]; present {
				features, ok := raw.(map[string]any)
				if !ok {
					return false, fmt.Errorf("invalid native profile features table")
				}
				if _, present := features[pluginsFeature]; present {
					delete(features, pluginsFeature)
					changed = true
				}
			}
		}
	}
	features, _ := document["features"].(map[string]any)
	if document["features"] != nil && features == nil {
		return false, fmt.Errorf("invalid native features table")
	}
	if features == nil {
		features = map[string]any{}
	}
	if features[pluginsFeature] != false {
		features[pluginsFeature] = false
		document["features"] = features
		changed = true
	}
	projects, _ := document["projects"].(map[string]any)
	if document["projects"] != nil && projects == nil {
		return false, fmt.Errorf("invalid native projects table")
	}
	if projects == nil {
		projects = map[string]any{}
	}
	untrust := func(key string) error {
		entry, _ := projects[key].(map[string]any)
		if projects[key] != nil && entry == nil {
			return fmt.Errorf("invalid native project entry")
		}
		if entry == nil {
			entry = map[string]any{}
		}
		if entry["trust_level"] != projectTrustUntrusted {
			entry["trust_level"] = projectTrustUntrusted
			projects[key] = entry
			changed = true
		}
		return nil
	}
	requested := map[string]struct{}{}
	for _, key := range keys {
		requested[normalizeProjectKey(key, projectKeysFoldCase)] = struct{}{}
	}
	for _, existing := range sortedKeys(projects) {
		if _, equivalent := requested[normalizeProjectKey(existing, projectKeysFoldCase)]; equivalent {
			if err := untrust(existing); err != nil {
				return false, err
			}
		}
	}
	for _, key := range keys {
		if err := untrust(key); err != nil {
			return false, err
		}
	}
	if changed {
		document["projects"] = projects
	}
	return changed, nil
}

func sortedKeys(table map[string]any) []string {
	keys := make([]string, 0, len(table))
	for key := range table {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// isolateHomeConfig applies isolateHomeDocument to the private home's
// config.toml. The private home belongs to DeliDev, which never authors an MCP,
// profile or plugin selection there, so such an entry is foreign and would
// otherwise load for every project. The profile lease already serializes
// processes on the home; the in-process lock serializes shared-owner members.
// Other content is preserved semantically (comments are not), an unchanged file
// is not rewritten and a changed file is replaced atomically with owner-only
// permissions.
func isolateHomeConfig(home string, keys []string) error {
	if len(keys) == 0 || !filepath.IsAbs(home) {
		return fmt.Errorf("invalid project isolation request")
	}
	defer lockHomeConfig(home)()
	path := filepath.Join(home, "config.toml")
	document := map[string]any{}
	if _, err := os.Lstat(path); err == nil {
		raw, err := security.ReadPrivate(path, maxProjectIsolationConfig)
		if err != nil {
			return err
		}
		if err := toml.Unmarshal(raw, &document); err != nil {
			return fmt.Errorf("invalid native configuration")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	changed, err := isolateHomeDocument(document, keys)
	if err != nil || !changed {
		return err
	}
	var encoded bytes.Buffer
	if err := toml.NewEncoder(&encoded).Encode(document); err != nil {
		return err
	}
	return security.WriteAtomicOwned(path, encoded.Bytes())
}
