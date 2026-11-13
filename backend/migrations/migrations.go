// Package migrations embeds the SQL migration files, so the binary carries its own schema.
// Files are named NNNN_description.sql and applied in numeric order by internal/migrate.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
