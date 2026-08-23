package postgres

import (
	"testing"
	"time"
)

func validPoolConfig() PoolConfig {
	return PoolConfig{URL: "postgres://localhost/test", MaxConnections: 4, MaxConnectionLifetime: time.Hour, MaxConnectionIdleTime: time.Minute, HealthCheckPeriod: time.Minute, ConnectTimeout: time.Second, ReadinessTimeout: time.Second, StatementTimeout: time.Second, LockTimeout: time.Second, IdleTransactionTimeout: time.Second, TransactionTimeout: time.Second, ShutdownTimeout: time.Second}
}

func TestValidatePoolConfig(t *testing.T) {
	t.Parallel()
	if err := validatePoolConfig(validPoolConfig()); err != nil {
		t.Fatalf("valid config: %v", err)
	}
	for name, mutate := range map[string]func(*PoolConfig){
		"URL":         func(c *PoolConfig) { c.URL = "" },
		"connections": func(c *PoolConfig) { c.MinConnections = 5 },
		"timeout":     func(c *PoolConfig) { c.StatementTimeout = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := validPoolConfig()
			mutate(&cfg)
			if validatePoolConfig(cfg) == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestQueryOperationIsBounded(t *testing.T) {
	t.Parallel()
	for sql, want := range map[string]string{" SELECT secret FROM tenant": "select", "INSERT INTO x": "insert", "TRUNCATE private_table": "other", "": "other"} {
		if got := queryOperation(sql); got != want {
			t.Errorf("queryOperation(%q) = %q, want %q", sql, got, want)
		}
	}
}
