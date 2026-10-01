// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func browserCommand(ctx context.Context, c client, o options, args []string) (any, error) {
	if len(args) == 0 {
		return nil, domain.Fail(domain.MissingInput, "Select a browser profile operation.", "Use register, status, list, account-status or confirm-removal.")
	}
	f := flags("browser-profile " + args[0])
	id := f.String("id", "", "profile or session ID")
	account := f.String("account-id", "", "selected AI account")
	revision := f.Uint64("revision", 0, "original revision")
	deletion := f.String("deletion-request-id", "", "original account deletion request")
	page := f.String("page-token", "", "original scoped page")
	size := f.Uint("page-size", 50, "bounded page size")
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	switch args[0] {
	case "capabilities":
		response, err := c.browsers.GetBrowserCapabilities(ctx, request(c, &pb.GetBrowserCapabilitiesRequest{}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return response.Msg, nil
	case "register":
		r, err := c.browsers.RegisterBrowserProfile(ctx, request(c, &pb.RegisterBrowserProfileRequest{Session: &pb.Mutation{RequestId: string(o.requestID), Id: *id, ExpectedRevision: *revision}, AccountId: *account}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"profile": browserProfileJSON(r.Msg.Profile), "replayed": r.Msg.Replayed}, nil
	case "account-status":
		response, err := c.browsers.GetAccountBrowserCleanup(ctx, request(c, &pb.GetAccountBrowserCleanupRequest{AccountId: *account}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return response.Msg, nil
	case "status":
		r, err := c.browsers.GetBrowserProfile(ctx, request(c, &pb.GetBrowserProfileRequest{Id: *id}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"profile": browserProfileJSON(r.Msg.Profile)}, nil
	case "list":
		if *size > 200 {
			return nil, domain.Fail(domain.InvalidArgument, "Browser profile page exceeds its bound.", "Use at most 200 profiles.")
		}
		r, err := c.browsers.ListBrowserProfiles(ctx, request(c, &pb.ListBrowserProfilesRequest{PageSize: uint32(*size), PageToken: *page}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		profiles := make([]any, 0, len(r.Msg.Profiles))
		for _, p := range r.Msg.Profiles {
			profiles = append(profiles, browserProfileJSON(p))
		}
		return map[string]any{"profiles": profiles, "next_page_token": r.Msg.NextPageToken}, nil
	case "confirm-removal":
		r, err := c.browsers.ConfirmBrowserProfileRemoval(ctx, request(c, &pb.ConfirmBrowserProfileRemovalRequest{Mutation: &pb.Mutation{RequestId: string(o.requestID), Id: *id, ExpectedRevision: *revision}, DeletionRequestId: *deletion}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"profile": browserProfileJSON(r.Msg.Profile), "replayed": r.Msg.Replayed}, nil
	}
	return nil, domain.Fail(domain.InvalidArgument, "Unknown browser profile operation.", "Use register, status, list, account-status or confirm-removal.")
}

func browserProfileJSON(p *pb.BrowserProfile) any {
	if p == nil {
		return nil
	}
	state := domain.BrowserProfileState("")
	switch p.State {
	case pb.BrowserProfileState_BROWSER_PROFILE_STATE_ACTIVE:
		state = domain.BrowserProfileActive
	case pb.BrowserProfileState_BROWSER_PROFILE_STATE_REMOVAL_PENDING:
		state = domain.BrowserProfileRemovalPending
	case pb.BrowserProfileState_BROWSER_PROFILE_STATE_REMOVED:
		state = domain.BrowserProfileRemoved
	}
	return domain.BrowserProfileRecord{ID: domain.ID(p.Id), Revision: p.Revision, Data: domain.BrowserProfile{ServerID: domain.ID(p.ServerId), DeviceID: domain.ID(p.DeviceId), AccountID: domain.ID(p.AccountId), State: state, DeletionRequestID: domain.ID(p.DeletionRequestId)}}
}
