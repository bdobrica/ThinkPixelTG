package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/jackc/pgx/v5"
)

type AcquisitionKind string

const (
	AcquisitionOwned     AcquisitionKind = "owned"
	AcquisitionRecovered AcquisitionKind = "recovered"
	AcquisitionPending   AcquisitionKind = "pending"
	AcquisitionReplay    AcquisitionKind = "replay"
	AcquisitionConflict  AcquisitionKind = "conflict"
	AcquisitionExhausted AcquisitionKind = "recovery_exhausted"
)

type InvocationAcquisition struct {
	Kind               AcquisitionKind
	Invocation         Invocation
	ReplayStatusCode   *int
	ReplayResultDigest []byte
	SafeReplayPayload  []byte
}

// Complete publishes the safe, exact replay envelope and releases ownership.
// A stale owner cannot complete a claim recovered by another process.
func (a *LogicalInvocationAcquirer) Complete(
	ctx context.Context,
	runID, toolCallID, ownerID string,
	statusCode int,
	resultDigest []byte,
	safePayload []byte,
	now time.Time,
) error {
	if runID == "" || toolCallID == "" || ownerID == "" || statusCode < 100 || statusCode > 599 ||
		len(resultDigest) != 32 || len(safePayload) == 0 || now.IsZero() {
		return domain.NewError(domain.CodeInvalidArgument, "logical invocation completion fields are invalid", nil)
	}
	const query = `UPDATE idempotency_records SET state='completed',claim_owner=NULL,claim_expires_at=NULL,
		replay_status_code=$5,replay_result_digest=$6,safe_replay_payload=$7,updated_at=$8
		WHERE tenant_id=$1 AND idempotency_scope=$2 AND idempotency_key=$3
		  AND state='claimed' AND claim_owner=$4`
	tag, err := a.db.Exec(ctx, query, a.tenantID, "logical_invocation:"+runID,
		toolCallID, ownerID, statusCode, resultDigest, safePayload, now)
	if err != nil {
		return repositoryError("complete logical invocation acquisition", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.NewError(domain.CodeConflict, "logical invocation ownership is no longer current", nil)
	}
	return nil
}

type AcquisitionRequest struct {
	Invocation    Invocation
	OwnerID       string
	Now           time.Time
	LeaseDuration time.Duration
	MaxRecoveries int
}

// LogicalInvocationAcquirer serializes ownership of a logical tool call. Its
// transaction creates the invocation and claim together, so a visible claim
// always refers to a durable invocation.
type LogicalInvocationAcquirer struct {
	transactor *Transactor
	db         DBTX
	tenantID   string
}

type AcquisitionDatabase interface {
	Beginner
	DBTX
}

func NewLogicalInvocationAcquirer(database AcquisitionDatabase, tenantID string) (*LogicalInvocationAcquirer, error) {
	transactor, err := NewTransactor(database)
	if err != nil {
		return nil, err
	}
	id, err := domain.ParseUUID(tenantID)
	if err != nil {
		return nil, fmt.Errorf("invalid acquisition tenant ID: %w", err)
	}
	return &LogicalInvocationAcquirer{transactor: transactor, db: database, tenantID: id.String()}, nil
}

func (a *LogicalInvocationAcquirer) Acquire(ctx context.Context, request AcquisitionRequest) (InvocationAcquisition, error) {
	if err := validateAcquisitionRequest(request); err != nil {
		return InvocationAcquisition{}, err
	}
	var result InvocationAcquisition
	err := a.transactor.WithinTransaction(ctx, pgx.TxOptions{}, func(txCtx context.Context, db DBTX) error {
		return a.acquire(txCtx, db, request, &result)
	})
	return result, err
}

func (a *LogicalInvocationAcquirer) acquire(ctx context.Context, db DBTX, request AcquisitionRequest, result *InvocationAcquisition) error {
	scope := "logical_invocation:" + request.Invocation.RunID
	key := request.Invocation.ToolCallID
	expiresAt := request.Now.Add(request.LeaseDuration)
	requestDigest := logicalRequestDigest(request.Invocation)
	const claim = `INSERT INTO idempotency_records
		(tenant_id,idempotency_scope,idempotency_key,request_digest,state,claim_owner,claim_expires_at,
		 created_at,updated_at,expires_at,recovery_count,max_recoveries)
		VALUES ($1,$2,$3,$4,'claimed',$5,$6,$7,$7,$8,0,$9)
		ON CONFLICT (tenant_id,idempotency_scope,idempotency_key) DO UPDATE
		SET claim_owner=EXCLUDED.claim_owner, claim_expires_at=EXCLUDED.claim_expires_at,
			updated_at=EXCLUDED.updated_at, recovery_count=idempotency_records.recovery_count+1
		WHERE idempotency_records.state='claimed'
		  AND idempotency_records.request_digest=EXCLUDED.request_digest
		  AND idempotency_records.claim_expires_at <= EXCLUDED.updated_at
		  AND idempotency_records.recovery_count < idempotency_records.max_recoveries
		RETURNING recovery_count, invocation_id`
	var recoveryCount int
	var existingInvocationID *string
	err := db.QueryRow(ctx, claim, a.tenantID, scope, key, requestDigest[:],
		request.OwnerID, expiresAt, request.Now, request.Now.Add(24*time.Hour), request.MaxRecoveries).
		Scan(&recoveryCount, &existingInvocationID)
	if err == nil {
		repositories, bindErr := NewTenantRepositories(db, a.tenantID)
		if bindErr != nil {
			return bindErr
		}
		if recoveryCount > 0 {
			if existingInvocationID == nil {
				return errors.New("recovered logical invocation has no durable invocation")
			}
			existing, getErr := repositories.Invocations.Get(ctx, *existingInvocationID)
			if getErr != nil {
				return getErr
			}
			result.Kind, result.Invocation = AcquisitionRecovered, existing
			return nil
		}
		if createErr := repositories.Invocations.Create(ctx, request.Invocation); createErr != nil {
			return createErr
		}
		const link = `UPDATE idempotency_records SET invocation_id=$4, updated_at=$5
			WHERE tenant_id=$1 AND idempotency_scope=$2 AND idempotency_key=$3 AND claim_owner=$6`
		if _, updateErr := db.Exec(ctx, link, a.tenantID, scope, key, request.Invocation.ID, request.Now, request.OwnerID); updateErr != nil {
			return repositoryError("link invocation acquisition", updateErr)
		}
		result.Kind = AcquisitionOwned
		result.Invocation = request.Invocation
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return repositoryError("claim logical invocation", err)
	}
	return a.classifyExisting(ctx, db, scope, key, request, result)
}

func (a *LogicalInvocationAcquirer) classifyExisting(ctx context.Context, db DBTX, scope, key string, request AcquisitionRequest, result *InvocationAcquisition) error {
	const query = `SELECT r.request_digest,r.state,r.claim_expires_at,r.recovery_count,r.max_recoveries,
		r.replay_status_code,r.replay_result_digest,r.safe_replay_payload,
		i.invocation_id,i.run_id,i.tool_call_id,i.tool_id,i.tool_version,i.argument_profile,i.argument_digest,
		i.resource_projection,i.resource_digest,i.retry_class,i.state,i.state_version,i.terminal_code,
		i.terminal_at,i.created_at,i.updated_at
		FROM idempotency_records r LEFT JOIN invocations i
		ON i.tenant_id=r.tenant_id AND i.invocation_id=r.invocation_id
		WHERE r.tenant_id=$1 AND r.idempotency_scope=$2 AND r.idempotency_key=$3 FOR UPDATE OF r`
	var digest, replayDigest []byte
	var state string
	var claimExpiry *time.Time
	var recoveryCount, maxRecoveries int
	var replayCode *int
	var replayPayload []byte
	var invocation Invocation
	err := db.QueryRow(ctx, query, a.tenantID, scope, key).Scan(&digest, &state, &claimExpiry,
		&recoveryCount, &maxRecoveries, &replayCode, &replayDigest, &replayPayload,
		&invocation.ID, &invocation.RunID, &invocation.ToolCallID, &invocation.ToolID, &invocation.ToolVersion,
		&invocation.ArgumentProfile, &invocation.ArgumentDigest, &invocation.ResourceProjection,
		&invocation.ResourceDigest, &invocation.RetryClass, &invocation.State, &invocation.StateVersion,
		&invocation.TerminalCode, &invocation.TerminalAt, &invocation.CreatedAt, &invocation.UpdatedAt)
	if err != nil {
		return repositoryError("classify logical invocation", err)
	}
	result.Invocation = invocation
	requestDigest := logicalRequestDigest(request.Invocation)
	if !bytes.Equal(digest, requestDigest[:]) || invocation.ToolID != request.Invocation.ToolID ||
		invocation.ToolVersion != request.Invocation.ToolVersion || !bytes.Equal(invocation.ArgumentDigest, request.Invocation.ArgumentDigest) {
		result.Kind = AcquisitionConflict
		return nil
	}
	if state == "completed" {
		result.Kind, result.ReplayStatusCode = AcquisitionReplay, replayCode
		result.ReplayResultDigest, result.SafeReplayPayload = replayDigest, replayPayload
		return nil
	}
	if claimExpiry != nil && !claimExpiry.After(request.Now) && recoveryCount >= maxRecoveries {
		result.Kind = AcquisitionExhausted
		return nil
	}
	result.Kind = AcquisitionPending
	return nil
}

func logicalRequestDigest(invocation Invocation) [sha256.Size]byte {
	hash := sha256.New()
	for _, field := range [][]byte{[]byte(invocation.ToolID), []byte(invocation.ToolVersion), invocation.ArgumentDigest} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(field)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write(field)
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result
}

func validateAcquisitionRequest(request AcquisitionRequest) error {
	if request.OwnerID == "" || request.Invocation.RunID == "" || request.Invocation.ToolCallID == "" ||
		request.Invocation.ToolID == "" || request.Invocation.ToolVersion == "" || len(request.Invocation.ArgumentDigest) != 32 {
		return domain.NewError(domain.CodeInvalidArgument, "logical invocation acquisition fields are invalid", nil)
	}
	if request.Now.IsZero() || request.LeaseDuration <= 0 || request.LeaseDuration > 5*time.Minute || request.MaxRecoveries < 0 || request.MaxRecoveries > 10 {
		return domain.NewError(domain.CodeInvalidArgument, "logical invocation acquisition bounds are invalid", nil)
	}
	return nil
}
