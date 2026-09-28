package cli

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func validateSessionPRResource(r *pb.Resource, session string) (domain.SessionPullRequest, error) {
	var value domain.SessionPullRequest
	if r == nil || r.Kind != pb.EntityKind_ENTITY_KIND_PULL_REQUEST || r.SchemaVersion != 1 || r.SessionId != session || domain.ID(r.Id).Validate() != nil || domain.ID(r.ProjectId).Validate() != nil || r.Revision == 0 || r.Revision >= 1<<63 || domain.Decode(r.DocumentJson, &value) != nil || value.Validate() != nil {
		return value, domain.Fail(domain.RecoveryRequired, "The PR association response is inconsistent.", "Inspect the selected session's current associations.")
	}
	return value, nil
}

func sessionPRCommand(ctx context.Context, c client, o options, args []string) (any, error) {
	if len(args) == 0 {
		return nil, usage()
	}
	action := args[0]
	f := flags("session pr " + action)
	session := f.String("id", "", "session UUID")
	var repository, number, association, page string
	var revision uint64
	var limit uint
	switch action {
	case "link":
		f.StringVar(&repository, "repository-id", "", "repository UUID from this session's project")
		f.StringVar(&number, "number", "", "canonical PR number")
	case "unlink", "get":
		f.StringVar(&association, "association-id", "", "retained association UUID")
		if action == "unlink" {
			f.Uint64Var(&revision, "revision", 0, "original association revision")
		}
	case "list":
		f.StringVar(&page, "page-token", "", "original next page token")
		f.UintVar(&limit, "limit", 50, "page size 1 through 100")
	default:
		return nil, usage()
	}
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	if err := domain.ID(*session).Validate(); err != nil {
		return nil, err
	}
	if action == "list" {
		if limit == 0 || limit > 100 {
			return nil, domain.Fail(domain.InvalidArgument, "Invalid association page size.", "Use 1 through 100.")
		}
		r, err := c.resources.ListResources(ctx, request(c, &pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_PULL_REQUEST, SessionId: *session, PageSize: uint32(limit), PageToken: page}}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		if len(r.Msg.Resources) > int(limit) {
			return nil, domain.Fail(domain.RecoveryRequired, "The association page exceeds its bound.", "Refresh the selected session.")
		}
		seen := map[string]bool{}
		for _, row := range r.Msg.Resources {
			if _, err := validateSessionPRResource(row, *session); err != nil {
				return nil, err
			}
			if seen[row.Id] {
				return nil, domain.Fail(domain.RecoveryRequired, "The association page repeats a record.", "Refresh the selected session.")
			}
			seen[row.Id] = true
		}
		return map[string]any{"associations": resourcesJSON(r.Msg.Resources), "next_page_token": r.Msg.NextPageToken}, nil
	}
	if action == "link" {
		if domain.ID(repository).Validate() != nil || !domain.PositiveDecimal(number) {
			return nil, domain.Fail(domain.InvalidArgument, "Invalid PR selection.", "Provide a project repository UUID and a canonical positive PR number.")
		}
		ensureRequest(&o)
		r, err := c.sessions.LinkSessionPullRequest(ctx, request(c, &pb.LinkSessionPullRequestRequest{RequestId: string(o.requestID), SessionId: *session, RepositoryId: repository, Number: number}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		value, err := validateSessionPRResource(r.Msg.Association, *session)
		if err != nil {
			return nil, err
		}
		if value.RepositoryID != domain.ID(repository) || value.Number != number || r.Msg.Association.Id != string(o.requestID) || r.Msg.RequestId != string(o.requestID) {
			return nil, domain.Fail(domain.RecoveryRequired, "The acknowledgment belongs to another PR link.", "Retry only the original request identity after inspecting current associations.")
		}
		return map[string]any{"association": resourceJSON(r.Msg.Association), "replayed": r.Msg.Replayed}, nil
	}
	if err := domain.ID(association).Validate(); err != nil {
		return nil, err
	}
	if action == "get" {
		r, err := c.resources.GetResource(ctx, request(c, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_PULL_REQUEST, Id: association}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		if _, err := validateSessionPRResource(r.Msg.Resource, *session); err != nil {
			return nil, err
		}
		if r.Msg.Resource.Id != association {
			return nil, domain.Fail(domain.RecoveryRequired, "The response belongs to another association.", "Select its original association ID.")
		}
		return resourceJSON(r.Msg.Resource), nil
	}
	if revision == 0 || revision >= 1<<63 {
		return nil, domain.Fail(domain.InvalidArgument, "Invalid association revision.", "Provide the original --revision.")
	}
	ensureRequest(&o)
	r, err := c.sessions.UnlinkSessionPullRequest(ctx, request(c, &pb.UnlinkSessionPullRequestRequest{SessionId: *session, Mutation: &pb.Mutation{RequestId: string(o.requestID), Id: association, ExpectedRevision: revision}}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	if r.Msg.Id != association || r.Msg.RequestId != string(o.requestID) {
		return nil, domain.Fail(domain.RecoveryRequired, "The unlink acknowledgment is inconsistent.", "Inspect current associations and retain the original request identity.")
	}
	return map[string]any{"id": r.Msg.Id, "deleted": true, "replayed": r.Msg.Replayed}, nil
}
