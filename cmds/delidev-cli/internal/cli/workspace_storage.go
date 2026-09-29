package cli

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func workspaceStorageCommand(ctx context.Context, c client, o options, args []string) (any, error) {
	if len(args) == 0 {
		return nil, usage()
	}
	f := flags("storage " + args[0])
	if args[0] == "cancel" {
		id := f.String("id", "", "original accepted storage job UUID")
		revision := f.Uint64("expected-revision", 0, "current original job revision")
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		if domain.ID(*id).Validate() != nil || *revision == 0 {
			return nil, usage()
		}
		result, err := c.storage.CancelWorkspaceStorageOperation(ctx, request(c, &pb.CancelWorkspaceStorageOperationRequest{Mutation: &pb.Mutation{RequestId: string(o.requestID), Id: *id, ExpectedRevision: *revision}}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return backupOutput(result.Msg)
	}
	if args[0] == "operation" {
		id := f.String("id", "", "original accepted storage job UUID")
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		if err := domain.ID(*id).Validate(); err != nil {
			return nil, err
		}
		result, err := c.storage.GetWorkspaceStorageOperation(ctx, request(c, &pb.GetWorkspaceStorageOperationRequest{Id: *id}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return backupOutput(result.Msg)
	}
	action := pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_UNSPECIFIED
	switch args[0] {
	case "preview":
		action = pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_PREVIEW
	case "create":
		action = pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_CREATE
	case "cleanup":
		action = pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_CLEANUP
	case "inspect":
		action = pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_INSPECT
	case "restore":
		action = pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_RESTORE
	case "recover":
		action = pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_RECOVER
	case "delete":
		action = pb.WorkspaceStorageAction_WORKSPACE_STORAGE_ACTION_DELETE
	default:
		return nil, usage()
	}
	session := f.String("session-id", "", "owning session UUID")
	revision := f.Uint64("expected-revision", 0, "exact current session revision")
	snapshot := f.String("snapshot-id", "", "original snapshot UUID for inspect/restore/delete")
	preview := f.String("preview-job-id", "", "exact successful cleanup preview job")
	recovery := f.String("recovery-job-id", "", "original uncertain operation to inspect and reconcile")
	confirm := f.Bool("confirm", false, "confirm source cleanup or permanent snapshot deletion")
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	if domain.ID(*session).Validate() != nil || *revision == 0 {
		return nil, usage()
	}
	if (args[0] == "cleanup" || args[0] == "delete") && !*confirm {
		return nil, domain.Fail(domain.MissingInput, "Storage removal requires explicit confirmation.", "Inspect the original preview/snapshot and supply --confirm.")
	}
	result, err := c.storage.RequestWorkspaceStorage(ctx, request(c, &pb.RequestWorkspaceStorageRequest{Mutation: &pb.Mutation{RequestId: string(o.requestID), Id: *session, ExpectedRevision: *revision}, Action: action, SnapshotId: *snapshot, PreviewJobId: *preview, RecoveryJobId: *recovery}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	return backupOutput(result.Msg)
}
