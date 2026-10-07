// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestConfigurationDeletionRejectionLogsSafeOriginalIdentity(t *testing.T) {
	s, _ := newDoctorFixture(t)
	var logs bytes.Buffer
	s.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	id, account := doctorAccount(t, s, domain.APIAccount, domain.BearerAuth, true)
	account.Alias = "private-account-display"
	doctorPut(t, s, domain.AccountKind, id, 1, account)
	ctx := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	requestID, correlation := domain.NewID(), domain.NewID()
	request := connect.NewRequest(&pb.DeleteConfigurationRequest{Kind: pb.EntityKind_ENTITY_KIND_ACCOUNT, Mutation: &pb.Mutation{Id: string(id), ExpectedRevision: 2, RequestId: string(requestID)}})
	request.Header().Set(rpc.CorrelationHeader, string(correlation))
	_, err := s.DeleteConfiguration(ctx, request)
	wantAccountCode(t, err, domain.Conflict)
	var row map[string]any
	if err := json.Unmarshal(logs.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	if row["msg"] != "configuration_delete_rejected" || row["phase"] != string(configurationDeleteReferences) || row["error_code"] != string(domain.Conflict) || row["request_id"] != string(requestID) || row["correlation_id"] != string(correlation) {
		t.Fatal("original safe rejection metadata missing", row)
	}
	for _, private := range []string{account.Alias, string(id), string(account.Connection.ID), "Connected accounts require"} {
		if strings.Contains(logs.String(), private) {
			t.Fatal("rejection log disclosed account content")
		}
	}
}

func TestConfigurationDeletionRejectionOmitsMalformedIdentity(t *testing.T) {
	var logs bytes.Buffer
	s := &Service{logger: slog.New(slog.NewJSONHandler(&logs, nil))}
	request := connect.NewRequest(&pb.DeleteConfigurationRequest{Mutation: &pb.Mutation{RequestId: "private-unvalidated-request"}})
	request.Header().Set(rpc.CorrelationHeader, "private-unvalidated-correlation")
	_, err := s.DeleteConfiguration(context.Background(), request)
	wantAccountCode(t, err, domain.MissingInput)
	if strings.Contains(logs.String(), "private-unvalidated") {
		t.Fatal("malformed input entered rejection log")
	}
}
