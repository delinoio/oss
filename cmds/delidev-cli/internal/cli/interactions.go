package cli

import (
	"context"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func respondQuestion(ctx context.Context, c client, o options, args []string, streams IO) (any, error) {
	f := flags("interaction respond")
	id := f.String("id", "", "original question interaction")
	revision := f.Uint64("revision", 0, "current interaction revision")
	input := f.String("input", "-", "non-secret answer document")
	if err := parse(f, args); err != nil {
		return nil, err
	}
	if *id == "" || *revision == 0 {
		return nil, domain.Fail(domain.MissingInput, "A question response requires its original interaction ID and revision.", "Inspect the question with interaction get, then supply --id and --revision.")
	}
	if err := domain.ID(*id).Validate(); err != nil {
		return nil, err
	}
	if o.tokenStdin && *input == "-" {
		return nil, domain.Fail(domain.InvalidArgument, "Credential stdin and question responses cannot share one stream.", "Pass the non-secret response document using --input PATH.")
	}
	raw, err := readDocument(*input, streams.In)
	if err != nil {
		return nil, err
	}
	var value domain.QuestionResponseInput
	if err := domain.Decode(raw, &value); err != nil {
		return nil, err
	}
	original, err := c.resources.GetResource(ctx, request(c, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_INTERACTION, Id: *id}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	var question domain.ExecutionInteraction
	if original.Msg.Resource == nil || original.Msg.Resource.Id != *id || original.Msg.Resource.Kind != pb.EntityKind_ENTITY_KIND_INTERACTION || original.Msg.Resource.SchemaVersion != 1 || domain.Decode(original.Msg.Resource.DocumentJson, &question) != nil || question.Type != domain.UserQuestionInteraction {
		return nil, domain.Fail(domain.Unsupported, "This interaction is not a supported user question.", "Use its dedicated native interaction operation; question answers never grant approval.")
	}
	if err := value.Validate(question.Questions); err != nil {
		return nil, err
	}
	raw, err = json.Marshal(value)
	if err != nil {
		return nil, domain.SafeError(err)
	}
	// Do not reject an old revision locally: its exact durable receipt can be
	// retried after native closure and must return current state without resend.
	response, err := c.interactions.RespondQuestion(ctx, request(c, &pb.RespondQuestionRequest{Mutation: &pb.Mutation{RequestId: string(o.requestID), Id: *id, ExpectedRevision: *revision}, ResponseJson: raw}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	return map[string]any{"interaction": resourceJSON(response.Msg.Interaction), "replayed": response.Msg.Replayed}, nil
}
