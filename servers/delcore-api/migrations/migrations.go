// SPDX-License-Identifier: Apache-2.0
package migrations

import "embed"

// Files contains ordered, immutable schema migrations.
//
//go:embed *.sql
var Files embed.FS

const Version = 1
