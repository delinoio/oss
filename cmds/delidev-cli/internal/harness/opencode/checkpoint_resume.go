package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type checkpointResume struct {
	source  nativeCheckpoint
	raw     []byte
	ref     CheckpointReference
	request domain.ID
}

// OpenResumedAPI is a private native profile. The caller must independently
// hold the continuation workspace lease, prove original report acceptance and
// register fresh account authority. Claim must durably consume this exact
// predecessor before staging or launch. It never creates another session or
// replays input. The initial replacement profile is same-mode, non-VCS text;
// auxiliary/tool/project history needs separate native replacement evidence.
func OpenResumedAPI(ctx context.Context, config APIExecutionConfig, home string, raw []byte, ref CheckpointReference, request domain.ID, explicitResume bool) (result *OwnedAPI, returned error) {
	source, err := decodeCheckpoint(raw, ref, home)
	if err != nil || ctx.Err() != nil || request.Validate() != nil || request == ref.CreationRequestID || request == ref.InputRequestID || request == ref.OwnerID || config.Probe.Process.OwnerID == ref.OwnerID || source.Reference.RequiresResume && !explicitResume || config.Workspace != source.Workspace || config.NativeRoot != source.NativeRoot || source.Project != "global" || filepath.Dir(source.NativeRoot) != source.NativeRoot {
		return nil, sessionUncertain()
	}
	if checkpointReplacementProfile(source) != nil || request == config.Probe.Process.OwnerID || config.Probe.Process.OwnerID == ref.CreationRequestID || config.Probe.Process.OwnerID == ref.InputRequestID {
		return nil, incompatible()
	}
	for _, history := range checkpointHistories(source) {
		if request == history.RequestID || config.Probe.Process.OwnerID == history.RequestID {
			return nil, sessionUncertain()
		}
	}
	resume := &checkpointResume{source: source, raw: bytes.Clone(raw), ref: ref, request: request}
	if err := InspectCheckpoint(ctx, home, resume.raw, ref); err != nil {
		return nil, err
	}
	session, err := openAPISessionRestoring(ctx, config, resume)
	if err != nil {
		return nil, err
	}
	api := &OwnedAPI{session: session, reading: make(chan struct{}, 1)}
	ready := false
	defer func() {
		if !ready {
			cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := api.Close(cleanup); err != nil {
				returned = sessionUncertain()
				if session.logger != nil {
					session.logger.WarnContext(cleanup, "opencode_checkpoint_replacement_cleanup_failed", "owner_id", session.owner, "code", domain.SafeError(returned).Code)
				}
			}
		}
	}()
	// Retain the original creation marker and identity without inventing a new
	// creation claim or HTTP acknowledgment on this replacement process.
	session.creation = &sessionCreation{request: ref.CreationRequestID, settings: session.apiProfile.Settings, identity: sessionIdentity{ref.SessionID, source.Project, source.Slug, source.Created}}
	if err := session.inspectCheckpointHistory(ctx, source); err != nil {
		return nil, err
	}
	session.creation.recorded = true
	session.predecessor, session.predecessorDigest, session.resumeRequest = &resume.source, ref.SHA256, request
	ready = true
	return api, nil
}

func (s *sessionAPI) freshCheckpointInput(request domain.ID, message, part string) bool {
	if s.predecessor == nil {
		return true
	}
	if request == s.resumeRequest || request == s.owner {
		return false
	}
	for _, prior := range checkpointHistories(*s.predecessor) {
		if request == prior.RequestID {
			return false
		}
		for _, old := range prior.Messages {
			if old.ID == message {
				return false
			}
			for _, previous := range old.Parts {
				if previous.ID == part {
					return false
				}
			}
		}
	}
	return true
}

func (r *checkpointResume) stage(ctx context.Context, config apiSessionConfig, profile *nativeAPIProfile) error {
	home := filepath.Dir(config.Probe.Home)
	if r == nil || profile == nil || home == r.source.RuntimeHome || directoryContains(home, r.source.RuntimeHome) || directoryContains(r.source.RuntimeHome, home) || mutationDigest([]byte(profile.Token)) == r.source.CredentialSHA256 {
		return sessionUncertain()
	}
	settings, err := checkpointSettings(&sessionAPI{apiProfile: profile, creation: &sessionCreation{settings: config.Settings}})
	if err != nil || settings != r.source.SettingsSHA256 || InspectCheckpoint(ctx, r.source.RuntimeHome, r.raw, r.ref) != nil {
		return sessionUncertain()
	}
	claim, err := CheckpointResumeClaim(config, r.ref, r.request)
	if err != nil {
		return err
	}
	if claim.Validate() != nil || config.Claim(ctx, claim) != nil {
		return sessionUncertain()
	}
	if err := copyCheckpointDatabase(ctx, r.source, home); err != nil {
		return err
	}
	return InspectCheckpoint(ctx, r.source.RuntimeHome, r.raw, r.ref)
}

// InspectReplacementCheckpoint is a read-only eligibility check for the
// current native text profile. It supplies neither accepted report authority
// nor a lease, account grant or permission to launch a replacement.
func InspectReplacementCheckpoint(ctx context.Context, home string, raw []byte, ref CheckpointReference) error {
	value, err := decodeCheckpoint(raw, ref, home)
	if err != nil {
		return err
	}
	if err := checkpointReplacementProfile(value); err != nil {
		return err
	}
	return InspectCheckpoint(ctx, home, raw, ref)
}

func checkpointReplacementProfile(value nativeCheckpoint) error {
	if value.Project != "global" || filepath.Dir(value.NativeRoot) != value.NativeRoot {
		return incompatible()
	}
	for _, history := range checkpointHistories(value) {
		for _, message := range history.Messages {
			for _, part := range message.Parts {
				switch part.Kind {
				case TextPartKind, ReasoningPartKind, StepStartPartKind, StepFinishPartKind:
				default:
					return incompatible()
				}
			}
		}
	}
	return nil
}

// CheckpointResumeClaim derives only the exact metadata intent that an owning
// Worker must synchronize. Calling it performs no mutation or native launch;
// OpenResumedAPI independently validates the original closed evidence.
func CheckpointResumeClaim(config APIExecutionConfig, ref CheckpointReference, request domain.ID) (SessionClaim, error) {
	home := filepath.Dir(config.Probe.Home)
	if !checkpointDigest(ref.SHA256) || config.Probe.Process.OwnerID.Validate() != nil || !checkpointPath(home) || config.Token == "" {
		return SessionClaim{}, sessionInvalid()
	}
	intent, err := json.Marshal(struct {
		CheckpointSHA256 string
		OwnerID          domain.ID
		RuntimeSHA256    string
		CredentialSHA256 string
	}{ref.SHA256, config.Probe.Process.OwnerID, mutationDigest([]byte(home)), mutationDigest([]byte(config.Token))})
	claim := SessionClaim{RequestID: request, Kind: ResumeSessionMutation, SessionID: ref.SessionID, MessageID: ref.InputID, PartID: ref.PartID, InputRequestID: ref.InputRequestID, BodyDigest: mutationDigest(intent)}
	if err != nil || claim.Validate() != nil {
		return SessionClaim{}, sessionInvalid()
	}
	return claim, nil
}
