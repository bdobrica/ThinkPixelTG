package migrations

import (
	"embed"
	"io/fs"
)

// sqlFiles contains the immutable, numbered PostgreSQL migrations shipped with
// the migration artifact.
//
//go:embed sql/*.sql manifest.json
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

// Manifest returns the immutable checksum manifest shipped with the migration
// artifact. A released migration must never be edited; corrections are new,
// monotonically numbered migrations.
func Manifest() []byte {
	manifest, err := sqlFiles.ReadFile("manifest.json")
	if err != nil {
		panic("open embedded PostgreSQL migration manifest: " + err.Error())
	}
	return manifest
}
