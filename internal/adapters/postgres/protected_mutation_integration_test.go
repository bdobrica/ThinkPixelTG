//go:build integration

package postgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestProtectedMutationIntegrationAtomicity(t *testing.T) {
	ctx := context.Background()
	pool := repositoryTestPool(t, ctx)
	const tenantID = "019b0000-0000-7000-8000-000000000301"
	if _, err := pool.Exec(ctx, `INSERT INTO tenants (tenant_id, created_at) VALUES ($1, now())`, tenantID); err != nil {
		t.Fatalf("insert tenant: %v", err)
	}
	executor, err := NewProtectedMutationExecutor(pool, tenantID)
	if err != nil {
		t.Fatal(err)
	}

	records := protectedIntegrationRecords(
		"019b0000-0000-7000-8000-000000000302",
		"019b0000-0000-7000-8000-000000000303",
	)
	connectorID := "019b0000-0000-7000-8000-000000000304"
	if err := executor.Execute(ctx, records, putProtectedConnector(connectorID)); err != nil {
		t.Fatalf("execute successful protected mutation: %v", err)
	}
	assertProtectedCounts(t, ctx, pool, tenantID, connectorID, records.Audit.EventID, 1, 1, 1)

	mutationFailureID := "019b0000-0000-7000-8000-000000000305"
	mutationFailureRecords := protectedIntegrationRecords(
		"019b0000-0000-7000-8000-000000000306",
		"019b0000-0000-7000-8000-000000000307",
	)
	sentinel := errors.New("reject mutation")
	err = executor.Execute(ctx, mutationFailureRecords, func(context.Context, *TenantRepositories) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("mutation failure = %v", err)
	}
	assertProtectedCounts(t, ctx, pool, tenantID, mutationFailureID, mutationFailureRecords.Audit.EventID, 0, 0, 0)

	// A pre-existing outbox ID makes the final insert fail. Both the protected
	// mutation and the audit insert made earlier in that transaction must vanish.
	conflictOutboxID := "019b0000-0000-7000-8000-000000000308"
	if _, err := pool.Exec(ctx, `INSERT INTO outbox_messages
		(tenant_id,outbox_id,event_id,topic,event_type,safe_payload,payload_digest,created_at,available_at)
		VALUES ($1,$2,$3,'existing','existing','{}',$4,now(),now())`, tenantID, conflictOutboxID,
		"019b0000-0000-7000-8000-000000000309", make([]byte, 32)); err != nil {
		t.Fatalf("seed conflicting outbox: %v", err)
	}
	rollbackRecords := protectedIntegrationRecords(
		"019b0000-0000-7000-8000-000000000310", conflictOutboxID,
	)
	rollbackConnectorID := "019b0000-0000-7000-8000-000000000311"
	if err := executor.Execute(ctx, rollbackRecords, putProtectedConnector(rollbackConnectorID)); err == nil {
		t.Fatal("outbox conflict unexpectedly committed")
	}
	assertProtectedCounts(t, ctx, pool, tenantID, rollbackConnectorID, rollbackRecords.Audit.EventID, 0, 0, 0)
}

func protectedIntegrationRecords(eventID, outboxID string) ProtectedMutationRecords {
	records := validProtectedMutationRecords()
	records.Audit.EventID = eventID
	records.Outbox[0].ID = outboxID
	records.Outbox[0].EventID = eventID
	return records
}

func putProtectedConnector(id string) ProtectedMutation {
	return func(ctx context.Context, repositories *TenantRepositories) error {
		now := time.Now().UTC()
		return repositories.ConnectorInstances.Put(ctx, ConnectorInstance{
			ID: id, Type: "test", DestinationConfig: []byte(`{}`), ConfigDigest: make([]byte, 32),
			Enabled: true, CreatedAt: now, UpdatedAt: now,
		})
	}
}

func assertProtectedCounts(t *testing.T, ctx context.Context, db DBTX, tenantID, connectorID, eventID string, connector, audit, outbox int) {
	t.Helper()
	for _, check := range []struct {
		name  string
		query string
		id    string
		want  int
	}{
		{"connector", `SELECT count(*) FROM connector_instances WHERE tenant_id=$1 AND connector_instance_id=$2`, connectorID, connector},
		{"audit", `SELECT count(*) FROM audit_events WHERE tenant_id=$1 AND event_id=$2`, eventID, audit},
		{"outbox", `SELECT count(*) FROM outbox_messages WHERE tenant_id=$1 AND event_id=$2`, eventID, outbox},
	} {
		var got int
		if err := db.QueryRow(ctx, check.query, tenantID, check.id).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", check.name, err)
		}
		if got != check.want {
			t.Fatalf("%s count = %d, want %d", check.name, got, check.want)
		}
	}
}
