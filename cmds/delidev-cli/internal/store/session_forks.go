package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

// A durable fork job reserves only its original source boundary. Uncertainty
// keeps that reservation; another request cannot bypass native ownership loss.
func (t *Tx) RequireNoSessionFork(session domain.ID) error {
	var exists bool
	err := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM entities WHERE kind='job' AND session_id=? AND json_extract(body,'$.type')='fork-session' AND json_extract(body,'$.state') IN ('queued','claimed','uncertain'))`, session).Scan(&exists)
	if err != nil {
		return storageError(err)
	}
	if exists {
		return domain.Fail(domain.Conflict, "This session has an unfinished native fork.", "Observe the original fork job before advancing its source boundary.")
	}
	return nil
}

func (t *Tx) RequireForkActor(actor domain.Principal) error {
	if actor.Type != domain.OwnerDevice && actor.Type != domain.ClientDevice {
		return domain.Fail(domain.PermissionDenied, "Fork requires owner or paired-client authority.", "Use the original authorized client.")
	}
	if actor.Type == domain.OwnerDevice && actor.DeviceID == "" {
		return nil
	}
	r, err := t.Get(domain.DeviceKind, actor.DeviceID)
	if err != nil {
		return err
	}
	device, err := Decode[domain.Device](r)
	if err != nil || device.Revoked || device.Type != actor.Type {
		return domain.Fail(domain.PermissionDenied, "The original fork client is no longer authorized.", "Preserve the accepted operation; do not replace its actor.")
	}
	return nil
}
