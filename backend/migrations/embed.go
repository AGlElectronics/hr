// Package migrations embeds the .sql Goose migration files so cmd/migrate
// can apply them from a built binary — no goose CLI needed in the image.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
