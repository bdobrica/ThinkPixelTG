package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPrecedence(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "config.json")
	if err := os.WriteFile(name, []byte(`{"mode":"development","http":{"address":"file:1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load([]string{"--config", name, "--http-address=flag:3"}, []string{"TPTG_HTTP_ADDRESS=env:2"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Address != "flag:3" {
		t.Fatalf("address = %q", cfg.HTTP.Address)
	}
}

func TestRejectsUnknownInputs(t *testing.T) {
	t.Run("file field", func(t *testing.T) {
		name := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(name, []byte(`{"surprise":true}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load([]string{"--config", name}, nil); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("environment", func(t *testing.T) {
		if _, err := Load(nil, []string{"TPTG_SURPRISE=true"}); err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("flag", func(t *testing.T) {
		if _, err := Load([]string{"--surprise"}, nil); err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestProductionRejectsDevelopmentAuth(t *testing.T) {
	if _, err := Load([]string{"--mode=production", "--dev-auth"}, nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestStringRedactsSecrets(t *testing.T) {
	const canary = "SECRET_CANARY_7f98"
	cfg := Default()
	cfg.Database.URL = "postgres://user:" + canary + "@db/service"
	rendered := cfg.String()
	if strings.Contains(rendered, canary) || strings.Contains(rendered, "postgres://") {
		t.Fatalf("secret leaked: %s", rendered)
	}
	if !strings.Contains(rendered, "[REDACTED]") {
		t.Fatalf("redaction marker missing: %s", rendered)
	}
}

func TestDatabaseConfigurationFromEnvironment(t *testing.T) {
	cfg, err := Load(nil, []string{"TPTG_DATABASE_MAX_CONNECTIONS=7", "TPTG_DATABASE_STATEMENT_TIMEOUT=3s"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database.MaxConnections != 7 || cfg.Database.StatementTimeout.String() != "3s" {
		t.Fatalf("database configuration not applied: %+v", cfg.Database)
	}
}
