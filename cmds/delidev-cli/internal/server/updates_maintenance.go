// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/updates"
	"slices"
	"time"
)

// Automatic Worker updates share the exact original admission/fence/claim path.
// A canceled, failed or uncertain attempt is never silently submitted again.
func (s *Service) runWorkerUpdateMaintenance(parent context.Context) {
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-parent.Done():
			return
		case <-timer.C:
		}
		if updates.ProductionReady() {
			ctx, cancel := context.WithTimeout(domain.WithPrincipal(parent, domain.Principal{Type: domain.OwnerDevice}), 2*time.Minute)
			err := s.observeWorkerUpdates(ctx)
			cancel()
			if err != nil && parent.Err() == nil {
				s.logger.WarnContext(parent, "worker_update_check_failed", "code", domain.SafeError(err).Code)
			}
		}
		timer.Reset(24 * time.Hour)
	}
}
func (s *Service) observeWorkerUpdates(ctx context.Context) error {
	client, err := s.releaseClient()
	if err != nil {
		return err
	}
	defer client.Close()
	candidate, err := client.Latest(ctx, "0.0.0", time.Now().UTC())
	if err != nil {
		if domain.SafeError(err).Code == domain.NotFound {
			return nil
		}
		return err
	}
	var after domain.ID
	for {
		rows, err := s.Store.List(ctx, store.Filter{Kind: domain.MachineKind, After: after, Limit: store.MaxPage})
		if err != nil {
			return err
		}
		for _, r := range rows {
			after = r.ID
			m, err := store.Decode[domain.Machine](r)
			if err != nil {
				return err
			}
			if m.Disabled || !slices.Contains(m.WorkerCapabilities, domain.SignedWorkerUpdatesV1) {
				continue
			}
			newer, err := updates.Newer(candidate.Payload.Version, m.Version)
			if err != nil || !newer {
				continue
			}
			target, err := updates.SelectTarget(m.OS, m.Architecture)
			if err != nil {
				continue
			}
			if _, err = candidate.Artifact(updates.Worker, target); err != nil {
				return err
			}
			id := domain.NewID()
			_, err = s.Store.Mutate(ctx, id, "installation.update.automatic", struct {
				Machine domain.ID
				Version string
			}{r.ID, candidate.Payload.Version}, func(tx *store.Tx) (any, error) {
				if e := tx.Authorize(); e != nil {
					return nil, e
				}
				current, latest, e := activeMachine(tx, r.ID)
				if e != nil {
					return nil, e
				}
				if current.Revision != r.Revision || latest.Version != m.Version {
					return nil, installationFailure(domain.Conflict)
				}
				if seen, e := tx.WorkerUpdateAttemptExists(r.ID, candidate.Payload.Version); e != nil || seen {
					return nil, e
				}
				if e = tx.WorkerUpdateAdmission(r.ID); e != nil {
					return nil, e
				}
				device, e := tx.InstallationWorkerDevice(r.ID)
				if e != nil {
					return nil, e
				}
				op := updateOperation{ServerID: s.Identity.ServerID, Actor: domain.Principal{Type: domain.OwnerDevice}, State: updates.Waiting, Component: updates.Worker, Target: target, CurrentVersion: m.Version, Version: candidate.Payload.Version, Manifest: candidate.Canonical, ManifestSHA256: candidate.ManifestSHA256, MachineID: r.ID, MachineRevision: r.Revision, DeviceID: device}
				_, e = tx.Put(domain.UpdateKind, id, 0, "", "", op)
				return installationReceipt{id}, e
			})
			if err != nil && domain.SafeError(err).Code != domain.Conflict {
				return err
			}
		}
		if len(rows) < store.MaxPage {
			return nil
		}
	}
}
