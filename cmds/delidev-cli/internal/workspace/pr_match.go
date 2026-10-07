package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type PRWorkspaceMatchState string

const (
	PRWorkspaceMatches   PRWorkspaceMatchState = "matches"
	PRWorkspaceDifferent PRWorkspaceMatchState = "different"
)

// PRWorkspaceMatch belongs only to the private Worker read profile. It carries
// no patch, path, remote URL or credential, and grants no execution authority.
type PRWorkspaceMatch struct {
	Version         uint32                `json:"version"`
	ReadID          domain.ID             `json:"read_id"`
	SessionID       domain.ID             `json:"session_id"`
	RepositoryID    domain.ID             `json:"repository_id"`
	State           PRWorkspaceMatchState `json:"state"`
	SelectionDigest string                `json:"selection_digest"`
	ObservedAt      time.Time             `json:"observed_at"`
}

func validPRCandidateRequest(request ReadRequest) bool {
	if request.ID.Validate() != nil || request.Deadline.IsZero() || request.Preparation.SessionID.Validate() != nil || request.Preparation.MachineID.Validate() != nil ||
		domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(request.Manifest.SessionID), request.Manifest.SessionID != request.Preparation.SessionID) ||
		domain.OwnershipBlocks(domain.OwnershipMachine, domain.ID(request.Manifest.MachineID), request.Manifest.MachineID != request.Preparation.MachineID) ||
		request.Manifest.Type != request.Preparation.Type || request.PRCandidate == nil || request.PRCandidate.Validate() != nil || request.Query != (domain.WorkspaceReadQuery{}) || (request.Preparation.Type != domain.Worktree && request.Preparation.Type != domain.Local) {
		return false
	}
	count := 0
	for _, repo := range request.Preparation.Repositories {
		if repo.ID == request.PRCandidate.Target.RepositoryID {
			count++
		}
	}
	return count == 1
}

func prMatchDigest(request ReadRequest) string {
	raw, _ := json.Marshal(request)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func ValidatePRWorkspaceMatch(request ReadRequest, result PRWorkspaceMatch) error {
	if !validPRCandidateRequest(request) || result.Version != 1 || result.ReadID != request.ID ||
		domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(result.SessionID), result.SessionID != request.Preparation.SessionID) ||
		result.RepositoryID != request.PRCandidate.Target.RepositoryID ||
		(result.State != PRWorkspaceMatches && result.State != PRWorkspaceDifferent) || result.SelectionDigest != prMatchDigest(request) || result.ObservedAt.IsZero() ||
		result.ObservedAt.Before(request.Deadline.Add(-16*time.Second)) || result.ObservedAt.After(request.Deadline) || result.ObservedAt.After(time.Now().UTC().Add(time.Second)) {
		return ResultUncertain()
	}
	return nil
}

// MatchPRWorkspace checks an existing prepared workspace using only the owning
// Worker's native Git transport. Matching is a current read, not permission to
// reuse a native history, clear uncertainty, prepare files or commit/push.
func (m *Manager) MatchPRWorkspace(ctx context.Context, request ReadRequest) (PRWorkspaceMatch, error) {
	var result PRWorkspaceMatch
	if !validPRCandidateRequest(request) {
		return result, readUnsupported()
	}
	state := PRWorkspaceMatches
	err := m.observeWorkspace(ctx, request, request.PRCandidate.Target.RepositoryID, true, func(ctx context.Context, git Git, _ Manifest, _ *os.Root, selected string) error {
		var spec RepositorySpec
		for _, repo := range request.Preparation.Repositories {
			if repo.ID == request.PRCandidate.Target.RepositoryID {
				spec = repo
			}
		}
		spec.PRTarget = request.PRCandidate
		if err := git.preflightPRWorkspace(ctx, selected, spec, prHeadOrDetached); err != nil {
			if domain.SafeError(err).Code != domain.Conflict {
				return err
			}
			state = PRWorkspaceDifferent
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	result = PRWorkspaceMatch{Version: 1, ReadID: request.ID, SessionID: request.Preparation.SessionID, RepositoryID: request.PRCandidate.Target.RepositoryID, State: state, SelectionDigest: prMatchDigest(request), ObservedAt: time.Now().UTC()}
	if err := ValidatePRWorkspaceMatch(request, result); err != nil {
		return PRWorkspaceMatch{}, err
	}
	m.Logger.InfoContext(ctx, "workspace_pr_match_observed", "read_id", request.ID, "session_id", result.SessionID, "repository_id", result.RepositoryID, "state", result.State)
	return result, nil
}
