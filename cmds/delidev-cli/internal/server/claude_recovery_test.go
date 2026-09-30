package server

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func TestClaudeRecoveryRechecksOriginalSettledBoundary(t *testing.T) {
	for _, outcome := range []domain.ExecutionOutcome{domain.ExecutionSucceeded, domain.ExecutionFailed} {
		for _, stage := range []string{"acceptance", "completion"} {
			for _, scenario := range []string{"original", "permission-change", "permission-mismatch", "terminal-missing", "denial", "interruption", "stop", "unconfirmed"} {
				t.Run(string(outcome)+"/"+stage+"/"+scenario, func(t *testing.T) {
					f, _ := claudeRootOutcomeCompletionFixture(t, outcome, func(f *publicationFixture) { f.registerGrant(t) })
					replaceRecoveryFixtureWorker(t, f)
					var claimed store.Record
					var evidence domain.ExecutionRecoveryEvidence
					if stage == "completion" {
						_, change := acceptRecovery(t, f)
						claimed, evidence = claimRecovery(t, f, change.ExecutionRecoveryJob)
					}
					_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.claude-recovery-boundary", nil, func(tx *store.Tx) (any, error) {
						r, session, err := sessionRecord(tx, f.input.SessionID)
						if err != nil {
							return nil, err
						}
						switch scenario {
						case "permission-change":
							session.Execution.ClaudeProgress = &domain.ClaudeProgressState{NativeTurnID: session.Execution.NativeTurnID, PermissionChanged: true}
						case "permission-mismatch":
							mode := domain.ClaudePermissionPlan
							session.Execution.ClaudeProgress = &domain.ClaudeProgressState{NativeTurnID: session.Execution.NativeTurnID, Permission: &mode}
						case "terminal-missing":
							session.Execution.ClaudeTerminal = nil
						case "denial":
							session.Execution.ClaudeDenial = &domain.ClaudeDenialCompletion{}
						case "interruption":
							session.Execution.ClaudeInterruption = &domain.ClaudeInterruptionProgress{}
						case "stop":
							session.Execution.ClaudeStop = &domain.ClaudeStopObservation{}
						case "unconfirmed":
							session.Execution.UnconfirmedResponses = 1
						}
						return tx.Put(r.Kind, r.ID, r.Revision, r.ID, r.ProjectID, session)
					})
					if err != nil {
						t.Fatal(err)
					}
					err = f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
						if stage == "completion" {
							job, err := store.Decode[domain.Job](claimed)
							if err != nil {
								return err
							}
							raw, _ := json.Marshal(evidence)
							return validateExecutionRecoveryResult(tx, claimed, job, raw)
						}
						r, session, err := sessionRecord(tx, f.input.SessionID)
						if err != nil {
							return err
						}
						comparison, err := executionRecoveryRequest(tx, f.service.Identity.ServerID, r, session)
						if err != nil {
							return err
						}
						raw, _ := json.Marshal(comparison)
						if comparison.Claude == nil || comparison.Claude.InstructionsDigest != continuationDigest([]byte(f.input.Configuration.Instructions)) || comparison.Claude.BindingRequestID != f.input.ThreadRequestID || comparison.Claude.InputRequestID != f.input.TurnRequestID || bytes.Contains(raw, []byte(f.input.Input.Prompt)) {
							t.Fatal("recovery changed original comparison or retained prompt")
						}
						return nil
					})
					if (err == nil) != (scenario == "original") {
						t.Fatal("recovery boundary mismatch", err)
					}
				})
			}
		}
	}
}
