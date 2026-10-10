// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
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
