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
	want := []string{
		"00001_catalog.sql",
		"00002_invocations.sql",
		"00003_attempts.sql",
		"00004_controls_results.sql",
		"00005_evidence_replay_outbox.sql",
		"00006_invocation_acquisition.sql",
	}
	if len(entries) != len(want) {
		t.Fatalf("embedded migration count = %d, want %d", len(entries), len(want))
	}
	for i := range want {
		if entries[i].Name() != want[i] {
			t.Fatalf("embedded migration %d = %q, want %q", i, entries[i].Name(), want[i])
		}
	}
}
