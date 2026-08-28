package authn

import (
	"context"
	"errors"
)

// GovernedContext is the identity scope used by application and authorization
// code. It can be constructed only from an authenticated principal held in the
// private request context; ordinary invocation fields are never an input.
type GovernedContext struct {
	TenantID     string
	Subject      string
	Actor        string
	AgentID      string
	AgentVersion string
	RunID        string
}

// DeriveGovernedContext returns the complete governed identity for a protected
// invocation. Empty or malformed governed dimensions fail closed.
func DeriveGovernedContext(ctx context.Context) (GovernedContext, error) {
	principal, ok := PrincipalFromContext(ctx)
	if !ok {
		return GovernedContext{}, errors.New("authenticated principal is required")
	}
	governed := GovernedContext{
		TenantID: principal.TenantID, Subject: principal.Subject, Actor: principal.Actor,
		AgentID: principal.AgentID, AgentVersion: principal.AgentVersion, RunID: principal.RunID,
	}
	if !validIdentityValue(governed.TenantID) || !validIdentityValue(governed.Subject) ||
		!validIdentityValue(governed.AgentID) || !validIdentityValue(governed.AgentVersion) ||
		!validIdentityValue(governed.RunID) || governed.Actor != "" && !validIdentityValue(governed.Actor) {
		return GovernedContext{}, errors.New("complete governed identity is required")
	}
	return governed, nil
}
