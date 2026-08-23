//go:build integration

package postgres

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/adapters/postgres/migrations"
	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestRepositoriesIntegrationTenantIsolationAndRollback(t *testing.T) {
	ctx := context.Background()
	pool := repositoryTestPool(t, ctx)

	const (
		tenantA    = "019b0000-0000-7000-8000-000000000101"
		tenantB    = "019b0000-0000-7000-8000-000000000102"
		connectorA = "019b0000-0000-7000-8000-000000000103"
		rolledBack = "019b0000-0000-7000-8000-000000000104"
	)
	for _, tenantID := range []string{tenantA, tenantB} {
		if _, err := pool.Exec(ctx, `INSERT INTO tenants (tenant_id, created_at) VALUES ($1, now())`, tenantID); err != nil {
			t.Fatalf("insert tenant: %v", err)
		}
	}

	repositoriesA, err := NewTenantRepositories(pool, tenantA)
	if err != nil {
		t.Fatalf("create tenant A repositories: %v", err)
	}
	repositoriesB, err := NewTenantRepositories(pool, tenantB)
	if err != nil {
		t.Fatalf("create tenant B repositories: %v", err)
	}
	now := time.Now().UTC()
	connector := ConnectorInstance{
		ID: connectorA, Type: "github", DestinationConfig: []byte(`{"organization":"acme"}`),
		ConfigDigest: make([]byte, 32), Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := repositoriesA.ConnectorInstances.Put(ctx, connector); err != nil {
		t.Fatalf("put connector: %v", err)
	}
	if _, err := repositoriesA.ConnectorInstances.Get(ctx, connectorA); err != nil {
		t.Fatalf("get own connector: %v", err)
	}
	if _, err := repositoriesB.ConnectorInstances.Get(ctx, connectorA); !isNotFound(err) {
		t.Fatalf("cross-tenant get error = %v, want not_found", err)
	}

	transactor, err := NewTransactor(pool)
	if err != nil {
		t.Fatalf("create transactor: %v", err)
	}
	rollbackSentinel := errors.New("force rollback")
	err = transactor.WithinTransaction(ctx, pgx.TxOptions{}, func(txCtx context.Context, tx DBTX) error {
		txRepositories, bindErr := repositoriesA.WithDB(tx)
		if bindErr != nil {
			return bindErr
		}
		connector.ID = rolledBack
		if putErr := txRepositories.ConnectorInstances.Put(txCtx, connector); putErr != nil {
			return putErr
		}
		return rollbackSentinel
	})
	if !errors.Is(err, rollbackSentinel) {
		t.Fatalf("WithinTransaction() error = %v, want rollback sentinel", err)
	}
	if _, err := repositoriesA.ConnectorInstances.Get(ctx, rolledBack); !isNotFound(err) {
		t.Fatalf("rolled-back connector get error = %v, want not_found", err)
	}
}

func isNotFound(err error) bool {
	var domainErr *domain.Error
	return errors.As(err, &domainErr) && domainErr.Code == domain.CodeNotFound
}

func repositoryTestPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("TPTG_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TPTG_TEST_DATABASE_URL is not set")
	}
	schema := "tptg_repository_" + strconv.FormatInt(time.Now().UnixNano(), 10)

	adminConfig, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse administration database URL: %v", err)
	}
	admin := stdlib.OpenDB(*adminConfig)
	t.Cleanup(func() { _ = admin.Close() })
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	t.Cleanup(func() {
		if _, dropErr := admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); dropErr != nil {
			t.Errorf("drop test schema: %v", dropErr)
		}
	})

	migrationConfig := *adminConfig
	migrationConfig.RuntimeParams = cloneRuntimeParams(adminConfig.RuntimeParams)
	migrationConfig.RuntimeParams["search_path"] = schema
	migrationDB := stdlib.OpenDB(migrationConfig)
	t.Cleanup(func() { _ = migrationDB.Close() })
	provider, err := migrations.NewProvider(migrationDB, migrations.Files())
	if err != nil {
		t.Fatalf("create migration provider: %v", err)
	}
	if err := migrations.Up(ctx, provider); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse pool database URL: %v", err)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatalf("open repository test pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func cloneRuntimeParams(source map[string]string) map[string]string {
	result := make(map[string]string, len(source)+1)
	for key, value := range source {
		result[key] = value
	}
	return result
}
