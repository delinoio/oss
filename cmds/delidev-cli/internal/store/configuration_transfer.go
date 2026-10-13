package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

// RequireUnusedID checks both the global identity space and durable deletion
// tombstones before accepting/deferred-publishing a reviewed import. A deleted
// target is a terminal preview conflict, not an endlessly retryable Worker report.
func (t *Tx) RequireUnusedID(id domain.ID) error {
	if err := id.Validate(); err != nil {
		return err
	}
	var used bool
	err := t.tx.QueryRowContext(t.ctx, "SELECT EXISTS(SELECT 1 FROM entities WHERE id=?) OR EXISTS(SELECT 1 FROM tombstones WHERE id=?)", id, id).Scan(&used)
	if err != nil {
		return storageError(err)
	}
	if used {
		return domain.Fail(domain.Conflict, "An imported target identity was already used or permanently deleted.", "Generate a new preview with fresh target identities; never recreate a deleted entity.")
	}
	return nil
}

// RequireUnusedCreateID applies the same global identity and tombstone fence as
// RequireUnusedID for ordinary creation, with guidance that asks for a new ID
// instead of a new import preview.
func (t *Tx) RequireUnusedCreateID(id domain.ID) error {
	if err := id.Validate(); err != nil {
		return err
	}
	var live, deleted bool
	err := t.tx.QueryRowContext(t.ctx, "SELECT EXISTS(SELECT 1 FROM entities WHERE id=?), EXISTS(SELECT 1 FROM tombstones WHERE id=?)", id, id).Scan(&live, &deleted)
	if err != nil {
		return storageError(err)
	}
	if deleted {
		return domain.Fail(domain.Conflict, "This entity was permanently deleted.", "Create a new entity with a new ID.")
	}
	if live {
		return domain.Fail(domain.Conflict, "This identity is already used.", "Create the entity with a new ID.")
	}
	return nil
}
