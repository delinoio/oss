// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func mutateStartupConsumerFixture(t *testing.T, f *authorityFixture, mutate func(*store.Tx) error) {
	t.Helper()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.startup-consumer", nil, func(tx *store.Tx) (any, error) { return nil, mutate(tx) })
	if err != nil {
		t.Fatal(err)
	}
}

func readyStartupConsumer(t *testing.T, f *authorityFixture) {
	t.Helper()
	ready := domain.ExecutionStartupObservation{State: domain.StartupReady, Phase: domain.StartupSettings, Harness: domain.Codex, NativeVersion: "fixture-observed-native-version", ExecutableSHA256: strings.Repeat("a", 64), Protocol: domain.CodexAppServer, CorrelationID: f.job, InputDelivery: domain.StartupNotSent}
	if _, err := f.client.ReportExecutionStartup(context.Background(), startupRequest(f, ready)); err != nil {
		t.Fatal(err)
	}
}

func nativeStartupRouteFixture(t *testing.T) (*authorityFixture, *pb.ReportWorkerNativeRouteRequest) {
	t.Helper()
	f := directStartupFixture(t)
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	key, routeID, profileID := domain.NewID(), domain.NewID(), domain.NewID()
	mutateStartupConsumerFixture(t, f, func(tx *store.Tx) error {
		profile := domain.NetworkProfile{ProxyDefinition: domain.ProxyDefinition{Name: "Fixture", Mode: domain.ProxyHTTP, Host: "127.0.0.1", Port: 46319}}
		if _, err := tx.Put(domain.NetworkProfileKind, profileID, 0, "", "", profile); err != nil {
			return err
		}
		_, err := tx.Put(domain.NetworkRouteKind, routeID, 0, "", "", domain.NetworkRoute{MachineID: f.input.MachineID, ProfileID: profileID, ProfileRevision: 1, Profile: profile, Binding: &domain.WorkerNetworkBinding{DeviceID: f.device, PairingID: domain.NewID(), KeyID: key, Recipient: identity.Recipient().String(), Endpoint: f.http.URL}})
		if err != nil {
			return err
		}
		mr, machine, err := activeMachine(tx, f.input.MachineID)
		if err != nil {
			return err
		}
		machine.WorkerCapabilities = append(machine.WorkerCapabilities, domain.NetworkBootstrapV1, domain.CodexAPIProxyV1)
		machine.Network = &domain.WorkerNetworkState{InstanceID: f.instance, KeyID: key, Recipient: identity.Recipient().String(), RouteID: routeID, EffectiveGeneration: 1, NativeState: domain.WorkerRouteNotApplied, ObservedAt: time.Now().UTC()}
		_, err = tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine)
		return err
	})
	f.registerGrant(t)
	return f, &pb.ReportWorkerNativeRouteRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.job), ExpectedRevision: 1}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), ExecutionId: string(f.input.ExecutionID), RouteId: string(routeID), Generation: 1, State: pb.WorkerNativeRouteState_WORKER_NATIVE_ROUTE_STATE_UNVERIFIED}
}

func TestV4NativeRouteInitializesBeforeReadyWithoutInferenceAuthority(t *testing.T) {
	f, request := nativeStartupRouteFixture(t)
	if !reflect.DeepEqual(f.input.Installation, domain.Installation{}) {
		t.Fatal("v4 fixture unexpectedly has legacy installation authority")
	}
	result, err := f.client.ReportWorkerNativeRoute(context.Background(), subscriptionRequest(f.workerToken, request))
	if err != nil || result.Msg.Replayed {
		t.Fatal("original pre-ready route was rejected", err)
	}
	if lease, err := f.service.executionAuthority.Acquire(context.Background(), f.token); err == nil {
		lease.Release()
		t.Fatal("pre-ready route granted inference")
	}
	replay, err := f.client.ReportWorkerNativeRoute(context.Background(), subscriptionRequest(f.workerToken, request))
	if err != nil || !replay.Msg.Replayed {
		t.Fatal("route receipt replay lost original identity", err)
	}
	readyStartupConsumer(t, f)
	lease, err := f.service.executionAuthority.Acquire(context.Background(), f.token)
	if err != nil {
		t.Fatal("ready original execution lost inference authority", err)
	}
	lease.Release()
	request.Mutation.RequestId = string(domain.NewID())
	request.State = pb.WorkerNativeRouteState_WORKER_NATIVE_ROUTE_STATE_OBSERVED
	if _, err := f.client.ReportWorkerNativeRoute(context.Background(), subscriptionRequest(f.workerToken, request)); err != nil {
		t.Fatal("original ready route could not report observation", err)
	}
}

func TestV4NativeRouteRejectsForeignAndUnsupportedOwners(t *testing.T) {
	for _, name := range []string{"stale-epoch", "foreign-execution", "stale-generation", "unsupported-proxy"} {
		t.Run(name, func(t *testing.T) {
			f, request := nativeStartupRouteFixture(t)
			switch name {
			case "stale-epoch":
				f.service.executionAuthority.epoch = domain.NewID()
			case "foreign-execution":
				request.ExecutionId = string(domain.NewID())
			case "stale-generation":
				request.Generation++
			case "unsupported-proxy":
				mutateStartupConsumerFixture(t, f, func(tx *store.Tx) error {
					mr, machine, err := activeMachine(tx, f.input.MachineID)
					if err != nil {
						return err
					}
					machine.WorkerCapabilities = []domain.WorkerCapability{domain.InlineModelExecutionV1, domain.ExecutionStartupV1, domain.NetworkBootstrapV1}
					_, err = tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine)
					return err
				})
			}
			if _, err := f.client.ReportWorkerNativeRoute(context.Background(), subscriptionRequest(f.workerToken, request)); err == nil {
				t.Fatal("unowned route was accepted")
			}
			if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
				_, err := tx.WorkerNativeRoute(f.job)
				if domain.SafeError(err).Code != domain.NotFound {
					t.Fatal("denial retained a new native route", err)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestV4SteerRequiresOriginalReadyStartupAndLiveAuthority(t *testing.T) {
	for _, name := range []string{"ready", "missing-startup", "missing-ready", "foreign-startup", "failed-startup", "stale-epoch", "foreign-turn", "stale-input", "disabled-account"} {
		t.Run(name, func(t *testing.T) {
			a := directStartupFixture(t)
			a.registerGrant(t)
			readyStartupConsumer(t, a)
			f, request := newSteerFixture(t, a)
			switch name {
			case "missing-startup", "missing-ready", "foreign-startup", "failed-startup":
				mutateStartupConsumerFixture(t, a, func(tx *store.Tx) error {
					sr, session, err := sessionRecord(tx, a.input.SessionID)
					if err != nil {
						return err
					}
					switch name {
					case "missing-startup":
						session.Startup = nil
					case "missing-ready":
						session.Startup.Ready = nil
					case "foreign-startup":
						session.Startup.ExecutionID = domain.NewID()
					case "failed-startup":
						session.Startup.Failure = &domain.ExecutionStartupObservation{State: domain.StartupFailed}
					}
					_, err = tx.Put(domain.SessionKind, sr.ID, sr.Revision, sr.ID, "", session)
					return err
				})
			case "stale-epoch":
				a.service.executionAuthority.epoch = domain.NewID()
			case "foreign-turn":
				request.ExpectedTurnId = string(domain.NewID())
			case "stale-input":
				request.Mutation.ExpectedRevision++
			case "disabled-account":
				mutateStartupConsumerFixture(t, a, func(tx *store.Tx) error {
					r, err := tx.Get(domain.AccountKind, a.input.AccountID)
					if err != nil {
						return err
					}
					account, err := store.Decode[domain.Account](r)
					if err != nil {
						return err
					}
					account.Enabled = false
					_, err = tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", account)
					return err
				})
			}
			response, err := callSteer(f, request)
			if name == "ready" {
				if err != nil {
					t.Fatal("original ready v4 execution could not steer", err)
				}
				claimSteer(t, f, response.Msg.Steer)
			} else {
				if err == nil {
					t.Fatal("invalid v4 authority accepted Steer")
				}
				r, err := a.service.Store.Get(context.Background(), domain.QueueKind, domain.ID(request.Mutation.Id))
				if err != nil {
					t.Fatal(err)
				}
				input, err := store.Decode[domain.QueuedInput](r)
				if err != nil || input.Delivery != domain.InputQueued {
					t.Fatal("denied Steer consumed the queued input", err)
				}
			}
		})
	}
}

func TestLegacySteerRetainsInstallationVersionAndProtocolGates(t *testing.T) {
	for _, name := range []string{"invalid-version-metadata", "unverified-protocol"} {
		t.Run(name, func(t *testing.T) {
			f, request := newSteerFixture(t)
			mutateStartupConsumerFixture(t, f.authorityFixture, func(tx *store.Tx) error {
				r, err := tx.Get(domain.JobKind, f.job)
				if err != nil {
					return err
				}
				job, err := store.Decode[domain.Job](r)
				if err != nil {
					return err
				}
				input := f.input
				if name == "invalid-version-metadata" {
					input.Installation.Version = ""
				} else {
					input.Installation.ProtocolVerified = false
				}
				job.Input, err = json.Marshal(input)
				if err != nil {
					return err
				}
				_, err = tx.Put(domain.JobKind, r.ID, r.Revision, r.SessionID, r.ProjectID, job)
				return err
			})
			if _, err := callSteer(f, request); err == nil {
				t.Fatal("legacy installation authority was bypassed")
			}
		})
	}
}
