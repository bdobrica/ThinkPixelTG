package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

// InvocationLedger adapts the transactional logical-invocation acquisition
// primitive and tenant decision repository to the application port.
type InvocationLedger struct {
	acquirer      *LogicalInvocationAcquirer
	decisions     DecisionRepository
	ownerID       string
	lease         time.Duration
	maxRecoveries int
	clock         domain.Clock
}

func NewInvocationLedger(acquirer *LogicalInvocationAcquirer, decisions DecisionRepository, ownerID string, lease time.Duration, maxRecoveries int, clock domain.Clock) (*InvocationLedger, error) {
	if acquirer == nil || decisions.db == nil || ownerID == "" || lease <= 0 || lease > 5*time.Minute || maxRecoveries < 0 || maxRecoveries > 10 || clock == nil {
		return nil, errors.New("invocation ledger configuration is invalid")
	}
	return &InvocationLedger{acquirer: acquirer, decisions: decisions, ownerID: ownerID, lease: lease, maxRecoveries: maxRecoveries, clock: clock}, nil
}

func (ledger *InvocationLedger) Acquire(ctx context.Context, identity ports.InvocationIdentity, value ports.LogicalInvocation) (ports.InvocationAcquisition, error) {
	if identity.TenantID != ledger.acquirer.tenantID || identity.RunID != value.RunID {
		return ports.InvocationAcquisition{}, domain.NewError(domain.CodeInvalidArgument, "invocation scope does not match the tenant ledger", nil)
	}
	invocation := Invocation{ID: value.ID, RunID: value.RunID, ToolCallID: value.ToolCallID, ToolID: value.ToolID, ToolVersion: value.ToolVersion, ArgumentProfile: value.ArgumentProfile, ArgumentDigest: append([]byte(nil), value.ArgumentDigest[:]...), ResourceProjection: append([]byte(nil), value.ResourceProjection...), ResourceDigest: append([]byte(nil), value.ResourceDigest[:]...), RetryClass: value.RetryClass, State: value.State, StateVersion: 1, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
	result, err := ledger.acquirer.Acquire(ctx, AcquisitionRequest{Invocation: invocation, OwnerID: ledger.ownerID, Now: domain.UTCNow(ledger.clock), LeaseDuration: ledger.lease, MaxRecoveries: ledger.maxRecoveries})
	if err != nil {
		return ports.InvocationAcquisition{}, err
	}
	kind := ports.InvocationExisting
	if result.Kind == AcquisitionOwned || result.Kind == AcquisitionRecovered {
		kind = ports.InvocationOwned
	}
	if result.Kind == AcquisitionConflict {
		kind = ports.InvocationConflict
	}
	return ports.InvocationAcquisition{Kind: kind, Invocation: portInvocation(result.Invocation)}, nil
}

func (ledger *InvocationLedger) RecordAuthorization(ctx context.Context, identity ports.InvocationIdentity, invocationID string, decision ports.AuthorizationDecision, argumentDigest, resourceDigest domain.Digest, at time.Time) error {
	if identity.TenantID != ledger.acquirer.tenantID {
		return domain.NewError(domain.CodeInvalidArgument, "authorization scope does not match the tenant ledger", nil)
	}
	contextDigest, err := domain.ParseDigest(decision.ContextDigest)
	if err != nil {
		return domain.NewError(domain.CodeInternal, "authorization context digest is invalid", err)
	}
	constraints, err := json.Marshal(decision.Constraints)
	if err != nil {
		return domain.NewError(domain.CodeInternal, "authorization constraints could not be persisted", err)
	}
	checkpoint := decision.RevocationCheckpoint
	return ledger.decisions.RecordAuthorization(ctx, AuthorizationDecision{InvocationID: invocationID, DecisionID: decision.DecisionID, ContextDigest: contextDigest[:], ArgumentDigest: argumentDigest[:], ResourceDigest: resourceDigest[:], Outcome: string(decision.Outcome), PolicyRef: decision.PolicyID + "@" + decision.PolicyVersion, Constraints: constraints, IssuedAt: decision.IssuedAt, ExpiresAt: decision.ExpiresAt, RecordedAt: at, RevocationCheckpoint: &checkpoint})
}

func portInvocation(value Invocation) ports.LogicalInvocation {
	var argument, resource domain.Digest
	copy(argument[:], value.ArgumentDigest)
	copy(resource[:], value.ResourceDigest)
	return ports.LogicalInvocation{ID: value.ID, RunID: value.RunID, ToolCallID: value.ToolCallID, ToolID: value.ToolID, ToolVersion: value.ToolVersion, ArgumentProfile: value.ArgumentProfile, ArgumentDigest: argument, ResourceDigest: resource, ResourceProjection: append([]byte(nil), value.ResourceProjection...), RetryClass: value.RetryClass, State: value.State, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}
