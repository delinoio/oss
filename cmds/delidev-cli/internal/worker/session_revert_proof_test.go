// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"crypto/sha256"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type revertProofFixture struct {
	root      string
	session   domain.ID
	machine   domain.ID
	thread    domain.ID
	sourceRun domain.ID
	input     domain.SessionCompactionInput
	source    CodexExecutionCheckpoint
}

func skilledPrompt(text string) domain.SessionInput {
	return domain.SessionInput{Mode: domain.ExecuteMode, Prompt: text, Skills: []domain.SkillBinding{{WorkerDeviceID: domain.NewID(), InventoryID: domain.NewID(), SkillID: domain.NewID(), ContentRevision: "r1", SnapshotID: domain.NewID()}}}
}

func skillDigest(name string) [32]byte { return sha256.Sum256([]byte("skill:" + name)) }

func newRevertProofFixture(t *testing.T, prompt domain.SessionInput, latest []codex.HistoricalInput) *revertProofFixture {
	t.Helper()
	f := newCheckpointFixture(t)
	f.input.Input = prompt
	run := domain.NewID()
	inputID := domain.NewID()
	fx := &revertProofFixture{root: f.root, session: f.input.SessionID, machine: f.input.MachineID, thread: domain.NewID(), sourceRun: run}
	fx.source = CodexExecutionCheckpoint{Version: 1, Completion: domain.ExecutionCompletion{ExecutionID: run}, Native: codex.ContinuationCheckpoint{ThreadID: fx.thread, Inputs: latest}}
	fx.input = domain.SessionCompactionInput{ActionID: domain.NewID(), Assignment: domain.ExecutionJobInput{SessionID: fx.session, MachineID: fx.machine, ExecutionID: run}, Revert: &domain.SessionRevertTarget{MessageID: domain.NewID(), InputID: inputID, NativeTurnID: domain.NativeIdentity(domain.NewID()), Prompt: prompt}}
	return fx
}

func (fx *revertProofFixture) target(skill [32]byte) codex.HistoricalInput {
	return codex.HistoricalInput{ID: fx.input.Revert.InputID, PromptDigest: fx.input.Revert.Prompt.InputDigest(), SkillDigest: skill}
}

// writeEarlierCheckpoint retains a native checkpoint for a different execution
// and points the Revert target at it.
func (fx *revertProofFixture) writeEarlierCheckpoint(t *testing.T, mutate func(*CodexExecutionCheckpoint), skill [32]byte) {
	t.Helper()
	run := domain.NewID()
	completion := domain.ExecutionCompletion{Version: 2, ExecutionID: run, InputID: fx.input.Revert.InputID}
	cp := CodexExecutionCheckpoint{Version: 1, SessionID: fx.session, MachineID: fx.machine, Completion: completion, Native: codex.ContinuationCheckpoint{ThreadID: fx.thread, TurnID: domain.ID(fx.input.Revert.NativeTurnID), Inputs: []codex.HistoricalInput{fx.target(skill)}}}
	if mutate != nil {
		mutate(&cp)
	}
	raw, err := json.Marshal(cp)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(fx.root, "runtimes", string(run))
	if err := security.PrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := security.WriteAtomic(filepath.Join(dir, "native-completion.json"), raw); err != nil {
		t.Fatal(err)
	}
	fx.input.Revert.ExecutionID, fx.input.Revert.CheckpointDigest = run, executionInputDigest(raw)
}

func TestResolveRevertTargetUsesRetainedSourceProof(t *testing.T) {
	prompt := skilledPrompt("skilled prompt")
	fx := newRevertProofFixture(t, prompt, nil)
	fx.source.Native.Inputs = []codex.HistoricalInput{fx.target(skillDigest("original"))}
	got, err := resolveRevertTarget(fx.root, fx.source, fx.input, nil)
	if err != nil || got.SkillDigest != skillDigest("original") || got.ID != fx.input.Revert.InputID || got.PromptDigest != prompt.InputDigest() {
		t.Fatal("original selected-skill proof not bound", err)
	}
}

func TestResolveRevertTargetIgnoresOtherInputProofs(t *testing.T) {
	prompt := skilledPrompt("earlier skilled prompt")
	fx := newRevertProofFixture(t, prompt, nil)
	// The latest source input has a different selection and must never authorize the target.
	latest := codex.HistoricalInput{ID: domain.NewID(), PromptDigest: sha256.Sum256([]byte("latest")), SkillDigest: skillDigest("latest")}
	fx.source.Native.Inputs = []codex.HistoricalInput{latest}
	if _, err := resolveRevertTarget(fx.root, fx.source, fx.input, nil); err == nil {
		t.Fatal("latest source proof authorized an earlier skilled target")
	}
	// The same input id retained with another prompt digest cannot lend its proof.
	wrong := fx.target(skillDigest("original"))
	wrong.PromptDigest = sha256.Sum256([]byte("different prompt"))
	fx.source.Native.Inputs = []codex.HistoricalInput{wrong}
	if _, err := resolveRevertTarget(fx.root, fx.source, fx.input, nil); err == nil {
		t.Fatal("proof for another prompt digest authorized the target")
	}
}

func TestResolveRevertTargetRejectsMissingOrZeroProofForSkilledPrompt(t *testing.T) {
	fx := newRevertProofFixture(t, skilledPrompt("skilled prompt"), nil)
	if _, err := resolveRevertTarget(fx.root, fx.source, fx.input, nil); err == nil {
		t.Fatal("missing proof fell back to observed history")
	}
	fx.source.Native.Inputs = []codex.HistoricalInput{fx.target([32]byte{})}
	_, err := resolveRevertTarget(fx.root, fx.source, fx.input, nil)
	if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("zero legacy proof fell back to observed history", err)
	}
}

func TestResolveRevertTargetKeepsTextOnlyZeroDigestCompatibility(t *testing.T) {
	prompt := domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "text only"}
	fx := newRevertProofFixture(t, prompt, nil)
	got, err := resolveRevertTarget(fx.root, fx.source, fx.input, nil)
	if err != nil || got.SkillDigest != ([32]byte{}) || got.PromptDigest != prompt.InputDigest() {
		t.Fatal("text-only target changed", err)
	}
	fx.source.Native.Inputs = []codex.HistoricalInput{fx.target([32]byte{})}
	if got, err := resolveRevertTarget(fx.root, fx.source, fx.input, nil); err != nil || got.SkillDigest != ([32]byte{}) {
		t.Fatal("retained zero digest rejected for text-only target", err)
	}
}

func TestResolveRevertTargetReadsEarlierExecutionCheckpoint(t *testing.T) {
	fx := newRevertProofFixture(t, skilledPrompt("earlier skilled prompt"), nil)
	latest := codex.HistoricalInput{ID: domain.NewID(), PromptDigest: sha256.Sum256([]byte("latest")), SkillDigest: skillDigest("latest")}
	fx.source.Native.Inputs = []codex.HistoricalInput{latest}
	fx.writeEarlierCheckpoint(t, nil, skillDigest("earlier"))
	got, err := resolveRevertTarget(fx.root, fx.source, fx.input, nil)
	if err != nil || got.SkillDigest != skillDigest("earlier") {
		t.Fatal("earlier target did not use its own frozen proof", err)
	}
}

func TestResolveRevertTargetRejectsUnboundEarlierCheckpoint(t *testing.T) {
	cases := map[string]func(*revertProofFixture, *testing.T){
		"zero-proof": func(fx *revertProofFixture, t *testing.T) { fx.writeEarlierCheckpoint(t, nil, [32]byte{}) },
		"changed-digest": func(fx *revertProofFixture, t *testing.T) {
			fx.writeEarlierCheckpoint(t, nil, skillDigest("earlier"))
			fx.input.Revert.CheckpointDigest = executionInputDigest([]byte("other"))
		},
		"other-session": func(fx *revertProofFixture, t *testing.T) {
			fx.writeEarlierCheckpoint(t, func(c *CodexExecutionCheckpoint) { c.SessionID = domain.NewID() }, skillDigest("earlier"))
		},
		"other-machine": func(fx *revertProofFixture, t *testing.T) {
			fx.writeEarlierCheckpoint(t, func(c *CodexExecutionCheckpoint) { c.MachineID = domain.NewID() }, skillDigest("earlier"))
		},
		"other-thread": func(fx *revertProofFixture, t *testing.T) {
			fx.writeEarlierCheckpoint(t, func(c *CodexExecutionCheckpoint) { c.Native.ThreadID = domain.NewID() }, skillDigest("earlier"))
		},
		"other-turn": func(fx *revertProofFixture, t *testing.T) {
			fx.writeEarlierCheckpoint(t, func(c *CodexExecutionCheckpoint) { c.Native.TurnID = domain.NewID() }, skillDigest("earlier"))
		},
		"other-input": func(fx *revertProofFixture, t *testing.T) {
			fx.writeEarlierCheckpoint(t, func(c *CodexExecutionCheckpoint) { c.Completion.InputID = domain.NewID() }, skillDigest("earlier"))
		},
		"other-prompt": func(fx *revertProofFixture, t *testing.T) {
			fx.writeEarlierCheckpoint(t, func(c *CodexExecutionCheckpoint) {
				c.Native.Inputs[0].PromptDigest = sha256.Sum256([]byte("changed"))
			}, skillDigest("earlier"))
		},
		"missing-file": func(fx *revertProofFixture, t *testing.T) {
			fx.writeEarlierCheckpoint(t, nil, skillDigest("earlier"))
			fx.input.Revert.ExecutionID = domain.NewID()
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			fx := newRevertProofFixture(t, skilledPrompt("earlier skilled prompt"), nil)
			setup(fx, t)
			if _, err := resolveRevertTarget(fx.root, fx.source, fx.input, nil); err == nil {
				t.Fatal("unbound earlier checkpoint authorized a skilled target")
			}
		})
	}
}

func TestResolveRevertTargetEarlierTextOnlyTargetStaysCompatible(t *testing.T) {
	fx := newRevertProofFixture(t, domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "earlier text"}, nil)
	// Without a readable earlier checkpoint, text-only compatibility is unchanged.
	fx.input.Revert.ExecutionID, fx.input.Revert.CheckpointDigest = domain.NewID(), executionInputDigest([]byte("absent"))
	got, err := resolveRevertTarget(fx.root, fx.source, fx.input, nil)
	if err != nil || got.SkillDigest != ([32]byte{}) {
		t.Fatal("text-only earlier target changed", err)
	}
}

func TestSessionRevertTargetCheckpointReferenceIsAllOrNothing(t *testing.T) {
	target := domain.SessionRevertTarget{MessageID: domain.NewID(), InputID: domain.NewID(), NativeTurnID: domain.NativeIdentity(domain.NewID()), Prompt: domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "p"}}
	if target.Validate() != nil {
		t.Fatal("absent reference must stay valid")
	}
	target.ExecutionID = domain.NewID()
	if target.Validate() == nil {
		t.Fatal("execution without digest accepted")
	}
	target.CheckpointDigest = "not-a-digest"
	if target.Validate() == nil {
		t.Fatal("malformed digest accepted")
	}
	target.CheckpointDigest = executionInputDigest([]byte("x"))
	if target.Validate() != nil {
		t.Fatal("complete reference rejected")
	}
}

func TestVerifyRevertIntentSkillProofQuarantinesLegacySkilledClaims(t *testing.T) {
	fx := newRevertProofFixture(t, skilledPrompt("skilled prompt"), nil)
	fx.source.Native.Inputs = []codex.HistoricalInput{fx.target(skillDigest("original"))}
	// The claim of a Worker that bound the retained proof reconciles.
	if err := verifyRevertIntentSkillProof(fx.root, fx.source, fx.input, fx.target(skillDigest("original")), nil); err != nil {
		t.Fatal("claim carrying the retained proof was rejected", err)
	}
	// A pre-fix claim froze a zero digest, which never verified the skills.
	if err := verifyRevertIntentSkillProof(fx.root, fx.source, fx.input, fx.target([32]byte{}), nil); err == nil {
		t.Fatal("legacy skilled claim with a zero digest was accepted")
	}
	// A claim for removed, changed or reordered skills differs from the retained proof.
	if err := verifyRevertIntentSkillProof(fx.root, fx.source, fx.input, fx.target(skillDigest("changed")), nil); err == nil {
		t.Fatal("claim for another skill selection was accepted")
	}
	// Without a retained proof nothing can vouch for the claim.
	fx.source.Native.Inputs = nil
	if err := verifyRevertIntentSkillProof(fx.root, fx.source, fx.input, fx.target(skillDigest("original")), nil); err == nil {
		t.Fatal("claim was accepted without a retained proof")
	}
}

func TestVerifyRevertIntentSkillProofReadsEarlierExecutionCheckpoint(t *testing.T) {
	fx := newRevertProofFixture(t, skilledPrompt("earlier skilled prompt"), nil)
	fx.writeEarlierCheckpoint(t, nil, skillDigest("earlier"))
	if err := verifyRevertIntentSkillProof(fx.root, fx.source, fx.input, fx.target(skillDigest("earlier")), nil); err != nil {
		t.Fatal("claim bound to the earlier checkpoint was rejected", err)
	}
	if err := verifyRevertIntentSkillProof(fx.root, fx.source, fx.input, fx.target([32]byte{}), nil); err == nil {
		t.Fatal("legacy zero-digest claim for an earlier skilled target was accepted")
	}
}

func TestVerifyRevertIntentSkillProofKeepsTextOnlyClaims(t *testing.T) {
	fx := newRevertProofFixture(t, domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "text only"}, nil)
	if err := verifyRevertIntentSkillProof(fx.root, fx.source, fx.input, fx.target([32]byte{}), nil); err != nil {
		t.Fatal("text-only claim was rejected", err)
	}
	if verifyRevertIntentSkillProof(fx.root, fx.source, domain.SessionCompactionInput{}, fx.target([32]byte{}), nil) == nil {
		t.Fatal("non-Revert input was accepted")
	}
}
