// Package migrations wraps the repository's PostgreSQL migration engine.
package migrations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// NewProvider creates the single migration entry point used by cmd/migrate and
// migration tests. The supplied filesystem is expected to be an embedded,
// immutable set of numbered SQL migrations.
func NewProvider(db *sql.DB, migrationFS fs.FS) (*goose.Provider, error) {
	if db == nil {
		return nil, errors.New("migration database is required")
	}
	if migrationFS == nil {
		return nil, errors.New("migration filesystem is required")
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
