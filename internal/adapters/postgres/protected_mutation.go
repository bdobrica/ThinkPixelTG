package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/jackc/pgx/v5"
)

// ProtectedMutation applies an authoritative state change using repositories
// bound to the same transaction as its evidence and publication records.
type ProtectedMutation func(context.Context, *TenantRepositories) error

// ProtectedMutationRecords describes the records which must accompany a
// protected mutation. Every outbox message publishes the mandatory audit event,
// retaining the same stable event identity and type across retries and sinks.
type ProtectedMutationRecords struct {
	Audit  AuditEvent
	Outbox []OutboxMessage
}

// ProtectedMutationExecutor is the only adapter helper for state changes which
// require evidence publication. Omitting either evidence or publication is an
// invalid request, before a transaction or mutation can begin.
type ProtectedMutationExecutor struct {
	transactor   *Transactor
	repositories *TenantRepositories
}

func NewProtectedMutationExecutor(database AcquisitionDatabase, tenantID string) (*ProtectedMutationExecutor, error) {
	transactor, err := NewTransactor(database)
	if err != nil {
		return nil, err
	}
	repositories, err := NewTenantRepositories(database, tenantID)
	if err != nil {
		return nil, err
	}
	return &ProtectedMutationExecutor{transactor: transactor, repositories: repositories}, nil
}

func (e *ProtectedMutationExecutor) Execute(
	ctx context.Context,
	records ProtectedMutationRecords,
	mutation ProtectedMutation,
) error {
	if mutation == nil {
		return invalidProtectedMutation("protected mutation callback is required")
	}
	if err := validateProtectedMutationRecords(records); err != nil {
		return err
	}

	return e.transactor.WithinTransaction(ctx, pgx.TxOptions{}, func(txCtx context.Context, db DBTX) error {
		repositories, err := e.repositories.WithDB(db)
		if err != nil {
			return fmt.Errorf("bind protected mutation repositories: %w", err)
		}
		if err := mutation(txCtx, repositories); err != nil {
			return fmt.Errorf("apply protected mutation: %w", err)
		}
		if err := repositories.Audit.Record(txCtx, records.Audit); err != nil {
			return err
		}
		for _, message := range records.Outbox {
			if err := repositories.Outbox.Enqueue(txCtx, message); err != nil {
				return err
			}
		}
		return nil
	})
}

func validateProtectedMutationRecords(records ProtectedMutationRecords) error {
	audit := records.Audit
	if _, err := domain.ParseUUID(audit.EventID); err != nil {
		return invalidProtectedMutation("audit event ID must be a UUID")
	}
	if strings.TrimSpace(audit.EventType) == "" || strings.TrimSpace(audit.EvidenceProfile) == "" ||
		strings.TrimSpace(audit.ActorClass) == "" || strings.TrimSpace(audit.Outcome) == "" {
		return invalidProtectedMutation("audit event type, evidence profile, actor class, and outcome are required")
	}
	if len(audit.PayloadDigest) != 32 || audit.OccurredAt.IsZero() || audit.RecordedAt.Before(audit.OccurredAt) {
		return invalidProtectedMutation("audit digest and ordered timestamps are required")
	}
	if !jsonObject(audit.Correlation) || !jsonObject(audit.SafePayload) {
		return invalidProtectedMutation("audit correlation and safe payload must be JSON objects")
	}
	if len(records.Outbox) == 0 {
		return invalidProtectedMutation("at least one outbox publication is required")
	}
	seenTopics := make(map[string]struct{}, len(records.Outbox))
	for _, message := range records.Outbox {
		if _, err := domain.ParseUUID(message.ID); err != nil {
			return invalidProtectedMutation("outbox ID must be a UUID")
		}
		if message.EventID != audit.EventID || message.EventType != audit.EventType {
			return invalidProtectedMutation("outbox event identity and type must match the audit event")
		}
		if strings.TrimSpace(message.Topic) == "" || len(message.PayloadDigest) != 32 ||
			message.CreatedAt.IsZero() || message.AvailableAt.Before(message.CreatedAt) || !jsonObject(message.SafePayload) {
			return invalidProtectedMutation("outbox topic, payload, digest, and ordered timestamps are required")
		}
		if _, exists := seenTopics[message.Topic]; exists {
			return invalidProtectedMutation("outbox topics must be unique within a protected mutation")
		}
		seenTopics[message.Topic] = struct{}{}
	}
	return nil
}

func jsonObject(value json.RawMessage) bool {
	if len(value) == 0 || !json.Valid(value) {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(value, &object) == nil && object != nil
}

func invalidProtectedMutation(message string) error {
	return domain.NewError(domain.CodeInvalidArgument, message, errors.New(message))
}
