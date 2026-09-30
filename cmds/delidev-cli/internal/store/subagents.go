package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

// Generic resources need no schema migration. This read joins original native
// identities before publication, including records from a previous execution.
func (t *Tx) SubagentIdentityOwned(session, execution, id domain.ID, native string) (bool, error) {
	var foreign bool
	err := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM entities WHERE kind=? AND session_id=? AND json_extract(body,'$.observation.native_id')=? AND (id!=? OR json_extract(body,'$.execution_id')!=?))`, domain.SubagentKind, session, native, id, execution).Scan(&foreign)
	return !foreign, storageError(err)
}
