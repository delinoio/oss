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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const maxGrokClaimBytes = 256 << 10
const maxGrokClaims = 132                   // Four input stages plus at most 128 original tool replies.
const maxGrokPlanClaims = maxGrokClaims + 2 // Original mode claim and independent binding.

// The immutable publisher supplies assignment authority. Native configuration
// inspection and session setup remain the adapter's separate responsibility.
// No prompt, credential, native endpoint or workspace path enters this journal.
type grokClaimReference struct {
	Version             uint32             `json:"version"`
	JobID               domain.ID          `json:"job_id"`
	InstanceID          domain.ID          `json:"instance_id"`
	ServerID            domain.ID          `json:"server_id"`
	DeviceID            domain.ID          `json:"device_id"`
	MachineID           domain.ID          `json:"machine_id"`
	ExecutionID         domain.ID          `json:"execution_id"`
	SessionID           domain.ID          `json:"session_id"`
	InputID             domain.ID          `json:"input_id"`
	AccountID           domain.ID          `json:"account_id"`
	ConnectionID        domain.ID          `json:"connection_id"`
	CreationRequestID   domain.ID          `json:"creation_request_id"`
	InputRequestID      domain.ID          `json:"input_request_id"`
	Revision            uint64             `json:"revision"`
	AssignmentDigest    string             `json:"assignment_digest"`
	ConfigurationDigest string             `json:"configuration_digest"`
	InputMode           domain.SessionMode `json:"input_mode,omitempty"`
}

type grokClaim struct {
	Creation      *grok.CreationClaim       `json:"creation,omitempty"`
	Input         *grok.InputClaim          `json:"input,omitempty"`
	Closure       *grok.ClosureClaim        `json:"closure,omitempty"`
	Stop          *grok.StopClaim           `json:"stop,omitempty"`
	FileReply     *grok.FilePermissionClaim `json:"file_reply,omitempty"`
	QuestionReply *grok.QuestionClaim       `json:"question_reply,omitempty"`
	Mode          *grok.ModeClaim           `json:"mode,omitempty"`
	PlanReply     *grok.PlanClaim           `json:"plan_reply,omitempty"`
}

// Tool families share original response namespaces in a mixed native input.
// This comparison value contains no answer bytes and grants no send authority.
type grokReplyOwner struct {
	owner, product, input, request, native, arrival domain.ID
	prompt, tool, requestDigest                     string
}

func grokReplyOwnerOf(record grokClaim) (grokReplyOwner, error) {
	if record.FileReply != nil && record.QuestionReply == nil && record.PlanReply == nil {
		c := record.FileReply
		if c.Validate() != nil {
			return grokReplyOwner{}, grokClaimUncertain()
		}
		return grokReplyOwner{c.OwnerID, c.ProductSessionID, c.InputRequestID, c.RequestID, c.NativeSessionID, c.ArrivalID, c.NativePromptID, c.ToolID, c.RequestDigest}, nil
	}
	if record.QuestionReply != nil && record.FileReply == nil && record.PlanReply == nil {
		c := record.QuestionReply
		if c.Validate() != nil {
			return grokReplyOwner{}, grokClaimUncertain()
		}
		return grokReplyOwner{c.OwnerID, c.ProductSessionID, c.InputRequestID, c.RequestID, c.NativeSessionID, c.ArrivalID, c.NativePromptID, c.ToolID, c.RequestDigest}, nil
	}
	if record.PlanReply != nil && record.FileReply == nil && record.QuestionReply == nil {
		c := record.PlanReply
		if c.Validate() != nil {
			return grokReplyOwner{}, grokClaimUncertain()
		}
		return grokReplyOwner{c.OwnerID, c.ProductSessionID, c.InputRequestID, c.RequestID, c.NativeSessionID, c.ArrivalID, c.NativePromptID, c.ToolID, c.RequestDigest}, nil
	}
	return grokReplyOwner{}, grokClaimUncertain()
}

type grokClaimState struct {
	Reference grokClaimReference `json:"reference"`
	Claims    []grokClaim        `json:"claims"`
}

type grokClaimJournal struct {
	mu       sync.Mutex
	root     string
	path     string
	state    grokClaimState
	saved    []byte
	prompt   string
	release  func() error
	closed   bool
	failed   bool
	closeErr error
	logger   *slog.Logger
}

func grokClaimUncertain() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "The original Grok Build mutation journal requires reconciliation.", "Retain original assignment, input and process ownership; never repeat an uncertain native operation.")
}

func (r grokClaimReference) validate() error {
	if (r.Version != 1 || r.InputMode != "") && (r.Version != 2 || r.InputMode != domain.PlanMode) || r.Revision == 0 || r.CreationRequestID == r.InputRequestID || r.CreationRequestID == r.SessionID || r.InputRequestID == r.SessionID {
		return grokClaimUncertain()
	}
	for _, id := range []domain.ID{r.JobID, r.InstanceID, r.ServerID, r.DeviceID, r.MachineID, r.ExecutionID, r.SessionID, r.InputID, r.AccountID, r.ConnectionID, r.CreationRequestID, r.InputRequestID} {
		if id.Validate() != nil {
			return grokClaimUncertain()
		}
	}
	for _, value := range []string{r.AssignmentDigest, r.ConfigurationDigest} {
		raw, err := hex.DecodeString(value)
		if err != nil || len(raw) != sha256.Size || hex.EncodeToString(raw) != value {
			return grokClaimUncertain()
		}
	}
	return nil
}

func openGrokClaims(p *ExecutionPublisher) (*grokClaimJournal, error) {
	if p == nil {
		return nil, grokClaimUncertain()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	i, publication := p.input, p.state
	if p.closed || p.release == nil || i.Validate() != nil || i.Configuration.Harness != domain.GrokBuild || (i.Version != 4 && i.Installation.Version != grok.SupportedVersion) || i.Continuation != nil || publication.Pending != nil || publication.LastSequence != 0 || publication.JobID != p.job || i.ExecutionID != p.execution ||
		domain.OwnershipBlocks(domain.OwnershipInstance, domain.ID(publication.InstanceID), publication.InstanceID != p.config.Instance) ||
		domain.OwnershipBlocks(domain.OwnershipInstance, domain.ID(publication.ServerID), publication.ServerID != p.config.Credential.ServerID) ||
		domain.OwnershipBlocks(domain.OwnershipDevice, domain.ID(publication.DeviceID), publication.DeviceID != p.config.Credential.DeviceID) ||
		domain.OwnershipBlocks(domain.OwnershipMachine, domain.ID(i.MachineID), i.MachineID != p.config.Credential.MachineID) {
		return nil, grokClaimUncertain()
	}
	ref := grokClaimReference{Version: 1, JobID: p.job, InstanceID: publication.InstanceID, ServerID: publication.ServerID, DeviceID: publication.DeviceID, MachineID: i.MachineID, ExecutionID: i.ExecutionID, SessionID: i.SessionID, InputID: i.InputID, AccountID: i.AccountID, ConnectionID: i.ConnectionID, CreationRequestID: i.ThreadRequestID, InputRequestID: i.TurnRequestID, Revision: publication.Revision, AssignmentDigest: publication.AssignmentDigest, ConfigurationDigest: i.ConfigurationDigest}
	if i.Input.Mode == domain.PlanMode {
		ref.Version, ref.InputMode = 2, domain.PlanMode
	} else if i.Input.Mode != domain.ExecuteMode {
		return nil, grokClaimUncertain()
	}
	if ref.validate() != nil {
		return nil, grokClaimUncertain()
	}
	path, err := grokClaimsPath(p.config.Root, ref.JobID)
	if err != nil {
		return nil, err
	}
	lock, err := security.TryLock(path + ".lock")
	if err != nil {
		return nil, grokClaimUncertain()
	}
	// Even an empty journal retains a prior initialization attempt. A restarted
	// reader may reconcile it, but may never acquire another native send lease.
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		_ = lock.Close()
		return nil, grokClaimUncertain()
	}
	state := grokClaimState{Reference: ref, Claims: []grokClaim{}}
	raw, err := json.Marshal(state)
	if err != nil || security.WriteAtomic(path, raw) != nil {
		_ = lock.Close()
		return nil, grokClaimUncertain()
	}
	return &grokClaimJournal{root: p.config.Root, path: path, state: state, saved: raw, prompt: i.Input.Prompt, release: lock.Close, logger: p.config.Logger}, nil
}

func grokClaimsPath(root string, job domain.ID) (string, error) {
	if job.Validate() != nil || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", grokClaimUncertain()
	}
	for _, path := range []string{root, filepath.Join(root, "jobs"), filepath.Join(root, "jobs", string(job))} {
		actual, err := filepath.EvalSymlinks(path)
		if err != nil || actual != path || security.CheckPrivateDir(path) != nil {
			return "", grokClaimUncertain()
		}
	}
	return filepath.Join(root, "jobs", string(job), "grok-claims.json"), nil
}

func (s grokClaimState) validateNext(c grokClaim) error {
	if s.Reference.validate() != nil {
		return grokClaimUncertain()
	}
	if s.Reference.Version == 1 {
		if c.Mode != nil {
			return grokClaimUncertain()
		}
		return s.validateNextInput(c)
	}
	if len(s.Claims) >= maxGrokPlanClaims {
		return grokClaimUncertain()
	}
	if len(s.Claims) < 2 {
		if c.Mode != nil {
			return grokClaimUncertain()
		}
		return s.validateNextInput(c)
	}
	if len(s.Claims) < 4 {
		m := c.Mode
		if m == nil || c.Creation != nil || c.Input != nil || c.Stop != nil || c.Closure != nil || c.FileReply != nil || c.QuestionReply != nil || c.PlanReply != nil || m.Validate() != nil ||
			domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(m.OwnerID), m.OwnerID != s.Reference.JobID) ||
			domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(m.ProductSessionID), m.ProductSessionID != s.Reference.SessionID) ||
			m.NativeSessionID != s.Claims[1].Creation.NativeSessionID || m.RequestID == s.Reference.CreationRequestID || m.RequestID == s.Reference.InputRequestID {
			return grokClaimUncertain()
		}
		if len(s.Claims) == 2 {
			if m.Phase != grok.ClaimMode {
				return grokClaimUncertain()
			}
		} else {
			prior := *s.Claims[2].Mode
			prior.Phase, prior.EventID, prior.TimestampMS = grok.BindMode, m.EventID, m.TimestampMS
			if prior != *m {
				return grokClaimUncertain()
			}
		}
		return nil
	}
	// Initial Plan inputs retain the original four input stages after their
	// separate mode mutation. Tool replies retain their own native authority;
	// the original mode alone never grants default file, Stop or history rights.
	if c.Mode != nil || c.Stop != nil || c.Closure != nil {
		return grokClaimUncertain()
	}
	if c.QuestionReply != nil || c.FileReply != nil || c.PlanReply != nil {
		reply, err := grokReplyOwnerOf(c)
		if err != nil || reply.request == s.Claims[2].Mode.RequestID || reply.arrival == s.Claims[2].Mode.RequestID {
			return grokClaimUncertain()
		}
		if c.FileReply != nil {
			var last *grok.PlanClaim
			for _, record := range s.Claims[4:] {
				if record.PlanReply != nil {
					last = record.PlanReply
				}
			}
			if last == nil || last.Outcome != grok.PlanApproved && last.Outcome != grok.PlanAbandoned {
				return grokClaimUncertain()
			}
		}
	}
	inputState := grokClaimState{Reference: s.Reference, Claims: append(append([]grokClaim{}, s.Claims[:2]...), s.Claims[4:]...)}
	return inputState.validateNextInput(c)
}

func (s grokClaimState) validateNextInput(c grokClaim) error {
	variants := 0
	if c.Creation != nil {
		variants++
	}
	if c.Input != nil {
		variants++
	}
	if c.Closure != nil {
		variants++
	}
	if c.Stop != nil {
		variants++
	}
	if c.FileReply != nil {
		variants++
	}
	if c.QuestionReply != nil {
		variants++
	}
	if c.PlanReply != nil {
		variants++
	}
	if len(s.Claims) >= maxGrokClaims || variants != 1 {
		return grokClaimUncertain()
	}
	if c.QuestionReply != nil || c.FileReply != nil || c.PlanReply != nil {
		reply, err := grokReplyOwnerOf(c)
		if len(s.Claims) < 4 || err != nil || reply.owner != s.Reference.JobID || reply.product != s.Reference.SessionID || reply.input != s.Reference.InputRequestID || reply.request == s.Reference.CreationRequestID || reply.native != s.Claims[3].Input.NativeSessionID || reply.prompt != s.Claims[3].Input.NativePromptID {
			return grokClaimUncertain()
		}
		for _, record := range s.Claims[4:] {
			prior, err := grokReplyOwnerOf(record)
			if err != nil || prior.request == reply.request || prior.arrival == reply.arrival || prior.request == reply.arrival || prior.arrival == reply.request || prior.tool == reply.tool || prior.requestDigest == reply.requestDigest {
				return grokClaimUncertain()
			}
			if p := record.PlanReply; p != nil && (reply.tool == p.EntryToolID || reply.tool == p.WriteToolID) {
				return grokClaimUncertain()
			}
			if p := c.PlanReply; p != nil && (prior.tool == p.EntryToolID || prior.tool == p.WriteToolID) {
				return grokClaimUncertain()
			}
			if old, next := record.PlanReply, c.PlanReply; old != nil && next != nil && old.WriteToolID == next.WriteToolID && old.Revision != next.Revision {
				return grokClaimUncertain()
			}
		}
		if c.PlanReply != nil {
			var prior *grok.PlanClaim
			for _, record := range s.Claims[4:] {
				if record.PlanReply != nil {
					prior = record.PlanReply
				}
			}
			p := c.PlanReply
			if prior != nil && (prior.Outcome != grok.PlanCancelled || p.EntryToolID != prior.EntryToolID || p.EntryEventID != prior.EntryEventID || p.Revision < prior.Revision || p.Revision == prior.Revision && (p.WriteToolID != prior.WriteToolID || p.ContentDigest != prior.ContentDigest) || p.Revision > prior.Revision && p.WriteToolID == prior.WriteToolID) {
				return grokClaimUncertain()
			}
		}
		return nil
	}
	if c.Stop != nil {
		stop := c.Stop
		if len(s.Claims) != 4 || stop.Validate() != nil ||
			domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(stop.OwnerID), stop.OwnerID != s.Reference.JobID) ||
			domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(stop.ProductSessionID), stop.ProductSessionID != s.Reference.SessionID) ||
			stop.InputRequestID != s.Reference.InputRequestID || stop.RequestID == s.Reference.CreationRequestID || stop.NativeSessionID != s.Claims[3].Input.NativeSessionID || stop.NativePromptID != s.Claims[3].Input.NativePromptID {
			return grokClaimUncertain()
		}
		return nil
	}
	if len(s.Claims) >= 4 {
		if c.Closure == nil || c.Closure.Validate() != nil || len(s.Claims) > 5 || len(s.Claims) == 5 && s.Claims[4].Closure == nil {
			return grokClaimUncertain()
		}
		input := s.Claims[3].Input
		closure := c.Closure
		if domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(closure.ProductSessionID), closure.ProductSessionID != s.Reference.SessionID) ||
			closure.NativeSessionID != input.NativeSessionID || closure.NativePromptID != input.NativePromptID || closure.RequestID == s.Reference.InputRequestID || closure.RequestID == s.Reference.CreationRequestID {
			return grokClaimUncertain()
		}
		if len(s.Claims) == 4 {
			if closure.Phase != grok.ClaimClosure {
				return grokClaimUncertain()
			}
		} else {
			prior := *s.Claims[4].Closure
			prior.Phase = grok.BindClosure
			if prior != *closure {
				return grokClaimUncertain()
			}
		}
		return nil
	}
	if len(s.Claims) < 2 {
		if c.Creation == nil || c.Creation.Validate() != nil || c.Creation.RequestID != s.Reference.CreationRequestID ||
			domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(c.Creation.ProductSessionID), c.Creation.ProductSessionID != s.Reference.SessionID) {
			return grokClaimUncertain()
		}
		if len(s.Claims) == 0 {
			if c.Creation.Phase != grok.ClaimCreation {
				return grokClaimUncertain()
			}
		} else {
			prior := *s.Claims[0].Creation
			prior.Phase, prior.NativeSessionID = grok.BindCreation, c.Creation.NativeSessionID
			if *c.Creation != prior {
				return grokClaimUncertain()
			}
		}
		return nil
	}
	if c.Input == nil || c.Input.Validate() != nil || c.Input.RequestID != s.Reference.InputRequestID ||
		domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(c.Input.ProductSessionID), c.Input.ProductSessionID != s.Reference.SessionID) ||
		c.Input.NativeSessionID != s.Claims[1].Creation.NativeSessionID {
		return grokClaimUncertain()
	}
	if len(s.Claims) == 2 {
		if c.Input.Phase != grok.ClaimInput {
			return grokClaimUncertain()
		}
	} else {
		prior := *s.Claims[2].Input
		prior.Phase, prior.NativePromptID = grok.BindInput, c.Input.NativePromptID
		if *c.Input != prior {
			return grokClaimUncertain()
		}
	}
	return nil
}

func (j *grokClaimJournal) Creation(ctx context.Context, c grok.CreationClaim) error {
	return j.claim(ctx, grokClaim{Creation: &c})
}

func (j *grokClaimJournal) Input(ctx context.Context, c grok.InputClaim) error {
	return j.claim(ctx, grokClaim{Input: &c})
}

func (j *grokClaimJournal) Closure(ctx context.Context, c grok.ClosureClaim) error {
	return j.claim(ctx, grokClaim{Closure: &c})
}

func (j *grokClaimJournal) Stop(ctx context.Context, c grok.StopClaim) error {
	return j.claim(ctx, grokClaim{Stop: &c})
}

func (j *grokClaimJournal) FileReply(ctx context.Context, c grok.FilePermissionClaim) error {
	return j.claim(ctx, grokClaim{FileReply: &c})
}

func (j *grokClaimJournal) QuestionReply(ctx context.Context, c grok.QuestionClaim) error {
	return j.claim(ctx, grokClaim{QuestionReply: &c})
}

func (j *grokClaimJournal) Mode(ctx context.Context, c grok.ModeClaim) error {
	return j.claim(ctx, grokClaim{Mode: &c})
}

func (j *grokClaimJournal) PlanReply(ctx context.Context, c grok.PlanClaim) error {
	return j.claim(ctx, grokClaim{PlanReply: &c})
}

func (j *grokClaimJournal) claim(ctx context.Context, c grokClaim) (returned error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	phase := "validation"
	defer func() {
		if returned != nil && j.logger != nil {
			j.logger.WarnContext(ctx, "Grok Build original mutation claim failed", "job_id", j.state.Reference.JobID, "execution_id", j.state.Reference.ExecutionID, "phase", phase, "code", domain.SafeError(returned).Code)
		}
	}()
	if j.closed || j.failed || ctx.Err() != nil {
		return grokClaimUncertain()
	}
	if err := j.state.validateNext(c); err != nil {
		return err
	}
	if c.Input != nil {
		phase = "immutable-input"
		expected, err := grok.TextInputClaimDigest(c.Input.NativeSessionID, j.prompt)
		if err != nil || expected != c.Input.BodyDigest {
			j.failed = true
			return grokClaimUncertain()
		}
	}
	phase = "original-journal"
	path, err := grokClaimsPath(j.root, j.state.Reference.JobID)
	if err != nil || path != j.path {
		j.failed = true
		return grokClaimUncertain()
	}
	prior, err := security.ReadPrivate(j.path, maxGrokClaimBytes)
	if err != nil || !bytes.Equal(prior, j.saved) {
		j.failed = true
		return grokClaimUncertain()
	}
	// Consume before synchronization: a failed directory sync may have committed
	// the file, so no later call can recover permission to send it again.
	j.state.Claims = append(j.state.Claims, c)
	phase = "persistence"
	raw, err := json.Marshal(j.state)
	if err != nil || len(raw) > maxGrokClaimBytes || ctx.Err() != nil || security.WriteAtomic(j.path, raw) != nil {
		j.failed = true
		return grokClaimUncertain()
	}
	j.saved = raw
	return nil
}

func (j *grokClaimJournal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return j.closeErr
	}
	j.closed, j.prompt = true, ""
	if j.release() != nil {
		j.closeErr = grokClaimUncertain()
	}
	return j.closeErr
}

func readGrokClaims(root string, ref grokClaimReference) ([]grokClaim, error) {
	if ref.validate() != nil {
		return nil, grokClaimUncertain()
	}
	path, err := grokClaimsPath(root, ref.JobID)
	if err != nil {
		return nil, err
	}
	raw, err := security.ReadPrivate(path, maxGrokClaimBytes)
	var state grokClaimState
	if err != nil || domain.Decode(raw, &state) != nil || state.Reference != ref || state.Claims == nil || len(state.Claims) > maxGrokPlanClaims {
		return nil, grokClaimUncertain()
	}
	canonical, err := json.Marshal(state)
	if err != nil || !bytes.Equal(raw, canonical) {
		return nil, grokClaimUncertain()
	}
	checked := grokClaimState{Reference: ref, Claims: []grokClaim{}}
	for _, claim := range state.Claims {
		if err := checked.validateNext(claim); err != nil {
			return nil, err
		}
		checked.Claims = append(checked.Claims, claim)
	}
	return checked.Claims, nil
}
