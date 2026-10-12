// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// Dedicated native reviews have independent jobs. Ordinary feedback submission
// retains its existing command and conversation input semantics.
func sessionNativeReview(ctx context.Context, c client, o options, args []string, streams IO) (any, error) {
	if len(args) == 0 {
		return nil, domain.Fail(domain.MissingInput, "Select a dedicated native review operation.", "Use create or get.")
	}
	action := args[0]
	f := flags("session native-review " + action)
	session := f.String("id", "", "original session UUID")
	job := f.String("job-id", "", "original review job UUID")
	revision := f.Uint64("revision", 0, "expected session revision")
	input := f.String("input", "-", "closed target JSON document, or - for stdin")
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	if err := domain.ID(*session).Validate(); err != nil {
		return nil, err
	}
	var review *pb.NativeCodeReview
	var replayed bool
	switch action {
	case "get":
		if err := domain.ID(*job).Validate(); err != nil {
			return nil, err
		}
		response, err := c.sessions.GetNativeCodeReview(ctx, request(c, &pb.GetNativeCodeReviewRequest{SessionId: *session, JobId: *job}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		review = response.Msg.Review
	case "create":
		if *revision == 0 {
			return nil, domain.Fail(domain.InvalidArgument, "The original session revision is required.", "Read the session before selecting its review target.")
		}
		if o.tokenStdin && *input == "-" {
			return nil, domain.Fail(domain.InvalidArgument, "Credential and document stdin cannot share one stream.", "Pass the target document with --input PATH.")
		}
		raw, err := readDocument(*input, streams.In)
		if err != nil {
			return nil, err
		}
		var target domain.NativeCodeReviewTarget
		if err = domain.Decode(raw, &target); err != nil {
			return nil, err
		}
		if err = target.Validate(); err != nil {
			return nil, err
		}
		ensureRequest(&o)
		response, err := c.sessions.CreateNativeCodeReview(ctx, request(c, &pb.CreateNativeCodeReviewRequest{Mutation: &pb.Mutation{RequestId: string(o.requestID), Id: *session, ExpectedRevision: *revision}, Target: rpc.NativeReviewTargetWire(target)}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		review, replayed = response.Msg.Review, response.Msg.Replayed
	default:
		return nil, domain.Fail(domain.InvalidArgument, "Unknown dedicated native review operation.", "Use create or get.")
	}
	if review == nil || review.Job == nil {
		return nil, domain.NativeCodeReviewUnavailable()
	}
	encoded, err := protojson.Marshal(review)
	if err != nil {
		return nil, err
	}
	var result any
	if err = json.Unmarshal(encoded, &result); err != nil {
		return nil, err
	}
	return map[string]any{"review": result, "replayed": replayed}, nil
}
