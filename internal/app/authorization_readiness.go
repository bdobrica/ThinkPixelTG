package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// AuthorizationDependency reports whether a live authorization request can be
// served. Implementations may probe AG's authenticated readiness endpoint.
type AuthorizationDependency interface {
	Ready(context.Context) error
}

type ProtectedWriteScope struct {
	TenantID      string
	PolicyProfile string
}

type AuthorizationAvailability string

const (
	AuthorizationReady            AuthorizationAvailability = "protected_writes_ready"
	AuthorizationReadOnlyDegraded AuthorizationAvailability = "read_only_degraded"
)

type AuthorizationReadiness struct {
	dependency  AuthorizationDependency
	revocations RevocationState
	scopes      []ProtectedWriteScope
}

func NewAuthorizationReadiness(dependency AuthorizationDependency, revocations RevocationState, scopes []ProtectedWriteScope) (*AuthorizationReadiness, error) {
	if dependency == nil || revocations == nil || len(scopes) == 0 {
		return nil, errors.New("authorization readiness configuration is invalid")
	}
	copyScopes := append([]ProtectedWriteScope(nil), scopes...)
	for _, scope := range copyScopes {
		if strings.TrimSpace(scope.TenantID) == "" || strings.TrimSpace(scope.PolicyProfile) == "" {
			return nil, errors.New("authorization readiness scope is invalid")
		}
	}
	return &AuthorizationReadiness{dependency: dependency, revocations: revocations, scopes: copyScopes}, nil
}

// Ready implements the readiness callback consumed by the HTTP adapter. A
// failure means protected writes must not be advertised as ready.
func (readiness *AuthorizationReadiness) Ready(ctx context.Context) error {
	_, err := readiness.Status(ctx)
	return err
}

func (readiness *AuthorizationReadiness) Status(ctx context.Context) (AuthorizationAvailability, error) {
	if readiness == nil {
		return AuthorizationReadOnlyDegraded, errors.New("authorization readiness is nil")
	}
	if err := readiness.dependency.Ready(ctx); err != nil {
		return AuthorizationReadOnlyDegraded, fmt.Errorf("authorization dependency is not ready: %w", err)
	}
	for _, scope := range readiness.scopes {
		_, checkpoint, err := readiness.revocations.Current(ctx, scope.TenantID, scope.PolicyProfile)
		if err != nil || strings.TrimSpace(checkpoint) == "" {
			return AuthorizationReadOnlyDegraded, errors.New("authorization revocation freshness is not ready")
		}
	}
	return AuthorizationReady, nil
}
