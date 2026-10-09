// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func readNativeContextFixture(f *publicationFixture) (sessionContextView, error) {
	client := delidevv1connect.NewSessionServiceClient(http.DefaultClient, f.http.URL)
	request := connect.NewRequest(&pb.GetSessionContextRequest{SessionId: string(f.input.SessionID)})
	request.Header().Set("Authorization", "Bearer "+f.service.Identity.Token)
	reply, err := client.GetSessionContext(context.Background(), request)
	var view sessionContextView
	if err == nil {
		err = json.Unmarshal(reply.Msg.DocumentJson, &view)
	}
	return view, err
}
func nativeContextFixture(t *testing.T) *publicationFixture {
	t.Helper()
	f := newPublicationFixture(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	return f
}
func publishContextFixture(t *testing.T, f *publicationFixture, sequence uint64, total, last int64) domain.ID {
	t.Helper()
	e := f.event(domain.ExecutionUsageObserved, sequence)
	e.ObservationID = domain.NewID()
	e.Usage = &domain.NativeTokenUsage{Total: reportedCounts(total), Last: reportedCounts(last)}
	f.publish(t, e)
	return e.ObservationID
}
func mutateContextSession(t *testing.T, f *publicationFixture, update func(*domain.Session)) {
	t.Helper()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.context-state", nil, func(tx *store.Tx) (any, error) {
		r, s, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return nil, err
		}
		update(&s)
		return tx.Put(r.Kind, r.ID, r.Revision, r.ID, r.ProjectID, s)
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestNativeContextProjectsExactLastRequestWithOriginalProvenance(t *testing.T) {
	f := nativeContextFixture(t)
	view, err := readNativeContextFixture(f)
	if err != nil || view.NativeContext != nil || view.CurrentTokens != nil {
		t.Fatal(view, err)
	}
	for i, count := range []int64{20, 2, 0, 9007199254740993} {
		id := publishContextFixture(t, f, uint64(i+3), 30, count)
		record, err := f.service.Store.Get(context.Background(), domain.UsageKind, id)
		if err != nil {
			t.Fatal(err)
		}
		view, err = readNativeContextFixture(f)
		if err != nil || view.NativeContext == nil {
			t.Fatal(view, err)
		}
		native := view.NativeContext
		if native.Tokens != strconv.FormatInt(count, 10) || native.ObservationID != id || native.ExecutionID != f.input.ExecutionID || native.NativeThreadID != string(f.thread) || native.NativeTurnID != string(f.turn) || native.Sequence != strconv.Itoa(i+3) || !native.ObservedAt.Equal(record.UpdatedAt) || native.Status != nativeContextLatest || native.Harness != domain.Codex || native.Source != "last-request-total" || view.CurrentTokens != nil {
			t.Fatal("incorrect projection", native)
		}
	}
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.UsageKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(rows) != 4 {
		t.Fatal("read changed retained usage", len(rows), err)
	}
}
func TestNativeContextRetainsHistoricalTurnExecutionAndCompaction(t *testing.T) {
	for _, kind := range []string{"turn", "execution", "pending-input", "compaction"} {
		t.Run(kind, func(t *testing.T) {
			f := nativeContextFixture(t)
			id := publishContextFixture(t, f, 3, 30, 20)
			if kind == "compaction" {
				e := f.event(domain.ExecutionProgressObserved, 4)
				e.Progress = &domain.ExecutionProgressUpdate{ID: domain.NewID(), Progress: domain.NativeProgress{Kind: domain.NativeCompactionProgress, Compaction: &domain.NativeCompactionObservation{Harness: domain.Codex, Trigger: domain.NativeAutomaticCompaction, Stage: domain.NativeCompactionStarted, NativeItemID: "context-item"}}}
				f.publish(t, e)
			} else {
				mutateContextSession(t, f, func(s *domain.Session) {
					switch kind {
					case "turn":
						s.Execution.NativeTurnID = string(domain.NewID())
					case "execution":
						s.CurrentExecution = &domain.ExecutionSelection{ID: domain.NewID(), InputID: domain.NewID(), AccountID: f.input.AccountID, ConnectionID: f.input.ConnectionID}
					case "pending-input":
						s.PendingInputs = 1
					}
				})
			}
			view, err := readNativeContextFixture(f)
			if err != nil || view.NativeContext == nil || view.NativeContext.ObservationID != id || view.NativeContext.Tokens != "20" || view.NativeContext.Status != nativeContextHistorical {
				t.Fatal("old report presented as current", view, err)
			}
			if kind == "turn" {
				r, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				s, err := store.Decode[domain.Session](r)
				if err != nil {
					t.Fatal(err)
				}
				f.turn = domain.ID(s.Execution.NativeTurnID)
				publishContextFixture(t, f, 4, 35, 2)
				view, err = readNativeContextFixture(f)
				if err != nil || view.NativeContext == nil || view.NativeContext.Status != nativeContextLatest || view.NativeContext.Tokens != "2" {
					t.Fatal("matching new report unavailable", view, err)
				}
			}
		})
	}
}
func TestNativeContextRejectsForeignOrMalformedRetainedUsage(t *testing.T) {
	for _, kind := range []string{"session", "execution", "account", "connection", "model", "provider", "harness", "child-thread", "turn", "sequence", "missing-total", "negative", "worker"} {
		t.Run(kind, func(t *testing.T) {
			f := nativeContextFixture(t)
			id := publishContextFixture(t, f, 3, 30, 20)
			_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.context-corruption", nil, func(tx *store.Tx) (any, error) {
				if kind == "worker" {
					r, err := tx.Get(domain.JobKind, f.job)
					if err != nil {
						return nil, err
					}
					j, err := store.Decode[domain.Job](r)
					if err != nil {
						return nil, err
					}
					j.MachineID = domain.NewID()
					return tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, j)
				}
				r, err := tx.Get(domain.UsageKind, id)
				if err != nil {
					return nil, err
				}
				u, err := store.Decode[domain.ExecutionUsageObservation](r)
				if err != nil {
					return nil, err
				}
				session := r.SessionID
				switch kind {
				case "session":
					session = domain.NewID()
				case "execution":
					u.ExecutionID = domain.NewID()
				case "account":
					u.AccountID = domain.NewID()
				case "connection":
					u.ConnectionID = domain.NewID()
				case "model":
					u.ModelID = domain.NewID()
				case "provider":
					u.ProviderID = domain.NewID()
				case "harness":
					u.Harness = domain.ClaudeCode
				case "child-thread":
					u.ThreadID = string(domain.NewID())
				case "turn":
					u.TurnID = "bad"
				case "sequence":
					u.Sequence = 4
				case "missing-total":
					u.Usage.Last.Total = nil
				case "negative":
					n := int64(-1)
					u.Usage.Last.Total = &n
				}
				return tx.Put(r.Kind, r.ID, r.Revision, session, r.ProjectID, u)
			})
			if err != nil {
				t.Fatal(err)
			}
			if view, err := readNativeContextFixture(f); err == nil || view.NativeContext != nil {
				t.Fatal("foreign/malformed projection accepted", view, err)
			}
		})
	}
}

func TestNativeContextSameTurnSteerRequiresMatchingNewReport(t *testing.T) {
	f, request := newSteerFixture(t)
	publishContextFixture(t, f, 3, 30, 20)
	accepted, err := callSteer(f, request)
	if err != nil {
		t.Fatal(err)
	}
	claim, _ := claimSteer(t, f, accepted.Msg.Steer)
	event := f.event(domain.ExecutionSteerObserved, 4)
	event.Steer = &domain.ExecutionSteerUpdate{SteerID: domain.ID(request.Mutation.RequestId), InputID: domain.ID(request.Mutation.Id), ClaimID: domain.ID(claim.Mutation.RequestId), Delivery: domain.SteerNativeAccepted, Evidence: domain.SteerNativeHistory}
	f.publish(t, event)
	view, err := readNativeContextFixture(f)
	if err != nil || view.NativeContext == nil || view.NativeContext.Status != nativeContextHistorical || view.NativeContext.Tokens != "20" {
		t.Fatal("accepted Steer refreshed prior snapshot", view, err)
	}
	publishContextFixture(t, f, 5, 40, 2)
	view, err = readNativeContextFixture(f)
	if err != nil || view.NativeContext == nil || view.NativeContext.Status != nativeContextLatest || view.NativeContext.Tokens != "2" {
		t.Fatal("matching Steer report unavailable", view, err)
	}
}
func TestNativeContextManualAdmissionMarksOriginalReportHistorical(t *testing.T) {
	f := nativeContextFixture(t)
	id := publishContextFixture(t, f, 3, 30, 20)
	record, err := f.service.Store.Get(context.Background(), domain.UsageKind, id)
	if err != nil {
		t.Fatal(err)
	}
	err = f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		_, session, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(map[string]any{"accepted_at": record.UpdatedAt})
		if err != nil {
			return err
		}
		view := sessionContextView{SessionID: f.input.SessionID, ManualAction: &sessionContextResource{Document: raw}}
		if err = projectNativeContext(tx, session, &view); err != nil {
			return err
		}
		if view.NativeContext == nil || view.NativeContext.Status != nativeContextHistorical || view.NativeContext.Tokens != "20" {
			t.Fatal("manual admission inferred new occupancy", view)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
