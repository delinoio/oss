// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestWorkerProtocolMismatchPreservesMachineAndOriginalRequest(t *testing.T) {
	f := newAccountFixture(t)
	ctx := context.Background()
	worker, paired := pairedWorker(t, ctx, f.endpoint, f.identity)
	before, err := f.resources.GetResource(ctx, ownerRequest(f.identity, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_MACHINE, Id: paired.Machine.Id}))
	if err != nil {
		t.Fatal(err)
	}
	client := delidevv1connect.NewWorkerServiceClient(http.DefaultClient, f.endpoint.URL)
	request := &pb.AttachWorkerRequest{RequestId: string(domain.NewID()), MachineId: paired.Machine.Id, InstanceId: string(domain.NewID()), Version: "0.1.0"}
	for _, version := range []uint32{0, 1, 3} {
		request.ProtocolVersion = version
		_, err := client.AttachWorker(ctx, ownerRequest(worker, request))
		wantAccountCode(t, err, domain.Unsupported)
		after, err := f.resources.GetResource(ctx, ownerRequest(f.identity, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_MACHINE, Id: paired.Machine.Id}))
		if err != nil || after.Msg.Resource.Revision != before.Msg.Resource.Revision || !bytes.Equal(after.Msg.Resource.DocumentJson, before.Msg.Resource.DocumentJson) {
			t.Fatal("protocol rejection changed machine", err)
		}
	}
	request.ProtocolVersion = 2
	first, err := client.AttachWorker(ctx, ownerRequest(worker, request))
	if err != nil {
		t.Fatal(err)
	}
	retry, err := client.AttachWorker(ctx, ownerRequest(worker, request))
	if err != nil || retry.Msg.Machine.Id != first.Msg.Machine.Id || retry.Msg.Machine.Revision != first.Msg.Machine.Revision {
		t.Fatal("current protocol replay changed", err)
	}
}

func TestRemovedBackupRPCAndGenericAgentSaveAreUnavailable(t *testing.T) {
	f := newAccountFixture(t)
	request, err := http.NewRequest(http.MethodPost, f.endpoint.URL+"/delidev.v1.SystemService/CreateBackup", bytes.NewBufferString(`{"requestId":"`+string(domain.NewID())+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+f.identity.Token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatal("removed backup RPC remained callable", response.StatusCode)
	}
	_, err = f.config.SaveConfiguration(context.Background(), ownerRequest(f.identity, &pb.SaveConfigurationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID())}, Kind: pb.EntityKind_ENTITY_KIND_AGENT, SchemaVersion: 1, DocumentJson: []byte(`{}`)}))
	wantAccountCode(t, err, domain.Unsupported)
}

func TestAllVersionOneBundlesRejectBeforeConfigurationChanges(t *testing.T) {
	s, _ := newDoctorFixture(t)
	selection := transferSelection()
	selection.Bundle.Version = 1
	raw, _ := json.Marshal(selection)
	_, err := s.PreviewConfigurationImport(transferOwner(), connect.NewRequest(&pb.PreviewConfigurationImportRequest{SelectionJson: raw}))
	if err == nil {
		t.Fatal("API-only version-one bundle was accepted")
	}
	records, err := s.Store.List(transferOwner(), store.Filter{Kind: domain.TemplateKind, Limit: 10})
	if err != nil || len(records) != 0 {
		t.Fatal("rejected import changed configuration", err)
	}
}
