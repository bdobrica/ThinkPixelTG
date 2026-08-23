package migrations

import (
	"embed"
	"io/fs"
)

// sqlFiles contains the immutable, numbered PostgreSQL migrations shipped with
// the migration artifact.
//
//go:embed sql/*.sql
var sqlFiles embed.FS

// Files returns the embedded migration filesystem rooted at the migration
// directory expected by Goose.
func Files() fs.FS {
	migrations, err := fs.Sub(sqlFiles, "sql")
	if err != nil {
		// The path is compile-time embedded, so failure indicates a broken build.
		panic("open embedded PostgreSQL migrations: " + err.Error())
	}
	return migrations
}
