package migrations

import (
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
