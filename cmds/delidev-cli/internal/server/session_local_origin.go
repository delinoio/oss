// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/sha256"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func localOriginRequired() *domain.Error {
	return domain.Fail(domain.Unauthenticated, "Server authentication is required.", "Use the server token or a registered device credential.")
}

// Local origin remains immutable attribution. The primary authenticated caller
// selects the execution machine; a second device credential is no longer proof
// required for admission. The legacy token field remains wire-compatible.
func (s *Service) authenticateLocalOrigin(ctx context.Context, input domain.CreateSession, token string) (*domain.LocalOrigin, [sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	if _, ok := domain.PrincipalFrom(ctx); !ok {
		return nil, digest, localOriginRequired()
	}
	if input.Workspace != domain.Local {
		return nil, digest, nil
	}
	var origin *domain.LocalOrigin
	err := s.Store.Read(ctx, func(tx *store.Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		r, err := tx.Get(domain.MachineKind, input.MachineID)
		if err != nil {
			return err
		}
		_, err = store.Decode[domain.Machine](r)
		if err != nil {
			return err
		}
		device, err := tx.InstallationWorkerDevice(input.MachineID)
		if err != nil {
			return err
		}
		origin = &domain.LocalOrigin{MachineID: input.MachineID, DeviceID: device}
		return nil
	})
	return origin, digest, err
}

func validateLocalOrigin(tx *store.Tx, session domain.Session) error {
	if err := tx.Authorize(); err != nil {
		return err
	}
	if origin := session.LocalOrigin; origin != nil {
		if domain.UniqueIDs([]domain.ID{origin.MachineID, origin.DeviceID}) != nil {
			return domain.Fail(domain.InvalidArgument, "Local origin metadata is invalid.", "Use valid machine and device references.")
		}
		if origin.MachineID != session.MachineID {
			domain.ObserveOwnership(domain.OwnershipMachine, session.MachineID)
		}
	}
	return nil
}
