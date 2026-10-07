// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestBrowserInventoryIsAbsentFromGenericDeviceResponses(t *testing.T) {
	f := newBrowserFixture(t)
	first := f.register(t, f.first, f.session, domain.NewID())
	second := f.register(t, f.second, f.session, domain.NewID())
	deletion := domain.NewID()
	if _, err := f.s.Store.Mutate(f.first, deletion, "browser.fixture.account-delete", nil, func(tx *store.Tx) (any, error) {
		return nil, tx.Delete(domain.AccountKind, f.account, 1)
	}); err != nil {
		t.Fatal(err)
	}
	pending, err := f.s.GetBrowserProfile(f.first, connect.NewRequest(&pb.GetBrowserProfileRequest{Id: first.Profile.Id}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.ConfirmBrowserProfileRemoval(f.first, connect.NewRequest(&pb.ConfirmBrowserProfileRemovalRequest{
		Mutation: &pb.Mutation{Id: first.Profile.Id, ExpectedRevision: pending.Msg.Profile.Revision, RequestId: string(domain.NewID())}, DeletionRequestId: string(deletion),
	})); err != nil {
		t.Fatal(err)
	}

	identities := []security.Identity{f.s.Identity}
	deviceIDs := []domain.ID{}
	for _, ctx := range []context.Context{f.first, f.second} {
		actor, _ := domain.PrincipalFrom(ctx)
		deviceIDs = append(deviceIDs, actor.DeviceID)
		token := string(domain.NewID())
		digest := sha256.Sum256([]byte(token))
		if _, err := f.s.Store.Mutate(context.Background(), domain.NewID(), "browser.fixture.credential", nil, func(tx *store.Tx) (any, error) {
			return nil, tx.PutCredential(actor.DeviceID, digest[:])
		}); err != nil {
			t.Fatal(err)
		}
		identities = append(identities, security.Identity{Token: token})
	}
	before := map[domain.ID]store.Record{}
	for _, id := range deviceIDs {
		before[id], err = f.s.Store.Get(context.Background(), domain.DeviceKind, id)
		if err != nil {
			t.Fatal(err)
		}
	}
	host := httptest.NewServer(f.s.Handler(nil, true))
	defer host.Close()
	check := func(resource *pb.Resource) {
		t.Helper()
		var document map[string]json.RawMessage
		if resource == nil || json.Unmarshal(resource.DocumentJson, &document) != nil || document["type"] == nil || document["revoked"] == nil {
			t.Fatal("ordinary device metadata was lost", resource)
		}
		if _, exists := document["browser_profiles"]; exists {
			t.Fatal("generic response exposed device-scoped browser inventory")
		}
		for _, private := range []string{first.Profile.Id, second.Profile.Id, string(f.account), string(deletion)} {
			if bytes.Contains(resource.DocumentJson, []byte(private)) {
				t.Fatal("generic response exposed a browser ownership/removal identity")
			}
		}
	}
	for _, options := range [][]connect.ClientOption{nil, {connect.WithProtoJSON()}} {
		resources := delidevv1connect.NewResourceServiceClient(host.Client(), host.URL, options...)
		for _, identity := range identities {
			for _, id := range deviceIDs {
				response, err := resources.GetResource(context.Background(), ownerRequest(identity, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_DEVICE, Id: string(id)}))
				if err != nil {
					t.Fatal(err)
				}
				check(response.Msg.Resource)
			}
			page, err := resources.ListResources(context.Background(), ownerRequest(identity, &pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_DEVICE}}))
			if err != nil || len(page.Msg.Resources) != 2 {
				t.Fatal(page, err)
			}
			for _, resource := range page.Msg.Resources {
				check(resource)
			}
			snapshot, err := resources.GetSnapshot(context.Background(), ownerRequest(identity, &pb.GetSnapshotRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_DEVICE}}))
			if err != nil || len(snapshot.Msg.Resources) != 2 || snapshot.Msg.Cursor == "" {
				t.Fatal(snapshot, err)
			}
			for _, resource := range snapshot.Msg.Resources {
				check(resource)
			}
		}
	}
	for _, id := range deviceIDs {
		after, err := f.s.Store.Get(context.Background(), domain.DeviceKind, id)
		if err != nil || after.Revision != before[id].Revision || !bytes.Equal(after.Data, before[id].Data) {
			t.Fatal("resource projection changed the original stored device", err)
		}
	}
	if profileBody(t, f.s, f.first, first.Profile.Id).State != domain.BrowserProfileRemoved || profileBody(t, f.s, f.second, second.Profile.Id).State != domain.BrowserProfileRemovalPending {
		t.Fatal("dedicated browser inventory was lost")
	}
	if _, err := f.s.GetBrowserProfile(f.second, connect.NewRequest(&pb.GetBrowserProfileRequest{Id: first.Profile.Id})); err != nil {
		t.Fatal("authenticated cross-device browser read failed", err)
	}
	devices := delidevv1connect.NewDeviceServiceClient(host.Client(), host.URL)
	revoked, err := devices.RevokeDevice(context.Background(), ownerRequest(f.s.Identity, &pb.RevokeDeviceRequest{Mutation: &pb.Mutation{
		Id: string(deviceIDs[0]), ExpectedRevision: before[deviceIDs[0]].Revision, RequestId: string(domain.NewID()),
	}}))
	if err != nil {
		t.Fatal(err)
	}
	check(revoked.Msg.Device)
}

func TestBrowserDeviceProjectionPreservesUnrelatedFieldsAndRejectsInvalidJSON(t *testing.T) {
	original := []byte(`{"name":"device","type":"client","revoked":false,"future_field":{"value":17},"browser_profiles":[{"private":"identity"}]}`)
	projected, err := resourceProjection(store.Record{Kind: domain.DeviceKind, Data: original})
	if err != nil || !bytes.Contains(projected.DocumentJson, []byte(`"future_field":{"value":17}`)) || bytes.Contains(projected.DocumentJson, []byte("private")) {
		t.Fatal(projected, err)
	}
	if !bytes.Contains(original, []byte("browser_profiles")) {
		t.Fatal("projection modified original bytes")
	}
	for _, raw := range []string{`{`, `null`, `[]`} {
		if _, err = resourceProjection(store.Record{Kind: domain.DeviceKind, Data: []byte(raw)}); domain.SafeError(err).Code != domain.RecoveryRequired {
			t.Fatal("invalid device metadata was returned", raw, err)
		}
	}
}
