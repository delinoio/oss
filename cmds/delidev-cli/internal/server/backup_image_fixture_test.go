// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// Direct Service fixtures have no background maintenance loop. Drive the same
// durable creation job explicitly, then observe its original status RPC.
func requestBackupImageForTest(t *testing.T, s *Service, ctx context.Context, req *connect.Request[pb.RequestBackupRequest]) (*connect.Response[pb.RequestBackupResponse], error) {
	t.Helper()
	accepted, err := s.RequestBackup(ctx, req)
	if err != nil {
		return nil, err
	}
	if _, err := s.Store.RunBackupCreation(ctx, domain.ID(accepted.Msg.Job.Id), s.Identity.ServerID); err != nil {
		return nil, err
	}
	status, err := s.GetBackupCreation(ctx, connect.NewRequest(&pb.GetBackupCreationRequest{Id: accepted.Msg.Job.Id}))
	if err != nil {
		return nil, err
	}
	if status.Msg.Job.State != pb.BackupCreationState_BACKUP_CREATION_STATE_SUCCEEDED {
		t.Fatalf("backup creation did not succeed: %+v", status.Msg.Job)
	}
	accepted.Msg.Job = status.Msg.Job
	return accepted, nil
}
