package opencode

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
)

// The original durable resume claim owns this one restoration operation,
// including exact SQLite staging and this native permission append. Neither a
// failed request nor a lost reply can authorize another send. The pinned PATCH
// appends rules, so only the independently unmaterialized suffix is sent.
func (s *sessionAPI) restoreCheckpointPermissions(ctx context.Context, source nativeCheckpoint) (returned error) {
	if err := s.enter(ctx); err != nil {
		return err
	}
	defer s.leave()
	p := source.Tools
	if s.problem != nil || s.creation == nil || s.creation.recorded || s.input != nil || s.events != nil || s.permissionRestore != nil || !s.apiVerified || s.apiProfile == nil || len(s.creation.settings.Permission) != 0 || p == nil || !validCheckpointTools(source) || p.AppliedAlways != s.restoredAlways || p.AppliedAlways >= uint32(len(p.Always)) || s.creation.identity.id != source.Reference.SessionID || !slices.Equal(s.sessionPermissions, checkpointAppliedPermissions(p, p.AppliedAlways)) {
		return sessionUncertain()
	}
	next := checkpointAppliedPermissions(p, uint32(len(p.Always)))
	if len(next) <= len(s.sessionPermissions) || !slices.Equal(next[:len(s.sessionPermissions)], s.sessionPermissions) {
		return sessionUncertain()
	}
	body, err := json.Marshal(struct {
		Permission []PermissionRule `json:"permission"`
	}{next[len(s.sessionPermissions):]})
	if err != nil || len(body) > maxHTTPBody {
		return sessionUncertain()
	}
	path := "/session/" + source.Reference.SessionID
	s.permissionRestore = &interactionHTTPAttempt{path: path, digest: mutationDigest(body)}
	phase := "send"
	defer func() {
		s.permissionRestore = nil
		if returned != nil {
			s.problem = sessionProblem()
			if s.logger != nil {
				s.logger.WarnContext(ctx, "opencode_remembered_permission_restore_failed", "owner_id", s.owner, "phase", phase)
			}
		}
	}()
	raw, _, err := s.request(ctx, http.MethodPatch, path, body, http.StatusOK)
	if err != nil {
		return err
	}
	phase = "response"
	s.sessionPermissions = next
	creation := s.sessionMetadataCreation()
	identity, err := validateSession(raw, s.cwd, &creation, false)
	if err != nil || identity != s.creation.identity {
		return sessionUncertain()
	}
	phase = "inspection"
	if _, err := s.readSession(ctx); err != nil {
		return err
	}
	s.restoredAlways = uint32(len(p.Always))
	if s.logger != nil {
		s.logger.InfoContext(ctx, "opencode_remembered_permissions_restored", "owner_id", s.owner, "original_request_id", source.Reference.InputRequestID, "approvals", s.restoredAlways, "rules", len(next))
	}
	return nil
}
