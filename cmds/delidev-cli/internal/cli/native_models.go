// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func nativeModelCatalog(ctx context.Context, c client, o options, args []string) (any, error) {
	f := flags("model " + args[0])
	id := f.String("id", "", "original observation job ID")
	revision := f.Uint64("revision", 0, "current machine or job revision")
	if args[0] == "native-discover" {
		machine := f.String("machine-id", "", "selected Runner Device")
		account := f.String("account-id", "", "selected connected account")
		accountRevision := f.Uint64("account-revision", 0, "current account revision")
		hidden := f.Bool("include-hidden", false, "include hidden native picker entries")
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		response, err := c.nativeModels.DiscoverNativeModels(ctx, request(c, &pb.DiscoverNativeModelsRequest{Mutation: &pb.Mutation{RequestId: string(o.requestID), Id: *machine, ExpectedRevision: *revision}, AccountId: *account, AccountRevision: *accountRevision, IncludeHidden: *hidden}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return nativeModelChangeJSON(response.Msg), nil
	}
	if args[0] == "native-list" {
		limit := f.Uint64("limit", 50, "page size, 1 through 200")
		page := f.String("page-token", "", "observation-bound cursor")
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		if *limit < 1 || *limit > 200 {
			return nil, domain.NativeModelFailure()
		}
		response, err := c.nativeModels.ListNativeModels(ctx, request(c, &pb.ListNativeModelsRequest{JobId: *id, PageSize: uint32(*limit), PageToken: *page}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		var models []domain.NativeModel
		if json.Unmarshal(response.Msg.ModelsJson, &models) != nil {
			return nil, domain.NativeModelFailure()
		}
		return map[string]any{"job": resourceJSON(response.Msg.Job), "models": models, "next_page_token": response.Msg.NextPageToken}, nil
	}
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	var response nativeModelChangeView
	if args[0] == "native-cancel" {
		result, err := c.nativeModels.CancelNativeModelDiscovery(ctx, request(c, &pb.CancelNativeModelDiscoveryRequest{Mutation: &pb.Mutation{RequestId: string(o.requestID), Id: *id, ExpectedRevision: *revision}}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		response = result.Msg
	} else {
		result, err := c.nativeModels.GetNativeModelObservation(ctx, request(c, &pb.GetNativeModelObservationRequest{JobId: *id}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		response = result.Msg
	}
	value := nativeModelChangeJSON(response)
	var job domain.Job
	if response.GetJob() != nil && domain.Decode(response.GetJob().DocumentJson, &job) == nil && job.Problem != nil {
		return value, job.Problem
	}
	return value, nil
}

type nativeModelChangeView interface {
	GetJob() *pb.Resource
	GetLastSuccess() *pb.Resource
	GetReplayed() bool
}

func nativeModelChangeJSON(result nativeModelChangeView) any {
	return map[string]any{"job": resourceJSON(result.GetJob()), "last_success": resourceJSON(result.GetLastSuccess()), "replayed": result.GetReplayed()}
}
