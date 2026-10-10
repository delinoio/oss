// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

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
	if source.validate(ResumeAfterTerminal) != nil || source.ThreadID == "" || claim == nil || settings.Cwd == source.Effective.Cwd || len(source.Effective.WorkspaceRoots) == 0 || !directorySettingsMatch(source.Effective, settings) {
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

// VerifyDirectoryContinuation compares new settings except for the explicitly
// selected cwd, then verifies the original terminal turn and ordered inputs.
// Only the local comparison copy changes; historical checkpoints stay immutable.
func (c *Client) VerifyDirectoryContinuation(ctx context.Context, requestID domain.ID, source ContinuationCheckpoint, selected EffectiveSettings, intent ContinuationIntent) (Turn, error) {
	prior := source.Effective
	prior.Cwd = selected.Cwd
	prior.WorkspaceRoots = directoryRoots(source.Effective)
	if !sameEffectiveSettings(prior, selected) || !directoryInsideOriginalRoots(selected.Cwd, prior.WorkspaceRoots) {
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
		if err != nil || !before.Mode().IsRegular() || before.Size() > 256<<10 {
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
	if requestID.Validate() != nil || c.thread == "" || c.problem != nil || bound.Thread == nil || bound.Thread.ID != c.thread || bound.Effective == nil || bound.DirectorySources == nil {
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
	canonical, err := json.Marshal(config)
	if err != nil {
		return empty, directoryUncertain()
	}
	digest := directoryDigest(canonical)
	clear(canonical)
	instructions, err := directoryInstructionEvidence(bound.DirectorySources)
	if err != nil {
		return empty, err
	}
	return DirectoryReloadEvidence{ConfigDigest: digest, Instructions: instructions}, nil
}
