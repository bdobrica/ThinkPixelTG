package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/jackc/pgx/v5"
)

// TenantRepositories is the only construction point for tenant-owned data.
// The tenant identifier is validated once and is then supplied by the adapter
// to every statement; callers cannot substitute a tenant on individual calls.
type TenantRepositories struct {
	ToolCatalog        ToolCatalogRepository
	ConnectorInstances ConnectorInstanceRepository
	CredentialBindings CredentialBindingRepository
	Invocations        InvocationRepository
	Attempts           AttemptRepository
	Decisions          DecisionRepository
	Approvals          ApprovalRepository
	Results            ResultRepository
	Usage              UsageRepository
	Audit              AuditRepository
	Outbox             OutboxRepository
}

type tenantRepository struct {
	db       DBTX
	tenantID string
}

func NewTenantRepositories(db DBTX, tenantID string) (*TenantRepositories, error) {
	if db == nil {
		return nil, errors.New("postgres repository database is required")
	}
	id, err := domain.ParseUUID(tenantID)
	if err != nil {
		return nil, fmt.Errorf("invalid repository tenant ID: %w", err)
	}
	base := tenantRepository{db: db, tenantID: id.String()}
	return &TenantRepositories{
		ToolCatalog: ToolCatalogRepository{base}, ConnectorInstances: ConnectorInstanceRepository{base},
		CredentialBindings: CredentialBindingRepository{base}, Invocations: InvocationRepository{base},
		Attempts: AttemptRepository{base}, Decisions: DecisionRepository{base}, Approvals: ApprovalRepository{base},
		Results: ResultRepository{base}, Usage: UsageRepository{base}, Audit: AuditRepository{base},
		Outbox: OutboxRepository{base},
	}, nil
}

// WithDB returns the same tenant-scoped repository set backed by a transaction.
// This makes multi-record atomic boundaries explicit without putting a tx in context.
func (r *TenantRepositories) WithDB(db DBTX) (*TenantRepositories, error) {
	if r == nil {
		return nil, errors.New("postgres repositories are required")
	}
	return NewTenantRepositories(db, r.Invocations.tenantID)
}

type ToolCatalogRepository struct{ tenantRepository }
type ConnectorInstanceRepository struct{ tenantRepository }
type CredentialBindingRepository struct{ tenantRepository }
type InvocationRepository struct{ tenantRepository }
type AttemptRepository struct{ tenantRepository }
type DecisionRepository struct{ tenantRepository }
type ApprovalRepository struct{ tenantRepository }
type ResultRepository struct{ tenantRepository }
type UsageRepository struct{ tenantRepository }
type AuditRepository struct{ tenantRepository }
type OutboxRepository struct{ tenantRepository }

type ToolVersion struct {
	ToolID, Version, State string
	Definition             json.RawMessage
	DefinitionDigest       []byte
	PublishedAt            *time.Time
}

func (r ToolCatalogRepository) GetExposedVersion(ctx context.Context, toolID, version string) (ToolVersion, error) {
	const query = `SELECT v.tool_id, v.version, v.state, v.definition, v.definition_digest, v.published_at
		FROM tenant_tool_exposures e JOIN tool_versions v
		ON v.tool_id = e.tool_id AND v.version = e.version
		WHERE e.tenant_id = $1 AND e.tool_id = $2 AND e.version = $3 AND e.enabled`
	var value ToolVersion
	err := r.db.QueryRow(ctx, query, r.tenantID, toolID, version).Scan(&value.ToolID, &value.Version,
		&value.State, &value.Definition, &value.DefinitionDigest, &value.PublishedAt)
	return value, repositoryError("get exposed tool version", err)
}

func (r ToolCatalogRepository) SetExposure(ctx context.Context, toolID, version string, enabled bool, at time.Time) error {
	const query = `INSERT INTO tenant_tool_exposures (tenant_id, tool_id, version, enabled, updated_at)
		VALUES ($1, $2, $3, $4, $5) ON CONFLICT (tenant_id, tool_id, version)
		DO UPDATE SET enabled = EXCLUDED.enabled, updated_at = EXCLUDED.updated_at`
	_, err := r.db.Exec(ctx, query, r.tenantID, toolID, version, enabled, at)
	return repositoryError("set tool exposure", err)
}

type ConnectorInstance struct {
	ID, Type             string
	DestinationConfig    json.RawMessage
	ConfigDigest         []byte
	Enabled              bool
	CreatedAt, UpdatedAt time.Time
}

func (r ConnectorInstanceRepository) Put(ctx context.Context, value ConnectorInstance) error {
	const query = `INSERT INTO connector_instances (tenant_id, connector_instance_id, connector_type,
		destination_config, config_digest, enabled, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (tenant_id, connector_instance_id) DO UPDATE SET connector_type=EXCLUDED.connector_type,
		destination_config=EXCLUDED.destination_config, config_digest=EXCLUDED.config_digest,
		enabled=EXCLUDED.enabled, updated_at=EXCLUDED.updated_at`
	_, err := r.db.Exec(ctx, query, r.tenantID, value.ID, value.Type, value.DestinationConfig,
		value.ConfigDigest, value.Enabled, value.CreatedAt, value.UpdatedAt)
	return repositoryError("put connector instance", err)
}

func (r ConnectorInstanceRepository) Get(ctx context.Context, id string) (ConnectorInstance, error) {
	const query = `SELECT connector_instance_id, connector_type, destination_config, config_digest,
		enabled, created_at, updated_at FROM connector_instances WHERE tenant_id=$1 AND connector_instance_id=$2`
	var value ConnectorInstance
	err := r.db.QueryRow(ctx, query, r.tenantID, id).Scan(&value.ID, &value.Type, &value.DestinationConfig,
		&value.ConfigDigest, &value.Enabled, &value.CreatedAt, &value.UpdatedAt)
	return value, repositoryError("get connector instance", err)
}

type CredentialBinding struct {
	ID, ConnectorInstanceID, ProviderRef string
	CapabilityMetadata                   json.RawMessage
	Enabled                              bool
	CreatedAt, UpdatedAt                 time.Time
}

func (r CredentialBindingRepository) Put(ctx context.Context, value CredentialBinding) error {
	const query = `INSERT INTO credential_bindings (tenant_id, credential_binding_id, connector_instance_id,
		provider_ref, capability_metadata, enabled, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (tenant_id, credential_binding_id) DO UPDATE SET connector_instance_id=EXCLUDED.connector_instance_id,
		provider_ref=EXCLUDED.provider_ref, capability_metadata=EXCLUDED.capability_metadata,
		enabled=EXCLUDED.enabled, updated_at=EXCLUDED.updated_at`
	_, err := r.db.Exec(ctx, query, r.tenantID, value.ID, value.ConnectorInstanceID, value.ProviderRef,
		value.CapabilityMetadata, value.Enabled, value.CreatedAt, value.UpdatedAt)
	return repositoryError("put credential binding", err)
}

func (r CredentialBindingRepository) Get(ctx context.Context, id string) (CredentialBinding, error) {
	const query = `SELECT credential_binding_id, connector_instance_id, provider_ref, capability_metadata,
		enabled, created_at, updated_at FROM credential_bindings WHERE tenant_id=$1 AND credential_binding_id=$2`
	var value CredentialBinding
	err := r.db.QueryRow(ctx, query, r.tenantID, id).Scan(&value.ID, &value.ConnectorInstanceID,
		&value.ProviderRef, &value.CapabilityMetadata, &value.Enabled, &value.CreatedAt, &value.UpdatedAt)
	return value, repositoryError("get credential binding", err)
}

type Invocation struct {
	ID, RunID, ToolCallID, ToolID, ToolVersion, ArgumentProfile string
	ArgumentDigest, ResourceDigest                              []byte
	ResourceProjection                                          json.RawMessage
	RetryClass, State                                           string
	StateVersion                                                int64
	TerminalCode                                                *string
	TerminalAt                                                  *time.Time
	CreatedAt, UpdatedAt                                        time.Time
}

func (r InvocationRepository) Create(ctx context.Context, v Invocation) error {
	const query = `INSERT INTO invocations (tenant_id, invocation_id, run_id, tool_call_id, tool_id,
		tool_version, argument_profile, argument_digest, resource_projection, resource_digest, retry_class,
		state, state_version, terminal_code, terminal_at, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`
	_, err := r.db.Exec(ctx, query, r.tenantID, v.ID, v.RunID, v.ToolCallID, v.ToolID, v.ToolVersion,
		v.ArgumentProfile, v.ArgumentDigest, v.ResourceProjection, v.ResourceDigest, v.RetryClass, v.State,
		v.StateVersion, v.TerminalCode, v.TerminalAt, v.CreatedAt, v.UpdatedAt)
	return repositoryError("create invocation", err)
}

func (r InvocationRepository) Get(ctx context.Context, id string) (Invocation, error) {
	const query = `SELECT invocation_id,run_id,tool_call_id,tool_id,tool_version,argument_profile,
		argument_digest,resource_projection,resource_digest,retry_class,state,state_version,terminal_code,
		terminal_at,created_at,updated_at FROM invocations WHERE tenant_id=$1 AND invocation_id=$2`
	var v Invocation
	err := r.db.QueryRow(ctx, query, r.tenantID, id).Scan(&v.ID, &v.RunID, &v.ToolCallID, &v.ToolID,
		&v.ToolVersion, &v.ArgumentProfile, &v.ArgumentDigest, &v.ResourceProjection, &v.ResourceDigest,
		&v.RetryClass, &v.State, &v.StateVersion, &v.TerminalCode, &v.TerminalAt, &v.CreatedAt, &v.UpdatedAt)
	return v, repositoryError("get invocation", err)
}

type Attempt struct {
	InvocationID                                                                                                   string
	AttemptNo                                                                                                      int
	Fence                                                                                                          int64
	OwnerID                                                                                                        *string
	ClaimedAt, LeaseExpiresAt                                                                                      *time.Time
	DownstreamRequestRef, DownstreamResultRef, OutcomeClassification, RetryClassification, AmbiguityClassification *string
	Evidence                                                                                                       json.RawMessage
	StartedAt                                                                                                      time.Time
	FinishedAt                                                                                                     *time.Time
}

func (r AttemptRepository) Create(ctx context.Context, v Attempt) error {
	const query = `INSERT INTO invocation_attempts (tenant_id,invocation_id,attempt_no,fence,owner_id,
		claimed_at,lease_expires_at,downstream_request_ref,downstream_result_ref,outcome_classification,
		retry_classification,ambiguity_classification,evidence,started_at,finished_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`
	_, err := r.db.Exec(ctx, query, r.tenantID, v.InvocationID, v.AttemptNo, v.Fence, v.OwnerID,
		v.ClaimedAt, v.LeaseExpiresAt, v.DownstreamRequestRef, v.DownstreamResultRef, v.OutcomeClassification,
		v.RetryClassification, v.AmbiguityClassification, v.Evidence, v.StartedAt, v.FinishedAt)
	return repositoryError("create invocation attempt", err)
}

type AuthorizationDecision struct {
	InvocationID, DecisionID                      string
	ContextDigest, ArgumentDigest, ResourceDigest []byte
	Outcome, PolicyRef                            string
	Constraints                                   json.RawMessage
	IssuedAt, ExpiresAt, RecordedAt               time.Time
	RevocationCheckpoint                          *string
}

func (r DecisionRepository) RecordAuthorization(ctx context.Context, v AuthorizationDecision) error {
	const query = `INSERT INTO authorization_decisions (tenant_id,invocation_id,decision_id,context_digest,
		argument_digest,resource_digest,outcome,policy_ref,constraints,issued_at,expires_at,recorded_at,revocation_checkpoint)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`
	_, err := r.db.Exec(ctx, query, r.tenantID, v.InvocationID, v.DecisionID, v.ContextDigest,
		v.ArgumentDigest, v.ResourceDigest, v.Outcome, v.PolicyRef, v.Constraints, v.IssuedAt, v.ExpiresAt,
		v.RecordedAt, v.RevocationCheckpoint)
	return repositoryError("record authorization decision", err)
}

type GREvaluation struct {
	InvocationID, EvaluationID, Phase       string
	ContentDigest, TransformedContentDigest []byte
	Decision, PolicyRef                     string
	SafeMetadata                            json.RawMessage
	CreatedAt                               time.Time
}

func (r DecisionRepository) RecordGuardrail(ctx context.Context, v GREvaluation) error {
	const query = `INSERT INTO gr_evaluations (tenant_id,invocation_id,evaluation_id,phase,content_digest,
		transformed_content_digest,decision,policy_ref,safe_metadata,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`
	_, err := r.db.Exec(ctx, query, r.tenantID, v.InvocationID, v.EvaluationID, v.Phase, v.ContentDigest,
		nilIfEmpty(v.TransformedContentDigest), v.Decision, v.PolicyRef, v.SafeMetadata, v.CreatedAt)
	return repositoryError("record guardrail evaluation", err)
}

type ActionApproval struct {
	InvocationID, ApprovalID, ApprovalRef         string
	BindingDigest, ArgumentDigest, ResourceDigest []byte
	AuthorizationDecisionID, Status               string
	IssuedAt, ExpiresAt                           time.Time
	ConsumedAt                                    *time.Time
	RevocationCheckpoint                          *string
}

func (r ApprovalRepository) Create(ctx context.Context, v ActionApproval) error {
	const query = `INSERT INTO action_approvals (tenant_id,invocation_id,approval_id,approval_ref,binding_digest,
		argument_digest,resource_digest,authorization_decision_id,status,issued_at,expires_at,consumed_at,revocation_checkpoint)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`
	_, err := r.db.Exec(ctx, query, r.tenantID, v.InvocationID, v.ApprovalID, v.ApprovalRef,
		v.BindingDigest, v.ArgumentDigest, v.ResourceDigest, v.AuthorizationDecisionID, v.Status,
		v.IssuedAt, v.ExpiresAt, v.ConsumedAt, v.RevocationCheckpoint)
	return repositoryError("create action approval", err)
}

type ExecutionResult struct {
	InvocationID                             string
	AttemptNo                                *int
	Fence                                    *int64
	ConnectorInstanceID, CredentialBindingID *string
	ResultDigest                             []byte
	SafeResult                               json.RawMessage
	Classification                           string
	DownstreamResultRef                      *string
	DataClassification                       string
	CreatedAt                                time.Time
}

func (r ResultRepository) Create(ctx context.Context, v ExecutionResult) error {
	const query = `INSERT INTO execution_results (tenant_id,invocation_id,attempt_no,fence,connector_instance_id,
		credential_binding_id,result_digest,safe_result,classification,downstream_result_ref,data_classification,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`
	_, err := r.db.Exec(ctx, query, r.tenantID, v.InvocationID, v.AttemptNo, v.Fence, v.ConnectorInstanceID,
		v.CredentialBindingID, nilIfEmpty(v.ResultDigest), nilIfEmpty(v.SafeResult), v.Classification,
		v.DownstreamResultRef, v.DataClassification, v.CreatedAt)
	return repositoryError("create execution result", err)
}

type UsageEvent struct {
	EventID, InvocationID, AccountingKey, Dimension, Quantity, Unit string
	Attributes                                                      json.RawMessage
	OccurredAt, RecordedAt                                          time.Time
}

func (r UsageRepository) Record(ctx context.Context, v UsageEvent) error {
	const query = `INSERT INTO trusted_usage_events (tenant_id,event_id,invocation_id,accounting_key,
		dimension,quantity,unit,attributes,occurred_at,recorded_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`
	_, err := r.db.Exec(ctx, query, r.tenantID, v.EventID, v.InvocationID, v.AccountingKey, v.Dimension,
		v.Quantity, v.Unit, v.Attributes, v.OccurredAt, v.RecordedAt)
	return repositoryError("record trusted usage", err)
}

type AuditEvent struct {
	EventID                                string
	InvocationID                           *string
	EventType, EvidenceProfile, ActorClass string
	ActorRef                               *string
	Outcome                                string
	ReasonCode                             *string
	Correlation, SafePayload               json.RawMessage
	PayloadDigest                          []byte
	OccurredAt, RecordedAt                 time.Time
}

func (r AuditRepository) Record(ctx context.Context, v AuditEvent) error {
	const query = `INSERT INTO audit_events (tenant_id,event_id,invocation_id,event_type,evidence_profile,
		actor_class,actor_ref,outcome,reason_code,correlation,safe_payload,payload_digest,occurred_at,recorded_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`
	_, err := r.db.Exec(ctx, query, r.tenantID, v.EventID, v.InvocationID, v.EventType, v.EvidenceProfile,
		v.ActorClass, v.ActorRef, v.Outcome, v.ReasonCode, v.Correlation, v.SafePayload, v.PayloadDigest,
		v.OccurredAt, v.RecordedAt)
	return repositoryError("record audit event", err)
}

type OutboxMessage struct {
	ID, EventID, Topic, EventType string
	SafePayload                   json.RawMessage
	PayloadDigest                 []byte
	CreatedAt, AvailableAt        time.Time
}

func (r OutboxRepository) Enqueue(ctx context.Context, v OutboxMessage) error {
	const query = `INSERT INTO outbox_messages (tenant_id,outbox_id,event_id,topic,event_type,safe_payload,
		payload_digest,created_at,available_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`
	_, err := r.db.Exec(ctx, query, r.tenantID, v.ID, v.EventID, v.Topic, v.EventType, v.SafePayload,
		v.PayloadDigest, v.CreatedAt, v.AvailableAt)
	return repositoryError("enqueue outbox message", err)
}

func repositoryError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.NewError(domain.CodeNotFound, operation, err)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func nilIfEmpty[T ~[]byte](value T) any {
	if len(value) == 0 {
		return nil
	}
	return value
}
