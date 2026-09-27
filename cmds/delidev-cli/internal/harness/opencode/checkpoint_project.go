package opencode

import (
	"context"
	"encoding/json"
	"net/http"
)

// Native Project.fromDirectory adopts global Git sessions after repository
// identity becomes available. Preserve the original checkpoint and record the
// exact lineage boundary; DeliDev never rewrites native database identities.
type checkpointProjectAdoption struct {
	Version       uint32 `json:"version"`
	From          string `json:"from"`
	To            string `json:"to"`
	AfterInputs   uint32 `json:"after_inputs"`
	SourceSHA256  string `json:"source_sha256"`
	ProjectSHA256 string `json:"project_sha256"`
}

func validCheckpointProjectAdoption(value nativeCheckpoint) bool {
	proof := value.ProjectAdoption
	if proof == nil {
		return true
	}
	return proof.Version == 1 && proof.From == "global" && proof.To != "global" && proof.To == value.Project && snapshotProjectComponent(proof.To) && value.Snapshot != nil && validCheckpointSnapshot(value) && value.NativeRoot == value.Workspace && proof.AfterInputs > 0 && int(proof.AfterInputs) <= len(value.Previous) && checkpointDigest(proof.SourceSHA256) && checkpointDigest(proof.ProjectSHA256) && (int(proof.AfterInputs) != len(value.Previous) || proof.SourceSHA256 == value.PredecessorSHA256)
}

func (s *sessionAPI) checkpointIdentity(value nativeCheckpoint) sessionIdentity {
	project := value.Project
	if proof := s.projectAdoption; proof != nil && proof.Version == 1 && value.Project == "global" && proof.From == value.Project && proof.To != "global" && snapshotProjectComponent(proof.To) && checkpointDigest(proof.SourceSHA256) && checkpointDigest(proof.ProjectSHA256) && proof.SourceSHA256 == s.projectSourceDigest && int(proof.AfterInputs) == len(value.Previous)+1 {
		project = proof.To
	}
	return sessionIdentity{value.Reference.SessionID, project, value.Slug, value.Created}
}

func validAdoptedProject(raw []byte, project, root string) bool {
	fields, err := shape(raw, []string{"id", "worktree", "vcs", "time", "sandboxes"}, nil)
	if err != nil || !scalar(fields["id"], project) || !scalar(fields["worktree"], root) || !scalar(fields["vcs"], "git") {
		return false
	}
	var sandboxes []string
	if json.Unmarshal(fields["sandboxes"], &sandboxes) != nil || sandboxes == nil || len(sandboxes) != 0 {
		return false
	}
	times, err := shape(fields["time"], []string{"created", "updated"}, []string{"initialized"})
	if err != nil {
		return false
	}
	created, ok := nativeCount(times["created"])
	updated, valid := nativeCount(times["updated"])
	if !ok || !valid || created == 0 || updated < created {
		return false
	}
	if raw, found := times["initialized"]; found {
		if _, ok := nativeCount(raw); !ok {
			return false
		}
	}
	return true
}

func (s *sessionAPI) adoptCheckpointProject(ctx context.Context, source nativeCheckpoint) (returned error) {
	if source.Project != "global" || source.Snapshot == nil {
		return nil
	}
	if err := s.enter(ctx); err != nil {
		return err
	}
	defer s.leave()
	if !s.apiVerified || s.apiProfile == nil || !checkpointDigest(s.projectSourceDigest) || !s.projectProfile || !validCheckpointSnapshot(source) || source.ProjectAdoption != nil || s.projectAdoption != nil || s.creation == nil || s.input != nil || s.events != nil || s.observer != nil || s.problem != nil || s.creation.identity != (sessionIdentity{source.Reference.SessionID, source.Project, source.Slug, source.Created}) {
		return sessionUncertain()
	}
	s.projectRead = true
	defer func() {
		s.projectRead = false
		if returned != nil {
			s.problem = sessionProblem()
			if s.logger != nil {
				s.logger.WarnContext(ctx, "opencode_project_adoption_failed", "owner_id", s.owner, "code", "recovery_required")
			}
		}
	}()
	raw, _, err := s.request(ctx, http.MethodGet, "/session?limit=2", nil, http.StatusOK)
	var sessions []json.RawMessage
	if err != nil || json.Unmarshal(raw, &sessions) != nil || len(sessions) != 1 {
		return sessionUncertain()
	}
	creation := s.sessionMetadataCreation()
	identity, err := validateSession(sessions[0], s.cwd, &creation, false)
	if err != nil {
		return sessionUncertain()
	}
	if identity == s.creation.identity {
		return nil
	}
	old := s.creation.identity
	if identity.id != old.id || identity.slug != old.slug || identity.created != old.created || identity.project == "global" || !snapshotProjectComponent(identity.project) {
		return sessionUncertain()
	}
	project, _, err := s.request(ctx, http.MethodGet, "/project/current", nil, http.StatusOK)
	if err != nil || !validAdoptedProject(project, identity.project, source.NativeRoot) {
		return sessionUncertain()
	}
	if err := stageCheckpointSnapshotAs(ctx, source, s.runtimeHome, identity.project); err != nil {
		return err
	}
	// The full original history is still compared by OpenResumedAPI before any
	// input. This temporary identity supplies only that same-session comparison.
	s.projectAdoption = &checkpointProjectAdoption{Version: 1, From: source.Project, To: identity.project, AfterInputs: uint32(len(source.Previous) + 1), SourceSHA256: s.projectSourceDigest, ProjectSHA256: mutationDigest(project)}
	s.creation.identity = identity
	if s.logger != nil {
		s.logger.InfoContext(ctx, "opencode_project_adopted", "owner_id", s.owner, "prior_inputs", len(source.Previous)+1)
	}
	return nil
}

func (s *sessionAPI) recheckCheckpointProject(ctx context.Context) error {
	if s.projectAdoption == nil {
		return nil
	}
	if err := s.enter(ctx); err != nil {
		return err
	}
	defer s.leave()
	s.projectRead = true
	defer func() { s.projectRead = false }()
	raw, _, err := s.request(ctx, http.MethodGet, "/project/current", nil, http.StatusOK)
	if err != nil || !validAdoptedProject(raw, s.projectAdoption.To, s.runtimeRoot) || mutationDigest(raw) != s.projectAdoption.ProjectSHA256 {
		s.problem = sessionProblem()
		return sessionUncertain()
	}
	return nil
}
