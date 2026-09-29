package cli

import (
	"context"
	"encoding/json"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func backupOutput(value proto.Message) (any, error) {
	encoded, err := (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(value)
	return json.RawMessage(encoded), err
}

func backupCommand(ctx context.Context, c client, o options, args []string) (any, error) {
	if len(args) == 0 {
		return nil, usage()
	}
	f := flags("backup " + args[0])
	switch args[0] {
	case "create":
		wait := f.Bool("wait", false, "wait for the original durable job; interruption does not cancel it")
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		result, err := c.system.RequestBackup(ctx, request(c, &pb.RequestBackupRequest{RequestId: string(o.requestID)}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		if *wait {
			for result.Msg.Job != nil && result.Msg.Job.State == pb.BackupCreationState_BACKUP_CREATION_STATE_PENDING {
				timer := time.NewTimer(250 * time.Millisecond)
				select {
				case <-ctx.Done():
					timer.Stop()
					return nil, domain.SafeError(ctx.Err())
				case <-timer.C:
				}
				current, err := c.system.GetBackupCreation(ctx, request(c, &pb.GetBackupCreationRequest{Id: result.Msg.Job.Id}))
				if err != nil {
					return nil, rpc.ClientError(err)
				}
				result.Msg.Job = current.Msg.Job
			}
		}
		return backupOutput(result.Msg)
	case "creation":
		id := f.String("id", "", "original backup creation job UUID")
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		if err := domain.ID(*id).Validate(); err != nil {
			return nil, err
		}
		result, err := c.system.GetBackupCreation(ctx, request(c, &pb.GetBackupCreationRequest{Id: *id}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return backupOutput(result.Msg)
	case "creations":
		limit := f.Uint("limit", 20, "page size, from 1 to 99")
		page := f.String("page-token", "", "original creation page token")
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		if *limit < 1 || *limit > 99 {
			return nil, usage()
		}
		result, err := c.system.ListBackupCreations(ctx, request(c, &pb.ListBackupCreationsRequest{PageSize: uint32(*limit), PageToken: *page}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return backupOutput(result.Msg)
	case "list":
		limit := f.Uint("limit", 50, "page size, from 1 to 100")
		page := f.String("page-token", "", "original inventory page token")
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		if *limit < 1 || *limit > 100 {
			return nil, domain.Fail(domain.InvalidArgument, "Invalid backup page size.", "Use --limit between 1 and 100.")
		}
		result, err := c.system.ListBackups(ctx, request(c, &pb.ListBackupsRequest{PageSize: uint32(*limit), PageToken: *page}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return backupOutput(result.Msg)
	case "deletion":
		id := f.String("id", "", "original backup deletion job UUID")
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		if err := domain.ID(*id).Validate(); err != nil {
			return nil, err
		}
		result, err := c.system.GetBackupDeletion(ctx, request(c, &pb.GetBackupDeletionRequest{Id: *id}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return backupOutput(result.Msg)
	case "deletions":
		limit := f.Uint("limit", 20, "page size, from 1 to 99")
		page := f.String("page-token", "", "original deletion page token")
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		if *limit < 1 || *limit > 99 {
			return nil, usage()
		}
		result, err := c.system.ListBackupDeletions(ctx, request(c, &pb.ListBackupDeletionsRequest{PageSize: uint32(*limit), PageToken: *page}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return backupOutput(result.Msg)
	case "delete":
		id := f.String("id", "", "original managed backup UUID")
		revision := f.Uint64("expected-revision", 0, "original backup revision")
		size := f.Uint64("size-bytes", 0, "original inspected byte count")
		modified := f.String("modified-at", "", "original inspected timestamp")
		digest := f.String("sha256", "", "original inspected SHA-256")
		confirm := f.Bool("confirm", false, "confirm permanent deletion without cancellation")
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		if !*confirm {
			return nil, domain.Fail(domain.MissingInput, "Permanent backup deletion requires confirmation.", "Review the original inspection and supply --confirm; this cannot be undone.")
		}
		if err := domain.ID(*id).Validate(); err != nil {
			return nil, err
		}
		if _, err := time.Parse(time.RFC3339Nano, *modified); err != nil {
			return nil, usage()
		}
		result, err := c.system.DeleteBackup(ctx, request(c, &pb.DeleteBackupRequest{RequestId: string(o.requestID), Backup: &pb.ManagedBackup{Id: *id, Revision: *revision, SizeBytes: *size, ModifiedAt: *modified}, Sha256: *digest}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return backupOutput(result.Msg)
	case "inspect":
		id := f.String("id", "", "managed backup UUID")
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		if err := domain.ID(*id).Validate(); err != nil {
			return nil, err
		}
		result, err := c.system.InspectBackup(ctx, request(c, &pb.InspectBackupRequest{Id: *id}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return backupOutput(result.Msg)
	default:
		return nil, usage()
	}
}
