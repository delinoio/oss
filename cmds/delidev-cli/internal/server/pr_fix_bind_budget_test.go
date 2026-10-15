// SPDX-License-Identifier: Apache-2.0
package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

// Installing the PR remediation grows the assignment after the dispatch-time
// size check. bindPRFixAssignment must measure the final document.
func TestBindPRFixAssignmentRechecksSerializedSizeAfterRemediation(t *testing.T) {
	f := automaticPromptFixture(t, []string{strings.Repeat("a", 60<<10)})
	f.savePolicy(t, func(p *domain.RemediationPolicy) { p.SessionStrategy = domain.DedicatedSession })
	if err := f.service.requestAutomaticPRFix(f.owner, f.link, map[domain.PRProblemKind]bool{domain.PRFeedbackProblem: true}); err != nil {
		t.Fatal(err)
	}
	attempts, _ := f.attempts(t)
	if len(attempts) != 1 {
		t.Fatal("attempt absent")
	}
	attempt := attempts[0]
	_, _, _, observations := automaticPromptOriginals(t, f)

	// bind installs the remediation into a queued job whose prompt is sized so
	// the input fits before the remediation and, when oversize is set, not after.
	bind := func(oversize bool) error {
		_, err := f.service.Store.Mutate(f.owner, domain.NewID(), "fixture.bind-budget", nil, func(tx *store.Tx) (any, error) {
			_, a, err := tx.GetPRRemediationAttempt(attempt.ID)
			if err != nil {
				return nil, err
			}
			sr, err := tx.Get(domain.SessionKind, a.SessionID)
			if err != nil {
				return nil, err
			}
			session, err := store.Decode[domain.Session](sr)
			if err != nil {
				return nil, err
			}
			preparationRow, err := tx.Get(domain.JobKind, session.Preparation.JobID)
			if err != nil {
				return nil, err
			}
			preparation, err := store.Decode[domain.Job](preparationRow)
			if err != nil {
				return nil, err
			}
			now := time.Now().UTC()
			preparation.State, preparation.FinishedAt, preparation.Output = domain.JobSucceeded, &now, json.RawMessage(`{}`)
			if _, err := tx.PutJob(preparationRow.ID, preparationRow.Revision, sr.ID, f.project, preparation); err != nil {
				return nil, err
			}
			session.Preparation.State, session.Dispatch = domain.PreparationReady, domain.DispatchReady
			if sr, err = tx.Put(sr.Kind, sr.ID, sr.Revision, sr.ID, f.project, session); err != nil {
				return nil, err
			}
			qr, err := tx.Get(domain.QueueKind, a.InputID)
			if err != nil {
				return nil, err
			}
			queued, err := store.Decode[domain.QueuedInput](qr)
			if err != nil {
				return nil, err
			}
			claim, err := tx.ClaimInitialExecution(a.SessionID, sr.Revision, a.InputID, qr.Revision)
			if err != nil {
				return nil, err
			}
			selection, err := tx.PRFixSelection(attempt.ID)
			if err != nil {
				return nil, err
			}
			build := func(prompt string, remediation bool) json.RawMessage {
				input := domain.ExecutionJobInput{Version: 1, SessionID: a.SessionID, MachineID: session.MachineID, ExecutionID: claim.ID, InputID: a.InputID, ThreadRequestID: domain.NewID(), TurnRequestID: domain.NewID(), Configuration: claim.Configuration, ConfigurationDigest: claim.ConfigurationDigest, AccountID: claim.InitialAccountID, ConnectionID: claim.ConnectionID, Input: domain.SessionInput{Prompt: prompt, Mode: queued.Mode}, Installation: domain.Installation{Harness: domain.Codex, State: domain.InstallationDetected, Version: domain.CodexProtocolVersion, ProtocolVerified: true, Protocol: &domain.ProtocolObservation{Protocol: domain.CodexAppServer, State: domain.ProtocolVerified}}, Preparation: json.RawMessage(`{}`), Manifest: json.RawMessage(`{}`)}
				if remediation {
					input.Remediation = &selection
				}
				raw, _ := json.Marshal(input)
				return raw
			}
			fits := func(prompt string, remediation bool) bool {
				return domain.RequireExecutionJobInput(build(prompt, remediation), nil) == nil
			}
			// Escaped '<' costs six bytes, so binary search the largest count that
			// fits without the remediation, then fill the remainder with plain bytes.
			low, high := 0, domain.MaxPromptBytes
			for low < high {
				mid := (low + high + 1) / 2
				if fits(strings.Repeat("<", mid), false) {
					low = mid
				} else {
					high = mid - 1
				}
			}
			prompt := strings.Repeat("<", low)
			for fits(prompt+"p", false) {
				prompt += "p"
			}
			if !oversize {
				prompt = "short"
			} else if !fits(prompt, false) || fits(prompt, true) {
				t.Fatalf("fixture prompt does not straddle the limit: without=%v with=%v", fits(prompt, false), fits(prompt, true))
			}
			job, err := tx.PutJob(domain.NewID(), 0, a.SessionID, f.project, domain.Job{Type: domain.ExecuteSessionJob, MachineID: session.MachineID, State: domain.JobQueued, Input: build(prompt, false), AcceptedAt: now})
			if err != nil {
				return nil, err
			}
			return nil, bindPRFixAssignment(tx, attempt, job, observations)
		})
		return err
	}
	if err := bind(true); domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal("remediation pushed the assignment past the limit without a ResourceExhausted result", err)
	}
	if err := bind(false); err != nil {
		t.Fatal("ordinary remediation binding was rejected", err)
	}
}
