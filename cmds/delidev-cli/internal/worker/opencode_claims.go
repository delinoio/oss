package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const maxOpenCodeClaims = 2048
const maxOpenCodeClaimBytes = 1 << 20

// No token, native endpoint, prompt, answer or private path enters this journal.
// Its reference comes from the already opened immutable Worker publisher, not
// from a retained native file. A read cannot grant execution or reply authority.
type openCodeClaimReference struct {
	Version             uint32    `json:"version"`
	JobID               domain.ID `json:"job_id"`
	InstanceID          domain.ID `json:"instance_id"`
	ServerID            domain.ID `json:"server_id"`
	DeviceID            domain.ID `json:"device_id"`
	MachineID           domain.ID `json:"machine_id"`
	ExecutionID         domain.ID `json:"execution_id"`
	SessionID           domain.ID `json:"session_id"`
	InputID             domain.ID `json:"input_id"`
	AccountID           domain.ID `json:"account_id"`
	ConnectionID        domain.ID `json:"connection_id"`
	ThreadRequestID     domain.ID `json:"thread_request_id"`
	InputRequestID      domain.ID `json:"input_request_id"`
	Revision            uint64    `json:"revision"`
	AssignmentDigest    string    `json:"assignment_digest"`
	ConfigurationDigest string    `json:"configuration_digest"`
}

type openCodeClaimState struct {
	Reference openCodeClaimReference  `json:"reference"`
	Claims    []opencode.SessionClaim `json:"claims"`
}

type openCodeClaimJournal struct {
	mu       sync.Mutex
	path     string
	state    openCodeClaimState
	saved    []byte
	release  func() error
	closed   bool
	closeErr error
	failed   bool
	logger   *slog.Logger
}

func (r openCodeClaimReference) validate() error {
	if r.Version != 1 || r.Revision == 0 || r.ThreadRequestID == r.InputRequestID {
		return openCodeClaimUncertain()
	}
	for _, id := range []domain.ID{r.JobID, r.InstanceID, r.ServerID, r.DeviceID, r.MachineID, r.ExecutionID, r.SessionID, r.InputID, r.AccountID, r.ConnectionID, r.ThreadRequestID, r.InputRequestID} {
		if id.Validate() != nil {
			return openCodeClaimUncertain()
		}
	}
	for _, value := range []string{r.AssignmentDigest, r.ConfigurationDigest} {
		raw, err := hex.DecodeString(value)
		if err != nil || len(raw) != sha256.Size || hex.EncodeToString(raw) != value {
			return openCodeClaimUncertain()
		}
	}
	return nil
}

func openCodeClaimUncertain() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "The original OpenCode mutation journal requires reconciliation.", "Retain the original assignment, claims and process ownership; do not repeat native mutations.")
}

func openOpenCodeClaims(p *ExecutionPublisher) (*openCodeClaimJournal, error) {
	if p == nil {
		return nil, openCodeClaimUncertain()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	i, publication := p.input, p.state
	if p.closed || p.release == nil || i.Validate() != nil || i.Configuration.Harness != domain.OpenCode || i.Installation.Version != opencode.SupportedVersion || i.Continuation != nil || publication.Pending != nil || publication.LastSequence != 0 || publication.JobID != p.job || i.ExecutionID != p.execution || publication.InstanceID != p.config.Instance || publication.ServerID != p.config.Credential.ServerID || publication.DeviceID != p.config.Credential.DeviceID || i.MachineID != p.config.Credential.MachineID {
		return nil, openCodeClaimUncertain()
	}
	ref := openCodeClaimReference{Version: 1, JobID: p.job, InstanceID: publication.InstanceID, ServerID: publication.ServerID, DeviceID: publication.DeviceID, MachineID: i.MachineID, ExecutionID: i.ExecutionID, SessionID: i.SessionID, InputID: i.InputID, AccountID: i.AccountID, ConnectionID: i.ConnectionID, ThreadRequestID: i.ThreadRequestID, InputRequestID: i.TurnRequestID, Revision: publication.Revision, AssignmentDigest: publication.AssignmentDigest, ConfigurationDigest: i.ConfigurationDigest}
	if ref.validate() != nil {
		return nil, openCodeClaimUncertain()
	}
	path, err := openCodeClaimsPath(p.config.Root, ref.JobID)
	if err != nil {
		return nil, err
	}
	lock, err := security.TryLock(path + ".lock")
	if err != nil {
		return nil, openCodeClaimUncertain()
	}
	// Even an empty retained journal belongs to an earlier initializer attempt.
	// Reopening it is read-only reconciliation, never a fresh-process permission.
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		_ = lock.Close()
		return nil, openCodeClaimUncertain()
	}
	state := openCodeClaimState{Reference: ref, Claims: []opencode.SessionClaim{}}
	raw, err := json.Marshal(state)
	if err != nil || security.WriteAtomic(path, raw) != nil {
		_ = lock.Close()
		return nil, openCodeClaimUncertain()
	}
	return &openCodeClaimJournal{path: path, state: state, saved: raw, release: lock.Close, logger: p.config.Logger}, nil
}

func openCodeClaimsPath(root string, job domain.ID) (string, error) {
	if job.Validate() != nil || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", openCodeClaimUncertain()
	}
	for _, path := range []string{root, filepath.Join(root, "jobs"), filepath.Join(root, "jobs", string(job))} {
		actual, err := filepath.EvalSymlinks(path)
		if err != nil || actual != path || security.CheckPrivateDir(path) != nil {
			return "", openCodeClaimUncertain()
		}
	}
	return filepath.Join(root, "jobs", string(job), "opencode-claims.json"), nil
}

func (s openCodeClaimState) validateNext(c opencode.SessionClaim) error {
	if c.Validate() != nil || len(s.Claims) >= maxOpenCodeClaims {
		return openCodeClaimUncertain()
	}
	for _, prior := range s.Claims {
		if c.RequestID == prior.RequestID || c.InteractionID != "" && c.InteractionID == prior.InteractionID || c.ArrivalID != "" && c.ArrivalID == prior.ArrivalID {
			return openCodeClaimUncertain()
		}
	}
	if len(s.Claims) == 0 {
		if c.Kind != opencode.CreateSessionMutation || c.RequestID != s.Reference.ThreadRequestID {
			return openCodeClaimUncertain()
		}
		return nil
	}
	if len(s.Claims) == 1 {
		if c.Kind != opencode.SubmitInputMutation || c.RequestID != s.Reference.InputRequestID {
			return openCodeClaimUncertain()
		}
		return nil
	}
	input := s.Claims[1]
	if c.Kind == opencode.CreateSessionMutation || c.Kind == opencode.SubmitInputMutation || c.InputRequestID != input.RequestID || c.SessionID != input.SessionID {
		return openCodeClaimUncertain()
	}
	var stop *opencode.SessionClaim
	recoveries := 0
	for index := range s.Claims {
		claim := &s.Claims[index]
		switch claim.Kind {
		case opencode.StopInputMutation, opencode.StopOwnedRuntimeMutation:
			stop = claim
		case opencode.RecoverStoppedRuntimeMutation:
			recoveries++
		}
	}
	if stop != nil && c.Kind != opencode.RecoverStoppedRuntimeMutation {
		return openCodeClaimUncertain()
	}
	switch c.Kind {
	case opencode.StopInputMutation, opencode.StopOwnedRuntimeMutation, opencode.RecoverStoppedRuntimeMutation:
		if c.MessageID != input.MessageID || c.PartID != input.PartID {
			return openCodeClaimUncertain()
		}
		if c.Kind == opencode.RecoverStoppedRuntimeMutation && (stop == nil || c.StopRequestID != stop.RequestID || recoveries >= 32) {
			return openCodeClaimUncertain()
		}
	case opencode.ReplyPermissionMutation, opencode.ReplyQuestionMutation, opencode.RejectQuestionMutation:
		if c.MessageID == input.MessageID || c.PartID == input.PartID {
			return openCodeClaimUncertain()
		}
	default:
		return openCodeClaimUncertain()
	}
	return nil
}

func (j *openCodeClaimJournal) Claim(ctx context.Context, c opencode.SessionClaim) (returned error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	phase := "validation"
	defer func() {
		if returned != nil && j.logger != nil {
			j.logger.WarnContext(ctx, "OpenCode durable mutation claim failed", "job_id", j.state.Reference.JobID, "execution_id", j.state.Reference.ExecutionID, "phase", phase, "code", domain.SafeError(returned).Code)
		}
	}()
	if j.closed || j.failed || ctx.Err() != nil {
		return openCodeClaimUncertain()
	}
	if err := j.state.validateNext(c); err != nil {
		return err
	}
	phase = "original-journal"
	prior, err := security.ReadPrivate(j.path, maxOpenCodeClaimBytes)
	if err != nil || !bytes.Equal(prior, j.saved) {
		j.failed = true
		return openCodeClaimUncertain()
	}
	// Consume in memory before persistence. A failed sync may have committed;
	// neither this instance nor a restarted reader can grant a second attempt.
	j.state.Claims = append(j.state.Claims, c)
	phase = "persistence"
	raw, err := json.Marshal(j.state)
	if err != nil || len(raw) > maxOpenCodeClaimBytes || ctx.Err() != nil || security.WriteAtomic(j.path, raw) != nil {
		j.failed = true
		return openCodeClaimUncertain()
	}
	j.saved = raw
	return nil
}

func (j *openCodeClaimJournal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return j.closeErr
	}
	j.closed = true
	if j.release() != nil {
		j.closeErr = openCodeClaimUncertain()
	}
	return j.closeErr
}

func readOpenCodeClaims(root string, ref openCodeClaimReference) ([]opencode.SessionClaim, error) {
	if ref.validate() != nil {
		return nil, openCodeClaimUncertain()
	}
	path, err := openCodeClaimsPath(root, ref.JobID)
	if err != nil {
		return nil, err
	}
	raw, err := security.ReadPrivate(path, maxOpenCodeClaimBytes)
	var state openCodeClaimState
	if err != nil || domain.Decode(raw, &state) != nil || state.Reference != ref || state.Claims == nil || len(state.Claims) > maxOpenCodeClaims {
		return nil, openCodeClaimUncertain()
	}
	canonical, err := json.Marshal(state)
	if err != nil || !bytes.Equal(raw, canonical) {
		return nil, openCodeClaimUncertain()
	}
	checked := openCodeClaimState{Reference: ref}
	for _, claim := range state.Claims {
		if err := checked.validateNext(claim); err != nil {
			return nil, err
		}
		checked.Claims = append(checked.Claims, claim)
	}
	return checked.Claims, nil
}
