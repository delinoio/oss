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
						composed, err := f.input.Configuration.NativeInstructions(f.input.Input.Mode)
						if err != nil {
							return err
						}
						comparison, err := executionRecoveryRequest(tx, f.service.Identity.ServerID, r, session)
						if err != nil {
							return err
						}
						raw, _ := json.Marshal(comparison)
						if comparison.Claude == nil || comparison.Claude.InstructionsDigest != continuationDigest([]byte(composed)) || comparison.Claude.BindingRequestID != f.input.ThreadRequestID || comparison.Claude.InputRequestID != f.input.TurnRequestID || bytes.Contains(raw, []byte(f.input.Input.Prompt)) {
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

// Recovery must hash the same composed instructions the Worker passed to the
// native adapter, using only the frozen execution snapshot and original mode.
func TestClaudeRecoveryHashesComposedNativeInstructions(t *testing.T) {
	custom, empty := "custom/", ""
	for _, tc := range []struct {
		name     string
		mode     domain.SessionMode
		prefix   *string
		template string
		guided   bool
	}{
		{"execute-default-prefix", domain.ExecuteMode, &[]string{domain.DefaultBranchPrefix}[0], "Original template.", true},
		{"execute-default-prefix-empty-template", domain.ExecuteMode, &[]string{domain.DefaultBranchPrefix}[0], "", true},
		{"execute-custom-prefix", domain.ExecuteMode, &custom, "Original template.", true},
		{"execute-empty-prefix", domain.ExecuteMode, &empty, "Original template.", false},
		{"plan-default-prefix", domain.PlanMode, &[]string{domain.DefaultBranchPrefix}[0], "Original template.", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Rebuild the retained execution input with the frozen prefix before the
			// Worker claims it, then recompute the immutable configuration digest.
			f := publicationFixtureFromAuthority(t, newProfileAuthorityFixture(t, "http://127.0.0.1:1", domain.ClaudeCode, domain.AnthropicMessages, func(input *domain.ExecutionJobInput) {
				input.Input.Mode = tc.mode
				input.Installation.ResolvedPath = "/fixture/claude"
				if tc.template != "" {
					input.Configuration.Templates = []domain.AppliedTemplate{{ID: domain.NewID(), Revision: 1, Contents: tc.template}}
					input.Configuration.Instructions = tc.template
				}
				input.Configuration.BranchPrefix = &domain.BranchPrefixSelection{Version: 1, Prefix: *tc.prefix}
				digest, err := input.Configuration.Digest()
				if err != nil {
					t.Fatal(err)
				}
				input.ConfigurationDigest = digest
			}, false))
			f.thread, f.turn = f.input.SessionID, "123e4567-e89b-42d3-a456-426614174000"
			f.registerGrant(t)
			terminal := publishClaudeTerminalFixture(t, f, true)
			terminal = publishClaudeRootContent(t, terminal, f.input.Configuration.NativeModel, func(e domain.ExecutionEvent) { f.publish(t, e) })
			f.publish(t, terminal)
			replaceRecoveryFixtureWorker(t, f)
			want, err := f.input.Configuration.NativeInstructions(tc.mode)
			if err != nil || (want != tc.template) != tc.guided {
				t.Fatal("fixture did not produce the expected composed instructions", err)
			}
			// Later Settings changes must not affect the frozen original snapshot.
			changed := "changed-after-execution/"
			_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.changed-settings", nil, func(tx *store.Tx) (any, error) {
				settings := domain.DefaultSettings()
				settings.BranchPrefix = &changed
				return tx.Put(domain.SettingsKind, domain.NewID(), 0, "", "", settings)
			})
			if err != nil {
				t.Fatal(err)
			}
			err = f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
				r, session, err := sessionRecord(tx, f.input.SessionID)
				if err != nil {
					return err
				}
				request, err := executionRecoveryRequest(tx, f.service.Identity.ServerID, r, session)
				if err != nil {
					return err
				}
				if request.Claude == nil || request.Claude.InstructionsDigest != continuationDigest([]byte(want)) {
					t.Fatal("recovery digest differs from the composed original instructions")
				}
				if tc.guided && request.Claude.InstructionsDigest == continuationDigest([]byte(tc.template)) {
					t.Fatal("recovery digested the uncomposed template")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
