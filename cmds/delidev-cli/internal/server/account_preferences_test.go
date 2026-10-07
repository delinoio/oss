// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestAccountPreferenceTokensPreserveProtectedUint64(t *testing.T) {
	for _, revision := range []uint64{1, 9007199254740991, 9007199254740992, 9007199254740993, ^uint64(0)} {
		t.Run(strconv.FormatUint(revision, 10), func(t *testing.T) {
			f := newSubscriptionFixture(t)
			ctx := context.Background()
			// Seed only synthetic server-owned state. No native login, credentials,
			// quota observation or execution is performed by this fixture.
			_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.preference.lease", nil, func(tx *store.Tx) (any, error) {
				r, a, err := accountFromTx(tx, f.input.AccountID, 0)
				if err != nil {
					return nil, err
				}
				a.Alias, a.Enabled, a.ExcludeAutomatic, a.RecoveryNotifications = "Original", true, false, false
				a.Health = domain.AccountReady
				now, generation := time.Now().UTC(), domain.NewID()
				a.Connection = &domain.AccountConnection{ID: domain.NewID(), Authentication: domain.SubscriptionAuth, ConnectedAt: now}
				a.Subscription = &domain.SubscriptionState{Generation: generation, IdentityCommitment: strings.Repeat("a", 64), Lease: &domain.SubscriptionLease{ID: domain.NewID(), OperationID: domain.NewID(), Revision: revision, Action: domain.SubscriptionExecute, MachineID: f.input.MachineID, InstanceID: f.instance, DeviceID: f.device, Epoch: domain.NewID(), Generation: generation, StartedAt: now}}
				if err := a.Validate(); err != nil {
					return nil, err
				}
				return tx.Put(domain.AccountKind, r.ID, r.Revision, "", "", a)
			})
			if err != nil {
				t.Fatal(err)
			}
			client := delidevv1connect.NewConfigurationServiceClient(http.DefaultClient, f.http.URL)
			for _, patch := range [][2]string{{`"alias":"Original"`, `"alias":"Edited"`}, {`"enabled":true`, `"enabled":false`}, {`"exclude_automatic":false`, `"exclude_automatic":true`}, {`"recovery_notifications":false`, `"recovery_notifications":true`}} {
				r, _ := f.record()
				document := bytes.Replace(r.Data, []byte(patch[0]), []byte(patch[1]), 1)
				if bytes.Equal(document, r.Data) {
					t.Fatal("fixture preference patch was not applied")
				}
				request := &pb.SaveConfigurationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, SchemaVersion: 2, DocumentJson: document}
				result, err := client.SaveConfiguration(ctx, subscriptionRequest(f.service.Identity.Token, request))
				if err != nil {
					t.Fatal("lossless preference save rejected", err)
				}
				var saved domain.Account
				if err := domain.Decode(result.Msg.Resource.DocumentJson, &saved); err != nil || saved.Subscription.Lease.Revision != revision || !bytes.Contains(result.Msg.Resource.DocumentJson, []byte(`"revision":`+strconv.FormatUint(revision, 10))) {
					t.Fatal("protected lease revision changed", err)
				}
				replay, err := client.SaveConfiguration(ctx, subscriptionRequest(f.service.Identity.Token, request))
				if err != nil || !replay.Msg.Replayed || replay.Msg.Resource.Revision != result.Msg.Resource.Revision || !bytes.Equal(replay.Msg.Resource.DocumentJson, result.Msg.Resource.DocumentJson) {
					t.Fatal("original preference receipt did not replay exactly", err)
				}
			}

			r, _ := f.record()
			call := func(document []byte, expected uint64, token string) error {
				_, err := client.SaveConfiguration(ctx, subscriptionRequest(token, &pb.SaveConfigurationRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: expected}, Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, SchemaVersion: 2, DocumentJson: document}))
				return err
			}
			if err := call(r.Data, r.Revision-1, f.service.Identity.Token); connect.CodeOf(err) != connect.CodeAborted {
				t.Fatal("stale revision was not rejected", err)
			}
			if err := call(r.Data, r.Revision, f.workerToken); err != nil {
				t.Fatal("authenticated Worker preference write was rejected", err)
			}
			r, _ = f.record()
			for _, invalid := range []string{"0", "-1", "1.5", `"9007199254740993"`, "18446744073709551616", "9007199254740994", "9007199254740992"} {
				if invalid == strconv.FormatUint(revision, 10) {
					continue
				}
				bad := bytes.Replace(r.Data, []byte(`"revision":`+strconv.FormatUint(revision, 10)), []byte(`"revision":`+invalid), 1)
				if err := call(bad, r.Revision, f.service.Identity.Token); connect.CodeOf(err) != connect.CodeInvalidArgument {
					t.Fatalf("protected revision %s was not rejected: %v", invalid, err)
				}
			}
			// Malformed bytes may fail schema-family inference before JSON decode.
			if err := call(append(bytes.Clone(r.Data), '}'), r.Revision, f.service.Identity.Token); connect.CodeOf(err) != connect.CodeInvalidArgument && connect.CodeOf(err) != connect.CodeUnimplemented {
				t.Fatal("malformed document was not rejected", err)
			}
			current, _ := f.record()
			if current.Revision != r.Revision || !bytes.Equal(current.Data, r.Data) {
				t.Fatal("rejected preference write changed the resource")
			}
		})
	}
}
