package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

type RevocationState interface {
	Current(context.Context, string, string) (uint64, string, error)
}

type FreshnessConfig struct {
	AllowTTL, DenyTTL time.Duration
	MaxEntries        int
	Clock             func() time.Time
	Revocations       RevocationState
}

type FreshAuthorizer struct {
	next              ports.Authorizer
	allowTTL, denyTTL time.Duration
	maxEntries        int
	clock             func() time.Time
	revocations       RevocationState
	mu                sync.Mutex
	entries           map[string]cachedDecision
}

type cachedDecision struct {
	decision ports.AuthorizationDecision
	until    time.Time
}

func NewFreshAuthorizer(next ports.Authorizer, config FreshnessConfig) (*FreshAuthorizer, error) {
	if next == nil || config.AllowTTL < 0 || config.DenyTTL < 0 || config.MaxEntries < 0 {
		return nil, errors.New("authorization freshness configuration is invalid")
	}
	if config.MaxEntries > 0 && (config.Revocations == nil || config.AllowTTL == 0 && config.DenyTTL == 0) {
		return nil, errors.New("bounded cache requires TTL and revocation state")
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	return &FreshAuthorizer{next: next, allowTTL: config.AllowTTL, denyTTL: config.DenyTTL, maxEntries: config.MaxEntries,
		clock: config.Clock, revocations: config.Revocations, entries: make(map[string]cachedDecision)}, nil
}

func (authorizer *FreshAuthorizer) AuthorizeToolInvocation(ctx context.Context, request ports.AuthorizationRequest) (ports.AuthorizationDecision, error) {
	if authorizer == nil {
		return ports.AuthorizationDecision{}, errors.New("fresh authorizer is nil")
	}
	key, err := AuthorizationDecisionKey(request)
	if err != nil {
		return ports.AuthorizationDecision{}, err
	}
	now := authorizer.clock().UTC()
	if authorizer.maxEntries > 0 {
		epoch, checkpoint, stateErr := authorizer.revocations.Current(ctx, request.TenantID, request.PolicyProfile)
		if stateErr != nil {
			return ports.AuthorizationDecision{}, errors.New("authorization revocation freshness unavailable")
		}
		if decision, ok := authorizer.cached(key, request, now, epoch, checkpoint); ok {
			return decision, nil
		}
	}
	decision, err := authorizer.next.AuthorizeToolInvocation(ctx, request)
	if err != nil {
		return ports.AuthorizationDecision{}, err
	}
	if err := validateFreshDecision(decision, request, now); err != nil {
		return ports.AuthorizationDecision{}, err
	}
	if authorizer.revocations != nil {
		epoch, checkpoint, stateErr := authorizer.revocations.Current(ctx, request.TenantID, request.PolicyProfile)
		if stateErr != nil || !matchesRevocation(decision, epoch, checkpoint) {
			return ports.AuthorizationDecision{}, errors.New("authorization revocation freshness cannot be established")
		}
	}
	authorizer.store(key, decision, now)
	return cloneDecision(decision), nil
}

func AuthorizationDecisionKey(request ports.AuthorizationRequest) (string, error) {
	if err := request.Validate(); err != nil {
		return "", err
	}
	// Correlation is per transport attempt, not an authority dimension.
	request.RequestID = ""
	encoded, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (authorizer *FreshAuthorizer) cached(key string, request ports.AuthorizationRequest, now time.Time, epoch uint64, checkpoint string) (ports.AuthorizationDecision, bool) {
	authorizer.mu.Lock()
	defer authorizer.mu.Unlock()
	entry, ok := authorizer.entries[key]
	if !ok {
		return ports.AuthorizationDecision{}, false
	}
	decision := cloneDecision(entry.decision)
	decision.RequestID = request.RequestID
	if !now.Before(entry.until) || validateFreshDecision(decision, request, now) != nil || !matchesRevocation(decision, epoch, checkpoint) {
		delete(authorizer.entries, key)
		return ports.AuthorizationDecision{}, false
	}
	return decision, true
}

func (authorizer *FreshAuthorizer) store(key string, decision ports.AuthorizationDecision, now time.Time) {
	if authorizer.maxEntries == 0 {
		return
	}
	ttl := authorizer.allowTTL
	if decision.Outcome == ports.AuthorizationDeny {
		ttl = authorizer.denyTTL
	}
	if ttl == 0 {
		return
	}
	until := now.Add(ttl)
	if decision.ExpiresAt.Before(until) {
		until = decision.ExpiresAt
	}
	if !now.Before(until) {
		return
	}
	authorizer.mu.Lock()
	defer authorizer.mu.Unlock()
	if _, exists := authorizer.entries[key]; !exists && len(authorizer.entries) >= authorizer.maxEntries {
		return
	}
	authorizer.entries[key] = cachedDecision{decision: cloneDecision(decision), until: until}
}

func validateFreshDecision(decision ports.AuthorizationDecision, request ports.AuthorizationRequest, now time.Time) error {
	if err := decision.ValidateFor(request); err != nil {
		return err
	}
	if decision.IssuedAt.After(now) || decision.NotBefore.After(now) || !decision.ExpiresAt.After(now) {
		return errors.New("authorization decision is not currently valid")
	}
	return nil
}

func matchesRevocation(decision ports.AuthorizationDecision, epoch uint64, checkpoint string) bool {
	if decision.RevocationEpoch < epoch {
		return false
	}
	return decision.RevocationEpoch != epoch || decision.RevocationCheckpoint == checkpoint
}

func cloneDecision(input ports.AuthorizationDecision) ports.AuthorizationDecision {
	output := input
	output.Reasons = append([]ports.AuthorizationReason(nil), input.Reasons...)
	output.Constraints.Repositories = append([]string(nil), input.Constraints.Repositories...)
	output.Constraints.Resources = append([]string(nil), input.Constraints.Resources...)
	output.Constraints.Actions = append([]string(nil), input.Constraints.Actions...)
	if input.Constraints.ArgumentMax != nil {
		output.Constraints.ArgumentMax = make(map[string]int64, len(input.Constraints.ArgumentMax))
		for key, value := range input.Constraints.ArgumentMax {
			output.Constraints.ArgumentMax[key] = value
		}
	}
	return output
}
