// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func setFixtureCapability(t *testing.T, f *continuationFixture, capability domain.WorkerCapability, present bool) {
	t.Helper()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "test.named-sidechat-capability", nil, func(tx *store.Tx) (any, error) {
		row, err := tx.Get(domain.MachineKind, domain.ID(f.machine.Id))
		if err != nil {
			return nil, err
		}
		m, err := store.Decode[domain.Machine](row)
		if err != nil {
			return nil, err
		}
		m.WorkerCapabilities = slices.DeleteFunc(m.WorkerCapabilities, func(c domain.WorkerCapability) bool { return c == capability })
		if present {
			m.WorkerCapabilities = append(m.WorkerCapabilities, capability)
		}
		return tx.Put(domain.MachineKind, row.ID, row.Revision, "", "", m)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A Sidechat child owns only reference metadata, but its source inspection still
// resolves the parent's frozen directory names. Claim-time authority must
// therefore notice a Worker that lost the named-directory capability.
func TestSidechatClaimAuthorityRequiresNamedDirectoryCapabilityForNamedSource(t *testing.T) {
	f := newContinuationFixture(t, domain.ExecutionSucceeded)
	f.control(t, pb.SessionAction_SESSION_ACTION_STOP)
	setFixtureCapability(t, f, domain.CodexReadOnlySidechatWorkerV1, true)
	setFixtureCapability(t, f, domain.NamedWorkspaceDirectoriesV1, false)
	parent := f.refresh(t)
	request := &pb.ForkSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(parent.ID), ExpectedRevision: parent.Revision}, ExpectedTurnId: string(f.turn), Name: "Read-only child", Purpose: pb.ForkPurpose_FORK_PURPOSE_SIDECHAT}
	accepted, err := sessionClient(f.accountFixture).ForkSession(context.Background(), ownerRequest(f.identity, request))
	if err != nil {
		t.Fatal(err)
	}
	_, input := forkClaimFixture(t, f, accepted.Msg.Job.Id)
	if input.Purpose != domain.SidechatFork {
		t.Fatal("not a Sidechat assignment", input.Purpose)
	}
	validate := func(input domain.ForkJobInput) error {
		return f.service.Store.Read(context.Background(), func(tx *store.Tx) error { return validateForkAuthority(tx, input) })
	}
	if err := validate(input); err != nil {
		t.Fatal("unnamed Sidechat source changed behavior", err)
	}
	var preparation workspace.PrepareRequest
	if domain.Decode(input.SourceAssignment.Preparation, &preparation) != nil {
		t.Fatal("source preparation missing")
	}
	// Freeze a repository directory name on the otherwise unchanged source.
	preparation.Repositories = append(preparation.Repositories, workspace.RepositorySpec{ID: domain.NewID(), DirectoryName: "oss"})
	named := input
	named.SourceAssignment.Preparation, _ = json.Marshal(preparation)
	if err := validate(named); domain.SafeError(err).Code != domain.Unsupported || domain.SafeError(err).Message != workspace.NamedDirectoriesUnsupported().Message {
		t.Fatal("named Sidechat source was not rejected without the capability", err)
	}
	setFixtureCapability(t, f, domain.NamedWorkspaceDirectoriesV1, true)
	if err := validate(named); err != nil {
		t.Fatal("negotiated Worker rejected a named Sidechat source", err)
	}
}
