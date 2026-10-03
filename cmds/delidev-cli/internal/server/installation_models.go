// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/sshsetup"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/updates"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type installationState string

const (
	installationObserved  installationState = "OBSERVED"
	installationRequested installationState = "REQUESTED"
	installationRunning   installationState = "RUNNING"
	installationWaiting   installationState = "WAITING_FOR_IDLE"
	installationReady     installationState = "READY_TO_INSTALL"
	installationSucceeded installationState = "SUCCEEDED"
	installationFailed    installationState = "FAILED"
	installationUncertain installationState = "UNCERTAIN"
	installationCanceled  installationState = "CANCELED"
)

type sshOperation struct {
	ServerID              domain.ID             `json:"server_id"`
	Actor                 domain.Principal      `json:"actor"`
	Target                sshsetup.Target       `json:"target"`
	Identity              sshsetup.Identity     `json:"identity"`
	State                 installationState     `json:"state"`
	Name                  string                `json:"name,omitempty"`
	StartRequestID        domain.ID             `json:"start_request_id,omitempty"`
	CredentialRemoved     bool                  `json:"credential_removed"`
	ReconcileRequested    bool                  `json:"reconcile_requested"`
	CancellationRequested bool                  `json:"cancellation_requested"`
	ProblemCode           domain.Code           `json:"problem_code,omitempty"`
	Result                *sshsetup.SetupResult `json:"result,omitempty"`
}
type updateOperation struct {
	ServerID        domain.ID         `json:"server_id"`
	Actor           domain.Principal  `json:"actor"`
	State           installationState `json:"state"`
	Component       updates.Component `json:"component"`
	Target          updates.Target    `json:"target"`
	CurrentVersion  string            `json:"current_version"`
	Version         string            `json:"version"`
	Manifest        []byte            `json:"manifest"`
	ManifestSHA256  string            `json:"manifest_sha256"`
	MachineID       domain.ID         `json:"machine_id,omitempty"`
	MachineRevision uint64            `json:"machine_revision,omitempty"`
	DeviceID        domain.ID         `json:"device_id,omitempty"`
	ClaimedInstance domain.ID         `json:"claimed_instance,omitempty"`
	ClaimRequestID  domain.ID         `json:"claim_request_id,omitempty"`
	ClaimedRevision uint64            `json:"claimed_revision,omitempty"`
	ProblemCode     domain.Code       `json:"problem_code,omitempty"`
	FinishedAt      *time.Time        `json:"finished_at,omitempty"`
}
type releaseClient interface {
	Latest(context.Context, string, time.Time) (updates.Verified, error)
	Download(context.Context, updates.Verified, updates.Component, updates.Target, string) (string, error)
	Close()
}

func (s *Service) releaseClient() (releaseClient, error) {
	if s.releaseFactory != nil {
		return s.releaseFactory()
	}
	return updates.NewClient()
}
func installationFailure(code domain.Code) error {
	return domain.Fail(code, "The original installation operation could not proceed.", "Inspect its original state and identity. Preserve existing registrations, workspaces and the current working version.")
}
func installationActor(ctx context.Context) (domain.Principal, error) { return requireOAuthActor(ctx) }
func checkInstallationMutation(m *pb.Mutation) error {
	if m == nil || m.ExpectedRevision == 0 || domain.ID(m.Id).Validate() != nil || domain.ID(m.RequestId).Validate() != nil {
		return installationFailure(domain.InvalidArgument)
	}
	return nil
}
func installationRecord(tx *store.Tx, kind domain.Kind, id domain.ID, actor domain.Principal, expected uint64) (store.Record, error) {
	if err := tx.Authorize(); err != nil {
		return store.Record{}, err
	}
	r, e := tx.Get(kind, id)
	if e != nil {
		return r, e
	}
	if expected != 0 && r.Revision != expected {
		return r, installationFailure(domain.Conflict)
	}
	var projection struct {
		ServerID domain.ID        `json:"server_id"`
		Actor    domain.Principal `json:"actor"`
	}
	if json.Unmarshal(r.Data, &projection) != nil || projection.Actor != actor {
		return r, installationFailure(domain.PermissionDenied)
	}
	return r, nil
}
func (s *Service) readInstallation(ctx context.Context, kind domain.Kind, id string) (store.Record, error) {
	actor, e := installationActor(ctx)
	if e != nil {
		return store.Record{}, e
	}
	if domain.ID(id).Validate() != nil {
		return store.Record{}, installationFailure(domain.InvalidArgument)
	}
	var r store.Record
	e = s.Store.Read(ctx, func(tx *store.Tx) error {
		var e error
		r, e = installationRecord(tx, kind, domain.ID(id), actor, 0)
		if e == nil {
			var owner struct {
				ServerID domain.ID `json:"server_id"`
			}
			if json.Unmarshal(r.Data, &owner) != nil || owner.ServerID != s.Identity.ServerID {
				return installationFailure(domain.RecoveryRequired)
			}
		}
		return e
	})
	return r, e
}
