package ports

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
)

// InvocationIdentity is derived from authenticated request context. None of
// these fields may be populated from the invocation body.
type InvocationIdentity struct {
	TenantID, Subject, Actor, AgentID, AgentVersion, RunID, WorkloadID string
}

// ResolvedToolVersion is an exposed immutable publication selected by the
// tenant-bound catalog. An empty requested version may resolve only to the
// administrator-configured default.
type ResolvedToolVersion struct{ Definition domain.ToolVersionDefinition }

type InvocationToolResolver interface {
	ResolveInvocationTool(context.Context, InvocationIdentity, string, string) (ResolvedToolVersion, error)
}

type LogicalInvocation struct {
	ID, RunID, ToolCallID, ToolID, ToolVersion, ArgumentProfile string
	ArgumentDigest, ResourceDigest                              domain.Digest
	ResourceProjection                                          json.RawMessage
	RetryClass, State                                           string
	CreatedAt, UpdatedAt                                        time.Time
}

type InvocationAcquisitionKind string

const (
	InvocationOwned    InvocationAcquisitionKind = "owned"
	InvocationExisting InvocationAcquisitionKind = "existing"
	InvocationConflict InvocationAcquisitionKind = "conflict"
)

type InvocationAcquisition struct {
	Kind       InvocationAcquisitionKind
	Invocation LogicalInvocation
}

// InvocationLedger is the durable boundary. Acquire must atomically bind the
// tenant/run/tool-call identity to the immutable version and both digests.
type InvocationLedger interface {
	Acquire(context.Context, InvocationIdentity, LogicalInvocation) (InvocationAcquisition, error)
	RecordAuthorization(context.Context, InvocationIdentity, string, AuthorizationDecision, domain.Digest, domain.Digest, time.Time) error
}

// ToolCallView is the caller-safe projection of a logical invocation. Result
// is populated only after post-tool controls have finalized a successful
// invocation; it must never contain raw connector output.
type ToolCallView struct {
	ToolCallID, ToolID, ToolVersion, State string
	Result                                 json.RawMessage
	ErrorCode                              *string
	CreatedAt, UpdatedAt                   time.Time
}

// ToolCallReader resolves within the trusted tenant and run identity. A miss
// in any part of that scope must be indistinguishable from an unknown ID.
type ToolCallReader interface {
	GetToolCall(context.Context, InvocationIdentity, string) (ToolCallView, error)
}

// CredentialCapability exposes safe target/lifetime metadata for connector
// validation and provides scoped access to opaque C3 secret bytes. Implementors
// must redact all serialization and formatting paths.
type CredentialCapability interface {
	Metadata() CredentialCapabilityMetadata
	UseSecret(func([]byte) error) error
	Release()
}

type CredentialCapabilityMetadata struct {
	Kind                                domain.CredentialCapabilityKind
	ProviderRef, Issuer                 string
	Audiences, Resources, Scopes        []string
	IssuedAt, ExpiresAt                 time.Time
	LeaseID, RefreshID, RevocationEpoch string
}

type CredentialBroker interface {
	Resolve(context.Context, InvocationIdentity, domain.ToolVersionDefinition, AuthorizationDecision) (CredentialCapability, error)
}

// CredentialProvider resolves one administrator-authored binding for the
// governed subject. The context carries the caller's resolution deadline.
type CredentialProvider interface {
	Resolve(context.Context, domain.CredentialBinding, string) (CredentialCapability, error)
}

type ConnectorRequest struct {
	InvocationID       string
	Tool               domain.ToolVersionDefinition
	CanonicalArguments json.RawMessage
	ResourceProjection json.RawMessage
	Decision           AuthorizationDecision
	Credential         CredentialCapability
}

type ConnectorResult struct {
	Classification string
	Result         json.RawMessage
}

// ConnectorExecutor is the only application egress boundary for tool work.
type ConnectorExecutor interface {
	Execute(context.Context, ConnectorRequest) (ConnectorResult, error)
}

// ConnectorReconciliationRequest contains only stable, previously persisted
// execution evidence. It must not contain caller-selected destinations or
// credential material.
type ConnectorReconciliationRequest struct {
	InvocationID string
	Tool         domain.ToolVersionDefinition
	Evidence     json.RawMessage
}

type ConnectorReconciliationResult struct {
	Outcome  string
	Result   json.RawMessage
	Evidence json.RawMessage
}

// ConnectorReconciler is implemented only by connectors whose immutable
// operation contract provides authoritative reconciliation.
type ConnectorReconciler interface {
	Reconcile(context.Context, ConnectorReconciliationRequest) (ConnectorReconciliationResult, error)
}
