package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestAdminToolCatalogSaveDraftOwnsCanonicalDefinitionAndDigest(t *testing.T) {
	db := &toolCatalogDB{rowsAffected: 1}
	repository, err := NewAdminToolCatalogRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	if err := repository.SaveDraft(t.Context(), "github.pull.comment", "1.2.3",
		json.RawMessage(`{ "z": 1, "a": true }`), now); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(db.sql, "WHERE tool_versions.state='draft'") {
		t.Fatalf("draft persistence lacks released-state predicate: %s", db.sql)
	}
	definition, ok := db.arguments[2].([]byte)
	if !ok || string(definition) != `{"a":true,"z":1}` {
		t.Fatalf("stored definition = %q, want canonical JSON", definition)
	}
	digest, ok := db.arguments[3].([]byte)
	wantDigest := domain.DigestBytes(definition)
	if !ok || !bytes.Equal(digest, wantDigest[:]) {
		t.Fatalf("stored definition digest is not adapter-derived")
	}
}

func TestAdminToolCatalogRejectsReleasedMutationAndInvalidDefinitions(t *testing.T) {
	db := &toolCatalogDB{rowsAffected: 0}
	repository, _ := NewAdminToolCatalogRepository(db)
	now := time.Now().UTC()
	err := repository.SaveDraft(t.Context(), "github.pull.comment", "1.2.3", json.RawMessage(`{"risk":"read"}`), now)
	assertDomainCode(t, err, domain.CodeConflict)

	for _, test := range []struct {
		name       string
		toolID     string
		version    string
		definition json.RawMessage
		at         time.Time
	}{
		{"tool ID", "caller-url", "1.0.0", json.RawMessage(`{}`), now},
		{"version", "github.pull.comment", "latest", json.RawMessage(`{}`), now},
		{"definition", "github.pull.comment", "1.0.0", json.RawMessage(`[]`), now},
		{"timestamp", "github.pull.comment", "1.0.0", json.RawMessage(`{}`), time.Time{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := repository.SaveDraft(t.Context(), test.toolID, test.version, test.definition, test.at)
			assertDomainCode(t, err, domain.CodeInvalidArgument)
		})
	}
}

func TestAdminToolCatalogStateTransitionsAreConditional(t *testing.T) {
	db := &toolCatalogDB{rowsAffected: 1}
	repository, _ := NewAdminToolCatalogRepository(db)
	now := time.Now().UTC()
	if err := repository.Publish(t.Context(), "github.pull.comment", "1.2.3", now); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(db.sql, "state='draft'") || len(db.arguments) != 3 {
		t.Fatalf("publish query is not a bounded state transition: %s", db.sql)
	}
	if err := repository.Retire(t.Context(), "github.pull.comment", "1.2.3"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(db.sql, "state='published'") || strings.Contains(db.sql, "definition=") {
		t.Fatalf("retire query can alter definition or bypass state: %s", db.sql)
	}

	db.rowsAffected = 0
	assertDomainCode(t, repository.Publish(t.Context(), "github.pull.comment", "1.2.3", now), domain.CodeConflict)
	assertDomainCode(t, repository.Retire(t.Context(), "github.pull.comment", "1.2.3"), domain.CodeConflict)
}

func TestTenantToolCatalogQueriesRequireEnabledPublishedExposure(t *testing.T) {
	db := &toolCatalogDB{rowsAffected: 1}
	repositories, err := NewTenantRepositories(db, "019b0000-0000-7000-8000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = repositories.ToolCatalog.GetExposedVersion(t.Context(), "github.pull.comment", "1.0.0")
	if !strings.Contains(db.sql, "e.tenant_id = $1") || !strings.Contains(db.sql, "e.enabled") ||
		!strings.Contains(db.sql, "v.state = 'published'") {
		t.Fatalf("exposed lookup is not tenant/enabled/published scoped: %s", db.sql)
	}
	if err := repositories.ToolCatalog.SetExposure(t.Context(), "not-valid", "latest", true, time.Now()); err == nil {
		t.Fatal("invalid exposure key accepted")
	}
	if err := repositories.ToolCatalog.SetExposure(t.Context(), "github.pull.comment", "1.0.0", true, time.Time{}); err == nil {
		t.Fatal("zero exposure timestamp accepted")
	}
}

func assertDomainCode(t *testing.T, err error, code domain.ErrorCode) {
	t.Helper()
	var domainError *domain.Error
	if !errors.As(err, &domainError) || domainError.Code != code {
		t.Fatalf("error = %v, want domain code %s", err, code)
	}
}

type toolCatalogDB struct {
	rowsAffected int64
	sql          string
	arguments    []any
}

func (db *toolCatalogDB) Exec(_ context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	db.sql, db.arguments = sql, arguments
	return pgconn.NewCommandTag(commandTag(db.rowsAffected)), nil
}

func (db *toolCatalogDB) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, nil }
func (db *toolCatalogDB) QueryRow(_ context.Context, sql string, arguments ...any) pgx.Row {
	db.sql, db.arguments = sql, arguments
	return repositoryRowStub{}
}

func commandTag(rows int64) string {
	if rows == 1 {
		return "UPDATE 1"
	}
	return "UPDATE 0"
}
