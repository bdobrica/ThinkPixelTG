package migrations

import (
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestNewProviderRejectsMissingDependencies(t *testing.T) {
	t.Parallel()
	if _, err := NewProvider(nil, fstest.MapFS{}); err == nil {
		t.Fatal("NewProvider(nil database) error = nil")
	}
}

func TestUpRejectsNilProvider(t *testing.T) {
	t.Parallel()
	if err := Up(t.Context(), nil); err == nil {
		t.Fatal("Up(nil) error = nil")
	}
}

func TestEmbeddedMigrationsAreAvailable(t *testing.T) {
	t.Parallel()

	entries, err := fs.ReadDir(Files(), ".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "00001_catalog.sql" {
		t.Fatalf("embedded migrations = %v, want 00001_catalog.sql", entries)
	}
}
