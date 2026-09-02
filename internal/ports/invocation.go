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

type CredentialCapability interface{ Release() }

type CredentialBroker interface {
	Resolve(context.Context, InvocationIdentity, domain.ToolVersionDefinition, AuthorizationDecision) (CredentialCapability, error)
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
