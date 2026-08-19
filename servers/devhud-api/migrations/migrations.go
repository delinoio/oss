package migrations

import "embed"

// Files contains the ordered DevHud API database migrations.
//
//go:embed *.sql
var Files embed.FS

const LatestVersion = "00005_administration.sql"
