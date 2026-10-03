// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
)

func migration027(ctx context.Context, tx *sql.Tx, original int) error {
	_, err := tx.ExecContext(ctx, requestDiagnosticSchema)
	return storageError(err)
}
