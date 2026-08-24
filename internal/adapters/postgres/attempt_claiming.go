package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/jackc/pgx/v5"
)

const maxAttemptLease = 5 * time.Minute

type AttemptClaimKind string

const (
	AttemptClaimed    AttemptClaimKind = "claimed"
	AttemptClaimBusy  AttemptClaimKind = "busy"
	AttemptIneligible AttemptClaimKind = "ineligible"
)

type AttemptClaim struct {
	Kind    AttemptClaimKind
	Attempt Attempt
}

type AttemptClaimRequest struct {
	InvocationID  string
	OwnerID       string
	Now           time.Time
	LeaseDuration time.Duration
	Evidence      json.RawMessage
}

type AttemptFinalization struct {
	InvocationID            string
	AttemptNo               int
	Fence                   int64
	OwnerID                 string
	Now                     time.Time
	DownstreamRequestRef    *string
	DownstreamResultRef     *string
	OutcomeClassification   string
	RetryClassification     *string
	AmbiguityClassification *string
	Evidence                json.RawMessage
}

// AttemptClaimer gives each execution claim a new monotonic attempt number and
// fence. The invocation row is the serialization point for claims belonging to
// one logical invocation.
type AttemptClaimer struct {
	transactor *Transactor
	db         DBTX
	tenantID   string
}

func NewAttemptClaimer(database AcquisitionDatabase, tenantID string) (*AttemptClaimer, error) {
	transactor, err := NewTransactor(database)
	if err != nil {
		return nil, err
	}
	id, err := domain.ParseUUID(tenantID)
	if err != nil {
		return nil, fmt.Errorf("invalid attempt-claim tenant ID: %w", err)
	}
	return &AttemptClaimer{transactor: transactor, db: database, tenantID: id.String()}, nil
}

func (c *AttemptClaimer) Claim(ctx context.Context, request AttemptClaimRequest) (AttemptClaim, error) {
	if err := validateAttemptClaimRequest(request); err != nil {
		return AttemptClaim{}, err
	}
	var result AttemptClaim
	err := c.transactor.WithinTransaction(ctx, pgx.TxOptions{}, func(txCtx context.Context, db DBTX) error {
		return c.claim(txCtx, db, request, &result)
	})
	return result, err
}

func (c *AttemptClaimer) claim(ctx context.Context, db DBTX, request AttemptClaimRequest, result *AttemptClaim) error {
	const lockInvocation = `SELECT state FROM invocations
		WHERE tenant_id=$1 AND invocation_id=$2 FOR UPDATE`
	var state string
	if err := db.QueryRow(ctx, lockInvocation, c.tenantID, request.InvocationID).Scan(&state); err != nil {
		return repositoryError("lock invocation for attempt claim", err)
	}
	if state != "ready" && state != "retry_wait" {
		result.Kind = AttemptIneligible
		return nil
	}

	const latest = `SELECT attempt_no,fence,owner_id,claimed_at,lease_expires_at,
		downstream_request_ref,downstream_result_ref,outcome_classification,retry_classification,
		ambiguity_classification,evidence,started_at,finished_at
		FROM invocation_attempts WHERE tenant_id=$1 AND invocation_id=$2
		ORDER BY attempt_no DESC LIMIT 1`
	var previous Attempt
	previous.InvocationID = request.InvocationID
	err := scanAttempt(db.QueryRow(ctx, latest, c.tenantID, request.InvocationID), &previous)
	if err == nil && previous.FinishedAt == nil && previous.LeaseExpiresAt != nil && previous.LeaseExpiresAt.After(request.Now) {
		result.Kind, result.Attempt = AttemptClaimBusy, previous
		return nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return repositoryError("read latest invocation attempt", err)
	}

	attemptNo, fence := 1, int64(1)
	if err == nil {
		attemptNo, fence = previous.AttemptNo+1, previous.Fence+1
	}
	leaseExpiresAt := request.Now.Add(request.LeaseDuration)
	evidence := request.Evidence
	if len(evidence) == 0 {
		evidence = json.RawMessage(`{}`)
	}
	const insert = `INSERT INTO invocation_attempts
		(tenant_id,invocation_id,attempt_no,fence,owner_id,claimed_at,lease_expires_at,evidence,started_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$6)`
	if _, err := db.Exec(ctx, insert, c.tenantID, request.InvocationID, attemptNo, fence,
		request.OwnerID, request.Now, leaseExpiresAt, evidence); err != nil {
		return repositoryError("claim invocation attempt", err)
	}
	owner := request.OwnerID
	result.Kind = AttemptClaimed
	result.Attempt = Attempt{InvocationID: request.InvocationID, AttemptNo: attemptNo, Fence: fence,
		OwnerID: &owner, ClaimedAt: &request.Now, LeaseExpiresAt: &leaseExpiresAt,
		Evidence: evidence, StartedAt: request.Now}
	return nil
}

// IsCurrent reports whether the worker still holds the live, latest fence. A
// worker must check this immediately before an external send.
func (c *AttemptClaimer) IsCurrent(ctx context.Context, invocationID string, attemptNo int, fence int64, ownerID string, now time.Time) (bool, error) {
	if invocationID == "" || attemptNo <= 0 || fence <= 0 || ownerID == "" || now.IsZero() {
		return false, domain.NewError(domain.CodeInvalidArgument, "attempt fence fields are invalid", nil)
	}
	const query = `SELECT EXISTS (
		SELECT 1 FROM invocation_attempts a
		WHERE a.tenant_id=$1 AND a.invocation_id=$2 AND a.attempt_no=$3 AND a.fence=$4
		  AND a.owner_id=$5 AND a.finished_at IS NULL AND a.lease_expires_at > $6
		  AND NOT EXISTS (SELECT 1 FROM invocation_attempts newer
			WHERE newer.tenant_id=a.tenant_id AND newer.invocation_id=a.invocation_id
			  AND newer.attempt_no > a.attempt_no))`
	var current bool
	if err := c.db.QueryRow(ctx, query, c.tenantID, invocationID, attemptNo, fence, ownerID, now).Scan(&current); err != nil {
		return false, repositoryError("check invocation attempt fence", err)
	}
	return current, nil
}

// Finalize accepts an outcome only from the live owner of the latest fence.
func (c *AttemptClaimer) Finalize(ctx context.Context, final AttemptFinalization) error {
	if err := validateAttemptFinalization(final); err != nil {
		return err
	}
	evidence := final.Evidence
	if len(evidence) == 0 {
		evidence = json.RawMessage(`{}`)
	}
	const query = `UPDATE invocation_attempts a SET owner_id=NULL,claimed_at=NULL,lease_expires_at=NULL,
		downstream_request_ref=$7,downstream_result_ref=$8,outcome_classification=$9,
		retry_classification=$10,ambiguity_classification=$11,evidence=$12,finished_at=$6
		WHERE a.tenant_id=$1 AND a.invocation_id=$2 AND a.attempt_no=$3 AND a.fence=$4
		  AND a.owner_id=$5 AND a.finished_at IS NULL AND a.lease_expires_at > $6
		  AND NOT EXISTS (SELECT 1 FROM invocation_attempts newer
			WHERE newer.tenant_id=a.tenant_id AND newer.invocation_id=a.invocation_id
			  AND newer.attempt_no > a.attempt_no)`
	tag, err := c.db.Exec(ctx, query, c.tenantID, final.InvocationID, final.AttemptNo, final.Fence,
		final.OwnerID, final.Now, final.DownstreamRequestRef, final.DownstreamResultRef,
		final.OutcomeClassification, final.RetryClassification, final.AmbiguityClassification, evidence)
	if err != nil {
		return repositoryError("finalize invocation attempt", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.NewError(domain.CodeConflict, "invocation attempt fence is no longer current", nil)
	}
	return nil
}

func scanAttempt(row pgx.Row, attempt *Attempt) error {
	return row.Scan(&attempt.AttemptNo, &attempt.Fence, &attempt.OwnerID, &attempt.ClaimedAt,
		&attempt.LeaseExpiresAt, &attempt.DownstreamRequestRef, &attempt.DownstreamResultRef,
		&attempt.OutcomeClassification, &attempt.RetryClassification, &attempt.AmbiguityClassification,
		&attempt.Evidence, &attempt.StartedAt, &attempt.FinishedAt)
}

func validateAttemptClaimRequest(request AttemptClaimRequest) error {
	if request.InvocationID == "" || request.OwnerID == "" || request.Now.IsZero() ||
		request.LeaseDuration <= 0 || request.LeaseDuration > maxAttemptLease || !validJSONObject(request.Evidence) {
		return domain.NewError(domain.CodeInvalidArgument, "attempt claim fields are invalid", nil)
	}
	return nil
}

func validateAttemptFinalization(final AttemptFinalization) error {
	validOutcome := final.OutcomeClassification == "not_sent" || final.OutcomeClassification == "definitely_rejected" ||
		final.OutcomeClassification == "confirmed_success" || final.OutcomeClassification == "transient_safe" ||
		final.OutcomeClassification == "unknown"
	if final.InvocationID == "" || final.AttemptNo <= 0 || final.Fence <= 0 || final.OwnerID == "" ||
		final.Now.IsZero() || !validOutcome || !validJSONObject(final.Evidence) {
		return domain.NewError(domain.CodeInvalidArgument, "attempt finalization fields are invalid", nil)
	}
	if final.OutcomeClassification == "unknown" && final.AmbiguityClassification == nil {
		return domain.NewError(domain.CodeInvalidArgument, "unknown attempt outcome requires ambiguity classification", nil)
	}
	return nil
}

func validJSONObject(value json.RawMessage) bool {
	if len(value) == 0 {
		return true
	}
	var object map[string]any
	return json.Unmarshal(value, &object) == nil && object != nil
}
