package migrations

import "embed"

// Files contains the ordered DevHud API database migrations.
//
//go:embed *.sql
var Files embed.FS

const LatestVersion = "00002_restore_retry_window.sql"
