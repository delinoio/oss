package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type attemptWindow struct {
	At    time.Time
	Count int
}

func (s *Service) pairingAllowed(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	now := time.Now()
	s.connectionsMu.Lock()
	defer s.connectionsMu.Unlock()
	if s.pairAttempts == nil {
		s.pairAttempts = map[string]attemptWindow{}
	}
	for key, value := range s.pairAttempts {
		if now.Sub(value.At) > time.Minute {
			delete(s.pairAttempts, key)
		}
	}
	window := s.pairAttempts[host]
	if window.At.IsZero() {
		if len(s.pairAttempts) >= 1024 {
			return false
		}
		window.At = now
	}
	window.Count++
	s.pairAttempts[host] = window
	return window.Count <= 30
}
func (s *Service) authorizeRequest(r *http.Request) (*http.Request, func(), error) {
	reject := func() (*http.Request, func(), error) {
		return nil, nil, domain.Fail(domain.Unauthenticated, "Server authentication is required.", "Load the selected server's owner credential or pair this device.")
	}
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || len(token) > 512 {
		return reject()
	}
	if security.EqualToken(token, s.Identity.Token) {
		return r.WithContext(domain.WithPrincipal(r.Context(), domain.Principal{Type: domain.OwnerDevice})), func() {}, nil
	}
	digest := sha256.Sum256([]byte(token))
	// Hold the registry lock across authorization and registration. Revocation
	// commits first, then cancels this registry, closing the authenticate/register
	// race. Product mutations also recheck revocation inside their transactions.
	s.connectionsMu.Lock()
	defer s.connectionsMu.Unlock()
	actor, err := s.Store.Authenticate(r.Context(), digest[:])
	if err != nil {
		return nil, nil, err
	}
	if actor.Type == domain.WorkerDevice {
		switch r.URL.Path {
		case delidevv1connect.WorkerServiceAttachWorkerProcedure, delidevv1connect.WorkerServiceWatchWorkProcedure, delidevv1connect.WorkerServiceReportWorkProcedure, delidevv1connect.WorkerServiceRegisterExecutionProcedure, delidevv1connect.WorkerServicePublishExecutionProcedure, delidevv1connect.SystemServiceGetStatusProcedure:
		default:
			return nil, nil, domain.Fail(domain.PermissionDenied, "Worker credentials cannot invoke owner product operations.", "Use an owner or paired client credential.")
		}
	}
	ctx, cancel := context.WithCancel(domain.WithPrincipal(r.Context(), actor))
	connection := domain.NewID()
	if s.connections == nil {
		s.connections = map[domain.ID]map[domain.ID]context.CancelFunc{}
	}
	if s.connections[actor.DeviceID] == nil {
		s.connections[actor.DeviceID] = map[domain.ID]context.CancelFunc{}
	}
	s.connections[actor.DeviceID][connection] = cancel
	release := func() {
		s.connectionsMu.Lock()
		defer s.connectionsMu.Unlock()
		cancel()
		delete(s.connections[actor.DeviceID], connection)
		if len(s.connections[actor.DeviceID]) == 0 {
			delete(s.connections, actor.DeviceID)
		}
	}
	return r.WithContext(ctx), release, nil
}
func (s *Service) cancelDevice(id domain.ID) {
	s.connectionsMu.Lock()
	defer s.connectionsMu.Unlock()
	for _, cancel := range s.connections[id] {
		cancel()
	}
}
func deviceType(value pb.DeviceType) (domain.DeviceType, error) {
	switch value {
	case pb.DeviceType_DEVICE_TYPE_CLIENT:
		return domain.ClientDevice, nil
	case pb.DeviceType_DEVICE_TYPE_WORKER:
		return domain.WorkerDevice, nil
	default:
		return "", domain.Fail(domain.InvalidArgument, "Unknown device type.", "Select client or worker.")
	}
}
func (s *Service) CreatePairing(ctx context.Context, req *connect.Request[pb.CreatePairingRequest]) (*connect.Response[pb.CreatePairingResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	kind, err := deviceType(req.Msg.Type)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if err := domain.Text(req.Msg.Name, "device name", 256, true); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if len(req.Msg.CodeDigest) != 32 {
		return nil, rpc.Error(domain.Fail(domain.InvalidArgument, "A 32-byte pairing verifier is required.", "Generate a fresh 256-bit random pairing code locally."), correlation)
	}
	input := struct {
		Name   string
		Type   domain.DeviceType
		Digest []byte
	}{req.Msg.Name, kind, req.Msg.CodeDigest}
	result, err := s.Store.Mutate(ctx, domain.ID(req.Msg.RequestId), "device.create-pairing", input, func(tx *store.Tx) (any, error) {
		id := domain.NewID()
		value := domain.Pairing{Name: req.Msg.Name, Type: kind, ExpiresAt: time.Now().UTC().Add(5 * time.Minute)}
		record, err := tx.Put(domain.PairingKind, id, 0, "", "", value)
		if err != nil {
			return nil, err
		}
		if err := tx.PutPairingVerifier(id, req.Msg.CodeDigest); err != nil {
			return nil, err
		}
		return record, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var record store.Record
	if err := domain.Decode(result.Data, &record); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	response := connect.NewResponse(&pb.CreatePairingResponse{Pairing: rpc.Resource(record), RequestId: req.Msg.RequestId, Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}

type pairResult struct {
	Device  store.Record  `json:"device"`
	Machine *store.Record `json:"machine,omitempty"`
}

func (s *Service) PairDevice(ctx context.Context, req *connect.Request[pb.PairDeviceRequest]) (*connect.Response[pb.PairDeviceResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	raw, err := base64.RawURLEncoding.DecodeString(req.Msg.Code)
	if err != nil || len(raw) != 32 || len(req.Msg.CredentialDigest) != 32 {
		return nil, rpc.Error(domain.Fail(domain.Unauthenticated, "The pairing material is invalid.", "Request a fresh pairing code and generate a private device credential."), correlation)
	}
	if err := domain.ID(req.Msg.DeviceId).Validate(); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	codeHash := sha256.Sum256([]byte(req.Msg.Code))
	// Never pass the raw code to receipt hashing/serialization or persisted state.
	input := struct {
		PairingID        string
		DeviceID         string
		CodeDigest       []byte
		CredentialDigest []byte
		MachineID        string
		MachineJSON      json.RawMessage
	}{req.Msg.PairingId, req.Msg.DeviceId, codeHash[:], req.Msg.CredentialDigest, req.Msg.MachineId, nil}
	if len(req.Msg.MachineJson) > 0 {
		input.MachineJSON = req.Msg.MachineJson
	}
	result, err := s.Store.Mutate(ctx, domain.ID(req.Msg.RequestId), "device.pair", input, func(tx *store.Tx) (any, error) {
		grantRecord, err := tx.Get(domain.PairingKind, domain.ID(req.Msg.PairingId))
		if err != nil {
			return nil, domain.Fail(domain.Unauthenticated, "The pairing grant is unavailable.", "Request a fresh pairing code.")
		}
		grant, err := store.Decode[domain.Pairing](grantRecord)
		if err != nil {
			return nil, err
		}
		if grant.UsedBy != "" || !grant.ExpiresAt.After(time.Now()) {
			return nil, domain.Fail(domain.Unauthenticated, "The pairing code expired or was already used.", "Request a fresh pairing code.")
		}
		expected, err := tx.PairingVerifier(grantRecord.ID)
		if err != nil {
			return nil, err
		}
		if subtle.ConstantTimeCompare(expected, codeHash[:]) != 1 {
			return nil, domain.Fail(domain.Unauthenticated, "The pairing code is invalid.", "Use the exact unexpired pairing code.")
		}
		device := domain.Device{Name: grant.Name, Type: grant.Type, MachineID: domain.ID(req.Msg.MachineId), PairedAt: time.Now().UTC()}
		if err := device.Validate(); err != nil {
			return nil, err
		}
		var machine *store.Record
		if grant.Type == domain.WorkerDevice {
			var value domain.Machine
			if err := domain.Decode(req.Msg.MachineJson, &value); err != nil {
				return nil, err
			}
			if err := value.Validate(); err != nil {
				return nil, err
			}
			if value.Version != rpc.Version {
				return nil, domain.Fail(domain.Unsupported, "The Worker version is incompatible with the server.", "Install a matching DeliDev Worker before pairing.")
			}
			value.Disabled = false
			value.LastSeen = time.Time{}
			value.Installations = nil
			value.DiscoveryRevision = 0
			created, err := tx.Put(domain.MachineKind, device.MachineID, 0, "", "", value)
			if err != nil {
				return nil, err
			}
			machine = &created
		} else if len(req.Msg.MachineJson) > 0 {
			return nil, domain.Fail(domain.InvalidArgument, "Client pairing cannot contain Worker metadata.", "Use a Worker-specific pairing grant.")
		}
		record, err := tx.Put(domain.DeviceKind, domain.ID(req.Msg.DeviceId), 0, "", "", device)
		if err != nil {
			return nil, err
		}
		if err := tx.PutCredential(record.ID, req.Msg.CredentialDigest); err != nil {
			return nil, err
		}
		if err := tx.ConsumePairing(grantRecord.ID); err != nil {
			return nil, err
		}
		grant.UsedBy = record.ID
		if _, err := tx.Put(domain.PairingKind, grantRecord.ID, grantRecord.Revision, "", "", grant); err != nil {
			return nil, err
		}
		return pairResult{Device: record, Machine: machine}, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var paired pairResult
	if err := domain.Decode(result.Data, &paired); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	// An acknowledgment retry cannot restore a subsequently revoked device.
	current, err := s.Store.Get(ctx, domain.DeviceKind, paired.Device.ID)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	device, err := store.Decode[domain.Device](current)
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	if device.Revoked {
		return nil, rpc.Error(domain.Fail(domain.Unauthenticated, "This paired device has been revoked.", "Request a new grant and pair a new device identity."), correlation)
	}
	response := &pb.PairDeviceResponse{Device: rpc.Resource(paired.Device), ServerId: string(s.Identity.ServerID), Replayed: result.Replayed}
	if paired.Machine != nil {
		response.Machine = rpc.Resource(*paired.Machine)
	}
	s.logger.Info("device_paired", "device_id", paired.Device.ID, "device_type", device.Type, "replayed", result.Replayed)
	out := connect.NewResponse(response)
	rpc.CopyCorrelation(out, req.Header())
	return out, nil
}
func (s *Service) RevokeDevice(ctx context.Context, req *connect.Request[pb.RevokeDeviceRequest]) (*connect.Response[pb.RevokeDeviceResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	meta := req.Msg.Mutation
	if meta == nil || meta.ExpectedRevision == 0 {
		return nil, rpc.Error(domain.Fail(domain.MissingInput, "Device revocation requires its current revision and request ID.", "Inspect the device first."), correlation)
	}
	input := struct {
		ID       string
		Revision uint64
	}{meta.Id, meta.ExpectedRevision}
	result, err := s.Store.Mutate(ctx, domain.ID(meta.RequestId), "device.revoke", input, func(tx *store.Tx) (any, error) {
		record, err := tx.Get(domain.DeviceKind, domain.ID(meta.Id))
		if err != nil {
			return nil, err
		}
		device, err := store.Decode[domain.Device](record)
		if err != nil {
			return nil, err
		}
		now := time.Now().UTC()
		device.Revoked = true
		device.RevokedAt = &now
		updated, err := tx.Put(domain.DeviceKind, record.ID, meta.ExpectedRevision, "", "", device)
		if err != nil {
			return nil, err
		}
		if err := tx.RevokeCredential(record.ID); err != nil {
			return nil, err
		}
		if device.MachineID != "" {
			record, err := tx.Get(domain.MachineKind, device.MachineID)
			if err != nil {
				return nil, err
			}
			machine, err := store.Decode[domain.Machine](record)
			if err != nil {
				return nil, err
			}
			machine.Disabled = true
			if _, err := tx.Put(domain.MachineKind, record.ID, record.Revision, "", "", machine); err != nil {
				return nil, err
			}
			if err := revokeMachineJobs(tx, device.MachineID); err != nil {
				return nil, err
			}
		}
		return updated, nil
	})
	if err != nil {
		return nil, rpc.Error(err, correlation)
	}
	var record store.Record
	if err := domain.Decode(result.Data, &record); err != nil {
		return nil, rpc.Error(err, correlation)
	}
	s.cancelDevice(record.ID)
	s.logger.Info("device_revoked", "device_id", record.ID)
	response := connect.NewResponse(&pb.RevokeDeviceResponse{Device: rpc.Resource(record), RequestId: meta.RequestId, Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
