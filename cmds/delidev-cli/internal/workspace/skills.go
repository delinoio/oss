// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/skills"
	"os"
	"time"
)

// Skill reads share original workspace observation locks and identity checks.
func (m *Manager) ReadSkills(ctx context.Context, request ReadRequest) (domain.SkillReadResult, error) {
	result := domain.SkillReadResult{Entries: []domain.SkillEntry{}}
	if request.Skills == nil || request.Skills.MachineID != request.Preparation.MachineID || request.Skills.ActorID.Validate() != nil {
		return result, ResultUncertain()
	}
	if request.Deadline.IsZero() || request.Deadline.After(time.Now().Add(31*time.Second)) {
		return result, ResultUncertain()
	}
	ctx, cancel := context.WithDeadline(ctx, request.Deadline)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return result, domain.SafeError(err)
	}
	if request.Skills.Action == domain.CleanupSkillPreparation {
		return result, (skills.Manager{Root: m.Root}).CleanupPreparation(ctx, *request.Skills)
	}
	if request.Skills.Action != "" || len(request.Skills.Selections) > 0 && domain.ValidateSkillPreparation(*request.Skills) != nil {
		return result, ResultUncertain()
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return result, domain.SafeError(e)
	}
	packages := skills.Manager{Root: m.Root, Home: home}
	if len(request.Skills.Selections) > 0 {
		if e := packages.Prepare(ctx, *request.Skills); e != nil {
			return result, e
		}
		entries, e := packages.PreparedEntries(ctx, *request.Skills)
		result.Entries = entries
		return result, e
	}
	if request.Skills.SessionID == "" || request.Manifest.Version == 0 {
		return packages.List(ctx, *request.Skills, nil)
	}
	roots, e := skillWorkspaceRoots(request, func(observation ReadRequest, repository domain.ID) (string, error) {
		var path string
		err := m.observeWorkspace(ctx, observation, repository, true, func(_ context.Context, _ Git, _ Manifest, _ *os.Root, root string) error {
			path = root
			return nil
		})
		return path, err
	})
	if e != nil {
		return result, e
	}
	return packages.List(ctx, *request.Skills, roots)
}

// One observation budget covers all accepted roots. Package enumeration keeps
// the caller's original context; a later repository cannot renew this budget.
func skillWorkspaceRoots(request ReadRequest, observe func(ReadRequest, domain.ID) (string, error)) ([]string, error) {
	observation := request
	if deadline := time.Now().Add(15 * time.Second); deadline.Before(observation.Deadline) {
		observation.Deadline = deadline
	}
	ids := []domain.ID{""}
	if request.Preparation.Type != domain.GeneralChat {
		ids = make([]domain.ID, len(request.Manifest.Repositories))
		for i, repository := range request.Manifest.Repositories {
			ids[i] = repository.ID
		}
	}
	roots := make([]string, 0, len(ids))
	for _, id := range ids {
		root, err := observe(observation, id)
		if err != nil {
			return nil, err
		}
		roots = append(roots, root)
	}
	return roots, nil
}
