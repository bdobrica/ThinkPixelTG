package development

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/config"
	"github.com/bdobrica/ThinkPixelTG/internal/domain"
)

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

func TestProviderIsExplicitlyDevelopmentOnly(t *testing.T) {
	for _, mode := range []config.Mode{config.ModeProduction, "", "test"} {
		if _, err := New(Config{Mode: mode, Clock: fixedClock{time.Now()}}); err == nil {
			t.Fatalf("provider accepted mode %q", mode)
		}
	}
	if _, err := New(Config{Mode: config.ModeDevelopment, Clock: fixedClock{time.Now()}}); err != nil {
		t.Fatal(err)
	}
}

func TestProviderResolvesEnvironmentIntoOpaqueCapability(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	provider, err := New(Config{Mode: config.ModeDevelopment, Clock: fixedClock{now: now}, DefaultTTL: 10 * time.Minute,
		LookupEnv: func(name string) (string, bool) {
			if name != "TPTG_DEV_GITHUB_TOKEN" {
				t.Fatalf("environment name = %q", name)
			}
			return "synthetic-dev-canary", true
		}})
	if err != nil {
		t.Fatal(err)
	}
	capability, err := provider.Resolve(t.Context(), developmentBinding(t, "env:TPTG_DEV_GITHUB_TOKEN", 2*time.Minute), "subject-1")
	if err != nil {
		t.Fatal(err)
	}
	defer capability.Release()
	metadata := capability.Metadata()
	if metadata.Kind != domain.CapabilityAPIToken || metadata.ExpiresAt != now.Add(2*time.Minute) || metadata.Scopes[0] != "pull_request:write" {
		t.Fatalf("metadata = %#v", metadata)
	}
	if err := capability.UseSecret(func(secret []byte) error {
		if string(secret) != "synthetic-dev-canary" {
			t.Fatalf("secret = %q", secret)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestProviderResolvesBoundedFileAndErasesLoadedBuffer(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	loaded := []byte("synthetic-file-canary")
	path := filepath.Join(t.TempDir(), "credential")
	provider, err := New(Config{Mode: config.ModeDevelopment, Clock: fixedClock{now: now}, ReadFile: func(got string) ([]byte, error) {
		if got != path {
			t.Fatalf("path = %q", got)
		}
		return loaded, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	capability, err := provider.Resolve(t.Context(), developmentBinding(t, "file:"+path, 0), "subject-1")
	if err != nil {
		t.Fatal(err)
	}
	defer capability.Release()
	if !bytes.Equal(loaded, make([]byte, len(loaded))) {
		t.Fatal("provider retained un-erased file buffer")
	}
	if err := capability.UseSecret(func(secret []byte) error {
		if string(secret) != "synthetic-file-canary" {
			t.Fatalf("secret = %q", secret)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultFileReaderIsBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, maximumSecretBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	provider, err := New(Config{Mode: config.ModeDevelopment, Clock: fixedClock{now: time.Now()}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Resolve(t.Context(), developmentBinding(t, "file:"+path, 0), "subject-1"); err == nil {
		t.Fatal("oversized file accepted")
	}
}

func TestProviderFailsClosedWithoutLeakingSourceErrors(t *testing.T) {
	canary := "SYNTHETIC_PROVIDER_ERROR_CANARY"
	provider, err := New(Config{Mode: config.ModeDevelopment, Clock: fixedClock{now: time.Now()},
		LookupEnv: func(string) (string, bool) { return "", false },
		ReadFile:  func(string) ([]byte, error) { return nil, errors.New(canary) }})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, reference, subject string
		ctx                      context.Context
	}{
		{"missing environment", "env:TPTG_DEV_MISSING", "subject", t.Context()},
		{"bad environment name", "env:caller/path", "subject", t.Context()},
		{"file error", "file:/absolute/credential", "subject", t.Context()},
		{"unknown source", "vault:secret", "subject", t.Context()},
		{"missing subject", "env:TPTG_DEV_MISSING", "", t.Context()},
		{"cancelled", "env:TPTG_DEV_MISSING", "subject", cancelledContext()},
		{"nil context", "env:TPTG_DEV_MISSING", "subject", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, resolveErr := provider.Resolve(test.ctx, developmentBinding(t, test.reference, 0), test.subject)
			if resolveErr == nil || strings.Contains(resolveErr.Error(), canary) || strings.Contains(resolveErr.Error(), "secret") {
				t.Fatalf("unsafe error = %v", resolveErr)
			}
		})
	}
}

func developmentBinding(t *testing.T, reference string, maximumTTL time.Duration) domain.CredentialBinding {
	t.Helper()
	parseID := func(value string) domain.UUID {
		id, err := domain.ParseUUID(value)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	toolID, _ := domain.ParseToolID("github.pull.comment")
	version, _ := domain.ParseSemanticVersion("1.0.0")
	binding, err := domain.NewCredentialBinding(domain.CredentialBindingDefinition{
		ID: parseID("019b0000-0000-7000-8000-000000000001"), TenantID: parseID("019b0000-0000-7000-8000-000000000002"), ConnectorInstanceID: parseID("019b0000-0000-7000-8000-000000000003"),
		ToolSelectors: []domain.CredentialToolSelector{{ToolID: toolID, Version: version, CredentialSelector: "github_writer"}},
		Provider: domain.CredentialProviderBinding{ProviderType: "development", ProviderRef: reference, Capability: domain.CapabilityAPIToken,
			Audiences: []string{"api.github.com"}, Scopes: []string{"pull_request:write"}},
		Delegation: domain.SubjectDelegationPolicy{Mode: domain.DelegationNone}, Cache: domain.CredentialCachePolicy{MaximumTTL: maximumTTL}, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func cancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
