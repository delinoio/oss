// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// Do not print private job documents or identities. A receipt lookup observes
// the original actor-bound attempt and never repeats a native mutation.
func directoryOperationView(op *pb.SessionDirectoryOperation, session, requestID string, repository, relative string, roots []domain.WorkspaceRoot) (any, error) {
	if op == nil || op.SessionId != session || op.RequestId != requestID || op.Job == nil || op.Job.Kind != pb.EntityKind_ENTITY_KIND_JOB || op.Job.SessionId != session || domain.ID(op.Job.Id).Validate() != nil || op.Job.Revision == 0 || op.Job.SchemaVersion != 1 {
		return nil, domain.DirectoryUncertain()
	}
	var job domain.Job
	if domain.Decode(op.Job.DocumentJson, &job) != nil || job.Type != domain.ChangeSessionDirectoryJob || len(job.Input) != 0 || len(job.Output) != 0 {
		return nil, domain.DirectoryUncertain()
	}
	switch job.State {
	case domain.JobQueued, domain.JobClaimed, domain.JobUncertain, domain.JobFailed, domain.JobCanceled, domain.JobSucceeded:
	default:
		return nil, domain.DirectoryUncertain()
	}
	view := map[string]any{"state": job.State, "verified": false}
	if op.Generation != nil {
		g := op.Generation
		seen := map[string]bool{}
		for _, id := range []string{g.GenerationId, g.JobId, g.RequestId, g.SourceExecutionId} {
			if domain.ID(id).Validate() != nil || seen[id] {
				return nil, domain.DirectoryUncertain()
			}
			seen[id] = true
		}
		if job.State != domain.JobSucceeded || g.JobId != op.Job.Id || g.RequestId != requestID || domain.ValidateSessionDirectoryPath(g.RelativePath) != nil || g.PreviousGenerationId == g.GenerationId || g.PreviousGenerationId != "" && domain.ID(g.PreviousGenerationId).Validate() != nil || repository != "" && g.RepositoryId != repository || relative != "" && g.RelativePath != relative {
			return nil, domain.DirectoryUncertain()
		}
		name := ""
		for _, root := range roots {
			if string(root.RepositoryID) == g.RepositoryId {
				name = root.Name
			}
		}
		if name == "" || strings.ContainsAny(name, "/\\\x00") || domain.ID(name).Validate() == nil {
			return nil, domain.DirectoryUncertain()
		}
		view["repository"], view["relative_path"], view["verified"] = name, g.RelativePath, true
	} else if job.State == domain.JobSucceeded {
		return nil, domain.DirectoryUncertain()
	}
	return view, nil
}

func sessionDirectoryCommand(ctx context.Context, c client, o options, args []string) (any, error) {
	if len(args) == 0 || args[0] != "change" && args[0] != "operation" {
		return nil, usage()
	}
	f := flags("session directory " + args[0])
	id := f.String("id", "", "original session UUID")
	revision := new(uint64)
	repository, path := new(string), new(string)
	if args[0] == "change" {
		revision = f.Uint64("revision", 0, "exact current session revision")
		repository = f.String("repository-id", "", "original prepared repository; omit for General Chat")
		path = f.String("path", "", "canonical relative directory")
	}
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	if domain.ID(*id).Validate() != nil {
		return nil, usage()
	}
	if !o.requestIDExplicit || o.requestID.Validate() != nil {
		return nil, domain.Fail(domain.MissingInput, "Directory control requires an explicit original request ID.", "Provide --request-id; after an uncertain change, use directory operation with that same ID.")
	}
	if args[0] == "change" {
		if *revision == 0 {
			return nil, domain.Fail(domain.MissingInput, "Directory change requires the exact session revision.", "Provide --revision; do not invent a revision after an uncertain response.")
		}
		if *repository != "" && domain.ID(*repository).Validate() != nil {
			return nil, usage()
		}
		if err := domain.ValidateSessionDirectoryPath(*path); err != nil {
			return nil, err
		}
	}
	var op *pb.SessionDirectoryOperation
	if args[0] == "change" {
		r, err := c.directories.ChangeSessionDirectory(ctx, request(c, &pb.ChangeSessionDirectoryRequest{Mutation: &pb.Mutation{Id: *id, ExpectedRevision: *revision, RequestId: string(o.requestID)}, RepositoryId: *repository, RelativePath: *path}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		op = r.Msg.Operation
	} else {
		r, err := c.directories.GetSessionDirectoryOperation(ctx, request(c, &pb.GetSessionDirectoryOperationRequest{SessionId: *id, RequestId: string(o.requestID)}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		op = r.Msg.Operation
	}
	// Safe labels come from the original prepared-root read, never a live project
	// catalog that could rename or replace the native workspace authority.
	var roots []domain.WorkspaceRoot
	if op != nil && op.Generation != nil {
		raw, _ := json.Marshal(domain.WorkspaceReadQuery{Operation: domain.WorkspaceRoots})
		r, err := c.sessions.ReadSessionWorkspace(ctx, request(c, &pb.ReadSessionWorkspaceRequest{SessionId: *id, QueryJson: raw}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		var result domain.WorkspaceReadResult
		if domain.Decode(r.Msg.DocumentJson, &result) != nil || result.Validate(domain.WorkspaceReadQuery{Operation: domain.WorkspaceRoots}) != nil {
			return nil, domain.DirectoryUncertain()
		}
		roots = result.Roots
	}
	return directoryOperationView(op, *id, string(o.requestID), *repository, *path, roots)
}
