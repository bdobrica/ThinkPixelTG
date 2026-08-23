package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestPoolIntegrationRuntimePolicy(t *testing.T) {
	databaseURL := os.Getenv("TPTG_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TPTG_TEST_DATABASE_URL is not set")
	}
	cfg := validPoolConfig()
	cfg.URL = databaseURL
	cfg.StatementTimeout = 150 * time.Millisecond
	cfg.LockTimeout = 700 * time.Millisecond
	cfg.IdleTransactionTimeout = 2 * time.Second
	registry := prometheus.NewRegistry()
	pool, err := Open(context.Background(), cfg, registry)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	var statement, lock, idle string
	if err := pool.QueryRow(context.Background(), "SELECT current_setting('statement_timeout'), current_setting('lock_timeout'), current_setting('idle_in_transaction_session_timeout')").Scan(&statement, &lock, &idle); err != nil {
		t.Fatal(err)
	}
	if statement != "150ms" || lock != "700ms" || idle != "2s" {
		t.Fatalf("timeouts = %q, %q, %q", statement, lock, idle)
	}
	if _, err := pool.Exec(context.Background(), "SELECT pg_sleep(1)"); err == nil {
		t.Fatal("statement timeout did not cancel long query")
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, family := range families {
		names[family.GetName()] = true
	}
	for _, name := range []string{"thinkpixeltg_postgres_queries_total", "thinkpixeltg_postgres_query_duration_seconds", "thinkpixeltg_postgres_connections_total"} {
		if !names[name] {
			t.Errorf("metric %q missing", name)
		}
	}
}
