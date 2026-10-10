// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"testing"
)

func TestHarnessDefaultsSaveRejectsDestructiveLegacyWrite(t *testing.T) {
	f := newAccountFixture(t)
	provider := f.save(pb.EntityKind_ENTITY_KIND_PROVIDER, domain.Provider{Name: "Harness defaults fixture", Endpoint: "http://127.0.0.1:12345/v1", Protocol: domain.OpenAIResponses, Authentication: domain.KeylessAuth})
	accounts := []*pb.Resource{wizardAccount(f, provider, "Original account")}
	request := wizardRequest(accounts, "original-model")
	var agent domain.Agent
	if err := domain.Decode(request.DocumentJson, &agent); err != nil {
		t.Fatal(err)
	}
	agent.HarnessSelection = &domain.HarnessSelection{}
	agent.Routes[0].ModelSelection = &domain.InheritedValue[domain.InlineModel]{State: domain.InheritValue}
	request.DocumentJson, _ = json.Marshal(agent)
	request.SchemaVersion = 5
	saved, err := f.config.SaveAgentWorker(context.Background(), ownerRequest(f.identity, request))
	if err != nil || saved.Msg.Resource.SchemaVersion != 5 {
		t.Fatalf("schema-5 save: %v", err)
	}
	stale := wizardRequest(accounts, "replacement-model")
	stale.Mutation.Id = saved.Msg.Resource.Id
	stale.Mutation.ExpectedRevision = saved.Msg.Resource.Revision
	if _, err = f.config.SaveAgentWorker(context.Background(), ownerRequest(f.identity, stale)); err == nil {
		t.Fatal("legacy write erased inheritance")
	}
	replay, err := f.config.SaveAgentWorker(context.Background(), ownerRequest(f.identity, request))
	if err != nil || !replay.Msg.Replayed || replay.Msg.Resource.Id != saved.Msg.Resource.Id {
		t.Fatalf("original save receipt lost: %v", err)
	}
}
