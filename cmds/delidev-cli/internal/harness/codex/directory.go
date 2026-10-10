// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// DirectoryIntent is private durable mutation ownership. The coordinator must
// independently verify the original terminal history, closed workspace/native
// processes, account/Worker authority and empty pending-work inventories first.
// It cannot be used as a fresh-input claim or reconstructed from native settings.
type DirectoryIntent struct {
	RequestID      domain.ID
	Source         ContinuationCheckpoint
	Destination    string
	WorkspaceRoots []string
}

func directoryUncertain() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "The native working-directory change requires reconciliation.", "Retain the original directory request and history; never infer a new input or repeat an uncertain change.")
}

func directoryRoots(source EffectiveSettings) []string {
	roots := source.WorkspaceRoots
	if len(roots) == 0 {
		roots = []string{source.Cwd}
	}
	return slices.Clone(roots)
}
func directoryInsideOriginalRoots(cwd string, roots []string) bool {
	if !filepath.IsAbs(cwd) || filepath.Clean(cwd) != cwd {
		return false
	}
	for _, root := range roots {
		relative, err := filepath.Rel(root, cwd)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			continue
		}
		current := root
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
		if relative != "." {
			for _, part := range strings.Split(relative, string(filepath.Separator)) {
				current = filepath.Join(current, part)
				info, err = os.Lstat(current)
				if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
					return false
				}
			}
		}
		return true
	}
	return false
}

// Directory selection changes location only; explicit settings cannot acquire a
// different model, service tier, approval reviewer or sandbox during the change.
func directorySettingsMatch(source EffectiveSettings, settings ThreadSettings) bool {
	if settings.Model != source.Model || settings.Provider != source.Provider ||
		string(source.ApprovalPolicy) != settings.Options.ApprovalPolicy ||
		source.ApprovalsReviewer != string(settings.Options.ApprovalsReviewer.Effective()) {
		return false
	}
	if source.Effort == nil && settings.Effort != "" || source.Effort != nil && *source.Effort != settings.Effort ||
		source.ServiceTier == nil && settings.Options.ServiceTier != "" || source.ServiceTier != nil && *source.ServiceTier != settings.Options.ServiceTier {
		return false
	}
	switch source.Sandbox.Type {
	case ReadOnly:
		return settings.Options.Permission == domain.PermissionReadOnly
	case WorkspaceWrite:
		return settings.Options.Permission == domain.PermissionWorkspaceWrite
	case FullAccess:
		return settings.Options.Permission == domain.PermissionFullAccess
	default:
		return false
	}
}

// ResumeDirectory uses a fresh owned connection to the exact retained thread.
// Cwd-only thread/settings/update replaces runtime roots in the pinned native
// implementation; cold Resume can instead explicitly preserve every original
// root while reloading destination config/trust/instructions. Keep this profile
// until a verified standalone native update preserves those same boundaries.
func (c *Client) ResumeDirectory(ctx context.Context, requestID domain.ID, source ContinuationCheckpoint, settings ThreadSettings, claim func(DirectoryIntent) error) (ThreadResult, error) {
	if c.version != "0.162.0" || source.validate(ResumeAfterTerminal) != nil || source.ThreadID == "" || claim == nil || settings.Cwd == source.Effective.Cwd || !directorySettingsMatch(source.Effective, settings) {
		return ThreadResult{}, directoryUncertain()
	}
	roots := directoryRoots(source.Effective)
	if !slices.Equal(settings.WorkspaceRoots, roots) || !directoryInsideOriginalRoots(settings.Cwd, roots) {
		return ThreadResult{}, directoryUncertain()
	}
	settings.directorySource = &source
	settings.directoryClaim = func() error {
		return claim(DirectoryIntent{RequestID: requestID, Source: source, Destination: settings.Cwd, WorkspaceRoots: slices.Clone(roots)})
	}
	return c.bindThread(ctx, requestID, source.ThreadID, settings, resumeThread)
}

// ResumeDirectoryContinuation binds an already retained directory generation.
// The owning fresh execution must first verify its private generation and exact
// accepted source checkpoint. This retains the selected cwd, creates no directory
// intent and grants no input until ordinary continuation history verification.
func (c *Client) ResumeDirectoryContinuation(ctx context.Context, requestID domain.ID, source ContinuationCheckpoint, settings ThreadSettings) (ThreadResult, error) {
	if c.version != "0.162.0" || source.validate(ResumeAfterTerminal) != nil || source.Effective.Cwd != settings.Cwd || !directorySettingsMatch(source.Effective, settings) || !slices.Equal(settings.WorkspaceRoots, directoryRoots(source.Effective)) || !directoryInsideOriginalRoots(settings.Cwd, settings.WorkspaceRoots) {
		return ThreadResult{}, directoryUncertain()
	}
	settings.directorySource = &source
	settings.directoryClaim = func() error { return nil }
	return c.bindThread(ctx, requestID, source.ThreadID, settings, resumeThread)
}

// VerifyDirectoryContinuation compares new settings except for the explicitly
// selected cwd, then verifies the original terminal turn and ordered inputs.
// Only the local comparison copy changes; historical checkpoints stay immutable.
func (c *Client) VerifyDirectoryContinuation(ctx context.Context, requestID domain.ID, source ContinuationCheckpoint, selected EffectiveSettings, intent ContinuationIntent) (Turn, error) {
	if !DirectorySettingsEqual(source.Effective, selected) {
		return Turn{}, directoryUncertain()
	}
	comparison := source
	comparison.Effective = selected
	return c.VerifyContinuation(ctx, requestID, comparison, intent)
}

// RequireDirectoryQuiescence reads the exact bound thread's original native
// inventory. Empty data with a nonempty cursor is not proof of an empty complete
// inventory. This never terminates a process or grants directory/input authority.
func (c *Client) RequireDirectoryQuiescence(ctx context.Context, requestID domain.ID) error {
	if requestID.Validate() != nil || c.mode != ThreadProtocol || c.thread == "" || c.execution == nil || c.problem != nil {
		return directoryUncertain()
	}
	if err := c.acquireControl(ctx); err != nil {
		return err
	}
	defer func() { <-c.control }()
	response, err := c.wire.Call(ctx, requestID, "thread/backgroundTerminals/list", struct {
		ThreadID domain.ID `json:"threadId"`
		Limit    uint32    `json:"limit"`
	}{c.thread, 1})
	if err != nil || response.ErrorCode != nil {
		return directoryUncertain()
	}
	return emptyDirectoryBackgroundInventory(response.Result)
}

func emptyDirectoryBackgroundInventory(raw json.RawMessage) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return directoryUncertain()
	}
	data, present := fields["data"]
	cursor, cursorPresent := fields["nextCursor"]
	var rows []json.RawMessage
	if !present || !cursorPresent || string(data) == "null" || json.Unmarshal(data, &rows) != nil || rows == nil || len(rows) != 0 || string(cursor) != "null" {
		return directoryUncertain()
	}
	return nil
}

// DirectoryReloadEvidence contains only digests and private instruction source
// paths. Raw native configuration can include secrets and must never be retained
// or logged. This snapshot binds what a cold owned Resume actually loaded.
type DirectoryReloadEvidence struct {
	ConfigDigest string                         `json:"config_digest"`
	Instructions []DirectoryInstructionEvidence `json:"instructions"`
}
type DirectoryInstructionEvidence struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

func directoryDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func directoryInstructionEvidence(paths []string) ([]DirectoryInstructionEvidence, error) {
	return directoryInstructionEvidenceBefore(paths, time.Time{})
}

func directoryInstructionEvidenceBefore(paths []string, resumeStarted time.Time) ([]DirectoryInstructionEvidence, error) {
	if paths == nil || len(paths) > 100 {
		return nil, directoryUncertain()
	}
	proof := make([]DirectoryInstructionEvidence, 0, len(paths))
	for i, path := range paths {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || !filepath.IsAbs(path) || resolved != path || slices.Contains(paths[:i], path) {
			return nil, directoryUncertain()
		}
		before, err := os.Lstat(path)
		if err != nil || !before.Mode().IsRegular() || before.Size() > 256<<10 || !resumeStarted.IsZero() && before.ModTime().After(resumeStarted) {
			return nil, directoryUncertain()
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, directoryUncertain()
		}
		opened, statErr := f.Stat()
		raw, readErr := io.ReadAll(io.LimitReader(f, (256<<10)+1))
		closeErr := f.Close()
		after, afterErr := os.Lstat(path)
		if statErr != nil || readErr != nil || closeErr != nil || afterErr != nil || len(raw) > 256<<10 || !os.SameFile(before, opened) || !os.SameFile(opened, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
			clear(raw)
			return nil, directoryUncertain()
		}
		proof = append(proof, DirectoryInstructionEvidence{Path: path, Digest: directoryDigest(raw)})
		clear(raw)
	}
	return proof, nil
}

func (c *Client) ReadDirectoryReloadEvidence(ctx context.Context, requestID domain.ID, bound ThreadResult) (DirectoryReloadEvidence, error) {
	var empty DirectoryReloadEvidence
	if requestID.Validate() != nil || c.thread == "" || c.problem != nil || bound.Thread == nil || bound.Thread.ID != c.thread || bound.Effective == nil || bound.DirectorySources == nil || bound.DirectoryStartedAt.IsZero() {
		return empty, directoryUncertain()
	}
	if err := c.acquireControl(ctx); err != nil {
		return empty, err
	}
	defer func() { <-c.control }()
	response, err := c.wire.Call(ctx, requestID, "config/read", struct {
		Cwd           string `json:"cwd"`
		IncludeLayers bool   `json:"includeLayers"`
	}{bound.Effective.Cwd, true})
	if err != nil || response.ErrorCode != nil {
		return empty, directoryUncertain()
	}
	var config struct {
		Config  map[string]json.RawMessage `json:"config"`
		Origins map[string]json.RawMessage `json:"origins"`
		Layers  []json.RawMessage          `json:"layers"`
	}
	if domain.Decode(response.Result, &config) != nil || config.Config == nil || config.Origins == nil || config.Layers == nil {
		return empty, directoryUncertain()
	}
	if err := directoryConfigSourcesBefore(config.Layers, bound.DirectoryStartedAt); err != nil {
		return empty, err
	}
	canonical, err := json.Marshal(config)
	if err != nil {
		return empty, directoryUncertain()
	}
	digest := directoryDigest(canonical)
	clear(canonical)
	instructions, err := directoryInstructionEvidenceBefore(bound.DirectorySources, bound.DirectoryStartedAt)
	if err != nil {
		return empty, err
	}
	return DirectoryReloadEvidence{ConfigDigest: digest, Instructions: instructions}, nil
}

// DirectorySettingsEqual compares the same filesystem authority when native
// legacy workspace-write represents the original cwd as an implicit write root.
// Moving inside that original root must not turn an omitted legacy root into
// missing authority or permit an unrelated root. All other observed settings
// remain exact; historical representations are never rewritten.
func DirectorySettingsEqual(source, selected EffectiveSettings) bool {
	roots := directoryRoots(source)
	if !slices.Equal(directoryRoots(selected), roots) || !directoryInsideOriginalRoots(selected.Cwd, roots) {
		return false
	}
	before := source
	after := selected
	if source.Sandbox.Type == WorkspaceWrite && selected.Sandbox.Type == WorkspaceWrite {
		if !directoryWriteRootsEqual(source, selected, roots) {
			return false
		}
		before.Sandbox.WritableRoots = slices.Clone(roots)
		after.Sandbox.WritableRoots = slices.Clone(roots)
	}
	before.Cwd = selected.Cwd
	before.WorkspaceRoots = slices.Clone(roots)
	after.WorkspaceRoots = slices.Clone(roots)
	return sameEffectiveSettings(before, after)
}
func directoryWriteRootsEqual(source, selected EffectiveSettings, roots []string) bool {
	covers := func(policy EffectiveSettings, root string) bool {
		for _, writable := range append(slices.Clone(policy.Sandbox.WritableRoots), policy.Cwd) {
			if nativePathEqual(writable, root) {
				return true
			}
		}
		return false
	}
	for _, root := range roots {
		if !covers(source, root) || !covers(selected, root) {
			return false
		}
	}
	for _, policy := range []EffectiveSettings{source, selected} {
		for _, writable := range policy.Sandbox.WritableRoots {
			if !slices.ContainsFunc(roots, func(root string) bool { return nativePathEqual(root, writable) }) && !nativePathEqual(writable, policy.Cwd) {
				return false
			}
		}
	}
	return true
}

// ProjectDirectoryCompaction changes only a private comparison copy of a proven
// context action. Its immutable history/rollout/rollback evidence stays exact.
func ProjectDirectoryCompaction(p CompactedCheckpoint, selected EffectiveSettings) (CompactedCheckpoint, error) {
	if !DirectorySettingsEqual(p.Source.Effective, selected) {
		return CompactedCheckpoint{}, directoryUncertain()
	}
	copy := cloneCompactedCheckpoint(&p)
	copy.Source.Effective = selected
	return *copy, nil
}
func (c *Client) VerifyDirectoryCompactedContinuation(ctx context.Context, request domain.ID, p CompactedCheckpoint, selected EffectiveSettings) (Turn, error) {
	comparison, err := ProjectDirectoryCompaction(p, selected)
	if err != nil {
		return Turn{}, err
	}
	return c.VerifyCompactedContinuation(ctx, request, comparison)
}

// File-layer metadata is supplied by the pinned native configuration schema.
// A post-Resume write cannot establish what the native thread actually loaded.
func directoryConfigSourcesBefore(layers []json.RawMessage, started time.Time) error {
	if layers == nil || started.IsZero() {
		return directoryUncertain()
	}
	for _, raw := range layers {
		var layer struct {
			Name struct {
				Type    string  `json:"type"`
				File    string  `json:"file,omitempty"`
				Folder  string  `json:"dotCodexFolder,omitempty"`
				Domain  string  `json:"domain,omitempty"`
				Key     string  `json:"key,omitempty"`
				ID      string  `json:"id,omitempty"`
				Label   string  `json:"name,omitempty"`
				Profile *string `json:"profile,omitempty"`
			} `json:"name"`
			Version  string          `json:"version"`
			Config   json.RawMessage `json:"config"`
			Disabled *string         `json:"disabledReason,omitempty"`
		}
		if domain.Decode(raw, &layer) != nil || layer.Version == "" || len(layer.Config) == 0 {
			return directoryUncertain()
		}
		path := ""
		switch layer.Name.Type {
		case "packagedDefaults", "system", "user", "legacyManagedConfigTomlFromFile":
			path = layer.Name.File
		case "project":
			path = filepath.Join(layer.Name.Folder, "config.toml")
		case "mdm", "enterpriseManaged", "sessionFlags", "legacyManagedConfigTomlFromMdm":
			continue
		default:
			return directoryUncertain()
		}
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return directoryUncertain()
		}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			var absent map[string]json.RawMessage
			if domain.Decode(layer.Config, &absent) != nil || absent == nil || len(absent) != 0 {
				return directoryUncertain()
			}
			continue
		}
		resolved, resolveErr := filepath.EvalSymlinks(path)
		if err != nil || resolveErr != nil || resolved != path || !info.Mode().IsRegular() || info.ModTime().After(started) {
			return directoryUncertain()
		}
	}
	return nil
}
