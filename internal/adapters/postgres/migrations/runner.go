// Package migrations wraps the repository's PostgreSQL migration engine.
package migrations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

var migrationName = regexp.MustCompile(`^([0-9]{5})_[a-z0-9_]+\.sql$`)

type manifestEntry struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

// ValidateArtifact proves that every embedded migration is present, contiguous,
// and byte-for-byte identical to the reviewed release manifest.
func ValidateArtifact(migrationFS fs.FS, manifest []byte) error {
	if migrationFS == nil {
		return errors.New("migration filesystem is required")
	}
	var expected []manifestEntry
	if err := json.Unmarshal(manifest, &expected); err != nil {
		return fmt.Errorf("decode migration manifest: %w", err)
	}
	if len(expected) == 0 {
		return errors.New("migration manifest is empty")
	}
	entries, err := fs.ReadDir(migrationFS, ".")
	if err != nil {
		return fmt.Errorf("read migration filesystem: %w", err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && migrationName.MatchString(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	if len(names) != len(expected) {
		return fmt.Errorf("migration count %d does not match manifest count %d", len(names), len(expected))
	}
	for index, want := range expected {
		if names[index] != want.Name {
			return fmt.Errorf("migration %d is %q, manifest requires %q", index+1, names[index], want.Name)
		}
		match := migrationName.FindStringSubmatch(want.Name)
		if match == nil || match[1] != fmt.Sprintf("%05d", index+1) {
			return fmt.Errorf("migration %q is not a contiguous version", want.Name)
		}
		contents, readErr := fs.ReadFile(migrationFS, want.Name)
		if readErr != nil {
			return fmt.Errorf("read migration %q: %w", want.Name, readErr)
		}
		digest := sha256.Sum256(contents)
		if hex.EncodeToString(digest[:]) != want.SHA256 {
			return fmt.Errorf("migration %q checksum differs from immutable manifest", want.Name)
		}
	}
	return nil
}

// NewProvider creates the single migration entry point used by cmd/migrate and
// migration tests. The supplied filesystem is expected to be an embedded,
// immutable set of numbered SQL migrations.
func NewProvider(db *sql.DB, migrationFS fs.FS) (*goose.Provider, error) {
	return newProvider(db, migrationFS, Manifest())
}

func newProvider(db *sql.DB, migrationFS fs.FS, manifest []byte) (*goose.Provider, error) {
	if db == nil {
		return nil, errors.New("migration database is required")
	}
	if migrationFS == nil {
		return nil, errors.New("migration filesystem is required")
	}
	if err := ValidateArtifact(migrationFS, manifest); err != nil {
		return nil, fmt.Errorf("validate migration artifact: %w", err)
	}
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, fmt.Errorf("create postgres migration lock: %w", err)
	}
	provider, err := goose.NewProvider(
		goose.DialectPostgres,
		db,
		migrationFS,
		goose.WithSessionLocker(locker),
		goose.WithDisableGlobalRegistry(true),
	)
	if err != nil {
		return nil, fmt.Errorf("create postgres migration provider: %w", err)
	}
	return provider, nil
}

// Up applies every pending forward migration. Production rollback uses a
// forward corrective migration rather than a down migration.
func Up(ctx context.Context, provider *goose.Provider) error {
	if provider == nil {
		return errors.New("migration provider is required")
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("apply postgres migrations: %w", err)
	}
	return nil
}
