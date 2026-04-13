// Package migrations embeds all goose SQL migration files into the binary.
// Import this package anywhere migrations need to be run — no file paths required.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
