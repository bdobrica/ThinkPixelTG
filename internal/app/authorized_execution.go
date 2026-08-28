package app

import (
	"context"
	"errors"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

// CredentialLease is an opaque, short-lived capability. It is deliberately not
// serializable; the resolver owns its concrete representation and cleanup.
type CredentialLease interface {
	Release()
}

type CredentialResolution func(context.Context, ports.AuthorizationDecision) (CredentialLease, error)
type ConnectorExecution func(context.Context, ports.AuthorizationDecision, CredentialLease) error

// AuthorizedExecutor is the sole sequencing gate for downstream credential and
// connector callbacks. Neither callback is reachable until authorization has
// completed successfully with an explicit allow decision.
type AuthorizedExecutor struct {
	authorizer ports.Authorizer
}

func NewAuthorizedExecutor(authorizer ports.Authorizer) (*AuthorizedExecutor, error) {
	if authorizer == nil {
		return nil, errors.New("authorized executor requires an authorizer")
	}
	return &AuthorizedExecutor{authorizer: authorizer}, nil
}

func (executor *AuthorizedExecutor) Execute(ctx context.Context, request ports.AuthorizationRequest, resolve CredentialResolution, execute ConnectorExecution) error {
	if executor == nil || resolve == nil || execute == nil {
		return domain.NewError(domain.CodeInternal, "protected execution is not configured", nil)
	}
	decision, err := executor.authorizer.AuthorizeToolInvocation(ctx, request)
	if err != nil {
		return domain.NewError(domain.CodeUnavailable, "authorization could not be established", err)
	}
	if err := decision.ValidateFor(request); err != nil {
		return domain.NewError(domain.CodeForbidden, "authorization decision is invalid", err)
	}
	if decision.Outcome != ports.AuthorizationAllow {
		return domain.NewError(domain.CodeForbidden, "tool invocation is not authorized", nil)
	}

	lease, err := resolve(ctx, cloneDecision(decision))
	if err != nil {
		return err
	}
	if lease == nil {
		return domain.NewError(domain.CodeInternal, "credential resolver returned no capability", nil)
	}
	defer lease.Release()
	return execute(ctx, cloneDecision(decision), lease)
}
