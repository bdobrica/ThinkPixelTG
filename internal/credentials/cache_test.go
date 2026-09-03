package credentials

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

type countingProvider struct {
	clock     domain.Clock
	calls     atomic.Int32
	delay     time.Duration
	errorText string
	mismatch  bool
}

func (provider *countingProvider) Resolve(ctx context.Context, binding domain.CredentialBinding, _ ports.CredentialProviderContext) (ports.CredentialCapability, error) {
	call := provider.calls.Add(1)
	if provider.delay > 0 {
		select {
		case <-time.After(provider.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if provider.errorText != "" {
		return nil, errors.New(provider.errorText)
	}
	definition := binding.Definition()
	metadata := ports.CredentialCapabilityMetadata{
		Kind: definition.Provider.Capability, ProviderRef: definition.Provider.ProviderRef,
		Audiences: definition.Provider.Audiences, Resources: definition.Provider.Resources, Scopes: definition.Provider.Scopes,
		IssuedAt: domain.UTCNow(provider.clock), ExpiresAt: domain.UTCNow(provider.clock).Add(time.Hour), RevocationEpoch: definition.RevocationEpoch,
	}
	if provider.mismatch {
		metadata.Scopes = []string{"broader:admin"}
	}
	return NewCapability(metadata, []byte(fmt.Sprintf("synthetic-cache-canary-%d", call)), provider.clock)
}

func TestProviderCacheReusesSourceButReturnsIndependentCapabilities(t *testing.T) {
	clock := &mutableClock{now: time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)}
	upstream := &countingProvider{clock: clock}
	cache := newTestCache(t, upstream, clock, 8)
	defer cache.Close()
	binding := cacheBinding(t, "0001", "rev-1")
	governed := ports.CredentialProviderContext{Subject: "subject-1", RunID: "run-1"}

	first, err := cache.Resolve(t.Context(), binding, governed)
	if err != nil {
		t.Fatal(err)
	}
	first.Release()
	second, err := cache.Resolve(t.Context(), binding, governed)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Release()
	if upstream.calls.Load() != 1 {
		t.Fatalf("provider calls = %d", upstream.calls.Load())
	}
	if err := second.UseSecret(func(secret []byte) error {
		if string(secret) != "synthetic-cache-canary-1" {
			t.Fatalf("secret = %q", secret)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestProviderCacheSingleflightsConcurrentMisses(t *testing.T) {
	clock := &mutableClock{now: time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)}
	upstream := &countingProvider{clock: clock, delay: 20 * time.Millisecond}
	cache := newTestCache(t, upstream, clock, 8)
	defer cache.Close()
	binding := cacheBinding(t, "0001", "rev-1")
	governed := ports.CredentialProviderContext{Subject: "subject-1", RunID: "run-1"}
	errorsChannel := make(chan error, 20)
	for range 20 {
		go func() {
			capability, err := cache.Resolve(t.Context(), binding, governed)
			if capability != nil {
				capability.Release()
			}
			errorsChannel <- err
		}()
	}
	for range 20 {
		if err := <-errorsChannel; err != nil {
			t.Fatal(err)
		}
	}
	if upstream.calls.Load() != 1 {
		t.Fatalf("provider calls = %d", upstream.calls.Load())
	}
}

func TestProviderCacheKeysGovernedContextAndRevocationState(t *testing.T) {
	clock := &mutableClock{now: time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)}
	upstream := &countingProvider{clock: clock}
	cache := newTestCache(t, upstream, clock, 8)
	defer cache.Close()
	binding := cacheBinding(t, "0001", "rev-1")
	contexts := []ports.CredentialProviderContext{{Subject: "subject-1", RunID: "run-1"}, {Subject: "subject-2", RunID: "run-1"}, {Subject: "subject-1", RunID: "run-2"}}
	for _, governed := range contexts {
		capability, err := cache.Resolve(t.Context(), binding, governed)
		if err != nil {
			t.Fatal(err)
		}
		capability.Release()
	}
	changed := cacheBinding(t, "0001", "rev-2")
	capability, err := cache.Resolve(t.Context(), changed, contexts[0])
	if err != nil {
		t.Fatal(err)
	}
	capability.Release()
	if upstream.calls.Load() != 4 {
		t.Fatalf("provider calls = %d", upstream.calls.Load())
	}
}

func TestProviderCacheExpiryAndExplicitEvictions(t *testing.T) {
	clock := &mutableClock{now: time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)}
	upstream := &countingProvider{clock: clock}
	cache := newTestCache(t, upstream, clock, 8)
	defer cache.Close()
	binding := cacheBinding(t, "0001", "rev-1")
	definition := binding.Definition()
	governed := ports.CredentialProviderContext{Subject: "subject-1", RunID: "run-1"}

	resolveAndRelease(t, cache, binding, governed)
	clock.now = clock.now.Add(6 * time.Minute)
	resolveAndRelease(t, cache, binding, governed)
	cache.EvictBinding(definition.TenantID, definition.ID)
	resolveAndRelease(t, cache, binding, governed)
	cache.EvictRun(definition.TenantID, governed.RunID)
	resolveAndRelease(t, cache, binding, governed)
	if err := cache.SetProviderEpoch(definition.Provider.ProviderType, "provider-epoch-2"); err != nil {
		t.Fatal(err)
	}
	resolveAndRelease(t, cache, binding, governed)
	if upstream.calls.Load() != 5 {
		t.Fatalf("provider calls = %d", upstream.calls.Load())
	}
}

func TestProviderCacheEnforcesBoundsAndProviderMetadata(t *testing.T) {
	clock := &mutableClock{now: time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)}
	upstream := &countingProvider{clock: clock}
	cache := newTestCache(t, upstream, clock, 1)
	defer cache.Close()
	governed := ports.CredentialProviderContext{Subject: "subject-1", RunID: "run-1"}
	first := cacheBinding(t, "0001", "rev-1")
	second := cacheBinding(t, "0002", "rev-1")
	resolveAndRelease(t, cache, first, governed)
	resolveAndRelease(t, cache, second, governed)
	resolveAndRelease(t, cache, first, governed)
	if upstream.calls.Load() != 3 {
		t.Fatalf("LRU bound did not evict: provider calls = %d", upstream.calls.Load())
	}

	mismatched := &countingProvider{clock: clock, mismatch: true}
	strictCache := newTestCache(t, mismatched, clock, 1)
	defer strictCache.Close()
	if _, err := strictCache.Resolve(t.Context(), first, governed); err == nil {
		t.Fatal("broader provider capability accepted")
	}
}

func TestProviderCacheSuppressesProviderErrorsAndCloses(t *testing.T) {
	clock := &mutableClock{now: time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)}
	upstream := &countingProvider{clock: clock, errorText: "SYNTHETIC_PROVIDER_SECRET_CANARY"}
	cache := newTestCache(t, upstream, clock, 1)
	binding := cacheBinding(t, "0001", "rev-1")
	governed := ports.CredentialProviderContext{Subject: "subject-1", RunID: "run-1"}
	if _, err := cache.Resolve(t.Context(), binding, governed); err == nil || strings.Contains(err.Error(), upstream.errorText) {
		t.Fatalf("unsafe provider error = %v", err)
	}
	upstream.errorText = ""
	resolveAndRelease(t, cache, binding, governed)
	cache.Close()
	if _, err := cache.Resolve(t.Context(), binding, governed); err == nil {
		t.Fatal("closed cache resolved a capability")
	}
}

func newTestCache(t *testing.T, provider ports.CredentialProvider, clock domain.Clock, maximum int) *ProviderCache {
	t.Helper()
	cache, err := NewProviderCache(CacheConfig{Provider: provider, Clock: clock, MaxEntries: maximum, MaximumTTL: 5 * time.Minute, ExpirySkew: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	return cache
}

func resolveAndRelease(t *testing.T, cache *ProviderCache, binding domain.CredentialBinding, governed ports.CredentialProviderContext) {
	t.Helper()
	capability, err := cache.Resolve(t.Context(), binding, governed)
	if err != nil {
		t.Fatal(err)
	}
	capability.Release()
}

func cacheBinding(t *testing.T, suffix, revocation string) domain.CredentialBinding {
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
		ID: parseID("019b0000-0000-7000-8000-00000000" + suffix), TenantID: parseID("019b0000-0000-7000-8000-000000000010"), ConnectorInstanceID: parseID("019b0000-0000-7000-8000-000000000020"),
		ToolSelectors: []domain.CredentialToolSelector{{ToolID: toolID, Version: version, CredentialSelector: "github_writer"}},
		Provider:      domain.CredentialProviderBinding{ProviderType: "test_provider", ProviderRef: "provider:test", Capability: domain.CapabilityAPIToken, Audiences: []string{"api.github.com"}, Scopes: []string{"pull_request:write"}},
		Delegation:    domain.SubjectDelegationPolicy{Mode: domain.DelegationNone}, Cache: domain.CredentialCachePolicy{MaximumTTL: 10 * time.Minute, ExpirySkew: time.Minute}, RevocationEpoch: revocation, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return binding
}
