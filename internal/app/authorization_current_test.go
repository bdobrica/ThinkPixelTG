package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

func TestCurrentAuthorizerRequiresLiveCurrentDecisionForConfiguredWrite(t *testing.T) {
	now := time.Date(2026, 8, 28, 16, 0, 0, 0, time.UTC)
	request := freshnessRequest()
	request.Risk, request.SideEffect = "high", "write"
	regularCalls, liveCalls := 0, 0
	authorizer := newTestCurrentAuthorizer(t, now, &revocationStub{epoch: 2, checkpoint: "cp-2"},
		authorizerFunc(func(context.Context, ports.AuthorizationRequest) (ports.AuthorizationDecision, error) {
			regularCalls++
			return freshnessDecision(request, now), nil
		}), authorizerFunc(func(context.Context, ports.AuthorizationRequest) (ports.AuthorizationDecision, error) {
			liveCalls++
			return freshnessDecision(request, now), nil
		}))
	if _, err := authorizer.AuthorizeToolInvocation(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if regularCalls != 0 || liveCalls != 1 {
		t.Fatalf("regular calls=%d live calls=%d", regularCalls, liveCalls)
	}
}

func TestCurrentAuthorizerFailsClosedWhenMandatoryFreshnessCannotBeMet(t *testing.T) {
	now := time.Date(2026, 8, 28, 16, 0, 0, 0, time.UTC)
	request := freshnessRequest()
	request.Risk, request.SideEffect = "high", "write"
	tests := []struct {
		name        string
		revocations *revocationStub
		decision    ports.AuthorizationDecision
	}{
		{name: "revocation unavailable", revocations: &revocationStub{err: errors.New("offline")}, decision: freshnessDecision(request, now)},
		{name: "decision too old", revocations: &revocationStub{epoch: 2, checkpoint: "cp-2"}, decision: freshnessDecision(request, now.Add(-2*time.Second))},
		{name: "revocation behind", revocations: &revocationStub{epoch: 3, checkpoint: "cp-3"}, decision: freshnessDecision(request, now)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			liveCalls := 0
			authorizer := newTestCurrentAuthorizer(t, now, test.revocations,
				authorizerFunc(func(context.Context, ports.AuthorizationRequest) (ports.AuthorizationDecision, error) {
					t.Fatal("regular path called")
					return ports.AuthorizationDecision{}, nil
				}), authorizerFunc(func(context.Context, ports.AuthorizationRequest) (ports.AuthorizationDecision, error) {
					liveCalls++
					return test.decision, nil
				}))
			if _, err := authorizer.AuthorizeToolInvocation(t.Context(), request); err == nil {
				t.Fatal("freshness failure accepted")
			}
			if test.revocations.err != nil && liveCalls != 0 {
				t.Fatal("live authorization called without revocation freshness")
			}
		})
	}
}

func TestCurrentAuthorizerUsesRegularPathForUnconfiguredRequest(t *testing.T) {
	now := time.Date(2026, 8, 28, 16, 0, 0, 0, time.UTC)
	request := freshnessRequest()
	regularCalls := 0
	authorizer := newTestCurrentAuthorizer(t, now, &revocationStub{epoch: 2, checkpoint: "cp-2"},
		authorizerFunc(func(context.Context, ports.AuthorizationRequest) (ports.AuthorizationDecision, error) {
			regularCalls++
			return ports.AuthorizationDecision{}, errors.New("regular result")
		}), authorizerFunc(func(context.Context, ports.AuthorizationRequest) (ports.AuthorizationDecision, error) {
			t.Fatal("live path called")
			return ports.AuthorizationDecision{}, nil
		}))
	if _, err := authorizer.AuthorizeToolInvocation(t.Context(), request); err == nil || regularCalls != 1 {
		t.Fatalf("regular calls=%d error=%v", regularCalls, err)
	}
}

func newTestCurrentAuthorizer(t *testing.T, now time.Time, revocations RevocationState, regular, live ports.Authorizer) *CurrentAuthorizer {
	t.Helper()
	authorizer, err := NewCurrentAuthorizer(regular, live, CurrentAuthorizationConfig{
		HighRiskWrites: []HighRiskWrite{{Risk: "high", SideEffect: "write"}}, MaxDecisionAge: time.Second,
		Clock: func() time.Time { return now }, Revocations: revocations,
	})
	if err != nil {
		t.Fatal(err)
	}
	return authorizer
}
