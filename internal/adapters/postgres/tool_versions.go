package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/canonicaljson"
	"github.com/bdobrica/ThinkPixelTG/internal/domain"
)

// AdminToolCatalogRepository is the persistence boundary for global tool
// definitions. It must only be made available to the separately authorized
// administration path; tenant repositories intentionally cannot mutate it.
type AdminToolCatalogRepository struct {
	db DBTX
}

func NewAdminToolCatalogRepository(db DBTX) (*AdminToolCatalogRepository, error) {
	if db == nil {
		return nil, errors.New("postgres admin tool catalog database is required")
	}
	return &AdminToolCatalogRepository{db: db}, nil
}

// SaveDraft inserts or replaces a draft definition. The database predicate and
// released-row trigger both reject using this method to modify a published or
// retired version. Callers must choose a new SemVer for changed semantics.
func (r *AdminToolCatalogRepository) SaveDraft(
	ctx context.Context,
	toolID, version string,
	definition json.RawMessage,
	at time.Time,
) error {
	if r == nil || r.db == nil {
		return errors.New("postgres admin tool catalog repository is required")
	}
	if _, err := domain.ParseToolID(toolID); err != nil {
		return domain.NewError(domain.CodeInvalidArgument, "tool ID is invalid", err)
	}
	if _, err := domain.ParseSemanticVersion(version); err != nil {
		return domain.NewError(domain.CodeInvalidArgument, "tool version is invalid", err)
	}
	if at.IsZero() {
		return domain.NewError(domain.CodeInvalidArgument, "draft timestamp is required", nil)
	}
	parsed, err := canonicaljson.Parse(definition, canonicaljson.Limits{})
	if err != nil {
		return domain.NewError(domain.CodeInvalidArgument, "tool definition is invalid", err)
	}
	if _, ok := parsed.(map[string]any); !ok {
		return domain.NewError(domain.CodeInvalidArgument, "tool definition must be a JSON object", nil)
	}
	canonical, err := canonicaljson.Canonicalize(definition, canonicaljson.Limits{})
	if err != nil {
		return domain.NewError(domain.CodeInvalidArgument, "tool definition is invalid", err)
	}
	digest := domain.DigestBytes(canonical)

	const query = `WITH ensure_tool AS (
		INSERT INTO tools (tool_id, created_at) VALUES ($1, $5)
		ON CONFLICT (tool_id) DO NOTHING
	)
	INSERT INTO tool_versions (tool_id, version, state, definition, definition_digest, published_at)
	VALUES ($1, $2, 'draft', $3, $4, NULL)
	ON CONFLICT (tool_id, version) DO UPDATE
	SET definition=EXCLUDED.definition, definition_digest=EXCLUDED.definition_digest
	WHERE tool_versions.state='draft'`
	tag, err := r.db.Exec(ctx, query, toolID, version, canonical, digest[:], at)
	if err != nil {
		return repositoryError("save draft tool version", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.NewError(domain.CodeConflict, "released tool version is immutable; create a new version", nil)
	}
	return nil
}

// Publish performs only the draft-to-published state transition. Complete
// publication validation belongs before this persistence boundary.
func (r *AdminToolCatalogRepository) Publish(ctx context.Context, toolID, version string, at time.Time) error {
	if r == nil || r.db == nil {
		return errors.New("postgres admin tool catalog repository is required")
	}
	if err := validateToolVersionKey(toolID, version); err != nil {
		return err
	}
	if at.IsZero() {
		return domain.NewError(domain.CodeInvalidArgument, "publication timestamp is required", nil)
	}
	const query = `UPDATE tool_versions SET state='published', published_at=$3
		WHERE tool_id=$1 AND version=$2 AND state='draft'`
	tag, err := r.db.Exec(ctx, query, toolID, version, at)
	if err != nil {
		return repositoryError("publish tool version", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.NewError(domain.CodeConflict, "tool version is not a current draft", nil)
	}
	return nil
}

func (r *AdminToolCatalogRepository) Retire(ctx context.Context, toolID, version string) error {
	if r == nil || r.db == nil {
		return errors.New("postgres admin tool catalog repository is required")
	}
	if err := validateToolVersionKey(toolID, version); err != nil {
		return err
	}
	const query = `UPDATE tool_versions SET state='retired'
		WHERE tool_id=$1 AND version=$2 AND state='published'`
	tag, err := r.db.Exec(ctx, query, toolID, version)
	if err != nil {
		return repositoryError("retire tool version", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.NewError(domain.CodeConflict, "tool version is not currently published", nil)
	}
	return nil
}

func (r *AdminToolCatalogRepository) Get(ctx context.Context, toolID, version string) (ToolVersion, error) {
	if r == nil || r.db == nil {
		return ToolVersion{}, errors.New("postgres admin tool catalog repository is required")
	}
	if err := validateToolVersionKey(toolID, version); err != nil {
		return ToolVersion{}, err
	}
	const query = `SELECT tool_id, version, state, definition, definition_digest, published_at
		FROM tool_versions WHERE tool_id=$1 AND version=$2`
	var value ToolVersion
	err := r.db.QueryRow(ctx, query, toolID, version).Scan(&value.ToolID, &value.Version,
		&value.State, &value.Definition, &value.DefinitionDigest, &value.PublishedAt)
	return value, repositoryError("get tool version", err)
}

func validateToolVersionKey(toolID, version string) error {
	if _, err := domain.ParseToolID(toolID); err != nil {
		return domain.NewError(domain.CodeInvalidArgument, "tool ID is invalid", err)
	}
	if _, err := domain.ParseSemanticVersion(version); err != nil {
		return domain.NewError(domain.CodeInvalidArgument, "tool version is invalid", err)
	}
	return nil
}
