package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestNewTenantRepositoriesValidatesDependencies(t *testing.T) {
	t.Parallel()

	if _, err := NewTenantRepositories(nil, "019b0000-0000-7000-8000-000000000001"); err == nil {
		t.Fatal("NewTenantRepositories() with nil database succeeded")
	}
	if _, err := NewTenantRepositories(repositoryDBStub{}, "not-a-uuid"); err == nil {
		t.Fatal("NewTenantRepositories() with malformed tenant ID succeeded")
	}
}

func TestGetToolCallViewBindsTenantAndRunAndSelectsNoInternalProviderFields(t *testing.T) {
	t.Parallel()
	database := &capturingRepositoryDB{}
	repositories, err := NewTenantRepositories(database, "019b0000-0000-7000-8000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = repositories.Invocations.GetToolCallView(context.Background(), "trusted-run", "call-0001")
	if len(database.arguments) != 3 || database.arguments[0] != "019b0000-0000-7000-8000-000000000001" || database.arguments[1] != "trusted-run" || database.arguments[2] != "call-0001" {
		t.Fatalf("query arguments = %#v", database.arguments)
	}
	selectClause := strings.Split(database.query, "FROM invocations")[0]
	for _, prohibited := range []string{"invocation_id", "credential_binding_id", "connector_instance_id", "downstream_result_ref", "resource_projection", "argument_digest"} {
		if strings.Contains(selectClause, prohibited) {
			t.Fatalf("caller projection selects %q: %s", prohibited, selectClause)
		}
	}
	if !strings.Contains(database.query, "i.tenant_id=$1 AND i.run_id=$2 AND i.tool_call_id=$3") || !strings.Contains(database.query, "i.state='succeeded'") {
		t.Fatalf("query lacks scope or result gate: %s", database.query)
	}
}

func TestRepositoryNotFoundUsesDomainError(t *testing.T) {
	t.Parallel()

	repositories, err := NewTenantRepositories(repositoryDBStub{}, "019b0000-0000-7000-8000-000000000001")
	if err != nil {
		t.Fatalf("NewTenantRepositories() error = %v", err)
	}
	_, err = repositories.Invocations.Get(context.Background(), "019b0000-0000-7000-8000-000000000002")
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) || !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("Get() error = %v, want wrapped pgx.ErrNoRows", err)
	}
}

type repositoryDBStub struct{}

func (repositoryDBStub) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (repositoryDBStub) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, nil }
func (repositoryDBStub) QueryRow(context.Context, string, ...any) pgx.Row {
	return repositoryRowStub{}
}

type repositoryRowStub struct{}

func (repositoryRowStub) Scan(...any) error { return pgx.ErrNoRows }

type capturingRepositoryDB struct {
	query     string
	arguments []any
}

func (*capturingRepositoryDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (*capturingRepositoryDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, nil
}
func (database *capturingRepositoryDB) QueryRow(_ context.Context, query string, arguments ...any) pgx.Row {
	database.query = query
	database.arguments = append([]any(nil), arguments...)
	return repositoryRowStub{}
}
