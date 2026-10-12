// Package migrations menyimpan file migrasi SQL (goose) di dalam binary.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
