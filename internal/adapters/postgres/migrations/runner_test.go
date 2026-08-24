package migrations

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"sort"
	"strings"
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

func TestMigrationArtifactMatchesImmutableManifest(t *testing.T) {
	t.Parallel()
	if err := ValidateArtifact(Files(), Manifest()); err != nil {
		t.Fatalf("ValidateArtifact() error = %v", err)
	}
}

func TestMigrationArtifactRejectsReleasedMigrationDrift(t *testing.T) {
	t.Parallel()
	files := fstest.MapFS{"00001_initial.sql": {Data: []byte("-- +goose Up\nSELECT 1;\n")}}
	manifest := testManifest(t, files)
	files["00001_initial.sql"].Data = []byte("-- +goose Up\nSELECT 2;\n")
	if err := ValidateArtifact(files, manifest); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("ValidateArtifact(drift) error = %v, want checksum error", err)
	}
}

func TestMigrationArtifactRejectsGapsAndUnreviewedFiles(t *testing.T) {
	t.Parallel()
	files := fstest.MapFS{"00002_late.sql": {Data: []byte("-- +goose Up\nSELECT 1;\n")}}
	if err := ValidateArtifact(files, testManifest(t, files)); err == nil || !strings.Contains(err.Error(), "contiguous") {
		t.Fatalf("ValidateArtifact(gap) error = %v, want contiguous error", err)
	}
	files["00001_first.sql"] = &fstest.MapFile{Data: []byte("-- +goose Up\nSELECT 1;\n")}
	manifest := testManifest(t, fstest.MapFS{"00001_first.sql": files["00001_first.sql"]})
	if err := ValidateArtifact(files, manifest); err == nil || !strings.Contains(err.Error(), "count") {
		t.Fatalf("ValidateArtifact(unreviewed file) error = %v, want count error", err)
	}
}

func testManifest(t *testing.T, files fstest.MapFS) []byte {
	t.Helper()
	entries := make([]manifestEntry, 0, len(files))
	for name, file := range files {
		digest := sha256.Sum256(file.Data)
		entries = append(entries, manifestEntry{Name: name, SHA256: hex.EncodeToString(digest[:])})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	encoded, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshal test manifest: %v", err)
	}
	return encoded
}
