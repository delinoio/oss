// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/sshsetup"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/updates"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
)

type sshClaim struct {
	Version     uint32       `json:"version"`
	ID          domain.ID    `json:"id"`
	InputSHA256 string       `json:"input_sha256"`
	Operation   sshOperation `json:"operation"`
}

func (s *Service) sshProtected(ctx context.Context, ref credentials.Ref, input []byte) ([]byte, error) {
	unlock, e := s.lockAccounts(ctx)
	if e != nil {
		return nil, e
	}
	defer unlock()
	vault, e := s.secrets()
	if e != nil {
		return nil, e
	}
	if input != nil {
		_, e = vault.Put(ctx, ref, input)
		return nil, e
	}
	return vault.Get(ctx, ref)
}
func (s *Service) updateSSHState(parent context.Context, id domain.ID, start domain.ID, apply func(*store.Tx, *sshOperation) error) error {
	ctx, cancel := context.WithTimeout(domain.WithPrincipal(parent, domain.Principal{Type: domain.OwnerDevice}), 5*time.Second)
	defer cancel()
	_, e := s.Store.Mutate(ctx, domain.NewID(), "installation.ssh.progress", struct{ ID, Start domain.ID }{id, start}, func(tx *store.Tx) (any, error) {
		r, e := tx.Get(domain.SSHSetupKind, id)
		if e != nil {
			return nil, e
		}
		var o sshOperation
		if domain.Decode(r.Data, &o) != nil || o.ServerID != s.Identity.ServerID || o.StartRequestID != start {
			return nil, installationFailure(domain.RecoveryRequired)
		}
		before := string(r.Data)
		if e = apply(tx, &o); e != nil {
			return nil, e
		}
		after, _ := json.Marshal(o)
		if before == string(after) {
			return installationReceipt{id}, nil
		}
		_, e = tx.Put(r.Kind, r.ID, r.Revision, "", "", o)
		return installationReceipt{id}, e
	})
	return e
}
func (s *Service) recoverSSHClaims(ctx context.Context) error {
	directory := filepath.Join(s.Store.Root(), "ssh-setup-claims")
	if e := security.PrivateDir(directory); e != nil {
		return e
	}
	entries, e := os.ReadDir(directory)
	if e != nil || len(entries) > 4096 {
		return installationFailure(domain.RecoveryRequired)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".pending-") {
			continue
		}
		id := domain.ID(strings.TrimSuffix(entry.Name(), ".json"))
		if entry.IsDir() || id.Validate() != nil || entry.Name() != string(id)+".json" {
			return installationFailure(domain.RecoveryRequired)
		}
		raw, e := security.ReadPrivate(filepath.Join(directory, entry.Name()), 32<<10)
		var claim sshClaim
		if e != nil || domain.Decode(raw, &claim) != nil || claim.Version != 1 || claim.ID != id || claim.Operation.ServerID != s.Identity.ServerID || claim.InputSHA256 != sshOperationDigest(claim.Operation) || claim.Operation.StartRequestID.Validate() != nil {
			return installationFailure(domain.RecoveryRequired)
		}
		_, e = s.Store.Mutate(ctx, domain.NewID(), "installation.ssh.recover-claim", struct {
			ID     domain.ID
			Digest string
		}{id, claim.InputSHA256}, func(tx *store.Tx) (any, error) {
			r, e := tx.Get(domain.SSHSetupKind, id)
			if e != nil {
				return nil, e
			}
			var o sshOperation
			if domain.Decode(r.Data, &o) != nil {
				return nil, installationFailure(domain.RecoveryRequired)
			}
			if o.State != installationObserved && o.State != installationRequested && o.State != installationRunning {
				return installationReceipt{id}, nil
			}
			if o.StartRequestID != "" && sshOperationDigest(o) != claim.InputSHA256 {
				return nil, installationFailure(domain.RecoveryRequired)
			}
			if o.Actor != claim.Operation.Actor || o.Target != claim.Operation.Target || o.Identity != claim.Operation.Identity {
				return nil, installationFailure(domain.RecoveryRequired)
			}
			o = claim.Operation
			o.State = installationUncertain
			o.ProblemCode = domain.RecoveryRequired
			_, e = tx.Put(r.Kind, r.ID, r.Revision, "", "", o)
			return installationReceipt{id}, e
		})
		if e != nil {
			return e
		}
	}
	return nil
}
func (s *Service) runSSHSetups(parent context.Context) {
	ctx, cancel := context.WithCancel(domain.WithPrincipal(parent, domain.Principal{Type: domain.OwnerDevice}))
	var group sync.WaitGroup
	defer func() { cancel(); group.Wait() }()
	if e := s.recoverSSHClaims(ctx); e != nil {
		s.logger.Error("ssh_setup_recovery_failed", "code", domain.SafeError(e).Code)
		return
	}
	active := map[domain.ID]bool{}
	done := make(chan domain.ID, 4)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if len(active) < 4 {
			records, e := s.Store.PendingSSHInstallations(ctx)
			if e != nil {
				s.logger.Warn("ssh_setup_scan_failed", "code", domain.SafeError(e).Code)
			} else {
				for _, r := range records {
					if len(active) >= 4 {
						break
					}
					if active[r.ID] {
						continue
					}
					active[r.ID] = true
					group.Add(1)
					go func(r store.Record) {
						defer group.Done()
						s.runSSHSetup(ctx, r)
						select {
						case done <- r.ID:
						case <-ctx.Done():
						}
					}(r)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case id := <-done:
			delete(active, id)
		case <-ticker.C:
		}
	}
}
func (s *Service) runSSHSetup(parent context.Context, r store.Record) {
	var o sshOperation
	if domain.Decode(r.Data, &o) != nil || o.ServerID != s.Identity.ServerID || o.StartRequestID != "" && o.StartRequestID.Validate() != nil {
		return
	}
	if o.CancellationRequested && (o.State == installationCanceled || o.State == installationSucceeded || o.State == installationFailed) {
		s.removeSSHCredentials(parent, r.ID, o)
		return
	}
	reconcile := o.ReconcileRequested
	ctx, cancel := context.WithTimeout(domain.WithPrincipal(parent, o.Actor), 15*time.Minute)
	defer cancel()
	authorityDone := make(chan struct{})
	go func() {
		defer close(authorityDone)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				e := s.Store.Read(ctx, func(tx *store.Tx) error {
					if e := tx.Authorize(); e != nil {
						return e
					}
					current, e := tx.Get(domain.SSHSetupKind, r.ID)
					if e != nil {
						return e
					}
					var v sshOperation
					if domain.Decode(current.Data, &v) != nil || v.StartRequestID != o.StartRequestID || (v.CancellationRequested && !reconcile) || v.CredentialRemoved {
						return installationFailure(domain.PermissionDenied)
					}
					return nil
				})
				if e != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-authorityDone }()
	outcome, result := s.performSSHSetup(ctx, r, o, reconcile)
	finishCtx, finishCancel := context.WithTimeout(domain.WithPrincipal(context.WithoutCancel(parent), domain.Principal{Type: domain.OwnerDevice}), 5*time.Second)
	defer finishCancel()
	e := s.updateSSHState(finishCtx, r.ID, o.StartRequestID, func(tx *store.Tx, current *sshOperation) error {
		current.ReconcileRequested = false
		if outcome != nil {
			current.State = installationUncertain
			current.ProblemCode = domain.SafeError(outcome).Code
			return nil
		}
		if result == nil {
			return installationFailure(domain.RecoveryRequired)
		}
		if e := originalInstallationActor(tx, current.Actor); e != nil {
			current.State = installationUncertain
			current.ProblemCode = domain.PermissionDenied
			return nil
		}
		device, e := tx.Get(domain.DeviceKind, result.DeviceID)
		if e != nil {
			return e
		}
		d, e := store.Decode[domain.Device](device)
		if e != nil || d.Revoked || d.MachineID != result.MachineID || d.Type != domain.WorkerDevice {
			return installationFailure(domain.PermissionDenied)
		}
		_, machine, e := activeMachine(tx, result.MachineID)
		if e != nil || machine.Version != result.WorkerVersion || machine.OS+"-"+machine.Architecture != string(result.Target) {
			return installationFailure(domain.RecoveryRequired)
		}
		current.State = installationSucceeded
		current.Result = result
		current.ProblemCode = ""
		return nil
	})
	if e != nil {
		s.logger.Warn("ssh_setup_publication_failed", "operation_id", r.ID, "code", domain.SafeError(e).Code)
	} else {
		s.logger.Info("ssh_setup_settled", "operation_id", r.ID, "verified", outcome == nil, "failed", outcome != nil, "cause", func() string {
			if outcome != nil {
				return domain.SafeError(outcome).Cause
			}
			return ""
		}())
	}
}

// The transaction is already owner-authorized for metadata publication. Recheck
// the original paired actor independently without acquiring another transaction.
func originalInstallationActor(tx *store.Tx, actor domain.Principal) error {
	if actor.Type == domain.OwnerDevice {
		return nil
	}
	if actor.Type != domain.ClientDevice {
		return installationFailure(domain.PermissionDenied)
	}
	r, e := tx.Get(domain.DeviceKind, actor.DeviceID)
	if e != nil {
		return e
	}
	d, e := store.Decode[domain.Device](r)
	if e != nil || d.Revoked || d.Type != actor.Type {
		return installationFailure(domain.PermissionDenied)
	}
	return nil
}
func (s *Service) performSSHSetup(ctx context.Context, r store.Record, o sshOperation, reconcile bool) (error, *sshsetup.SetupResult) {
	s.logger.InfoContext(ctx, "ssh_setup_phase", "operation_id", r.ID, "phase", "claim")
	path := s.sshClaimPath(r.ID)
	if !reconcile {
		if o.State != installationRequested {
			return installationFailure(domain.RecoveryRequired), nil
		}
		if _, e := os.Lstat(path); !errors.Is(e, os.ErrNotExist) {
			return installationFailure(domain.RecoveryRequired), nil
		}
		claim := sshClaim{1, r.ID, sshOperationDigest(o), o}
		raw, _ := json.Marshal(claim)
		if e := security.WriteAtomic(path, raw); e != nil {
			return e, nil
		}
		if e := s.updateSSHState(ctx, r.ID, o.StartRequestID, func(tx *store.Tx, v *sshOperation) error {
			if e := originalInstallationActor(tx, v.Actor); e != nil {
				return e
			}
			if v.State != installationRequested || v.CancellationRequested {
				return installationFailure(domain.Conflict)
			}
			v.State = installationRunning
			return nil
		}); e != nil {
			return e, nil
		}
	}
	if reconcile {
		raw, e := security.ReadPrivate(path, 32<<10)
		var claim sshClaim
		if e != nil || domain.Decode(raw, &claim) != nil || claim.ID != r.ID || claim.Version != 1 || claim.Operation.ServerID != s.Identity.ServerID || claim.InputSHA256 != sshOperationDigest(o) || claim.InputSHA256 != sshOperationDigest(claim.Operation) {
			return installationFailure(domain.RecoveryRequired), nil
		}
	}
	secret, e := s.sshProtected(ctx, credentials.Ref{Owner: r.ID, ID: o.StartRequestID, Purpose: credentials.WorkerSSH}, nil)
	if e != nil {
		return e, nil
	}
	defer clear(secret)
	var credential sshsetup.Credential
	if domain.DecodeWithLimit(secret, &credential, 64<<10) != nil {
		return installationFailure(domain.RecoveryRequired), nil
	}
	defer credential.Clear()
	s.logger.InfoContext(ctx, "ssh_setup_phase", "operation_id", r.ID, "phase", "authenticate")
	connection, e := sshsetup.Connect(ctx, o.Target, o.Identity, credential)
	if e != nil {
		return e, nil
	}
	defer connection.Close()
	var document sshsetup.SetupDocument
	if reconcile {
		raw, e := s.sshProtected(ctx, credentials.Ref{Owner: r.ID, ID: r.ID, Purpose: credentials.WorkerSSH}, nil)
		if e != nil {
			return e, nil
		}
		defer clear(raw)
		if domain.Decode(raw, &document) != nil || document.OperationID != r.ID || document.ServerID != s.Identity.ServerID {
			return installationFailure(domain.RecoveryRequired), nil
		}
	} else {
		s.logger.InfoContext(ctx, "ssh_setup_phase", "operation_id", r.ID, "phase", "inspect-target")
		target, e := connection.Inspect(ctx)
		if e != nil {
			return e, nil
		}
		client, e := s.releaseClient()
		if e != nil {
			return e, nil
		}
		defer client.Close()
		candidate, e := client.Release(ctx, rpc.Version, time.Now().UTC())
		if e != nil {
			return e, nil
		}
		artifact, e := candidate.Artifact(updates.Worker, target)
		if e != nil {
			return e, nil
		}
		source, e := client.Download(ctx, candidate, updates.Worker, target, filepath.Join(s.Store.Root(), "update-downloads"))
		if e != nil {
			return e, nil
		}
		s.logger.InfoContext(ctx, "ssh_setup_phase", "operation_id", r.ID, "phase", "stage")
		if e = connection.Stage(ctx, s.Identity.ServerID, r.ID, artifact, source); e != nil {
			return e, nil
		}
		code, e := worker.RandomToken()
		if e != nil {
			return e, nil
		}
		grant := worker.PairingCode{Version: 1, PairingID: domain.NewID(), ServerID: s.Identity.ServerID, Endpoint: s.Endpoint.URL, Code: code}
		document = sshsetup.SetupDocument{Version: 1, OperationID: r.ID, ServerID: s.Identity.ServerID, ReleaseVersion: candidate.Payload.Version, SourceRevision: candidate.Payload.SourceRevision, Artifact: artifact, Grant: grant, Name: o.Name}
		raw, _ := json.Marshal(document)
		if _, e = s.sshProtected(ctx, credentials.Ref{Owner: r.ID, ID: r.ID, Purpose: credentials.WorkerSSH}, raw); e != nil {
			clear(raw)
			return e, nil
		}
		clear(raw)
		sum := sha256.Sum256([]byte(code))
		_, e = s.Store.Mutate(ctx, grant.PairingID, "installation.ssh.pairing", struct {
			ID, Setup domain.ID
			Digest    []byte
		}{grant.PairingID, r.ID, sum[:]}, func(tx *store.Tx) (any, error) {
			if e := tx.Authorize(); e != nil {
				return nil, e
			}
			_, e := tx.Put(domain.PairingKind, grant.PairingID, 0, "", "", domain.Pairing{Name: o.Name, Type: domain.WorkerDevice, ExpiresAt: time.Now().UTC().Add(5 * time.Minute)})
			if e != nil {
				return nil, e
			}
			e = tx.PutPairingVerifier(grant.PairingID, sum[:])
			return installationReceipt{grant.PairingID}, e
		})
		if e != nil {
			return e, nil
		}
	}
	s.logger.InfoContext(ctx, "ssh_setup_phase", "operation_id", r.ID, "phase", "native-setup", "inspect_only", reconcile)
	result, e := connection.Setup(ctx, document, reconcile)
	if e != nil {
		return e, nil
	}
	return nil, &result
}
func (s *Service) removeSSHCredentials(ctx context.Context, id domain.ID, o sshOperation) {
	unlock, e := s.lockAccounts(ctx)
	if e != nil {
		return
	}
	vault, e := s.secrets()
	if e == nil {
		refs, err := vault.UnremovedReferences(ctx, id)
		e = err
		if e == nil {
			for _, ref := range refs {
				if ref.Purpose == credentials.WorkerSSH {
					if e = vault.Delete(ctx, ref); e != nil {
						break
					}
				}
			}
		}
	}

	unlock()
	if e != nil {
		s.logger.Warn("ssh_credential_removal_pending", "operation_id", id, "code", domain.SafeError(e).Code)
		return
	}
	if e = s.updateSSHState(ctx, id, o.StartRequestID, func(_ *store.Tx, v *sshOperation) error { v.CredentialRemoved = true; return nil }); e != nil {
		s.logger.Warn("ssh_credential_removal_publication_failed", "operation_id", id, "code", domain.SafeError(e).Code)
	}
}
