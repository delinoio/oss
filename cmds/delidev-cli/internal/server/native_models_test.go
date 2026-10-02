// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type nativeModelsFixture struct {
	service                    *Service
	ctx                        context.Context
	root                       string
	machine, account, provider domain.ID
}

func newNativeModelsFixture(t *testing.T) *nativeModelsFixture {
	t.Helper()
	f := &nativeModelsFixture{ctx: domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice}), root: filepath.Join(t.TempDir(), "server"), machine: domain.NewID(), account: domain.NewID(), provider: domain.NewID()}
	db, err := store.Open(f.ctx, f.root)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := security.CreateIdentity(f.root)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	f.service = &Service{Store: db, Identity: identity, logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
	t.Cleanup(func() {
		if err := f.service.Store.Close(); err != nil {
			t.Error(err)
		}
	})
	_, err = db.Mutate(f.ctx, domain.NewID(), "fixture.seed", nil, func(tx *store.Tx) (any, error) {
		if _, err := tx.Put(domain.ProviderKind, f.provider, 0, "", "", domain.Provider{Name: "Fixture", Endpoint: "http://127.0.0.1:1", Protocol: domain.OpenAIResponses, Authentication: domain.KeylessAuth}); err != nil {
			return nil, err
		}
		if _, err := tx.Put(domain.AccountKind, f.account, 0, "", "", domain.Account{Alias: "Fixture", ProviderID: f.provider, Type: domain.APIAccount, Enabled: true, Health: domain.AccountUnverified, Connection: &domain.AccountConnection{ID: domain.NewID(), Authentication: domain.KeylessAuth, ConnectedAt: time.Now().UTC()}}); err != nil {
			return nil, err
		}
		return tx.Put(domain.MachineKind, f.machine, 0, "", "", domain.Machine{Name: "Fixture", OS: "linux", Architecture: "amd64", Version: rpc.Version, DiscoveryRevision: 1, WorkerCapabilities: []domain.WorkerCapability{domain.NativeModelsV1}, Installations: []domain.Installation{{Harness: domain.Codex, State: domain.InstallationDetected, Version: domain.CodexProtocolVersion, ResolvedPath: "/fixture/codex", ExecutableSHA256: strings.Repeat("a", 64)}}})
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *nativeModelsFixture) request() *connect.Request[pb.DiscoverNativeModelsRequest] {
	return connect.NewRequest(&pb.DiscoverNativeModelsRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.machine), ExpectedRevision: 1}, AccountId: string(f.account), AccountRevision: 1})
}

func (f *nativeModelsFixture) complete(t *testing.T, id domain.ID, problem *domain.Error) {
	t.Helper()
	_, err := f.service.Store.Mutate(f.ctx, domain.NewID(), "fixture.finish", struct{ ID domain.ID }{id}, func(tx *store.Tx) (any, error) {
		record, job, _, err := nativeModelJob(tx, id)
		if err != nil {
			return nil, err
		}
		observation := domain.NativeModelObservation{Version: 1, ObservedAt: time.Now().UTC(), CleanupVerified: true, Models: []domain.NativeModel{
			{ID: "picker-a", Model: "model-a", DisplayName: "First", Reasoning: []domain.NativeReasoningEffort{"medium"}, DefaultReasoning: "medium", Modalities: []string{"text"}},
			{ID: "picker-b", Model: "model-b", DisplayName: "Second", Reasoning: []domain.NativeReasoningEffort{"medium"}, DefaultReasoning: "medium", Modalities: []string{"text"}},
		}}
		raw, _ := json.Marshal(observation)
		return finishNativeModels(tx, record, job, raw, problem)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNativeModelReceiptRestartPagingAndExplicitRegistration(t *testing.T) {
	f := newNativeModelsFixture(t)
	before, err := f.service.Store.Get(f.ctx, domain.AccountKind, f.account)
	if err != nil {
		t.Fatal(err)
	}
	request := f.request()
	accepted, err := f.service.DiscoverNativeModels(f.ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(accepted.Msg.Job.DocumentJson), "/fixture/") {
		t.Fatal("private executable leaked into ordinary output")
	}
	id := domain.ID(accepted.Msg.Job.Id)
	f.complete(t, id, nil)
	first, err := f.service.ListNativeModels(f.ctx, connect.NewRequest(&pb.ListNativeModelsRequest{JobId: string(id), PageSize: 1}))
	if err != nil || first.Msg.NextPageToken == "" {
		t.Fatalf("first immutable page failed: %v", err)
	}
	other, err := f.service.DiscoverNativeModels(f.ctx, f.request())
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.service.ListNativeModels(f.ctx, connect.NewRequest(&pb.ListNativeModelsRequest{JobId: other.Msg.Job.Id, PageToken: first.Msg.NextPageToken}))
	if err == nil {
		t.Fatal("cursor changed observation")
	}
	f.complete(t, domain.ID(other.Msg.Job.Id), domain.Fail(domain.Unavailable, "Fixture failed.", "Inspect the retained observation."))
	failed, err := f.service.GetNativeModelObservation(f.ctx, connect.NewRequest(&pb.GetNativeModelObservationRequest{JobId: other.Msg.Job.Id}))
	if err != nil || failed.Msg.LastSuccess == nil || failed.Msg.LastSuccess.Id != string(id) {
		t.Fatal("failed discovery erased last success")
	}
	if err := f.service.Store.Close(); err != nil {
		t.Fatal(err)
	}
	f.service.Store, err = store.Open(f.ctx, f.root)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := f.service.DiscoverNativeModels(f.ctx, request)
	if err != nil || !replayed.Msg.Replayed || replayed.Msg.Job.Id != string(id) {
		t.Fatal("restart acceptance launched a replacement operation")
	}
	second, err := f.service.ListNativeModels(f.ctx, connect.NewRequest(&pb.ListNativeModelsRequest{JobId: string(id), PageSize: 200, PageToken: first.Msg.NextPageToken}))
	if err != nil {
		t.Fatal(err)
	}
	var models []domain.NativeModel
	if json.Unmarshal(second.Msg.ModelsJson, &models) != nil || len(models) != 1 || models[0].Model != "model-b" {
		t.Fatal("continuation moved to newer data")
	}
	after, err := f.service.Store.Get(f.ctx, domain.AccountKind, f.account)
	if err != nil || string(before.Data) != string(after.Data) || before.Revision != after.Revision {
		t.Fatal("observation changed account readiness or quota")
	}
	list, err := f.service.Store.List(f.ctx, store.Filter{Kind: domain.ModelKind, Limit: 50})
	if err != nil || len(list) != 0 {
		t.Fatal("discovery registered canonical models")
	}
	configuration := &pb.SaveConfigurationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID())}, Kind: pb.EntityKind_ENTITY_KIND_MODEL, SchemaVersion: 1}
	configuration.DocumentJson, _ = json.Marshal(domain.Model{Name: "Selected", ProviderID: f.provider, NativeID: models[0].Model, Harnesses: []domain.Harness{domain.Codex}, Manual: true, MetadataSource: domain.Unknown})
	saved, err := f.service.SaveConfiguration(f.ctx, connect.NewRequest(configuration))
	if err != nil {
		t.Fatal(err)
	}
	retry, err := f.service.SaveConfiguration(f.ctx, connect.NewRequest(configuration))
	if err != nil || !retry.Msg.Replayed || retry.Msg.Resource.Id != saved.Msg.Resource.Id {
		t.Fatal("explicit registration was not idempotent")
	}
	list, err = f.service.Store.List(f.ctx, store.Filter{Kind: domain.ModelKind, Limit: 50})
	if err != nil || len(list) != 1 {
		t.Fatal("registration did not retain exactly one manual model")
	}
	registered := list[0]
	later, err := f.service.DiscoverNativeModels(f.ctx, f.request())
	if err != nil {
		t.Fatal(err)
	}
	f.complete(t, domain.ID(later.Msg.Job.Id), domain.NativeModelFailure())
	preserved, err := f.service.Store.Get(f.ctx, domain.ModelKind, registered.ID)
	if err != nil || preserved.Revision != registered.Revision || string(preserved.Data) != string(registered.Data) {
		t.Fatal("subsequent failed discovery altered the registered manual entry")
	}
}

func TestNativeModelsSubscriptionRemainsExplicitlyUnsupported(t *testing.T) {
	f := newNativeModelsFixture(t)
	_, err := f.service.Store.Mutate(f.ctx, domain.NewID(), "fixture.subscription", nil, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.AccountKind, f.account)
		if err != nil {
			return nil, err
		}
		a, _ := store.Decode[domain.Account](r)
		a.Type = domain.SubscriptionAccount
		return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
	})
	if err != nil {
		t.Fatal(err)
	}
	request := f.request()
	request.Msg.AccountRevision = 2
	if _, err := f.service.DiscoverNativeModels(f.ctx, request); connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Fatalf("subscription scope did not return typed unsupported: %v", err)
	}
	jobs, err := f.service.Store.List(f.ctx, store.Filter{Kind: domain.JobKind, Limit: 50})
	if err != nil || len(jobs) != 0 {
		t.Fatal("unsupported subscription accepted native work")
	}
}

func TestNativeModelPublicationFencesAndWorkerDenial(t *testing.T) {
	for _, boundary := range []string{"account", "installation", "cancel", "actor"} {
		t.Run(boundary, func(t *testing.T) {
			f := newNativeModelsFixture(t)
			if boundary == "actor" {
				device := domain.NewID()
				_, err := f.service.Store.Mutate(f.ctx, domain.NewID(), "fixture.actor", nil, func(tx *store.Tx) (any, error) {
					return tx.Put(domain.DeviceKind, device, 0, "", "", domain.Device{Type: domain.ClientDevice, Name: "Client"})
				})
				if err != nil {
					t.Fatal(err)
				}
				f.ctx = domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.ClientDevice, DeviceID: device})
			}
			accepted, err := f.service.DiscoverNativeModels(f.ctx, f.request())
			if err != nil {
				t.Fatal(err)
			}
			owner := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
			_, err = f.service.Store.Mutate(owner, domain.NewID(), "fixture.fence", nil, func(tx *store.Tx) (any, error) {
				switch boundary {
				case "account":
					r, err := tx.Get(domain.AccountKind, f.account)
					if err != nil {
						return nil, err
					}
					a, _ := store.Decode[domain.Account](r)
					a.Connection = nil
					return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
				case "installation":
					r, err := tx.Get(domain.MachineKind, f.machine)
					if err != nil {
						return nil, err
					}
					m, _ := store.Decode[domain.Machine](r)
					m.DiscoveryRevision++
					return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
				case "cancel":
					return nil, tx.RequestJobCancellation(domain.ID(accepted.Msg.Job.Id))
				case "actor":
					actor, _ := domain.PrincipalFrom(f.ctx)
					r, err := tx.Get(domain.DeviceKind, actor.DeviceID)
					if err != nil {
						return nil, err
					}
					d, _ := store.Decode[domain.Device](r)
					d.Revoked = true
					return tx.Put(domain.DeviceKind, r.ID, r.Revision, "", "", d)
				}
				return nil, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			f.ctx = owner
			f.complete(t, domain.ID(accepted.Msg.Job.Id), nil)
			result, err := f.service.Store.Get(f.ctx, domain.JobKind, domain.ID(accepted.Msg.Job.Id))
			if err != nil {
				t.Fatal(err)
			}
			job, _ := store.Decode[domain.Job](result)
			if job.State == domain.JobSucceeded || len(job.Output) > 0 {
				t.Fatal("stale native observation published")
			}
			worker := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice, MachineID: f.machine})
			_, err = f.service.GetNativeModelObservation(worker, connect.NewRequest(&pb.GetNativeModelObservationRequest{JobId: accepted.Msg.Job.Id}))
			if err == nil {
				t.Fatal("Worker invoked owner observation control")
			}
		})
	}
}

func TestNativeModelsReportRequiresOriginalWorkerAssignment(t *testing.T) {
	f := newNativeModelsFixture(t)
	accepted, err := f.service.DiscoverNativeModels(f.ctx, f.request())
	if err != nil {
		t.Fatal(err)
	}
	device, instance := domain.NewID(), domain.NewID()
	var assignment store.Record
	_, err = f.service.Store.Mutate(f.ctx, domain.NewID(), "fixture.claim", nil, func(tx *store.Tx) (any, error) {
		if _, err := tx.Put(domain.DeviceKind, device, 0, "", "", domain.Device{Name: "Worker", Type: domain.WorkerDevice, MachineID: f.machine}); err != nil {
			return nil, err
		}
		record, job, _, err := nativeModelJob(tx, domain.ID(accepted.Msg.Job.Id))
		if err != nil {
			return nil, err
		}
		if err := tx.SetWorkerInstance(f.machine, instance, time.Now().UTC()); err != nil {
			return nil, err
		}
		job.State, job.InstanceID, job.AssignedDeviceID = domain.JobClaimed, instance, device
		assignment, err = tx.PutJob(record.ID, record.Revision, "", "", job)
		return assignment, err
	})
	if err != nil {
		t.Fatal(err)
	}
	observation := domain.NativeModelObservation{Version: 1, ObservedAt: time.Now().UTC(), CleanupVerified: true, Models: []domain.NativeModel{{ID: "picker", Model: "executable", DisplayName: "Fixture", Reasoning: []domain.NativeReasoningEffort{domain.NativeReasoningMedium}, DefaultReasoning: domain.NativeReasoningMedium, Modalities: []string{"text"}}}}
	raw, _ := json.Marshal(observation)
	request := &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: accepted.Msg.Job.Id, ExpectedRevision: assignment.Revision}, MachineId: string(f.machine), InstanceId: string(instance), OutputJson: raw}
	worker := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice, DeviceID: device, MachineID: f.machine})
	foreign := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.WorkerDevice, DeviceID: domain.NewID(), MachineID: f.machine})
	if _, err := f.service.ReportWork(foreign, connect.NewRequest(request)); err == nil {
		t.Fatal("foreign Worker device published")
	}
	request.InstanceId = string(domain.NewID())
	if _, err := f.service.ReportWork(worker, connect.NewRequest(request)); err == nil {
		t.Fatal("replacement Worker instance published")
	}
	request.InstanceId = string(instance)
	request.Mutation.ExpectedRevision--
	if _, err := f.service.ReportWork(worker, connect.NewRequest(request)); err == nil {
		t.Fatal("stale assignment revision published")
	}
	request.Mutation.ExpectedRevision++
	result, err := f.service.ReportWork(worker, connect.NewRequest(request))
	if err != nil {
		t.Fatal(err)
	}
	var public domain.Job
	if domain.Decode(result.Msg.Job.DocumentJson, &public) != nil || public.State != domain.JobSucceeded || strings.Contains(string(public.Output), "executable") {
		t.Fatal("complete observation publication leaked a private result body")
	}
	replayed, err := f.service.ReportWork(worker, connect.NewRequest(request))
	if err != nil || !replayed.Msg.Replayed {
		t.Fatal("report receipt did not retain the original observation")
	}
}
