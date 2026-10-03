// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/outbound"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type networkReceipt struct {
	ID      domain.ID `json:"id"`
	Deleted bool      `json:"deleted,omitempty"`
}

type networkSaveInput struct {
	Actor      domain.Principal
	ID         domain.ID
	Revision   uint64
	Definition domain.ProxyDefinition
	Commitment string
	Clear      bool
}

func directNetworkProfile() domain.NetworkProfile {
	return domain.NetworkProfile{ProxyDefinition: domain.ProxyDefinition{Name: "Direct", Mode: domain.ProxyDirect}}
}
func proxyRef(id, generation domain.ID) credentials.Ref {
	return credentials.Ref{Owner: id, ID: generation, Purpose: credentials.NetworkProxy}
}
func networkConflict() error {
	return domain.Fail(domain.Conflict, "The selected network configuration changed.", "Read the current profile/route revision before a new mutation; retry the original request unchanged after an uncertain result.")
}

type networkOutcome struct {
	Resource  *pb.Resource
	RequestId string
	Replayed  bool
	Deleted   bool
}

func (s *Service) networkReply(ctx context.Context, result store.Result, kind domain.Kind) (*networkOutcome, error) {
	var receipt networkReceipt
	if err := domain.Decode(result.Data, &receipt); err != nil {
		return nil, err
	}
	response := &networkOutcome{RequestId: string(result.RequestID), Replayed: result.Replayed, Deleted: receipt.Deleted}
	if !receipt.Deleted {
		record, err := s.Store.Get(ctx, kind, receipt.ID)
		if err != nil {
			if domain.SafeError(err).Code != domain.NotFound {
				return nil, err
			}
			// The receipt proves acceptance independently of the live resource.
			// A later deletion cannot authorize recreation by an exact retry.
			response.Deleted = true
		} else {
			response.Resource = rpc.Resource(record)
		}
	}
	return response, nil
}

func (s *Service) SaveNetworkProfile(ctx context.Context, req *connect.Request[pb.SaveNetworkProfileRequest]) (*connect.Response[pb.SaveNetworkProfileResponse], error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	correlation := req.Header().Get(rpc.CorrelationHeader)
	input, err := integrationMutation(ctx, req.Msg.Mutation, true)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var definition domain.ProxyDefinition
	if req.Msg.SchemaVersion != 1 {
		return nil, rpc.Error(domain.Fail(domain.Unsupported, "Unsupported network schema.", "Use schema 1."), correlation)
	}
	if err = domain.Decode(req.Msg.DocumentJson, &definition); err == nil {
		err = definition.Validate()
	}
	raw := req.Msg.CredentialJson
	defer clear(raw)
	if err == nil && len(raw) > 0 {
		var c domain.ProxyCredential
		if len(raw) > 4096 || domain.Decode(raw, &c) != nil || c.Validate() != nil || definition.Mode == domain.ProxyDirect || req.Msg.ClearCredential {
			err = domain.Fail(domain.InvalidArgument, "Invalid write-only proxy credential.", "Use bounded username/password JSON for a proxy profile, independently of explicit credential clearing.")
		}
	}
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	id := input.ID
	if id == "" {
		id = domain.ID(req.Msg.Mutation.RequestId)
	}
	commitment := ""
	if len(raw) > 0 {
		commitment = s.networkCommitment(domain.ID(req.Msg.Mutation.RequestId), raw)
	}
	bound := networkSaveInput{input.Actor, id, input.Revision, definition, commitment, req.Msg.ClearCredential}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	defer unlock()
	requestID := domain.ID(req.Msg.Mutation.RequestId)
	result, found, err := s.Store.Replay(ctx, requestID, "network.save", bound)
	if err == nil {
		_, err = s.reconcileNetworkDeleteIntent(ctx)
	}
	if err == nil {
		err = s.reconcileNetworkSaveIntent(ctx, requestID, bound)
	}
	var prior domain.NetworkProfile
	if err == nil && !found {
		err = s.Store.Read(ctx, func(tx *store.Tx) error {
			if err := tx.Authorize(); err != nil {
				return err
			}
			if input.Revision == 0 {
				if _, err := tx.Get(domain.NetworkProfileKind, id); err == nil {
					return networkConflict()
				} else if domain.SafeError(err).Code != domain.NotFound {
					return err
				}
				return tx.NetworkProfileCapacity()
			}
			r, e := tx.Get(domain.NetworkProfileKind, id)
			if e != nil {
				return e
			}
			if r.Revision != input.Revision {
				return networkConflict()
			}
			prior, e = store.Decode[domain.NetworkProfile](r)
			if e != nil {
				return e
			}
			return prior.Validate()
		})
		value := domain.NetworkProfile{ProxyDefinition: definition, CredentialGeneration: prior.CredentialGeneration}
		// Write-only credentials belong to this exact authentication authority.
		// Metadata edits may retain them, but a different protocol or peer must
		// receive an explicit new credential rather than the owner's old secret.
		// Keep old generations for independently pinned routes until deletion.
		if req.Msg.ClearCredential || prior.Mode != definition.Mode || prior.Host != definition.Host || prior.Port != definition.Port {
			value.CredentialGeneration = ""
		}
		if len(raw) > 0 {
			value.CredentialGeneration = requestID
		}
		if err == nil {
			err = value.Validate()
		}
		if err == nil && len(raw) > 0 {
			var vault accountSecrets
			vault, err = s.secrets()
			if err == nil {
				refs, e := vault.UnremovedReferences(ctx, id)
				err = e
				if err == nil {
					count := 0
					exact := false
					for _, ref := range refs {
						if ref.Purpose == credentials.NetworkProxy {
							count++
							exact = exact || ref.ID == requestID
						}
					}
					if count >= 256 && !exact {
						err = domain.Fail(domain.ResourceExhausted, "The profile credential-generation limit was reached.", "Select another profile and delete this profile to clean retained generations; at most 256 are retained.")
					}
				}
				if err == nil {
					err = s.writeNetworkSaveIntent(networkSaveIntent{Version: 1, ServerID: s.Identity.ServerID, RequestID: requestID, Input: bound, State: networkSavePending})
				}
				if err == nil {
					_, err = vault.Put(ctx, proxyRef(id, requestID), raw)
				}
			}
		}
		if err == nil {
			result, err = s.Store.Mutate(ctx, requestID, "network.save", bound, func(tx *store.Tx) (any, error) {
				if err := tx.Authorize(); err != nil {
					return nil, err
				}
				if input.Revision == 0 {
					if err := tx.NetworkProfileCapacity(); err != nil {
						return nil, err
					}
				}
				_, err := tx.Put(domain.NetworkProfileKind, id, input.Revision, "", "", value)
				return networkReceipt{ID: id}, err
			})
			if err == nil && len(raw) > 0 {
				err = s.clearNetworkSaveIntent()
			}
		}
	}
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.Info("network_profile_saved", "profile_id", id, "mode", definition.Mode, "replayed", result.Replayed)
	reply, err := s.networkReply(ctx, result, domain.NetworkProfileKind)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	return connect.NewResponse(&pb.SaveNetworkProfileResponse{Resource: reply.Resource, RequestId: reply.RequestId, Replayed: reply.Replayed, Deleted: reply.Deleted}), nil
}

func (s *Service) networkCommitment(request domain.ID, raw []byte) string {
	mac := hmac.New(sha256.New, []byte(s.Identity.Token))
	mac.Write([]byte("delidev/network/receipt/v1\x00" + string(s.Identity.ServerID) + "\x00" + string(request) + "\x00"))
	mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Service) SelectNetworkProfile(ctx context.Context, req *connect.Request[pb.SelectNetworkProfileRequest]) (*connect.Response[pb.SelectNetworkProfileResponse], error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	correlation := req.Header().Get(rpc.CorrelationHeader)
	input, err := integrationMutation(ctx, req.Msg.Mutation, true)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	machine, profile := domain.ID(req.Msg.MachineId), domain.ID(req.Msg.ProfileId)
	if machine != "" && machine.Validate() != nil || profile != "" && (profile.Validate() != nil || req.Msg.ProfileRevision == 0) || profile == "" && req.Msg.ProfileRevision != 0 {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "Invalid network target or profile revision.", "Use an exact registered machine and profile revision, or empty values for server/Direct."), correlation)
	}
	bound := struct {
		Actor           domain.Principal
		ID              domain.ID
		Revision        uint64
		Machine         domain.ID
		Profile         domain.ID
		ProfileRevision uint64
	}{input.Actor, input.ID, input.Revision, machine, profile, req.Msg.ProfileRevision}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	defer unlock()
	if _, _, err := s.Store.Replay(ctx, domain.ID(req.Msg.Mutation.RequestId), "network.select", bound); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if _, err := s.reconcileNetworkDeleteIntent(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	result, err := s.Store.Mutate(ctx, domain.ID(req.Msg.Mutation.RequestId), "network.select", bound, func(tx *store.Tx) (any, error) {
		if err := tx.Authorize(); err != nil {
			return nil, err
		}
		if machine != "" {
			if _, err := tx.Get(domain.MachineKind, machine); err != nil {
				return nil, err
			}
		}
		current, err := tx.NetworkRoute(machine)
		id := input.ID
		if store.MissingNetworkRoute(err) {
			if id != "" || input.Revision != 0 {
				return nil, networkConflict()
			}
			id = domain.ID(req.Msg.Mutation.RequestId)
		} else if err != nil {
			return nil, err
		} else if current.ID != id || current.Revision != input.Revision {
			return nil, networkConflict()
		}
		value := domain.NetworkRoute{MachineID: machine, Profile: directNetworkProfile()}
		if current.ID != "" {
			previous, err := store.Decode[domain.NetworkRoute](current)
			if err != nil {
				return nil, err
			}
			value.Binding = previous.Binding
		}
		if profile != "" {
			r, e := tx.Get(domain.NetworkProfileKind, profile)
			if e != nil {
				return nil, e
			}
			if r.Revision != req.Msg.ProfileRevision {
				return nil, networkConflict()
			}
			value.Profile, e = store.Decode[domain.NetworkProfile](r)
			if e != nil {
				return nil, e
			}
			value.ProfileID, value.ProfileRevision = profile, r.Revision
		}
		if err := value.Validate(); err != nil {
			return nil, err
		}
		_, err = tx.Put(domain.NetworkRouteKind, id, input.Revision, "", "", value)
		return networkReceipt{ID: id}, err
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.Info("network_route_selected", "machine_id", machine, "profile_id", profile, "profile_revision", req.Msg.ProfileRevision, "replayed", result.Replayed)
	reply, err := s.networkReply(ctx, result, domain.NetworkRouteKind)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	return connect.NewResponse(&pb.SelectNetworkProfileResponse{Resource: reply.Resource, RequestId: reply.RequestId, Replayed: reply.Replayed, Deleted: reply.Deleted}), nil
}

func (s *Service) DeleteNetworkProfile(ctx context.Context, req *connect.Request[pb.DeleteNetworkProfileRequest]) (*connect.Response[pb.DeleteNetworkProfileResponse], error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	correlation := req.Header().Get(rpc.CorrelationHeader)
	input, err := integrationMutation(ctx, req.Msg.Mutation, false)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	defer unlock()
	requestID := domain.ID(req.Msg.Mutation.RequestId)
	result, found, err := s.Store.Replay(ctx, requestID, "network.delete", input)
	var recovered *networkDeleteIntent
	if err == nil {
		recovered, err = s.reconcileNetworkDeleteIntent(ctx)
	}
	// A newly authorized actor can complete an accepted pending deletion with
	// its own receipt; it cannot replay or take over the historical actor's ID.
	completed := recovered != nil && recovered.Input.ID == input.ID && recovered.Input.Revision == input.Revision
	if err == nil && !found && !completed {
		err = s.Store.Read(ctx, func(tx *store.Tx) error { return networkDeleteAllowed(tx, input) })
	}
	// A settled receipt with no pending intent has no cleanup obligation.
	// Inspect existing recovery metadata above, but never recreate protected
	// work merely because an accepted deletion is retried.
	if err == nil && !found && !completed {
		err = s.writeNetworkDeleteIntent(networkDeleteIntent{Version: 1, ServerID: s.Identity.ServerID, RequestID: requestID, Input: input})
	}
	if err == nil && !found {
		result, err = s.Store.Mutate(ctx, requestID, "network.delete", input, func(tx *store.Tx) (any, error) {
			if err := tx.Authorize(); err != nil {
				return nil, err
			}
			if !completed {
				if err := networkDeleteAllowed(tx, input); err != nil {
					return nil, err
				}
				if err := tx.Delete(domain.NetworkProfileKind, input.ID, input.Revision); err != nil {
					return nil, err
				}
			}
			return networkReceipt{ID: input.ID, Deleted: true}, nil
		})
	}
	if err == nil && !found && !completed {
		_, err = s.reconcileNetworkDeleteIntent(ctx)
	}
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.logger.Info("network_profile_deleted", "profile_id", input.ID, "replayed", result.Replayed)
	reply, err := s.networkReply(ctx, result, domain.NetworkProfileKind)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	return connect.NewResponse(&pb.DeleteNetworkProfileResponse{Resource: reply.Resource, RequestId: reply.RequestId, Replayed: reply.Replayed, Deleted: reply.Deleted}), nil
}

func networkDeleteAllowed(tx *store.Tx, input integrationInput) error {
	if err := tx.Authorize(); err != nil {
		return err
	}
	record, err := tx.Get(domain.NetworkProfileKind, input.ID)
	if err != nil {
		return err
	}
	if record.Revision != input.Revision {
		return networkConflict()
	}
	selected, err := tx.NetworkProfileSelected(input.ID)
	if err != nil {
		return err
	}
	if selected {
		return domain.Fail(domain.Conflict, "The profile is selected by a server or Worker route.", "Explicitly change every selection before deletion.")
	}
	return nil
}

func (s *Service) readNetworkRoute(ctx context.Context, machine domain.ID) (store.Record, domain.NetworkRoute, error) {
	var r store.Record
	value := domain.NetworkRoute{MachineID: machine, Profile: directNetworkProfile()}
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		if machine != "" {
			if _, err := tx.Get(domain.MachineKind, machine); err != nil {
				return err
			}
		}
		var err error
		r, err = tx.NetworkRoute(machine)
		if store.MissingNetworkRoute(err) {
			return nil
		}
		if err != nil {
			return err
		}
		value, err = store.Decode[domain.NetworkRoute](r)
		if err != nil {
			return err
		}
		return value.Validate()
	})
	return r, value, err
}
func (s *Service) GetNetworkRoute(ctx context.Context, req *connect.Request[pb.GetNetworkRouteRequest]) (*connect.Response[pb.GetNetworkRouteResponse], error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := integrationActor(ctx); err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	machine := domain.ID(req.Msg.MachineId)
	if machine != "" && machine.Validate() != nil {
		return nil, rpc.Error(machine.Validate(), req.Header().Get(rpc.CorrelationHeader))
	}
	r, _, err := s.readNetworkRoute(ctx, machine)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	response := &pb.GetNetworkRouteResponse{}
	if r.ID != "" {
		response.Route = rpc.Resource(r)
	}
	return connect.NewResponse(response), nil
}

type workerNetworkMetadata struct {
	FormatVersion     uint32              `json:"format_version"`
	ServerID          domain.ID           `json:"server_id"`
	MachineID         domain.ID           `json:"machine_id"`
	ExportID          domain.ID           `json:"export_id"`
	RouteID           domain.ID           `json:"route_id"`
	DesiredGeneration uint64              `json:"desired_generation"`
	Route             domain.NetworkRoute `json:"route"`
	IssuedAt          time.Time           `json:"issued_at"`
	ExpiresAt         time.Time           `json:"expires_at"`
}

func (s *Service) ExportWorkerNetworkMetadata(ctx context.Context, req *connect.Request[pb.ExportWorkerNetworkMetadataRequest]) (*connect.Response[pb.ExportWorkerNetworkMetadataResponse], error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	correlation := req.Header().Get(rpc.CorrelationHeader)
	if _, err := integrationActor(ctx); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	machine := domain.ID(req.Msg.MachineId)
	if machine.Validate() != nil {
		return nil, rpc.Error(machine.Validate(), correlation)
	}
	r, v, err := s.readNetworkRoute(ctx, machine)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if r.ID == "" || r.Revision != req.Msg.DesiredGeneration {
		return nil, rpc.Error(networkConflict(), correlation)
	}
	now := time.Now().UTC().Truncate(time.Second)
	raw, err := json.Marshal(workerNetworkMetadata{1, s.Identity.ServerID, machine, domain.NewID(), r.ID, r.Revision, v, now, now.Add(5 * time.Minute)})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	mac := hmac.New(sha256.New, []byte(s.Identity.Token))
	mac.Write([]byte("delidev/worker-network-export/v1\x00"))
	mac.Write(raw)
	return connect.NewResponse(&pb.ExportWorkerNetworkMetadataResponse{MetadataJson: raw, Authentication: base64.RawURLEncoding.EncodeToString(mac.Sum(nil))}), nil
}

// Resolve only the server's immutable selected route, never a Worker setting.
// Internal routing reads do not borrow the caller's Worker product permissions.
func (s *Service) outboundResolver() outbound.Resolver {
	return func(ctx context.Context) (domain.NetworkProfile, []byte, error) {
		unlock, err := s.lockAccounts(ctx)
		if err != nil {
			return domain.NetworkProfile{}, nil, err
		}
		defer unlock()
		_, route, err := s.readNetworkRoute(domain.WithPrincipal(ctx, domain.Principal{Type: domain.OwnerDevice}), "")
		if err != nil {
			return domain.NetworkProfile{}, nil, err
		}
		if route.Profile.CredentialGeneration == "" {
			return route.Profile, nil, nil
		}
		vault, err := s.secrets()
		if err != nil {
			return domain.NetworkProfile{}, nil, err
		}
		raw, err := vault.Get(ctx, proxyRef(route.ProfileID, route.Profile.CredentialGeneration))
		return route.Profile, raw, err
	}
}
