// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/sshsetup"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type installationReceipt struct {
	ID domain.ID `json:"id"`
}

func (s *Service) installationCommitment(id domain.ID, secret []byte) string {
	h := hmac.New(sha256.New, []byte(s.Identity.Token))
	h.Write([]byte("delidev/ssh-setup/credential/v1\x00" + string(s.Identity.ServerID) + "\x00" + string(id) + "\x00"))
	h.Write(secret)
	return hex.EncodeToString(h.Sum(nil))
}
func (s *Service) installationResult(ctx context.Context, kind domain.Kind, result store.Result) (store.Record, error) {
	var receipt installationReceipt
	if domain.Decode(result.Data, &receipt) != nil {
		return store.Record{}, installationFailure(domain.RecoveryRequired)
	}
	return s.readInstallation(ctx, kind, string(receipt.ID))
}
func (s *Service) InspectSSHHost(ctx context.Context, req *connect.Request[pb.InspectSSHHostRequest]) (*connect.Response[pb.InspectSSHHostResponse], error) {
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, e := installationActor(ctx)
	target := sshsetup.Target{Host: req.Msg.Host, Port: uint16(req.Msg.Port), User: req.Msg.User}
	if e == nil && (domain.ID(req.Msg.RequestId).Validate() != nil || req.Msg.Port > 65535 || target.Validate() != nil) {
		e = installationFailure(domain.InvalidArgument)
	}
	if e != nil {
		return nil, rpc.Error(e, correlation)
	}
	input := struct {
		Actor  domain.Principal
		Target sshsetup.Target
	}{actor, target}
	result, replayed, e := s.Store.Replay(ctx, domain.ID(req.Msg.RequestId), "installation.ssh.inspect", input)
	if e != nil {
		return nil, rpc.Error(e, correlation)
	}
	if !replayed {
		ctx, cancel := context.WithTimeout(ctx, sshsetup.Timeout)
		defer cancel()
		identity, e := sshsetup.Observe(ctx, target)
		if e != nil {
			return nil, rpc.Error(e, correlation)
		}
		result, e = s.Store.Mutate(ctx, domain.ID(req.Msg.RequestId), "installation.ssh.inspect", input, func(tx *store.Tx) (any, error) {
			if e := tx.Authorize(); e != nil {
				return nil, e
			}
			r, e := tx.Put(domain.SSHSetupKind, domain.ID(req.Msg.RequestId), 0, "", "", sshOperation{ServerID: s.Identity.ServerID, Actor: actor, Target: target, Identity: identity, State: installationObserved})
			return installationReceipt{r.ID}, e
		})
		if e != nil {
			return nil, rpc.Error(e, correlation)
		}
	}
	r, e := s.installationResult(ctx, domain.SSHSetupKind, result)
	if e != nil {
		return nil, rpc.Error(e, correlation)
	}
	s.logger.InfoContext(ctx, "ssh_host_observed", "operation_id", r.ID, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.InspectSSHHostResponse{Setup: rpc.Resource(r), RequestId: string(result.RequestID), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) StartSSHSetup(ctx context.Context, req *connect.Request[pb.StartSSHSetupRequest]) (*connect.Response[pb.StartSSHSetupResponse], error) {
	defer clear(req.Msg.Credential)
	correlation := req.Header().Get(rpc.CorrelationHeader)
	actor, e := installationActor(ctx)
	m := req.Msg.Mutation
	if e == nil {
		e = checkInstallationMutation(m)
	}
	if e == nil {
		e = domain.Text(req.Msg.Name, "Runner Device name", 256, true)
	}
	if e != nil {
		return nil, rpc.Error(e, correlation)
	}
	input := struct {
		Actor                         domain.Principal
		ID                            domain.ID
		Revision                      uint64
		Name, Fingerprint, Commitment string
	}{actor, domain.ID(m.Id), m.ExpectedRevision, req.Msg.Name, req.Msg.ConfirmedFingerprint, s.installationCommitment(domain.ID(m.RequestId), req.Msg.Credential)}
	unlock, e := s.lockAccounts(ctx)
	if e != nil {
		return nil, rpc.Error(e, correlation)
	}
	defer unlock()
	result, replayed, e := s.Store.Replay(ctx, domain.ID(m.RequestId), "installation.ssh.start", input)
	if e != nil {
		return nil, rpc.Error(e, correlation)
	}
	if !replayed {
		// Resolve production release authority before consuming/staging a credential.
		client, e := s.releaseClient()
		if e != nil {
			return nil, rpc.Error(e, correlation)
		}
		client.Close()
		var credential sshsetup.Credential
		if domain.DecodeWithLimit(req.Msg.Credential, &credential, 64<<10) != nil || credential.Validate() != nil {
			return nil, rpc.Error(installationFailure(domain.InvalidArgument), correlation)
		}
		defer credential.Clear()
		var operation sshOperation
		e = s.Store.Read(ctx, func(tx *store.Tx) error {
			r, e := installationRecord(tx, domain.SSHSetupKind, input.ID, actor, input.Revision)
			if e != nil {
				return e
			}
			if domain.Decode(r.Data, &operation) != nil ||
				domain.OwnershipBlocks(domain.OwnershipInstance, "", operation.ServerID != s.Identity.ServerID) ||
				operation.State != installationObserved || operation.Identity.Fingerprint != input.Fingerprint {
				return installationFailure(domain.Conflict)
			}
			return nil
		})
		if e != nil {
			return nil, rpc.Error(e, correlation)
		}
		// A remote host cannot reach this server through its own loopback.
		// Reject the incompatible product topology before protected staging.
		if endpoint, problem := url.Parse(s.Endpoint.URL); problem == nil && endpoint.Host != "" && endpoint.Scheme == "http" {
			host := net.ParseIP(operation.Target.Host)
			local := operation.Target.Host == "localhost" || host != nil && host.IsLoopback()
			if !local {
				return nil, rpc.Error(domain.Fail(domain.Unsupported, "Remote SSH Workers require a reachable HTTPS server endpoint.", "Configure the server's explicit TLS endpoint before installing this Worker."), correlation)
			}
		}
		if _, e := os.Lstat(s.sshClaimPath(input.ID)); !errors.Is(e, os.ErrNotExist) {
			return nil, rpc.Error(installationFailure(domain.RecoveryRequired), correlation)
		}
		vault, e := s.secrets()
		if e != nil {
			return nil, rpc.Error(e, correlation)
		}
		ref := credentials.Ref{Owner: input.ID, ID: domain.ID(m.RequestId), Purpose: credentials.WorkerSSH}
		if _, e = vault.Put(ctx, ref, req.Msg.Credential); e != nil {
			return nil, rpc.Error(e, correlation)
		}
		result, e = s.Store.Mutate(ctx, domain.ID(m.RequestId), "installation.ssh.start", input, func(tx *store.Tx) (any, error) {
			r, e := installationRecord(tx, domain.SSHSetupKind, input.ID, actor, input.Revision)
			if e != nil {
				return nil, e
			}
			var current sshOperation
			if domain.Decode(r.Data, &current) != nil || current.State != installationObserved || current.Identity != operation.Identity {
				return nil, installationFailure(domain.Conflict)
			}
			current.State = installationRequested
			current.Name = input.Name
			current.StartRequestID = domain.ID(m.RequestId)
			_, e = tx.Put(r.Kind, r.ID, r.Revision, "", "", current)
			return installationReceipt{r.ID}, e
		})
		if e != nil {
			return nil, rpc.Error(e, correlation)
		}
	}
	r, e := s.installationResult(ctx, domain.SSHSetupKind, result)
	if e != nil {
		return nil, rpc.Error(e, correlation)
	}
	s.logger.InfoContext(ctx, "ssh_setup_accepted", "operation_id", r.ID, "request_id", result.RequestID, "replayed", result.Replayed)
	response := connect.NewResponse(&pb.StartSSHSetupResponse{Setup: rpc.Resource(r), RequestId: string(result.RequestID), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) GetSSHSetup(ctx context.Context, req *connect.Request[pb.GetSSHSetupRequest]) (*connect.Response[pb.GetSSHSetupResponse], error) {
	r, e := s.readInstallation(ctx, domain.SSHSetupKind, req.Msg.Id)
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	response := connect.NewResponse(&pb.GetSSHSetupResponse{Setup: rpc.Resource(r)})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) CancelSSHSetup(ctx context.Context, req *connect.Request[pb.CancelSSHSetupRequest]) (*connect.Response[pb.CancelSSHSetupResponse], error) {
	result, e := s.changeSSH(ctx, req.Msg.Mutation, "cancel")
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	r, e := s.installationResult(ctx, domain.SSHSetupKind, result)
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	response := connect.NewResponse(&pb.CancelSSHSetupResponse{Setup: rpc.Resource(r), RequestId: string(result.RequestID), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) ReconcileSSHSetup(ctx context.Context, req *connect.Request[pb.ReconcileSSHSetupRequest]) (*connect.Response[pb.ReconcileSSHSetupResponse], error) {
	result, e := s.changeSSH(ctx, req.Msg.Mutation, "reconcile")
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	r, e := s.installationResult(ctx, domain.SSHSetupKind, result)
	if e != nil {
		return nil, rpc.Error(e, req.Header().Get(rpc.CorrelationHeader))
	}
	response := connect.NewResponse(&pb.ReconcileSSHSetupResponse{Setup: rpc.Resource(r), RequestId: string(result.RequestID), Replayed: result.Replayed})
	rpc.CopyCorrelation(response, req.Header())
	return response, nil
}
func (s *Service) changeSSH(ctx context.Context, m *pb.Mutation, action string) (store.Result, error) {
	actor, e := installationActor(ctx)
	if e != nil {
		return store.Result{}, e
	}
	if e = checkInstallationMutation(m); e != nil {
		return store.Result{}, e
	}
	input := struct {
		Actor    domain.Principal
		ID       domain.ID
		Revision uint64
		Action   string
	}{actor, domain.ID(m.Id), m.ExpectedRevision, action}
	return s.Store.Mutate(ctx, domain.ID(m.RequestId), "installation.ssh."+action, input, func(tx *store.Tx) (any, error) {
		r, e := installationRecord(tx, domain.SSHSetupKind, input.ID, actor, input.Revision)
		if e != nil {
			return nil, e
		}
		var operation sshOperation
		if domain.Decode(r.Data, &operation) != nil {
			return nil, installationFailure(domain.RecoveryRequired)
		}
		if action == "cancel" {
			operation.CancellationRequested = true
			if operation.State == installationObserved || operation.State == installationRequested {
				operation.State = installationCanceled
			} else if operation.State == installationRunning {
				operation.State = installationUncertain
			}
		}
		if action == "reconcile" {
			if operation.State != installationUncertain || operation.CredentialRemoved {
				return nil, installationFailure(domain.Conflict)
			}
			operation.ReconcileRequested = true
		}
		_, e = tx.Put(r.Kind, r.ID, r.Revision, "", "", operation)
		return installationReceipt{r.ID}, e
	})
}
func (s *Service) sshClaimPath(id domain.ID) string {
	return filepath.Join(s.Store.Root(), "ssh-setup-claims", string(id)+".json")
}
func sshOperationDigest(o sshOperation) string {
	o.State = ""
	o.CancellationRequested = false
	o.ReconcileRequested = false
	o.ProblemCode = ""
	o.Result = nil
	o.CredentialRemoved = false
	raw, _ := json.Marshal(o)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
