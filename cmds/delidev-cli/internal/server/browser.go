// SPDX-License-Identifier: Apache-2.0
package server

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"time"
)

func browserActor(ctx context.Context) (domain.Principal, error) {
	p, ok := domain.PrincipalFrom(ctx)
	if !ok || p.Type != domain.ClientDevice || p.DeviceID.Validate() != nil {
		return p, domain.Fail(domain.PermissionDenied, "Browser profiles require this computer's paired client identity.", "Use the desktop client scope; owner and Worker credentials cannot represent a browser device.")
	}
	return p, nil
}
func (s *Service) browserProfile(ctx context.Context, id domain.ID) (domain.BrowserProfileRecord, error) {
	actor, err := browserActor(ctx)
	if err != nil {
		return domain.BrowserProfileRecord{}, err
	}
	var r domain.BrowserProfileRecord
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		var err error
		r, err = tx.BrowserProfile(actor.DeviceID, id)
		if err != nil {
			return err
		}
		p := r.Data
		if err = p.Validate(); err != nil {
			return err
		}
		if p.ServerID != s.Identity.ServerID || p.DeviceID != actor.DeviceID {
			return domain.Fail(domain.PermissionDenied, "This browser profile belongs to another device.", "Use the original server and paired client.")
		}
		return nil
	})
	return r, err
}
func (s *Service) RegisterBrowserProfile(ctx context.Context, req *connect.Request[pb.RegisterBrowserProfileRequest]) (*connect.Response[pb.RegisterBrowserProfileResponse], error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, err := browserActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	m := req.Msg.Session
	if m == nil || domain.ID(m.Id).Validate() != nil || m.ExpectedRevision == 0 || domain.ID(req.Msg.AccountId).Validate() != nil {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Select the current session revision and its selected account.", "Read the session before opening its shared browser."), correlation)
	}
	identity := struct {
		Actor    domain.Principal
		Session  domain.ID
		Revision uint64
		Account  domain.ID
	}{actor, domain.ID(m.Id), m.ExpectedRevision, domain.ID(req.Msg.AccountId)}
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "browser.register", identity, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.SessionKind, identity.Session)
		if err != nil {
			return nil, err
		}
		if r.Revision != identity.Revision {
			return nil, domain.Fail(domain.Conflict, "The session revision changed.", "Reload the session before opening its browser.")
		}
		session, err := store.Decode[domain.Session](r)
		if err != nil {
			return nil, err
		}
		if session.ExecutionSelection().AccountID != identity.Account {
			return nil, domain.Fail(domain.PermissionDenied, "The browser account is not this session's selected account.", "Use the original currently selected AI account.")
		}
		if _, err = tx.Get(domain.AccountKind, identity.Account); err != nil {
			return nil, err
		}
		existing, err := tx.FindBrowserProfile(actor.DeviceID, identity.Account)
		if err == nil {
			p := existing.Data
			if err = p.Validate(); err != nil {
				return nil, err
			}
			if p.State != domain.BrowserProfileActive {
				return nil, domain.Fail(domain.Conflict, "Browser profile removal is irrevocably pending.", "Finish the original device cleanup; this profile cannot be reopened.")
			}
			return struct {
				ID domain.ID `json:"id"`
			}{existing.ID}, nil
		}
		if domain.SafeError(err).Code != domain.NotFound {
			return nil, err
		}
		profile, err := tx.PutBrowserProfile(domain.BrowserProfileRecord{ID: domain.NewID(), Revision: 1, Data: domain.BrowserProfile{ServerID: s.Identity.ServerID, DeviceID: actor.DeviceID, AccountID: identity.Account, State: domain.BrowserProfileActive}}, 0)
		return struct {
			ID domain.ID `json:"id"`
		}{profile.ID}, err
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var ref struct {
		ID domain.ID `json:"id"`
	}
	if json.Unmarshal(result.Data, &ref) != nil {
		return nil, rpc.Error(domain.Fail(domain.RecoveryRequired, "The browser receipt is invalid.", "Preserve the original request."), correlation)
	}
	r, err := s.browserProfile(ctx, ref.ID)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "browser_profile_registered", "request_id", m.RequestId, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.RegisterBrowserProfileResponse{Profile: browserResource(r), RequestId: m.RequestId, Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) GetBrowserProfile(ctx context.Context, req *connect.Request[pb.GetBrowserProfileRequest]) (*connect.Response[pb.GetBrowserProfileResponse], error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	r, err := s.browserProfile(ctx, domain.ID(req.Msg.Id))
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	response := connect.NewResponse(&pb.GetBrowserProfileResponse{Profile: browserResource(r)})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) ListBrowserProfiles(ctx context.Context, req *connect.Request[pb.ListBrowserProfilesRequest]) (*connect.Response[pb.ListBrowserProfilesResponse], error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, err := browserActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	limit := int(req.Msg.PageSize)
	if limit == 0 {
		limit = 50
	}
	if limit > store.MaxPage {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Browser profile page exceeds its bound.", "Use at most 200 profiles."), correlation)
	}
	scope := "browser:" + string(s.Identity.ServerID) + ":" + string(actor.DeviceID)
	var after domain.ID
	if req.Msg.PageToken != "" {
		cursor, err := s.Identity.DecodeCursor(req.Msg.PageToken, scope)
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
		after = cursor.After
	}
	var records []domain.BrowserProfileRecord
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		var err error
		records, err = tx.BrowserProfiles(actor.DeviceID, after, limit+1)
		return err
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	response := &pb.ListBrowserProfilesResponse{}
	if len(records) > limit {
		records = records[:limit]
		response.NextPageToken, err = s.Identity.EncodeCursor(security.Cursor{Scope: scope, After: records[limit-1].ID})
		if err != nil {
			return nil, rpc.Error(err, correlation)
		}
	}
	for _, r := range records {
		p := r.Data
		if err = p.Validate(); err != nil {
			return nil, rpc.Error(err, correlation)
		}
		response.Profiles = append(response.Profiles, browserResource(r))
	}
	reply := connect.NewResponse(response)
	rpc.CopyCorrelation(reply, req.Header())
	return reply, nil
}
func (s *Service) ConfirmBrowserProfileRemoval(ctx context.Context, req *connect.Request[pb.ConfirmBrowserProfileRemovalRequest]) (*connect.Response[pb.ConfirmBrowserProfileRemovalResponse], error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, err := browserActor(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	m := req.Msg.Mutation
	if m == nil || domain.ID(m.Id).Validate() != nil || m.ExpectedRevision == 0 || domain.ID(req.Msg.DeletionRequestId).Validate() != nil {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Original profile removal identity is required.", "Confirm only after native shutdown and complete directory removal."), correlation)
	}
	input := struct {
		Actor    domain.Principal
		ID       domain.ID
		Revision uint64
		Deletion domain.ID
	}{actor, domain.ID(m.Id), m.ExpectedRevision, domain.ID(req.Msg.DeletionRequestId)}
	result, err := s.Store.Mutate(ctx, domain.ID(m.RequestId), "browser.removed", input, func(tx *store.Tx) (any, error) {
		r, err := tx.BrowserProfile(actor.DeviceID, input.ID)
		if err != nil {
			return nil, err
		}
		p := r.Data
		if err = p.Validate(); err != nil {
			return nil, err
		}
		if p.DeviceID != actor.DeviceID || p.ServerID != s.Identity.ServerID {
			return nil, domain.Fail(domain.PermissionDenied, "Another device owns this cleanup obligation.", "Use the original device's paired client.")
		}
		if r.Revision != input.Revision || p.State != domain.BrowserProfileRemovalPending || p.DeletionRequestID != input.Deletion {
			return nil, domain.Fail(domain.Conflict, "The original removal obligation does not match.", "Read its current status; do not substitute a new cleanup operation.")
		}
		p.State = domain.BrowserProfileRemoved
		r.Data = p
		_, err = tx.PutBrowserProfile(r, r.Revision)
		return struct{}{}, err
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	r, err := s.browserProfile(ctx, input.ID)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.InfoContext(ctx, "browser_profile_removal_confirmed", "request_id", m.RequestId, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.ConfirmBrowserProfileRemovalResponse{Profile: browserResource(r), RequestId: m.RequestId, Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func (s *Service) GetAccountBrowserCleanup(ctx context.Context, req *connect.Request[pb.GetAccountBrowserCleanupRequest]) (*connect.Response[pb.GetAccountBrowserCleanupResponse], error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || (actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice) {
		return nil, rpc.Error(domain.Fail(domain.PermissionDenied, "Account cleanup is a client operation.", "Use an authorized owner or paired client."), req.Header().Get(rpc.CorrelationHeader))
	}
	if err := domain.ID(req.Msg.AccountId).Validate(); err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	result := &pb.GetAccountBrowserCleanupResponse{}
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		var err error
		result.Active, result.Pending, result.Removed, err = tx.BrowserCleanupCounts(domain.ID(req.Msg.AccountId))
		return err
	})
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	response := connect.NewResponse(result)
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

func browserResource(r domain.BrowserProfileRecord) *pb.BrowserProfile {
	state := pb.BrowserProfileState_BROWSER_PROFILE_STATE_UNSPECIFIED
	switch r.Data.State {
	case domain.BrowserProfileActive:
		state = pb.BrowserProfileState_BROWSER_PROFILE_STATE_ACTIVE
	case domain.BrowserProfileRemovalPending:
		state = pb.BrowserProfileState_BROWSER_PROFILE_STATE_REMOVAL_PENDING
	case domain.BrowserProfileRemoved:
		state = pb.BrowserProfileState_BROWSER_PROFILE_STATE_REMOVED
	}
	return &pb.BrowserProfile{Id: string(r.ID), Revision: r.Revision, ServerId: string(r.Data.ServerID), DeviceId: string(r.Data.DeviceID), AccountId: string(r.Data.AccountID), State: state, DeletionRequestId: string(r.Data.DeletionRequestID)}
}
func (s *Service) GetBrowserCapabilities(ctx context.Context, req *connect.Request[pb.GetBrowserCapabilitiesRequest]) (*connect.Response[pb.GetBrowserCapabilitiesResponse], error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || (actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice) {
		return nil, rpc.Error(domain.Fail(domain.PermissionDenied, "Browser capabilities require a client.", "Use an authorized product client."), req.Header().Get(rpc.CorrelationHeader))
	}
	if err := s.Store.Read(ctx, func(tx *store.Tx) error { return tx.Authorize() }); err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	response := connect.NewResponse(&pb.GetBrowserCapabilitiesResponse{Capabilities: []pb.BrowserCapability{pb.BrowserCapability_BROWSER_CAPABILITY_PROTECTED_DEVICE_PROFILE_V1}})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
