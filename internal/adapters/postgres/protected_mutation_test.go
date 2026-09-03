package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	protectedTenantID = "019b0000-0000-7000-8000-000000000201"
	protectedEventID  = "019b0000-0000-7000-8000-000000000202"
	protectedOutboxID = "019b0000-0000-7000-8000-000000000203"
)

type credentialCanaryContextKey struct{}

func TestProtectedMutationRequiresAuditAndOutboxBeforeBeginning(t *testing.T) {
	t.Parallel()
	beginner := &fakeBeginner{tx: &fakeTx{}}
	executor, err := NewProtectedMutationExecutor(&protectedDatabase{fakeBeginner: beginner}, protectedTenantID)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	err = executor.Execute(context.Background(), ProtectedMutationRecords{}, func(context.Context, *TenantRepositories) error {
		called = true
		return nil
	})
	assertInvalidArgument(t, err)
	if called || beginner.beginCalls != 0 {
		t.Fatalf("invalid records began mutation: called=%v begins=%d", called, beginner.beginCalls)
	}

	records := validProtectedMutationRecords()
	records.Outbox = nil
	err = executor.Execute(context.Background(), records, func(context.Context, *TenantRepositories) error { return nil })
	assertInvalidArgument(t, err)
	if beginner.beginCalls != 0 {
		t.Fatalf("missing publication began %d transactions", beginner.beginCalls)
	}
	assertInvalidArgument(t, executor.Execute(context.Background(), validProtectedMutationRecords(), nil))
}

func TestProtectedMutationCommitsOnlyAfterMutationAuditAndOutbox(t *testing.T) {
	t.Parallel()
	tx := &protectedTx{}
	executor, err := NewProtectedMutationExecutor(&protectedDatabase{fakeBeginner: &fakeBeginner{tx: tx}}, protectedTenantID)
	if err != nil {
		t.Fatal(err)
	}
	err = executor.Execute(context.Background(), validProtectedMutationRecords(), func(ctx context.Context, repositories *TenantRepositories) error {
		return repositories.ConnectorInstances.Put(ctx, ConnectorInstance{
			ID: protectedOutboxID, Type: "test", DestinationConfig: []byte(`{}`), ConfigDigest: make([]byte, 32),
			Enabled: true, CreatedAt: time.Now(), UpdatedAt: time.Now(),
		})
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := strings.Join(tx.operations, ","); got != "mutation,audit,outbox" {
		t.Fatalf("operation order = %q", got)
	}
	if tx.commits != 1 || tx.rollbacks != 0 {
		t.Fatalf("commits=%d rollbacks=%d", tx.commits, tx.rollbacks)
	}
}

func TestCredentialCanaryIsExcludedFromDatabaseAuditAndOutboxArguments(t *testing.T) {
	const canary = "SYNTHETIC_CREDENTIAL_CANARY_database_013"
	tx := &protectedTx{}
	executor, err := NewProtectedMutationExecutor(&protectedDatabase{fakeBeginner: &fakeBeginner{tx: tx}}, protectedTenantID)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), credentialCanaryContextKey{}, canary)
	if err := executor.Execute(ctx, validProtectedMutationRecords(), func(ctx context.Context, repositories *TenantRepositories) error {
		return repositories.ConnectorInstances.Put(ctx, ConnectorInstance{
			ID: protectedOutboxID, Type: "test", DestinationConfig: []byte(`{}`), ConfigDigest: make([]byte, 32),
			Enabled: true, CreatedAt: time.Now(), UpdatedAt: time.Now(),
		})
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(tx.serializedArguments, canary) {
		t.Fatalf("database, audit, or outbox argument leaked credential canary: %s", tx.serializedArguments)
	}
}

func TestProtectedMutationRollsBackAtEveryFailureBoundary(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"mutation", "audit", "outbox"} {
		t.Run(failure, func(t *testing.T) {
			tx := &protectedTx{failAt: failure}
			executor, err := NewProtectedMutationExecutor(&protectedDatabase{fakeBeginner: &fakeBeginner{tx: tx}}, protectedTenantID)
			if err != nil {
				t.Fatal(err)
			}
			err = executor.Execute(context.Background(), validProtectedMutationRecords(), func(ctx context.Context, repositories *TenantRepositories) error {
				return repositories.ConnectorInstances.Put(ctx, ConnectorInstance{})
			})
			if !errors.Is(err, errTest) || tx.commits != 0 || tx.rollbacks != 1 {
				t.Fatalf("error=%v commits=%d rollbacks=%d", err, tx.commits, tx.rollbacks)
			}
		})
	}
}

func validProtectedMutationRecords() ProtectedMutationRecords {
	now := time.Now().UTC()
	return ProtectedMutationRecords{
		Audit: AuditEvent{EventID: protectedEventID, EventType: "connector.changed", EvidenceProfile: "tg.evidence/v1alpha1",
			ActorClass: "administrator", Outcome: "success", Correlation: []byte(`{}`), SafePayload: []byte(`{}`),
			PayloadDigest: make([]byte, 32), OccurredAt: now, RecordedAt: now},
		Outbox: []OutboxMessage{{ID: protectedOutboxID, EventID: protectedEventID, Topic: "evidence",
			EventType: "connector.changed", SafePayload: []byte(`{}`), PayloadDigest: make([]byte, 32),
			CreatedAt: now, AvailableAt: now}},
	}
}

type protectedDatabase struct{ *fakeBeginner }

func (*protectedDatabase) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (*protectedDatabase) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, nil }
func (*protectedDatabase) QueryRow(context.Context, string, ...any) pgx.Row        { return nil }

type protectedTx struct {
	pgx.Tx
	operations          []string
	failAt              string
	commits             int
	rollbacks           int
	serializedArguments string
}

func (t *protectedTx) Exec(_ context.Context, query string, arguments ...any) (pgconn.CommandTag, error) {
	t.serializedArguments += fmt.Sprint(arguments...)
	operation := "mutation"
	if strings.Contains(query, "INSERT INTO audit_events") {
		operation = "audit"
	} else if strings.Contains(query, "INSERT INTO outbox_messages") {
		operation = "outbox"
	}
	t.operations = append(t.operations, operation)
	if operation == t.failAt {
		return pgconn.CommandTag{}, errTest
	}
	return pgconn.CommandTag{}, nil
}
func (*protectedTx) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, nil }
func (*protectedTx) QueryRow(context.Context, string, ...any) pgx.Row        { return nil }
func (t *protectedTx) Commit(context.Context) error                          { t.commits++; return nil }
func (t *protectedTx) Rollback(context.Context) error                        { t.rollbacks++; return nil }
