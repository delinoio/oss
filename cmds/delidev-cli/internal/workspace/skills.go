// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/skills"
	"os"
)

// Skill reads share original workspace observation locks and identity checks.
func (m *Manager) ReadSkills(ctx context.Context, request ReadRequest) (domain.SkillReadResult, error) {
	result := domain.SkillReadResult{Entries: []domain.SkillEntry{}}
	if request.Skills == nil || request.Skills.MachineID != request.Preparation.MachineID || request.Skills.ActorID.Validate() != nil {
		return result, ResultUncertain()
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return result, domain.SafeError(e)
	}
	packages := skills.Manager{Root: m.Root, Home: home}
	if len(request.Skills.Selections) > 0 {
		return result, packages.Prepare(ctx, *request.Skills)
	}
	if request.Skills.SessionID == "" || request.Manifest.Version == 0 {
		return packages.List(ctx, *request.Skills, nil)
	}
	roots := []string{}
	// Collect only independently verified accepted roots. No preparation occurs.
	if request.Preparation.Type == domain.GeneralChat {
		e = m.observeWorkspace(ctx, request, "", true, func(_ context.Context, _ Git, _ Manifest, _ *os.Root, path string) error {
			roots = append(roots, path)
			return nil
		})
	} else {
		for _, repository := range request.Manifest.Repositories {
			e = m.observeWorkspace(ctx, request, repository.ID, true, func(_ context.Context, _ Git, _ Manifest, _ *os.Root, path string) error {
				roots = append(roots, path)
				return nil
			})
			if e != nil {
				break
			}
		}
	}
	if e != nil {
		return result, e
	}
	return packages.List(ctx, *request.Skills, roots)
}
