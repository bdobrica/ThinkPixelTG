package app

import (
	"context"
	"errors"
	"testing"
)

type readinessDependencyFunc func(context.Context) error

func (function readinessDependencyFunc) Ready(ctx context.Context) error { return function(ctx) }

func TestAuthorizationReadinessRequiresDependencyAndRevocationFreshness(t *testing.T) {
	tests := []struct {
		name       string
		dependency error
		state      *revocationStub
		want       AuthorizationAvailability
		wantError  bool
	}{
		{name: "ready", state: &revocationStub{epoch: 4, checkpoint: "cp-4"}, want: AuthorizationReady},
		{name: "AG unavailable", dependency: errors.New("offline"), state: &revocationStub{epoch: 4, checkpoint: "cp-4"}, want: AuthorizationReadOnlyDegraded, wantError: true},
		{name: "revocations unavailable", state: &revocationStub{err: errors.New("offline")}, want: AuthorizationReadOnlyDegraded, wantError: true},
		{name: "empty checkpoint", state: &revocationStub{epoch: 4}, want: AuthorizationReadOnlyDegraded, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			readiness, err := NewAuthorizationReadiness(readinessDependencyFunc(func(context.Context) error {
				return test.dependency
			}), test.state, []ProtectedWriteScope{{TenantID: "tenant", PolicyProfile: "default"}})
			if err != nil {
				t.Fatal(err)
			}
			status, statusErr := readiness.Status(t.Context())
			if status != test.want || (statusErr != nil) != test.wantError {
				t.Fatalf("Status() = %q, %v", status, statusErr)
			}
			if readyErr := readiness.Ready(t.Context()); (readyErr != nil) != test.wantError {
				t.Fatalf("Ready() error = %v", readyErr)
			}
		})
	}
}

func TestAuthorizationReadinessChecksEveryProtectedWriteScope(t *testing.T) {
	calls := 0
	state := revocationStateFunc(func(_ context.Context, tenant, _ string) (uint64, string, error) {
		calls++
		if tenant == "tenant-b" {
			return 0, "", errors.New("unavailable")
		}
		return 1, "cp-1", nil
	})
	readiness, err := NewAuthorizationReadiness(readinessDependencyFunc(func(context.Context) error { return nil }), state,
		[]ProtectedWriteScope{{TenantID: "tenant-a", PolicyProfile: "default"}, {TenantID: "tenant-b", PolicyProfile: "strict"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := readiness.Ready(t.Context()); err == nil || calls != 2 {
		t.Fatalf("Ready() error=%v calls=%d", err, calls)
	}
}

type revocationStateFunc func(context.Context, string, string) (uint64, string, error)

func (function revocationStateFunc) Current(ctx context.Context, tenant, profile string) (uint64, string, error) {
	return function(ctx, tenant, profile)
}
