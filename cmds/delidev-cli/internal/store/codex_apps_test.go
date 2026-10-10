// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func codexAppsStoreFixture() CodexAppsSnapshot {
	session, account, generation := domain.NewID(), domain.NewID(), domain.NewID()
	return CodexAppsSnapshot{Version: 1, Revision: 1, SessionID: session, AccountGeneration: domain.NewID(), ConnectionID: domain.NewID(), Configuration: &domain.CodexAppConfiguration{Version: 1, SessionID: session, AccountID: account, Generation: generation, AppIDs: []string{"original-app"}}}
}
func codexAppsStoreOperation(value CodexAppsSnapshot) domain.CodexAppsOperation {
	id := domain.NewID()
	return domain.CodexAppsOperation{Version: 1, ID: id, Revision: 1, RequestID: id, ActorID: domain.NewID(), Action: domain.CodexAppsInspect, State: domain.CodexAppsQueued, Original: value.Configuration.Clone(), ExecutionID: domain.NewID(), ExecutionJobID: domain.NewID(), MachineID: domain.NewID(), InstanceID: domain.NewID(), NativeThreadID: "original-native-thread"}
}
func TestCodexAppsMetadataReceiptsAndProtectedGeneration(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	value := codexAppsStoreFixture()
	request := domain.NewID()
	calls := 0
	mutate := func(input any) (Result, error) {
		return s.Mutate(ctx, request, "fixture.codex-apps", input, func(tx *Tx) (any, error) {
			calls++
			if err := tx.PutCodexAppsSnapshot(value.SessionID, 0, value); err != nil {
				return nil, err
			}
			return struct{ Session domain.ID }{value.SessionID}, nil
		})
	}
	first, err := mutate("original")
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := mutate("original")
	if err != nil || !replayed.Replayed || calls != 1 || !reflect.DeepEqual(first.Data, replayed.Data) {
		t.Fatal(replayed, err, calls)
	}
	if _, err := mutate("changed"); err == nil {
		t.Fatal("changed immutable receipt admitted")
	}
	err = s.Read(ctx, func(tx *Tx) error {
		selected, err := tx.CodexAppsAssignmentSelection(value.SessionID, value.Configuration.AccountID, value.AccountGeneration, value.ConnectionID)
		if err != nil || selected == nil || !reflect.DeepEqual(*selected, *value.Configuration) {
			t.Fatal(selected, err)
		}
		if _, err := tx.CodexAppsAssignmentSelection(value.SessionID, value.Configuration.AccountID, domain.NewID(), value.ConnectionID); err == nil {
			t.Fatal("protected generation transferred")
		}
		if _, err := tx.CodexAppsAssignmentSelection(value.SessionID, value.Configuration.AccountID, value.AccountGeneration, domain.NewID()); err == nil {
			t.Fatal("connection transferred")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestCodexAppsPendingSelectionAndRemovalOnly(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	value := codexAppsStoreFixture()
	original := value.Configuration.Clone()
	op := codexAppsStoreOperation(value)
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.codex-apps.pending", nil, func(tx *Tx) (any, error) {
		value.Operation = &op
		return nil, tx.PutCodexAppsSnapshot(value.SessionID, 0, value)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Read(ctx, func(tx *Tx) error {
		if _, err := tx.CodexAppsSelection(value.SessionID); err == nil {
			t.Fatal("queued native obligation allowed new assignment")
		}
		controls, err := tx.CodexAppsQueued(op.MachineID, op.InstanceID)
		if err != nil || len(controls) != 1 || controls[0].ID != op.ID {
			t.Fatal(controls, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.codex-apps.replace-pending", nil, func(tx *Tx) (any, error) {
		replacement := codexAppsStoreOperation(value)
		value.Operation = &replacement
		return nil, tx.PutCodexAppsSnapshot(value.SessionID, 1, value)
	})
	if err == nil {
		t.Fatal("pending operation replaced")
	}
	// Removal is a new generation; neither added IDs nor a different account can
	// borrow the original assignment's positive authority.
	next := original.Clone()
	next.Generation = domain.NewID()
	next.AppIDs = []string{}
	if !original.RemovalOnly(next) {
		t.Fatal("valid removal denied")
	}
	next.AppIDs = []string{"foreign-app"}
	if original.RemovalOnly(next) {
		t.Fatal("positive app addition admitted")
	}
	next.AppIDs = []string{}
	next.AccountID = domain.NewID()
	if original.RemovalOnly(next) {
		t.Fatal("foreign account admitted")
	}
}
func TestCodexAppsMetadataRejectsForeignCatalogAndKeepsUncertainty(t *testing.T) {
	value := codexAppsStoreFixture()
	operation := codexAppsStoreOperation(value)
	operation.State = domain.CodexAppsClaimed
	operation.ClaimID = domain.NewID()
	operation.Revision = 2
	inventory := domain.CodexAppsInventory{Version: 1, OperationID: operation.ID, ClaimID: operation.ClaimID, NativeCatalogRefreshVerified: true, SessionID: value.SessionID, AccountID: value.Configuration.AccountID, ConfigurationGeneration: value.Configuration.Generation, ExecutionID: operation.ExecutionID, ExecutionJobID: operation.ExecutionJobID, MachineID: operation.MachineID, InstanceID: operation.InstanceID, NativeThreadID: operation.NativeThreadID, ObservedAt: time.Now().UTC(), Apps: []domain.CodexApp{}}
	operation.State = domain.CodexAppsSucceeded
	operation.Inventory = &inventory
	value.Operation = &operation
	value.Inventory = &inventory
	if err := value.validate(value.SessionID); err != nil {
		t.Fatal(err)
	}
	inventory.AccountID = domain.NewID()
	if value.validate(value.SessionID) == nil {
		t.Fatal("foreign account inventory admitted")
	}
	inventory.AccountID = value.Configuration.AccountID
	inventory.NativeCatalogRefreshVerified = false
	if value.validate(value.SessionID) == nil {
		t.Fatal("absence without native catalog proof admitted")
	}
	operation.Inventory = nil
	operation.State = domain.CodexAppsUncertain
	operation.Problem = domain.Fail(domain.RecoveryRequired, "Original result unknown.", "Retain the original claim.")
	value.Inventory = nil
	if err := value.validate(value.SessionID); err != nil {
		t.Fatal(err)
	}
}

func TestCodexAppsClaimHistoryCannotResendOrDisappear(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	value := codexAppsStoreFixture()
	operation := codexAppsStoreOperation(value)
	value.Operation = &operation
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.codex-apps.history-create", nil, func(tx *Tx) (any, error) { return nil, tx.PutCodexAppsSnapshot(value.SessionID, 0, value) })
	if err != nil {
		t.Fatal(err)
	}
	operation.State, operation.ClaimID, operation.Revision = domain.CodexAppsClaimed, domain.NewID(), 2
	value.Operation = &operation
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.codex-apps.history-claim", nil, func(tx *Tx) (any, error) { return nil, tx.PutCodexAppsSnapshot(value.SessionID, 1, value) })
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.codex-apps.history-requeue", nil, func(tx *Tx) (any, error) {
		next := operation
		next.State = domain.CodexAppsQueued
		next.ClaimID = ""
		next.Revision++
		value.Operation = &next
		return nil, tx.PutCodexAppsSnapshot(value.SessionID, 2, value)
	})
	if err == nil {
		t.Fatal("claimed control requeued")
	}
	operation.State, operation.Problem, operation.Revision = domain.CodexAppsUncertain, domain.Fail(domain.RecoveryRequired, "Unknown original native result.", "Retain the original claim."), 3
	value.Operation = &operation
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.codex-apps.history-uncertain", nil, func(tx *Tx) (any, error) { return nil, tx.PutCodexAppsSnapshot(value.SessionID, 2, value) })
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Read(ctx, func(tx *Tx) error {
		saved, err := tx.CodexAppsOperation(operation.ID)
		if err != nil || saved == nil || saved.ClaimID != operation.ClaimID || saved.State != domain.CodexAppsUncertain {
			t.Fatal(saved, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.codex-apps.history-replace", nil, func(tx *Tx) (any, error) {
		next := codexAppsStoreOperation(value)
		value.Operation = &next
		return nil, tx.PutCodexAppsSnapshot(value.SessionID, 3, value)
	})
	if err == nil {
		t.Fatal("uncertain original history replaced")
	}
}
func TestCodexAppsCanceledHistorySurvivesFreshDeniedGeneration(t *testing.T) {
	s, _ := openTest(t)
	ctx := context.Background()
	value := codexAppsStoreFixture()
	operation := codexAppsStoreOperation(value)
	value.Operation = &operation
	_, err := s.Mutate(ctx, domain.NewID(), "fixture.codex-apps.cancel-history-create", nil, func(tx *Tx) (any, error) { return nil, tx.PutCodexAppsSnapshot(value.SessionID, 0, value) })
	if err != nil {
		t.Fatal(err)
	}
	operation.State, operation.PositiveNoNativeSend, operation.Revision = domain.CodexAppsCanceled, true, 2
	value.Operation = &operation
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.codex-apps.cancel-history", nil, func(tx *Tx) (any, error) { return nil, tx.PutCodexAppsSnapshot(value.SessionID, 1, value) })
	if err != nil {
		t.Fatal(err)
	}
	next := value.Configuration.Clone()
	next.Generation = domain.NewID()
	next.AppIDs = []string{}
	value.Configuration = &next
	value.AccountGeneration = domain.NewID()
	value.ConnectionID = domain.NewID()
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.codex-apps.fresh-denied", nil, func(tx *Tx) (any, error) { return nil, tx.PutCodexAppsSnapshot(value.SessionID, 2, value) })
	if err != nil {
		t.Fatal(err)
	}
	later := codexAppsStoreOperation(value)
	value.Operation = &later
	_, err = s.Mutate(ctx, domain.NewID(), "fixture.codex-apps.later-control", nil, func(tx *Tx) (any, error) { return nil, tx.PutCodexAppsSnapshot(value.SessionID, 3, value) })
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Read(ctx, func(tx *Tx) error {
		saved, err := tx.CodexAppsOperation(operation.ID)
		if err != nil || saved == nil || saved.State != domain.CodexAppsCanceled || !saved.PositiveNoNativeSend || saved.ClaimID != "" {
			t.Fatal(saved, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCodexAppsCancellationPreservesOrdinaryAndUncertainCompletion(t *testing.T) {
	for _, scenario := range []string{"absent", "queued-uncertain", "foreign-job", "claimed"} {
		t.Run(scenario, func(t *testing.T) {
			s, _ := openTest(t)
			value := codexAppsStoreFixture()
			operation := codexAppsStoreOperation(value)
			jobID := operation.ExecutionJobID
			_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.cancel-uncertain", scenario, func(tx *Tx) (any, error) {
				// An ordinary completion with no positive cleanup output must not
				// fail just because the Apps observer runs after PutJob.
				if _, err := tx.Put(domain.JobKind, jobID, 0, value.SessionID, "", domain.Job{Type: domain.ExecuteSessionJob, State: domain.JobFailed}); err != nil {
					return nil, err
				}
				if scenario != "absent" {
					if scenario == "foreign-job" {
						operation.ExecutionJobID = domain.NewID()
					}
					value.Operation = &operation
					if err := tx.PutCodexAppsSnapshot(value.SessionID, 0, value); err != nil {
						return nil, err
					}
					if scenario == "claimed" {
						operation.State, operation.ClaimID, operation.Revision = domain.CodexAppsClaimed, domain.NewID(), 2
						if err := tx.PutCodexAppsSnapshot(value.SessionID, 1, value); err != nil {
							return nil, err
						}
					}
				}
				if err := tx.CancelUnclaimedCodexAppsControl(jobID); err != nil {
					return nil, err
				}
				stored, err := tx.CodexAppsSnapshot(value.SessionID)
				if err != nil {
					return nil, err
				}
				if scenario == "absent" {
					if stored != nil {
						t.Fatal("absent Apps metadata created")
					}
				} else if stored.Operation.State != operation.State || stored.Operation.PositiveNoNativeSend {
					t.Fatal("uncertain original obligation synthesized cancellation")
				}
				return nil, nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCodexAppsPurgeProtectsOriginalClaimsAndRemovesSettledContent(t *testing.T) {
	s, _ := openTest(t)
	value := codexAppsStoreFixture()
	operation := codexAppsStoreOperation(value)
	value.Operation = &operation
	_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.apps-purge", nil, func(tx *Tx) (any, error) {
		if err := tx.PutCodexAppsSnapshot(value.SessionID, 0, value); err != nil {
			return nil, err
		}
		if err := tx.PurgeSessionCodexApps(value.SessionID); err == nil {
			t.Fatal("pending obligation purged")
		}
		operation.State, operation.PositiveNoNativeSend, operation.Revision = domain.CodexAppsCanceled, true, 2
		if err := tx.PutCodexAppsSnapshot(value.SessionID, 1, value); err != nil {
			return nil, err
		}
		if err := tx.PurgeSessionCodexApps(value.SessionID); err != nil {
			return nil, err
		}
		stored, err := tx.CodexAppsSnapshot(value.SessionID)
		if err != nil || stored != nil {
			t.Fatal(stored, err)
		}
		history, err := tx.CodexAppsOperation(operation.ID)
		if err != nil || history != nil {
			t.Fatal(history, err)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCodexAppsRestoreKeepsCurrentAuthorityAndRejectsOrphanClaims(t *testing.T) {
	for _, scenario := range []string{"current", "orphan", "collision"} {
		t.Run(scenario, func(t *testing.T) {
			s, _ := openTest(t)
			value := codexAppsStoreFixture()
			operation := codexAppsStoreOperation(value)
			if scenario != "current" {
				value.Operation = &operation
			}
			_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.apps-restore", scenario, func(tx *Tx) (any, error) {
				if err := tx.PutCodexAppsSnapshot(value.SessionID, 0, value); err != nil {
					return nil, err
				}
				if _, err := tx.tx.ExecContext(tx.ctx, "ATTACH DATABASE ':memory:' AS current_state"); err != nil {
					return nil, err
				}
				if _, err := tx.tx.ExecContext(tx.ctx, "CREATE TABLE current_state.metadata(key TEXT PRIMARY KEY,value TEXT NOT NULL)"); err != nil {
					return nil, err
				}
				if scenario != "orphan" {
					if _, err := tx.tx.ExecContext(tx.ctx, "INSERT INTO current_state.metadata SELECT * FROM metadata"); err != nil {
						return nil, err
					}
				}
				if scenario == "current" {
					old := value
					old.AccountGeneration = domain.NewID()
					old.ConnectionID = domain.NewID()
					old.Configuration = &domain.CodexAppConfiguration{Version: 1, SessionID: value.SessionID, AccountID: value.Configuration.AccountID, Generation: domain.NewID(), AppIDs: []string{"old-permission"}}
					raw, _ := json.Marshal(old)
					if _, err := tx.tx.ExecContext(tx.ctx, "UPDATE metadata SET value=? WHERE key=?", string(raw), codexAppsPrefix+string(value.SessionID)); err != nil {
						return nil, err
					}
				}
				if scenario == "collision" {
					var history codexAppsOperationHistory
					history.Version, history.AccountGeneration, history.ConnectionID, history.Operation = 1, value.AccountGeneration, value.ConnectionID, operation
					history.Operation.NativeThreadID = "foreign-original-thread"
					raw, _ := json.Marshal(history)
					if _, err := tx.tx.ExecContext(tx.ctx, "UPDATE current_state.metadata SET value=? WHERE key=?", string(raw), codexAppsOperationPrefix+string(operation.ID)); err != nil {
						return nil, err
					}
				}
				err := restoreCodexAppsMetadata(tx.ctx, tx.tx)
				if scenario != "current" {
					if err == nil {
						t.Fatal("unresolved or foreign original history restored")
					}
					return nil, nil
				}
				if err != nil {
					return nil, err
				}
				stored, err := tx.CodexAppsSnapshot(value.SessionID)
				if err != nil || stored.AccountGeneration != value.AccountGeneration || stored.ConnectionID != value.ConnectionID || !reflect.DeepEqual(stored.Configuration, value.Configuration) {
					t.Fatal(stored, err)
				}
				return nil, nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCodexAppsCancellationRequiresExactJoinedCleanup(t *testing.T) {
	for _, verified := range []bool{false, true} {
		t.Run(map[bool]string{false: "uncertain", true: "verified"}[verified], func(t *testing.T) {
			s, _ := openTest(t)
			value := codexAppsStoreFixture()
			operation := codexAppsStoreOperation(value)
			fixture := failedForkDeletionInput(t, value.SessionID, operation.MachineID)
			input := fixture.SourceAssignment
			value.Configuration.AccountID = input.AccountID
			input.CodexApps = value.Configuration
			operation.Original = value.Configuration.Clone()
			operation.ExecutionID = input.ExecutionID
			operation.NativeThreadID = fixture.Completion.NativeThreadID
			value.Operation = &operation
			complete := fixture.Completion
			complete.CleanupVerified = verified
			rawInput, _ := json.Marshal(input)
			rawComplete, _ := json.Marshal(complete)
			_, err := s.Mutate(context.Background(), domain.NewID(), "fixture.apps-exact-cleanup", verified, func(tx *Tx) (any, error) {
				if _, err := tx.Put(domain.JobKind, operation.ExecutionJobID, 0, value.SessionID, "", domain.Job{Type: domain.ExecuteSessionJob, State: domain.JobSucceeded, MachineID: operation.MachineID, InstanceID: operation.InstanceID, Input: rawInput, Output: rawComplete}); err != nil {
					return nil, err
				}
				if err := tx.PutCodexAppsSnapshot(value.SessionID, 0, value); err != nil {
					return nil, err
				}
				if err := tx.CancelUnclaimedCodexAppsControl(operation.ExecutionJobID); err != nil {
					return nil, err
				}
				stored, err := tx.CodexAppsOperation(operation.ID)
				if err != nil || stored == nil || stored.PositiveNoNativeSend != verified || verified && stored.State != domain.CodexAppsCanceled || !verified && stored.State != domain.CodexAppsQueued {
					t.Fatal(stored, err)
				}
				return nil, nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
