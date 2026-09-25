package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func localOriginRequired() *domain.Error {
	return domain.Fail(domain.PermissionDenied, "Local creation requires the originating computer's paired Worker authority.", "Load that computer's private Worker scope with --local-worker-dir; use Worktree for another execution machine.")
}

// A machine ID, loopback server URL or current viewing client is not provenance.
// Local creation additionally authenticates the paired Worker credential loaded
// from the caller's local private scope. Only its non-secret identities persist.
func (s *Service) authenticateLocalOrigin(ctx context.Context, input domain.CreateSession, token string) (*domain.LocalOrigin, [sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	if input.Workspace != domain.Local {
		if token != "" {
			return nil, digest, domain.Fail(domain.InvalidArgument, "Local Worker authority is only accepted for Local sessions.", "Remove the Local credential for Worktree or General Chat creation.")
		}
		return nil, digest, nil
	}
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || (actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice) {
		return nil, digest, localOriginRequired()
	}
	if len(token) != 43 {
		return nil, digest, localOriginRequired()
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 || base64.RawURLEncoding.EncodeToString(raw) != token {
		return nil, digest, localOriginRequired()
	}
	digest = sha256.Sum256([]byte(token))
	origin, err := s.Store.Authenticate(ctx, digest[:])
	if err != nil {
		return nil, digest, err
	}
	if origin.Type != domain.WorkerDevice || origin.MachineID != input.MachineID {
		return nil, digest, localOriginRequired()
	}
	return &domain.LocalOrigin{MachineID: origin.MachineID, DeviceID: origin.DeviceID}, digest, nil
}

func validateLocalOrigin(tx *store.Tx, session domain.Session) error {
	if session.Workspace != domain.Local {
		if session.LocalOrigin != nil {
			return localOriginRequired()
		}
		return nil
	}
	origin := session.LocalOrigin
	if origin == nil || origin.MachineID != session.MachineID || domain.UniqueIDs([]domain.ID{origin.MachineID, origin.DeviceID}) != nil {
		return localOriginRequired()
	}
	r, err := tx.Get(domain.DeviceKind, origin.DeviceID)
	if err != nil {
		if domain.SafeError(err).Code == domain.NotFound {
			return localOriginRequired()
		}
		return err
	}
	device, err := store.Decode[domain.Device](r)
	if err != nil || device.Revoked || device.Type != domain.WorkerDevice || device.MachineID != origin.MachineID {
		return localOriginRequired()
	}
	return nil
}
