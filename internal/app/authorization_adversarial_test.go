package app

import (
	"context"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

func TestAuthorizationAdversarialGovernedContextCannotReuseCachedAllow(t *testing.T) {
	now := time.Date(2026, 8, 28, 18, 0, 0, 0, time.UTC)
	for _, mutate := range []struct {
		name string
		fn   func(*ports.AuthorizationRequest)
	}{
		{name: "cross tenant", fn: func(request *ports.AuthorizationRequest) { request.TenantID = "tenant-attacker" }},
		{name: "cross run", fn: func(request *ports.AuthorizationRequest) { request.RunID = "run-attacker" }},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			calls := 0
			live := authorizerFunc(func(_ context.Context, request ports.AuthorizationRequest) (ports.AuthorizationDecision, error) {
				calls++
				return freshnessDecision(request, now), nil
			})
			wrapped, err := NewFreshAuthorizer(live, FreshnessConfig{AllowTTL: time.Minute, MaxEntries: 4,
				Clock: func() time.Time { return now }, Revocations: &revocationStub{epoch: 2, checkpoint: "cp-2"}})
			if err != nil {
				t.Fatal(err)
			}
			original := freshnessRequest()
			if _, err := wrapped.AuthorizeToolInvocation(t.Context(), original); err != nil {
				t.Fatal(err)
			}
			forged := original
			forged.RequestID = "request-attacker"
			mutate.fn(&forged)
			if _, err := wrapped.AuthorizeToolInvocation(t.Context(), forged); err != nil {
				t.Fatal(err)
			}
			if calls != 2 {
				t.Fatalf("governed-context change reused cached allow; live calls=%d", calls)
			}
		})
	}
}

func TestAuthorizationAdversarialStaleDecisionsFailClosed(t *testing.T) {
	now := time.Date(2026, 8, 28, 18, 0, 0, 0, time.UTC)
	request := freshnessRequest()
	for _, test := range []struct {
		name   string
		mutate func(*ports.AuthorizationDecision)
	}{
		{name: "expired", mutate: func(decision *ports.AuthorizationDecision) {
			decision.IssuedAt, decision.NotBefore, decision.ExpiresAt = now.Add(-time.Minute), now.Add(-time.Minute), now.Add(-time.Nanosecond)
		}},
		{name: "not yet valid", mutate: func(decision *ports.AuthorizationDecision) {
			decision.IssuedAt, decision.NotBefore = now, now.Add(time.Second)
		}},
		{name: "issued in future", mutate: func(decision *ports.AuthorizationDecision) {
			decision.IssuedAt, decision.NotBefore = now.Add(time.Second), now.Add(time.Second)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			decision := freshnessDecision(request, now)
			test.mutate(&decision)
			wrapped, err := NewFreshAuthorizer(authorizerFunc(func(context.Context, ports.AuthorizationRequest) (ports.AuthorizationDecision, error) {
				return decision, nil
			}), FreshnessConfig{Clock: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := wrapped.AuthorizeToolInvocation(t.Context(), request); err == nil {
				t.Fatal("stale decision accepted")
			}
		})
	}
}

func TestAuthorizationAdversarialRevocationAndCachePoisoning(t *testing.T) {
	now := time.Date(2026, 8, 28, 18, 0, 0, 0, time.UTC)
	request := freshnessRequest()
	calls := 0
	state := &revocationStub{epoch: 2, checkpoint: "cp-2"}
	wrapped, err := NewFreshAuthorizer(authorizerFunc(func(context.Context, ports.AuthorizationRequest) (ports.AuthorizationDecision, error) {
		calls++
		decision := freshnessDecision(request, now)
		decision.Constraints.Repositories = []string{"repo:a"}
		decision.Constraints.ArgumentMax = map[string]int64{"items": 3}
		return decision, nil
	}), FreshnessConfig{AllowTTL: time.Minute, MaxEntries: 2, Clock: func() time.Time { return now }, Revocations: state})
	if err != nil {
		t.Fatal(err)
	}
	first, err := wrapped.AuthorizeToolInvocation(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	first.Constraints.Repositories[0] = "repo:attacker"
	first.Constraints.ArgumentMax["items"] = 999
	second, err := wrapped.AuthorizeToolInvocation(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if second.Constraints.Repositories[0] != "repo:a" || second.Constraints.ArgumentMax["items"] != 3 || calls != 1 {
		t.Fatalf("cached decision was poisoned: %#v calls=%d", second.Constraints, calls)
	}

	state.epoch, state.checkpoint = 2, "cp-revoked"
	if _, err := wrapped.AuthorizeToolInvocation(t.Context(), request); err == nil || calls != 2 {
		t.Fatalf("revocation checkpoint mismatch accepted; calls=%d error=%v", calls, err)
	}
}

func TestAuthorizationAdversarialConstraintsNeverExpand(t *testing.T) {
	ceiling := ConstraintCeiling{Repositories: []string{"repo:a", "repo:b"}, Resources: []string{"issue:1", "issue:2"},
		Actions: []string{"read", "write"}, ArgumentMax: map[string]int64{"items": 10}, MaxResultBytes: 100, MaxDuration: 10 * time.Second}
	for limit := int64(1); limit <= 25; limit++ {
		decision := ports.AuthorizationDecision{Outcome: ports.AuthorizationAllow, Constraints: ports.AuthorizationConstraints{
			Repositories: []string{"repo:b", "repo:outside"}, Resources: []string{"issue:2", "issue:outside"},
			Actions: []string{"admin", "write"}, ArgumentMax: map[string]int64{"items": limit}, MaxResultBytes: limit,
			MaxDuration: time.Duration(limit) * time.Second}}
		effective, err := NarrowAuthorizationConstraints(decision, ceiling)
		if err != nil {
			t.Fatal(err)
		}
		if len(effective.Repositories) != 1 || effective.Repositories[0] != "repo:b" || len(effective.Resources) != 1 || effective.Resources[0] != "issue:2" ||
			len(effective.Actions) != 1 || effective.Actions[0] != "write" || effective.ArgumentMax["items"] > 10 || effective.ArgumentMax["items"] > limit ||
			effective.MaxResultBytes > 100 || effective.MaxResultBytes > limit || effective.MaxDuration > 10*time.Second || effective.MaxDuration > time.Duration(limit)*time.Second {
			t.Fatalf("constraints expanded at limit %d: %#v", limit, effective)
		}
	}
}
