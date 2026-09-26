package worker

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const maxOpenCodeExecutionCheckpointBytes = 9 << 20

// This reference comes from original immutable assignment, acknowledged
// publication and mutation journals. A checkpoint cannot supply these facts
// for itself or resolve a missing server report.
type openCodeCheckpointReference struct {
	Claim        openCodeClaimReference     `json:"claim"`
	Completion   domain.ExecutionCompletion `json:"completion"`
	InputMode    domain.SessionMode         `json:"input_mode"`
	PromptSHA256 string                     `json:"prompt_sha256"`
	ClaimsSHA256 string                     `json:"claims_sha256"`
}

type openCodeExecutionCheckpoint struct {
	Version         uint32                       `json:"version"`
	Reference       openCodeCheckpointReference  `json:"reference"`
	NativeReference opencode.CheckpointReference `json:"native_reference"`
	Native          json.RawMessage              `json:"native"`
}

func (r openCodeCheckpointReference) validate() error {
	c := r.Completion
	if r.Claim.validate() != nil || c.ValidateForHarness(domain.OpenCode) != nil || c.Version != 1 || c.ExecutionID != r.Claim.ExecutionID || c.InputID != r.Claim.InputID || !r.InputMode.Valid() {
		return executionCheckpointUncertain()
	}
	for _, value := range []string{r.PromptSHA256, r.ClaimsSHA256} {
		raw, err := hex.DecodeString(value)
		if err != nil || len(raw) != 32 || hex.EncodeToString(raw) != value {
			return executionCheckpointUncertain()
		}
	}
	return nil
}

func (p openCodeExecutionCheckpoint) matches(ref openCodeCheckpointReference) bool {
	native, original, completion := p.NativeReference, ref.Claim, ref.Completion
	return ref.validate() == nil && p.Version == 1 && p.Reference == ref &&
		native.OwnerID == original.JobID && native.CreationRequestID == original.ThreadRequestID && native.InputRequestID == original.InputRequestID &&
		native.SessionID == string(completion.NativeThreadID) && native.InputID == string(completion.NativeTurnID) && native.InputSHA256 == ref.PromptSHA256 &&
		(completion.Outcome == domain.ExecutionSucceeded || native.RequiresResume) && len(p.Native) > 0 && len(p.Native) <= 8<<20 &&
		json.Valid(p.Native) && executionInputDigest(p.Native) == native.SHA256
}

// The native snapshot hashes the entire closed runtime. Keep its Worker
// envelope in the already owned job directory, never inside that runtime.
func openCodeCheckpointPath(root string, job domain.ID) (string, error) {
	claims, err := openCodeClaimsPath(root, job)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(claims), "opencode-checkpoint.json"), nil
}

// RetainCheckpoint follows Complete and the separately closed workspace lease
// in the production runner. It rechecks the same live publisher, original
// acknowledged terminal and exact claims around native retention. This private
// digest alone does not upgrade version-1 completion or authorize Resume.
func (c *OpenCodeEventPublisher) RetainCheckpoint(ctx context.Context) (string, error) {
	if c == nil || c.text == nil || c.usage == nil || c.api == nil {
		return "", executionCheckpointUncertain()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.blocked || !c.finished || c.completion == nil {
		return "", executionCheckpointUncertain()
	}
	b := c.text.binding
	b.mu.Lock()
	defer b.mu.Unlock()
	fail := func(err error) (string, error) {
		// The binding lock remains held across retention. Do not call the
		// general composer failure helper, which acquires that same lock.
		c.blocked, c.text.blocked = true, true
		if b.publisher.config.Logger != nil {
			b.publisher.config.Logger.WarnContext(ctx, "opencode_worker_checkpoint_uncertain", "job_id", b.reference.JobID, "execution_id", b.reference.ExecutionID, "code", domain.SafeError(err).Code)
		}
		return "", err
	}
	check := func() ([]opencode.SessionClaim, error) {
		claims, err := b.readClaims()
		sequence, sequenceErr := b.publisher.acknowledgedSequence()
		if err != nil || sequenceErr != nil || c.text.blocked || c.usage.blocked || b.stage != openCodeAccepted || !b.validPublicationClaims(claims) || sequence != c.terminalSequence || c.completion.LastSequence != sequence {
			return nil, executionCheckpointUncertain()
		}
		return claims, nil
	}
	claims, err := check()
	if err != nil {
		return fail(err)
	}
	input := b.publisher.input
	if input.Continuation != nil || input.Configuration.Harness != domain.OpenCode || input.Installation.Version != opencode.SupportedVersion {
		return fail(executionCheckpointUncertain())
	}
	claimBytes, err := json.Marshal(claims)
	if err != nil {
		return fail(executionCheckpointUncertain())
	}
	ref := openCodeCheckpointReference{Claim: b.reference, Completion: *c.completion, InputMode: input.Input.Mode, PromptSHA256: executionInputDigest([]byte(input.Input.Prompt)), ClaimsSHA256: executionInputDigest(claimBytes)}
	if ref.validate() != nil {
		return fail(executionCheckpointUncertain())
	}
	native, nativeRef, err := c.api.RetainCheckpoint(ctx)
	if err != nil {
		return fail(err)
	}
	value := openCodeExecutionCheckpoint{Version: 1, Reference: ref, NativeReference: nativeRef, Native: native}
	if !value.matches(ref) || nativeRef.PartID != b.inputClaim.PartID {
		return fail(executionCheckpointUncertain())
	}
	root := b.publisher.config.Root
	home := filepath.Join(root, "runtimes", string(ref.Claim.ExecutionID))
	if opencode.InspectCheckpoint(ctx, home, native, nativeRef) != nil {
		return fail(executionCheckpointUncertain())
	}
	if _, err := check(); err != nil {
		return fail(err)
	}
	path, err := openCodeCheckpointPath(root, ref.Claim.JobID)
	if err != nil {
		return fail(err)
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > maxOpenCodeExecutionCheckpointBytes || ctx.Err() != nil {
		return fail(executionCheckpointUncertain())
	}
	old, err := security.ReadPrivate(path, maxOpenCodeExecutionCheckpointBytes)
	if err == nil {
		if !bytes.Equal(old, raw) {
			return fail(executionCheckpointUncertain())
		}
	} else if !errors.Is(err, os.ErrNotExist) || security.WriteAtomic(path, raw) != nil {
		return fail(executionCheckpointUncertain())
	}
	digest := executionInputDigest(raw)
	if _, err := readOpenCodeExecutionCheckpoint(ctx, root, ref, digest); err != nil {
		return fail(err)
	}
	if _, err := check(); err != nil {
		return fail(err)
	}
	if b.publisher.config.Logger != nil {
		b.publisher.config.Logger.InfoContext(ctx, "opencode_original_worker_checkpoint_retained", "job_id", ref.Claim.JobID, "execution_id", ref.Claim.ExecutionID, "sequence", ref.Completion.LastSequence)
	}
	return digest, nil
}

// Read-only verification requires independent original journal metadata and
// the exact retained digest. It never creates files, reconstructs claims or
// grants a replacement process, new input or version-2 report.
func readOpenCodeExecutionCheckpoint(ctx context.Context, root string, ref openCodeCheckpointReference, digest string) (openCodeExecutionCheckpoint, error) {
	if ctx.Err() != nil || ref.validate() != nil {
		return openCodeExecutionCheckpoint{}, executionCheckpointUncertain()
	}
	path, err := openCodeCheckpointPath(root, ref.Claim.JobID)
	if err != nil {
		return openCodeExecutionCheckpoint{}, err
	}
	raw, err := security.ReadPrivate(path, maxOpenCodeExecutionCheckpointBytes)
	if err != nil || executionInputDigest(raw) != digest {
		return openCodeExecutionCheckpoint{}, executionCheckpointUncertain()
	}
	var value openCodeExecutionCheckpoint
	if domain.Decode(raw, &value) != nil || !value.matches(ref) {
		return openCodeExecutionCheckpoint{}, executionCheckpointUncertain()
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(canonical, raw) {
		return openCodeExecutionCheckpoint{}, executionCheckpointUncertain()
	}
	home := filepath.Join(root, "runtimes", string(ref.Claim.ExecutionID))
	if opencode.InspectCheckpoint(ctx, home, value.Native, value.NativeReference) != nil {
		return openCodeExecutionCheckpoint{}, executionCheckpointUncertain()
	}
	return value, nil
}
