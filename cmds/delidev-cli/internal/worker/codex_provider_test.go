// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func codexProviderFixture(t *testing.T, subscription bool, provider string) (checkpointFixture, *ExecutionPublisher, *openCodeBindingRPC) {
	t.Helper()
	f := newCheckpointFixture(t)
	f.input.Configuration.Subscription = subscription
	f.input.ConfigurationDigest, _ = f.input.Configuration.Digest()
	f.job.Input, _ = json.Marshal(f.input)
	f.ref.ConfigurationDigest = f.input.ConfigurationDigest
	f.ref.Subscription = subscription
	f.ref.AssignmentInputDigest = executionInputDigest(f.job.Input)
	f.bound.Effective.Provider = provider
	f.job.InstanceID, f.job.AcceptedAt = domain.NewID(), time.Now().UTC()
	raw, _ := json.Marshal(f.job)
	credential := Credential{Version: 1, Type: domain.WorkerDevice, Endpoint: "http://127.0.0.1:1", ServerID: domain.NewID(), DeviceID: domain.NewID(), PairingID: domain.NewID(), MachineID: f.input.MachineID, Token: base64.RawURLEncoding.EncodeToString(make([]byte, 32))}
	client := &openCodeBindingRPC{t: t}
	publisher, err := OpenExecutionPublisher(PublicationConfig{Root: f.root, Credential: credential, Instance: f.job.InstanceID, Assignment: &pb.Resource{Id: string(f.jobID), Revision: 2, Kind: pb.EntityKind_ENTITY_KIND_JOB, SchemaVersion: 1, SessionId: string(f.input.SessionID), DocumentJson: raw}, Client: client})
	if err != nil {
		t.Fatal(err)
	}
	client.publisher = publisher
	t.Cleanup(func() { _ = publisher.Close() })
	return f, publisher, client
}

func TestCodexPublicationProviderMatchesAcceptedAuthenticationProfile(t *testing.T) {
	for _, subscription := range []bool{false, true} {
		for _, provider := range []string{codex.APIProvider, "openai"} {
			name := "api/" + provider
			if subscription {
				name = "subscription/" + provider
			}
			t.Run(name, func(t *testing.T) {
				f, publisher, client := codexProviderFixture(t, subscription, provider)
				err := NewCodexEventPublisher(publisher).BindThread(context.Background(), f.bound)
				want := subscription == (provider == "openai")
				if want && (err != nil || len(client.events) != 1) {
					t.Fatal("accepted native provider could not publish its original thread", err)
				}
				if !want && (err == nil || len(client.events) != 0) {
					t.Fatal("foreign authentication profile gained publication authority")
				}
			})
		}
	}
}

func TestCodexCheckpointProviderMatchesAcceptedAuthenticationProfile(t *testing.T) {
	for _, subscription := range []bool{false, true} {
		for _, provider := range []string{codex.APIProvider, "openai"} {
			name := "api/" + provider
			if subscription {
				name = "subscription/" + provider
			}
			t.Run(name, func(t *testing.T) {
				f, _, _ := codexProviderFixture(t, subscription, provider)
				err := f.retain()
				want := subscription == (provider == "openai")
				if want {
					if err != nil {
						t.Fatal("accepted native provider could not retain its checkpoint", err)
					}
					if _, err := ReadCodexExecutionCheckpoint(f.root, f.ref); err != nil {
						t.Fatal("accepted provider checkpoint could not be read", err)
					}
					wrong := f.ref
					wrong.Subscription = !subscription
					if _, err := ReadCodexExecutionCheckpoint(f.root, wrong); err == nil {
						t.Fatal("checkpoint read ignored the accepted authentication profile")
					}
				} else if err == nil {
					t.Fatal("foreign authentication profile gained checkpoint authority")
				}
			})
		}
	}
}
