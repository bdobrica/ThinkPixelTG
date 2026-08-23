package postgres

import (
	"context"
	"errors"
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
