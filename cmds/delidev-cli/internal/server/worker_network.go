// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"time"

	"connectrpc.com/connect"
	"filippo.io/age"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workernetwork"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type workerNetworkExportReceipt struct {
	ID         domain.ID `json:"id"`
	Generation uint64    `json:"generation"`
}

func networkBinding(a workernetwork.Authority, key domain.ID, recipient string) domain.WorkerNetworkBinding {
	return domain.WorkerNetworkBinding{DeviceID: a.DeviceID, PairingID: a.PairingID, KeyID: key, Recipient: recipient, Endpoint: a.Endpoint}
}
func validateWorkerNetworkAuthority(tx *store.Tx, a workernetwork.Authority) error {
	grantRecord, err := tx.Get(domain.PairingKind, a.PairingID)
	if err != nil {
		return err
	}
	grant, err := store.Decode[domain.Pairing](grantRecord)
	if err != nil {
		return err
	}
	if grant.Type != domain.WorkerDevice {
		return networkConflict()
	}
	if grant.UsedBy == "" {
		if !grant.ExpiresAt.After(time.Now()) {
			return networkConflict()
		}
		for _, scope := range []struct {
			kind domain.Kind
			id   domain.ID
		}{{domain.MachineKind, a.MachineID}, {domain.DeviceKind, a.DeviceID}} {
			if _, err := tx.Get(scope.kind, scope.id); domain.SafeError(err).Code != domain.NotFound {
				return networkConflict()
			}
		}
		return nil
	}
	if grant.UsedBy != a.DeviceID {
		return networkConflict()
	}
	r, err := tx.Get(domain.DeviceKind, a.DeviceID)
	if err != nil {
		return err
	}
	device, err := store.Decode[domain.Device](r)
	if err != nil {
		return err
	}
	if device.Revoked || device.Type != domain.WorkerDevice || device.MachineID != a.MachineID {
		return networkConflict()
	}
	_, _, err = activeMachine(tx, a.MachineID)
	return err
}
func (s *Service) ExportWorkerNetworkBundle(ctx context.Context, req *connect.Request[pb.ExportWorkerNetworkBundleRequest]) (*connect.Response[pb.ExportWorkerNetworkBundleResponse], error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	c := req.Header().Get(rpc.CorrelationHeader)
	input, err := integrationMutation(ctx, req.Msg.Mutation, true)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	a := workernetwork.Authority{ServerID: s.Identity.ServerID, Endpoint: req.Msg.Endpoint, MachineID: domain.ID(req.Msg.MachineId), DeviceID: domain.ID(req.Msg.DeviceId), PairingID: domain.ID(req.Msg.PairingId)}
	key := domain.ID(req.Msg.KeyId)
	binding := networkBinding(a, key, req.Msg.Recipient)
	if a.Validate() != nil || binding.Validate() != nil || key.Validate() != nil {
		return nil, rpc.Error(networkConflict(), c)
	}
	bound := struct {
		Input                       integrationInput
		Authority                   workernetwork.Authority
		Binding                     domain.WorkerNetworkBinding
		Profile                     domain.ID
		ProfileRevision, Generation uint64
	}{input, a, binding, domain.ID(req.Msg.ProfileId), req.Msg.ProfileRevision, req.Msg.DesiredGeneration}
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	result, err := s.Store.Mutate(ctx, domain.ID(req.Msg.Mutation.RequestId), "network.worker.export", bound, func(tx *store.Tx) (any, error) {
		if err := tx.Authorize(); err != nil {
			return nil, err
		}
		if err := validateWorkerNetworkAuthority(tx, a); err != nil {
			return nil, err
		}
		if err := tx.PutWorkerNetworkPairing(store.WorkerNetworkPairing{MachineID: a.MachineID, Binding: binding}); err != nil {
			return nil, err
		}
		current, err := tx.NetworkRoute(a.MachineID)
		if store.MissingNetworkRoute(err) {
			if input.ID != "" || input.Revision != 0 || bound.Generation != 0 {
				return nil, networkConflict()
			}
			value := domain.NetworkRoute{MachineID: a.MachineID, Binding: &binding, Profile: directNetworkProfile()}
			if bound.Profile != "" {
				profile, err := tx.Get(domain.NetworkProfileKind, bound.Profile)
				if err != nil {
					return nil, err
				}
				if profile.Revision != bound.ProfileRevision {
					return nil, networkConflict()
				}
				value.Profile, err = store.Decode[domain.NetworkProfile](profile)
				if err != nil {
					return nil, err
				}
				value.ProfileID, value.ProfileRevision = profile.ID, profile.Revision
			} else if bound.ProfileRevision != 0 {
				return nil, networkConflict()
			}
			id := domain.ID(req.Msg.Mutation.RequestId)
			created, err := tx.Put(domain.NetworkRouteKind, id, 0, "", "", value)
			return workerNetworkExportReceipt{ID: id, Generation: created.Revision}, err
		}
		if err != nil {
			return nil, err
		}
		if input.ID != current.ID || input.Revision != current.Revision || bound.Generation != current.Revision || bound.Profile != "" || bound.ProfileRevision != 0 {
			return nil, networkConflict()
		}
		value, err := store.Decode[domain.NetworkRoute](current)
		if err != nil {
			return nil, err
		}
		if value.Binding == nil {
			value.Binding = &binding
			current, err = tx.Put(domain.NetworkRouteKind, current.ID, current.Revision, "", "", value)
		} else if *value.Binding != binding {
			return nil, networkConflict()
		}
		return workerNetworkExportReceipt{ID: current.ID, Generation: current.Revision}, err
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	var receipt workerNetworkExportReceipt
	if domain.Decode(result.Data, &receipt) != nil {
		return nil, rpc.Error(networkConflict(), c)
	}
	r, route, err := s.readNetworkRoute(ctx, a.MachineID)
	if err != nil || r.ID != receipt.ID || route.Binding == nil || *route.Binding != binding {
		return nil, rpc.Error(networkConflict(), c)
	}
	// Replayed export acceptance cannot silently adopt a later selection.
	if r.Revision != receipt.Generation {
		return nil, rpc.Error(networkConflict(), c)
	}
	cipher, digest, err := s.encryptWorkerNetwork(ctx, a, key, binding.Recipient, r, route)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	s.logger.InfoContext(ctx, "worker_network_exported", "machine_id", a.MachineID, "route_id", r.ID, "generation", r.Revision, "replayed", result.Replayed)
	return connect.NewResponse(&pb.ExportWorkerNetworkBundleResponse{Ciphertext: cipher, CiphertextDigest: digest, Route: rpc.Resource(r), Replayed: result.Replayed}), nil
}
func (s *Service) encryptWorkerNetwork(ctx context.Context, a workernetwork.Authority, key domain.ID, recipient string, r store.Record, route domain.NetworkRoute) ([]byte, string, error) {
	now := time.Now().UTC()
	b := workernetwork.Bundle{Version: 1, Authority: a, KeyID: key, Recipient: recipient, ExportID: domain.NewID(), RouteID: r.ID, Generation: r.Revision, Route: route, IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute)}
	if route.Profile.CredentialGeneration != "" {
		vault, err := s.secrets()
		if err != nil {
			return nil, "", err
		}
		raw, err := vault.Get(ctx, proxyRef(route.ProfileID, route.Profile.CredentialGeneration))
		defer clear(raw)
		if err != nil {
			return nil, "", err
		}
		var credential domain.ProxyCredential
		if domain.Decode(raw, &credential) != nil || credential.Validate() != nil {
			return nil, "", networkConflict()
		}
		b.Credential = &credential
	}
	cipher, digest, err := workernetwork.Encrypt(ctx, b)
	if err != nil {
		return nil, "", err
	}
	pin := store.WorkerNetworkTransfer{MachineID: a.MachineID, DeviceID: a.DeviceID, KeyID: key, RouteID: r.ID, Generation: r.Revision, Digest: digest}
	_, err = s.Store.Mutate(ctx, domain.NewID(), "network.worker.transfer-issued", pin, func(tx *store.Tx) (any, error) {
		if err := tx.Authorize(); err != nil {
			return nil, err
		}
		if err := validateWorkerNetworkAuthority(tx, a); err != nil {
			return nil, err
		}
		current, err := tx.NetworkRoute(a.MachineID)
		if err != nil {
			return nil, err
		}
		if current.ID != r.ID || current.Revision != r.Revision {
			return nil, networkConflict()
		}
		return struct{}{}, tx.PutWorkerNetworkTransfer(pin)
	})
	return cipher, digest, err
}
func workerNetworkStatus(tx *store.Tx, machine domain.ID) (domain.WorkerNetworkStatus, error) {
	status := domain.WorkerNetworkStatus{Version: 1, MachineID: machine, ControlState: domain.WorkerRouteUnverified, NativeState: domain.WorkerRouteUnsupported}
	r, m, err := activeMachine(tx, machine)
	_ = r
	if err != nil {
		return status, err
	}
	route, err := tx.NetworkRoute(machine)
	if err == nil {
		status.RouteID, status.DesiredGeneration = route.ID, route.Revision
	} else if !store.MissingNetworkRoute(err) {
		return status, err
	}
	if m.Network != nil {
		status.EffectiveGeneration = m.Network.EffectiveGeneration
		status.NativeState = m.Network.NativeState
		status.NativeGeneration = m.Network.NativeGeneration
		status.NativeExecutionID = m.Network.NativeExecutionID
	}
	if status.DesiredGeneration != status.EffectiveGeneration {
		status.ControlState = domain.WorkerRouteStale
		status.NativeState = domain.WorkerRouteStale
	} else if status.DesiredGeneration == 0 || m.Network != nil && m.Network.RouteID == status.RouteID {
		status.ControlState = domain.WorkerRouteObserved
	}
	return status, nil
}
func workerNetworkReady(tx *store.Tx, machine domain.ID) error {
	status, err := workerNetworkStatus(tx, machine)
	if err != nil {
		return err
	}
	if status.ControlState != domain.WorkerRouteObserved {
		return domain.Fail(domain.RecoveryRequired, "The Worker network configuration is stale.", "Reconcile the current encrypted generation before starting another operation; control synchronization remains available.")
	}
	return nil
}

// A stale route is temporary admission backpressure, not stream failure. The
// claim transaction must check again after a watch read so profile selection
// cannot race a new native owner. Existing owners retain their original route.
func workerNetworkAdmission(tx *store.Tx, machine domain.ID) error {
	err := workerNetworkReady(tx, machine)
	if err != nil && domain.SafeError(err).Code == domain.RecoveryRequired {
		return domain.Fail(domain.ResourceExhausted, "The Worker route awaits its current generation.", "Keep accepted work queued while authenticated control reconciles the encrypted route.")
	}
	return err
}
func (s *Service) GetWorkerNetworkStatus(ctx context.Context, req *connect.Request[pb.GetWorkerNetworkStatusRequest]) (*connect.Response[pb.GetWorkerNetworkStatusResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	if _, err := integrationActor(ctx); err != nil {
		return nil, rpc.Error(err, c)
	}
	machine := domain.ID(req.Msg.MachineId)
	if machine.Validate() != nil {
		return nil, rpc.Error(networkConflict(), c)
	}
	var status domain.WorkerNetworkStatus
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		var err error
		status, err = workerNetworkStatus(tx, machine)
		return err
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	raw, _ := json.Marshal(status)
	return connect.NewResponse(&pb.GetWorkerNetworkStatusResponse{StatusJson: raw}), nil
}
func (s *Service) SyncWorkerNetwork(ctx context.Context, req *connect.Request[pb.SyncWorkerNetworkRequest]) (*connect.Response[pb.SyncWorkerNetworkResponse], error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	c := req.Header().Get(rpc.CorrelationHeader)
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, c)
	}
	if domain.ID(req.Msg.RequestId).Validate() != nil || domain.ID(req.Msg.KeyId).Validate() != nil {
		return nil, rpc.Error(networkConflict(), c)
	}
	public, err := age.ParseX25519Recipient(req.Msg.Recipient)
	if err != nil || public.String() != req.Msg.Recipient {
		return nil, rpc.Error(networkConflict(), c)
	}
	machine, instance := domain.ID(req.Msg.MachineId), domain.ID(req.Msg.InstanceId)
	unlock, err := s.lockAccounts(ctx)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	defer unlock()
	var r store.Record
	var route domain.NetworkRoute
	var a workernetwork.Authority
	inSync := false
	alreadyApplied := false
	err = s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		if err := currentInstance(tx, machine, instance); err != nil {
			return err
		}
		var err error
		r, err = tx.NetworkRoute(machine)
		if err != nil {
			return err
		}
		route, err = store.Decode[domain.NetworkRoute](r)
		if err != nil {
			return err
		}
		actor, _ := domain.PrincipalFrom(ctx)
		if route.Binding == nil || route.Binding.DeviceID != actor.DeviceID || route.Binding.KeyID != domain.ID(req.Msg.KeyId) || route.Binding.Recipient != req.Msg.Recipient || req.Msg.EffectiveGeneration > r.Revision {
			return networkConflict()
		}
		a = workernetwork.Authority{ServerID: s.Identity.ServerID, Endpoint: route.Binding.Endpoint, MachineID: machine, DeviceID: actor.DeviceID, PairingID: route.Binding.PairingID}
		if err := validateWorkerNetworkAuthority(tx, a); err != nil {
			return err
		}
		pin, err := tx.WorkerNetworkTransfer(machine)
		if err != nil && domain.SafeError(err).Code != domain.NotFound {
			return err
		}
		inSync = err == nil && req.Msg.EffectiveGeneration == r.Revision && req.Msg.RouteId == string(r.ID) && pin.Generation == r.Revision && pin.RouteID == r.ID && pin.KeyID == domain.ID(req.Msg.KeyId) && pin.DeviceID == actor.DeviceID && pin.Digest == req.Msg.CiphertextDigest
		if inSync {
			_, m, err := activeMachine(tx, machine)
			if err != nil {
				return err
			}
			alreadyApplied = m.Network != nil && m.Network.InstanceID == instance && m.Network.RouteID == r.ID && m.Network.EffectiveGeneration == r.Revision && m.Network.IssuedDigest == pin.Digest && m.Network.KeyID == domain.ID(req.Msg.KeyId) && m.Network.Recipient == req.Msg.Recipient
		}
		return nil
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	if inSync {
		// Periodic current-generation acknowledgements are authenticated reads.
		// Preserve receipt collision checks without writing a new receipt or
		// republishing an unchanged Machine every five seconds.
		if alreadyApplied {
			if _, _, err := s.Store.Replay(ctx, domain.ID(req.Msg.RequestId), "network.worker.apply", req.Msg); err != nil {
				return nil, rpc.Error(err, c)
			}
			return connect.NewResponse(&pb.SyncWorkerNetworkResponse{Route: rpc.Resource(r), InSync: true}), nil
		}
		_, err = s.Store.Mutate(ctx, domain.ID(req.Msg.RequestId), "network.worker.apply", req.Msg, func(tx *store.Tx) (any, error) {
			if err := tx.Authorize(); err != nil {
				return nil, err
			}
			if err := currentInstance(tx, machine, instance); err != nil {
				return nil, err
			}
			current, err := tx.NetworkRoute(machine)
			if err != nil {
				return nil, err
			}
			pin, err := tx.WorkerNetworkTransfer(machine)
			if err != nil {
				return nil, err
			}
			if current.ID != r.ID || current.Revision != r.Revision || pin.Digest != req.Msg.CiphertextDigest {
				return nil, networkConflict()
			}
			mr, m, err := activeMachine(tx, machine)
			if err != nil {
				return nil, err
			}
			if m.Network == nil || m.Network.InstanceID != instance {
				return nil, networkConflict()
			}
			m.Network.RouteID = r.ID
			m.Network.KeyID = domain.ID(req.Msg.KeyId)
			m.Network.Recipient = req.Msg.Recipient
			m.Network.EffectiveGeneration = r.Revision
			m.Network.IssuedDigest = pin.Digest
			m.Network.IssuedGeneration = r.Revision
			m.Network.ObservedAt = time.Now().UTC()
			if m.Network.NativeGeneration != r.Revision {
				m.Network.NativeState = domain.WorkerRouteNotApplied
			}
			_, err = tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", m)
			return struct{}{}, err
		})
		if err != nil {
			return nil, rpc.Error(err, c)
		}
		return connect.NewResponse(&pb.SyncWorkerNetworkResponse{Route: rpc.Resource(r), InSync: true}), nil
	}
	cipher, digest, err := s.encryptWorkerNetwork(ctx, a, domain.ID(req.Msg.KeyId), req.Msg.Recipient, r, route)
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	return connect.NewResponse(&pb.SyncWorkerNetworkResponse{Route: rpc.Resource(r), Ciphertext: cipher, CiphertextDigest: digest}), nil
}

func nativeRouteState(value pb.WorkerNativeRouteState) domain.WorkerNativeRouteState {
	switch value {
	case pb.WorkerNativeRouteState_WORKER_NATIVE_ROUTE_STATE_UNVERIFIED:
		return domain.WorkerRouteUnverified
	case pb.WorkerNativeRouteState_WORKER_NATIVE_ROUTE_STATE_OBSERVED:
		return domain.WorkerRouteObserved
	case pb.WorkerNativeRouteState_WORKER_NATIVE_ROUTE_STATE_FAILED:
		return domain.WorkerRouteFailed
	default:
		return ""
	}
}

func (s *Service) ReportWorkerNativeRoute(ctx context.Context, req *connect.Request[pb.ReportWorkerNativeRouteRequest]) (*connect.Response[pb.ReportWorkerNativeRouteResponse], error) {
	c := req.Header().Get(rpc.CorrelationHeader)
	if err := workerActor(ctx, req.Msg.MachineId, req.Msg.InstanceId); err != nil {
		return nil, rpc.Error(err, c)
	}
	meta := req.Msg.Mutation
	state := nativeRouteState(req.Msg.State)
	if meta == nil || meta.ExpectedRevision == 0 || state == "" || domain.ID(meta.RequestId).Validate() != nil || req.Msg.Generation == 0 || req.Msg.Generation >= 1<<63 {
		return nil, rpc.Error(networkConflict(), c)
	}
	actor, _ := domain.PrincipalFrom(ctx)
	value := store.WorkerNativeRoute{JobID: domain.ID(meta.Id), ExecutionID: domain.ID(req.Msg.ExecutionId), MachineID: domain.ID(req.Msg.MachineId), InstanceID: domain.ID(req.Msg.InstanceId), DeviceID: actor.DeviceID, RouteID: domain.ID(req.Msg.RouteId), Generation: req.Msg.Generation, JobRevision: meta.ExpectedRevision, State: state}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "network.worker.native-observation", req.Msg, func(tx *store.Tx) (any, error) {
		if err := tx.Authorize(); err != nil {
			return nil, err
		}
		if err := currentInstance(tx, value.MachineID, value.InstanceID); err != nil {
			return nil, err
		}
		jr, err := tx.Get(domain.JobKind, value.JobID)
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](jr)
		if err != nil || jr.Revision != value.JobRevision || job.State != domain.JobClaimed || job.MachineID != value.MachineID || job.InstanceID != value.InstanceID || job.AssignedDeviceID != value.DeviceID {
			return nil, executionDenied()
		}
		if canceled, err := tx.JobCancellationRequested(value.JobID); err != nil || canceled {
			return nil, executionDenied()
		}
		grant, err := tx.ExecutionGrantForJob(value.JobID)
		if err != nil || s.executionAuthority == nil || grant.ServerEpoch != s.executionAuthority.epoch || grant.ExecutionID != value.ExecutionID || grant.InstanceID != value.InstanceID || grant.DeviceID != value.DeviceID {
			return nil, executionDenied()
		}
		switch job.Type {
		case domain.ExecuteSessionJob:
			var input domain.ExecutionJobInput
			if domain.Decode(job.Input, &input) != nil || input.Validate() != nil || input.Configuration.Subscription || input.Configuration.Harness != domain.Codex || input.Installation.Version != domain.CodexProtocolVersion || input.ExecutionID != value.ExecutionID {
				return nil, executionDenied()
			}
		case domain.GenerateSessionTitleJob:
			var input domain.AuxiliaryTitleInput
			if domain.Decode(job.Input, &input) != nil || input.Validate() != nil || input.Harness != domain.Codex || input.NativeVersion != domain.CodexProtocolVersion || input.OriginalExecutionID != value.ExecutionID {
				return nil, executionDenied()
			}
		default:
			return nil, executionDenied()
		}
		mr, machine, err := activeMachine(tx, value.MachineID)
		if err != nil {
			return nil, err
		}
		if !machineCapabilityContains(machine.WorkerCapabilities, domain.CodexAPIProxyV1) || machine.Network == nil || machine.Network.InstanceID != value.InstanceID {
			return nil, domain.Fail(domain.Unsupported, "This Worker has no owned Codex API proxy profile.", "Update and reconnect the original Worker before launching a routed API runtime.")
		}
		original, err := tx.WorkerNativeRoute(value.JobID)
		if domain.SafeError(err).Code == domain.NotFound {
			if state != domain.WorkerRouteUnverified || workerNetworkReady(tx, value.MachineID) != nil || machine.Network.RouteID != value.RouteID || machine.Network.EffectiveGeneration != value.Generation {
				return nil, networkConflict()
			}
			routeRecord, err := tx.NetworkRoute(value.MachineID)
			if err != nil {
				return nil, err
			}
			route, err := store.Decode[domain.NetworkRoute](routeRecord)
			if err != nil || routeRecord.ID != value.RouteID || routeRecord.Revision != value.Generation || route.Profile.Mode == domain.ProxyDirect || route.Binding == nil || route.Binding.DeviceID != value.DeviceID {
				return nil, networkConflict()
			}
			if _, err := s.executionAuthority.scope(tx, grant); err != nil {
				return nil, err
			}
		} else if err != nil {
			return nil, err
		} else if original.JobID != value.JobID || original.ExecutionID != value.ExecutionID || original.MachineID != value.MachineID || original.InstanceID != value.InstanceID || original.DeviceID != value.DeviceID || original.RouteID != value.RouteID || original.Generation != value.Generation || original.JobRevision != value.JobRevision || state == domain.WorkerRouteUnverified {
			return nil, networkConflict()
		}
		value.ObservedAt = time.Now().UTC()
		if err := tx.PutWorkerNativeRoute(value); err != nil {
			return nil, err
		}
		machine.Network.NativeExecutionID = value.ExecutionID
		machine.Network.NativeGeneration = value.Generation
		machine.Network.NativeState = value.State
		machine.Network.ObservedAt = value.ObservedAt
		_, err = tx.Put(domain.MachineKind, mr.ID, mr.Revision, "", "", machine)
		return struct{}{}, err
	})
	if err != nil {
		return nil, rpc.Error(err, c)
	}
	s.logger.InfoContext(ctx, "worker_native_route_observed", "machine_id", value.MachineID, "job_id", value.JobID, "execution_id", value.ExecutionID, "route_id", value.RouteID, "generation", value.Generation, "state", value.State, "replayed", result.Replayed)
	return connect.NewResponse(&pb.ReportWorkerNativeRouteResponse{Replayed: result.Replayed}), nil
}

func machineCapabilityContains(values []domain.WorkerCapability, expected domain.WorkerCapability) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
