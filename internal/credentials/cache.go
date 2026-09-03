package credentials

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

type CacheConfig struct {
	Provider   ports.CredentialProvider
	Clock      domain.Clock
	MaxEntries int
	MaximumTTL time.Duration
	ExpirySkew time.Duration
}

type cachedCapability struct {
	source          ports.CredentialCapability
	expiresAt       time.Time
	tenantID        domain.UUID
	bindingID       domain.UUID
	runID           string
	providerType    string
	revocationEpoch string
	lastUsed        uint64
}

type ProviderCache struct {
	provider   ports.CredentialProvider
	clock      domain.Clock
	maxEntries int
	maximumTTL time.Duration
	expirySkew time.Duration

	mu             sync.Mutex
	entries        map[[sha256.Size]byte]*cachedCapability
	inflight       map[[sha256.Size]byte]chan struct{}
	providerEpochs map[string]string
	sequence       uint64
	closed         bool
}

var _ ports.CredentialProvider = (*ProviderCache)(nil)

func NewProviderCache(cacheConfig CacheConfig) (*ProviderCache, error) {
	if cacheConfig.Provider == nil || cacheConfig.Clock == nil || cacheConfig.MaxEntries < 1 || cacheConfig.MaxEntries > 10000 || cacheConfig.MaximumTTL <= 0 || cacheConfig.MaximumTTL > 24*time.Hour || cacheConfig.ExpirySkew < 0 || cacheConfig.ExpirySkew >= cacheConfig.MaximumTTL {
		return nil, errors.New("credential cache configuration is invalid")
	}
	return &ProviderCache{provider: cacheConfig.Provider, clock: cacheConfig.Clock, maxEntries: cacheConfig.MaxEntries, maximumTTL: cacheConfig.MaximumTTL, expirySkew: cacheConfig.ExpirySkew,
		entries: make(map[[sha256.Size]byte]*cachedCapability), inflight: make(map[[sha256.Size]byte]chan struct{}), providerEpochs: make(map[string]string)}, nil
}

func (cache *ProviderCache) Resolve(ctx context.Context, binding domain.CredentialBinding, governed ports.CredentialProviderContext) (ports.CredentialCapability, error) {
	if cache == nil || ctx == nil || !validCacheContext(governed) {
		return nil, errors.New("credential cache resolution context is invalid")
	}
	definition := binding.Definition()
	if err := domain.ValidateCredentialBindingDefinition(definition); err != nil || !definition.Enabled {
		return nil, errors.New("credential cache binding is invalid")
	}

	for {
		now := domain.UTCNow(cache.clock)
		cache.mu.Lock()
		if cache.closed {
			cache.mu.Unlock()
			return nil, errors.New("credential cache is closed")
		}
		epoch := cache.providerEpochs[definition.Provider.ProviderType]
		key := credentialCacheKey(definition, governed, epoch)
		cache.removeRevokedLocked(definition)
		cache.removeExpiredLocked(now)
		if entry := cache.entries[key]; entry != nil && now.Before(entry.expiresAt) {
			cache.sequence++
			entry.lastUsed = cache.sequence
			source := entry.source
			cache.mu.Unlock()
			capability, err := cloneCapability(source, cache.clock)
			if err == nil {
				return capability, nil
			}
			cache.removeEntry(key, entry)
			continue
		}
		if ready := cache.inflight[key]; ready != nil {
			cache.mu.Unlock()
			select {
			case <-ready:
				continue
			case <-ctx.Done():
				return nil, errors.New("credential cache resolution was cancelled")
			}
		}
		ready := make(chan struct{})
		cache.inflight[key] = ready
		cache.mu.Unlock()

		// Misses refresh only within this caller's governed Resolve operation;
		// the cache never performs background refresh outside authorization flow.
		source, loadErr := cache.provider.Resolve(ctx, binding, governed)
		entry, loadErr := cache.prepareEntry(source, definition, governed, domain.UTCNow(cache.clock), loadErr)

		cache.mu.Lock()
		delete(cache.inflight, key)
		close(ready)
		stored := false
		if loadErr == nil && !cache.closed && cache.providerEpochs[definition.Provider.ProviderType] == epoch {
			cache.sequence++
			entry.lastUsed = cache.sequence
			cache.entries[key] = entry
			cache.evictLRULocked()
			stored = true
		} else if source != nil {
			source.Release()
		}
		cache.mu.Unlock()
		if loadErr != nil {
			return nil, loadErr
		}
		if !stored {
			return nil, errors.New("credential cache changed during resolution")
		}
		capability, err := cloneCapability(source, cache.clock)
		if err != nil {
			cache.removeEntry(key, entry)
			return nil, errors.New("credential cache could not clone capability")
		}
		return capability, nil
	}
}

func (cache *ProviderCache) prepareEntry(source ports.CredentialCapability, definition domain.CredentialBindingDefinition, governed ports.CredentialProviderContext, now time.Time, loadErr error) (*cachedCapability, error) {
	if loadErr != nil {
		return nil, errors.New("credential provider resolution failed")
	}
	if source == nil {
		return nil, errors.New("credential provider returned no capability")
	}
	metadata := source.Metadata()
	if metadata.Kind != definition.Provider.Capability || metadata.ProviderRef != definition.Provider.ProviderRef || metadata.RevocationEpoch != definition.RevocationEpoch ||
		!slices.Equal(metadata.Audiences, definition.Provider.Audiences) || !slices.Equal(metadata.Resources, definition.Provider.Resources) || !slices.Equal(metadata.Scopes, definition.Provider.Scopes) {
		return nil, errors.New("credential provider returned mismatched capability metadata")
	}
	ttl := cache.maximumTTL
	if definition.Cache.MaximumTTL > 0 && definition.Cache.MaximumTTL < ttl {
		ttl = definition.Cache.MaximumTTL
	}
	skew := cache.expirySkew
	if definition.Cache.ExpirySkew > skew {
		skew = definition.Cache.ExpirySkew
	}
	expiresAt := metadata.ExpiresAt.Add(-skew)
	if ttlExpiry := now.Add(ttl); ttlExpiry.Before(expiresAt) {
		expiresAt = ttlExpiry
	}
	if !now.Before(expiresAt) {
		return nil, errors.New("credential provider capability cannot be cached safely")
	}
	return &cachedCapability{source: source, expiresAt: expiresAt, tenantID: definition.TenantID, bindingID: definition.ID, runID: governed.RunID, providerType: definition.Provider.ProviderType, revocationEpoch: definition.RevocationEpoch}, nil
}

func (cache *ProviderCache) EvictBinding(tenantID, bindingID domain.UUID) {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	for key, entry := range cache.entries {
		if entry.tenantID == tenantID && entry.bindingID == bindingID {
			entry.source.Release()
			delete(cache.entries, key)
		}
	}
}

func (cache *ProviderCache) EvictRun(tenantID domain.UUID, runID string) {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	for key, entry := range cache.entries {
		if entry.tenantID == tenantID && entry.runID == runID {
			entry.source.Release()
			delete(cache.entries, key)
		}
	}
}

func (cache *ProviderCache) SetProviderEpoch(providerType, epoch string) error {
	if cache == nil || !validCacheText(providerType) || !optionalCacheText(epoch) {
		return errors.New("credential provider epoch is invalid")
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.closed {
		return errors.New("credential cache is closed")
	}
	if cache.providerEpochs[providerType] == epoch {
		return nil
	}
	cache.providerEpochs[providerType] = epoch
	for key, entry := range cache.entries {
		if entry.providerType == providerType {
			entry.source.Release()
			delete(cache.entries, key)
		}
	}
	return nil
}

func (cache *ProviderCache) ProviderEpoch(providerType string) string {
	if cache == nil {
		return ""
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.providerEpochs[providerType]
}

func (cache *ProviderCache) Close() {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.closed = true
	for key, entry := range cache.entries {
		entry.source.Release()
		delete(cache.entries, key)
	}
}

func (cache *ProviderCache) removeExpiredLocked(now time.Time) {
	for key, entry := range cache.entries {
		if !now.Before(entry.expiresAt) {
			entry.source.Release()
			delete(cache.entries, key)
		}
	}
}

func (cache *ProviderCache) removeRevokedLocked(definition domain.CredentialBindingDefinition) {
	for key, entry := range cache.entries {
		if entry.tenantID == definition.TenantID && entry.bindingID == definition.ID && entry.revocationEpoch != definition.RevocationEpoch {
			entry.source.Release()
			delete(cache.entries, key)
		}
	}
}

func (cache *ProviderCache) evictLRULocked() {
	for len(cache.entries) > cache.maxEntries {
		var oldestKey [sha256.Size]byte
		var oldest *cachedCapability
		for key, entry := range cache.entries {
			if oldest == nil || entry.lastUsed < oldest.lastUsed {
				oldestKey, oldest = key, entry
			}
		}
		oldest.source.Release()
		delete(cache.entries, oldestKey)
	}
}

func (cache *ProviderCache) removeEntry(key [sha256.Size]byte, expected *cachedCapability) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if entry := cache.entries[key]; entry != nil && entry == expected {
		entry.source.Release()
		delete(cache.entries, key)
	}
}

func cloneCapability(source ports.CredentialCapability, clock domain.Clock) (ports.CredentialCapability, error) {
	if source == nil {
		return nil, errors.New("credential cache source is unavailable")
	}
	metadata := source.Metadata()
	var clone *Capability
	err := source.UseSecret(func(secret []byte) error {
		var err error
		clone, err = NewCapability(metadata, secret, clock)
		return err
	})
	if err != nil {
		return nil, errors.New("credential cache source is unusable")
	}
	return clone, nil
}

func credentialCacheKey(definition domain.CredentialBindingDefinition, governed ports.CredentialProviderContext, providerEpoch string) [sha256.Size]byte {
	digest := sha256.New()
	writeCachePart(digest, definition.ID.String(), definition.TenantID.String(), definition.ConnectorInstanceID.String(),
		definition.Provider.ProviderType, definition.Provider.ProviderRef, string(definition.Provider.Capability), string(definition.Delegation.Mode), definition.Delegation.TrustedIssuer,
		definition.RevocationEpoch, strconv.FormatBool(definition.Enabled), strconv.FormatInt(int64(definition.Cache.MaximumTTL), 10), strconv.FormatInt(int64(definition.Cache.ExpirySkew), 10),
		governed.Subject, governed.RunID, providerEpoch)
	for _, selector := range definition.ToolSelectors {
		writeCachePart(digest, string(selector.ToolID), selector.Version.String(), selector.CredentialSelector)
	}
	writeCachePart(digest, definition.Provider.Audiences...)
	writeCachePart(digest, definition.Provider.Resources...)
	writeCachePart(digest, definition.Provider.Scopes...)
	writeCachePart(digest, definition.PolicyTags...)
	var key [sha256.Size]byte
	copy(key[:], digest.Sum(nil))
	return key
}

func writeCachePart(digest hash.Hash, values ...string) {
	var length [8]byte
	for _, value := range values {
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = digest.Write(length[:])
		_, _ = digest.Write([]byte(value))
	}
}

func validCacheContext(governed ports.CredentialProviderContext) bool {
	return validCacheText(governed.Subject) && validCacheText(governed.RunID)
}

func validCacheText(value string) bool {
	return value != "" && len(value) <= 512 && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}

func optionalCacheText(value string) bool { return value == "" || validCacheText(value) }
