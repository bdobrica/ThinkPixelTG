package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

type leaseCanary struct{ events *[]string }

func (lease *leaseCanary) Release() { *lease.events = append(*lease.events, "release") }

func TestAuthorizedExecutorOrdersAuthorizationBeforeDownstreamBoundaries(t *testing.T) {
	now := time.Date(2026, 8, 28, 17, 0, 0, 0, time.UTC)
	request := freshnessRequest()
	events := []string{}
	executor, err := NewAuthorizedExecutor(authorizerFunc(func(context.Context, ports.AuthorizationRequest) (ports.AuthorizationDecision, error) {
		events = append(events, "authorize")
		return freshnessDecision(request, now), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	err = executor.Execute(t.Context(), request, func(context.Context, ports.AuthorizationDecision) (CredentialLease, error) {
		events = append(events, "credential")
		return &leaseCanary{events: &events}, nil
	}, func(_ context.Context, decision ports.AuthorizationDecision, _ CredentialLease) error {
		events = append(events, "connector")
		decision.Constraints.Actions[0] = "mutated"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"authorize", "credential", "connector", "release"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestAuthorizedExecutorCanariesRejectUnauthorizedDownstreamAccess(t *testing.T) {
	now := time.Date(2026, 8, 28, 17, 0, 0, 0, time.UTC)
	request := freshnessRequest()
	tests := []struct {
		name       string
		authorizer ports.Authorizer
	}{
		{name: "authorization error", authorizer: authorizerFunc(func(context.Context, ports.AuthorizationRequest) (ports.AuthorizationDecision, error) {
			return ports.AuthorizationDecision{}, errors.New("AG unavailable")
		})},
		{name: "explicit denial", authorizer: authorizerFunc(func(context.Context, ports.AuthorizationRequest) (ports.AuthorizationDecision, error) {
			decision := freshnessDecision(request, now)
			decision.Outcome = ports.AuthorizationDeny
			decision.Reasons = []ports.AuthorizationReason{ports.ReasonPolicyDenied}
			return decision, nil
		})},
		{name: "malformed allow", authorizer: authorizerFunc(func(context.Context, ports.AuthorizationRequest) (ports.AuthorizationDecision, error) {
			decision := freshnessDecision(request, now)
			decision.RequestID = "wrong-request"
			return decision, nil
		})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor, err := NewAuthorizedExecutor(test.authorizer)
			if err != nil {
				t.Fatal(err)
			}
			err = executor.Execute(t.Context(), request, func(context.Context, ports.AuthorizationDecision) (CredentialLease, error) {
				panic("credential boundary reached before authorization")
			}, func(context.Context, ports.AuthorizationDecision, CredentialLease) error {
				panic("connector boundary reached before authorization")
			})
			if err == nil {
				t.Fatal("unauthorized execution accepted")
			}
		})
	}
}

func TestAuthorizedExecutorDoesNotReachConnectorWhenCredentialResolutionFails(t *testing.T) {
	now := time.Date(2026, 8, 28, 17, 0, 0, 0, time.UTC)
	request := freshnessRequest()
	executor, _ := NewAuthorizedExecutor(authorizerFunc(func(context.Context, ports.AuthorizationRequest) (ports.AuthorizationDecision, error) {
		return freshnessDecision(request, now), nil
	}))
	sentinel := errors.New("credential unavailable")
	err := executor.Execute(t.Context(), request, func(context.Context, ports.AuthorizationDecision) (CredentialLease, error) {
		return nil, sentinel
	}, func(context.Context, ports.AuthorizationDecision, CredentialLease) error {
		panic("connector reached without credential")
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Execute() error = %v", err)
	}
}
