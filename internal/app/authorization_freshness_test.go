package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

type authorizerFunc func(context.Context, ports.AuthorizationRequest) (ports.AuthorizationDecision, error)

func (function authorizerFunc) AuthorizeToolInvocation(ctx context.Context, request ports.AuthorizationRequest) (ports.AuthorizationDecision, error) {
	return function(ctx, request)
}

type revocationStub struct {
	epoch      uint64
	checkpoint string
	err        error
}

func (state *revocationStub) Current(context.Context, string, string) (uint64, string, error) {
	return state.epoch, state.checkpoint, state.err
}

func TestFreshAuthorizerCachesOnlyWhileFreshAndUnrevoked(t *testing.T) {
	now := time.Date(2026, 8, 28, 13, 0, 0, 0, time.UTC)
	request := freshnessRequest()
	calls := 0
	next := authorizerFunc(func(context.Context, ports.AuthorizationRequest) (ports.AuthorizationDecision, error) {
		calls++
		return freshnessDecision(request, now), nil
	})
	state := &revocationStub{epoch: 2, checkpoint: "cp-2"}
	wrapped, err := NewFreshAuthorizer(next, FreshnessConfig{AllowTTL: time.Minute, DenyTTL: time.Second, MaxEntries: 2, Clock: func() time.Time { return now }, Revocations: state})
	if err != nil {
		t.Fatal(err)
	}
	first, err := wrapped.AuthorizeToolInvocation(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	first.Constraints.Actions[0] = "poisoned"
	second, err := wrapped.AuthorizeToolInvocation(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || second.Constraints.Actions[0] != "read" {
		t.Fatalf("cache calls=%d decision=%#v", calls, second)
	}
	state.epoch, state.checkpoint = 3, "cp-3"
	if _, err := wrapped.AuthorizeToolInvocation(t.Context(), request); err == nil {
		t.Fatal("revoked decision accepted")
	}
	if calls != 2 {
		t.Fatalf("live recheck calls = %d", calls)
	}
}

func TestFreshAuthorizerFailsClosedWhenRevocationStateUnavailable(t *testing.T) {
	request := freshnessRequest()
	state := &revocationStub{err: errors.New("offline")}
	called := false
	wrapped, _ := NewFreshAuthorizer(authorizerFunc(func(context.Context, ports.AuthorizationRequest) (ports.AuthorizationDecision, error) {
		called = true
		return ports.AuthorizationDecision{}, nil
	}), FreshnessConfig{AllowTTL: time.Second, MaxEntries: 1, Revocations: state})
	if _, err := wrapped.AuthorizeToolInvocation(t.Context(), request); err == nil || called {
		t.Fatal("unavailable revocation state did not fail closed")
	}
}

func TestAuthorizationDecisionKeyCoversSecurityContext(t *testing.T) {
	base := freshnessRequest()
	first, err := AuthorizationDecisionKey(base)
	if err != nil {
		t.Fatal(err)
	}
	variants := []ports.AuthorizationRequest{base, base, base, base}
	variants[0].TenantID = "other-tenant"
	variants[1].RunID = "other-run"
	variants[2].ToolVersion = "v2"
	variants[3].ArgumentDigest[0] = 1
	for _, variant := range variants {
		key, keyErr := AuthorizationDecisionKey(variant)
		if keyErr != nil || key == first {
			t.Fatalf("security field missing from key: %v", keyErr)
		}
	}
}

func freshnessRequest() ports.AuthorizationRequest {
	return ports.AuthorizationRequest{RequestID: "request-1", TenantID: "tenant", Subject: "subject", AgentID: "agent", AgentVersion: "v1",
		RunID: "run", WorkloadID: "workload", ToolID: "tool", ToolVersion: "v1", Risk: "low", SideEffect: "read", ApprovalMode: "none",
		RetryMode: "safe", ArgumentProfile: "jcs-v1", Resources: []string{"repo:a"}, Actions: []string{"read"}, ConnectorType: "github",
		Operation: "get", RequestedDeadline: time.Second, PolicyProfile: "default", PolicyVersion: "1"}
}

func freshnessDecision(request ports.AuthorizationRequest, now time.Time) ports.AuthorizationDecision {
	return ports.AuthorizationDecision{DecisionID: "decision", RequestID: request.RequestID, ContextDigest: "sha256:context", PolicyID: "policy", PolicyVersion: "1",
		Outcome: ports.AuthorizationAllow, Reasons: []ports.AuthorizationReason{ports.ReasonAllowed}, IssuedAt: now, NotBefore: now,
		ExpiresAt: now.Add(time.Hour), RevocationEpoch: 2, RevocationCheckpoint: "cp-2", EvidenceRef: "evidence",
		Constraints: ports.AuthorizationConstraints{Actions: []string{"read"}}}
}
