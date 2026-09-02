package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/bdobrica/ThinkPixelTG/internal/canonicaljson"
	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/bdobrica/ThinkPixelTG/internal/ports"
	"github.com/bdobrica/ThinkPixelTG/internal/resourceprojection"
	"github.com/bdobrica/ThinkPixelTG/internal/schema"
)

type ToolCallRequest struct {
	ToolCallID, ToolID, Version string
	Arguments                   json.RawMessage
	Identity                    ports.InvocationIdentity
}

type ToolCallResult struct {
	Invocation ports.LogicalInvocation
	State      string
	Existing   bool
}

type ToolCallService struct {
	tools                        ports.InvocationToolResolver
	ledger                       ports.InvocationLedger
	authorizer                   ports.Authorizer
	credentials                  ports.CredentialBroker
	connector                    ports.ConnectorExecutor
	validator                    *schema.Validator
	clock                        domain.Clock
	newID                        func() (domain.UUID, error)
	policyProfile, policyVersion string
}

func NewToolCallService(tools ports.InvocationToolResolver, ledger ports.InvocationLedger, authorizer ports.Authorizer, credentials ports.CredentialBroker, connector ports.ConnectorExecutor, validator *schema.Validator, clock domain.Clock, newID func() (domain.UUID, error), policyProfile, policyVersion string) (*ToolCallService, error) {
	if tools == nil || ledger == nil || authorizer == nil || credentials == nil || connector == nil || validator == nil || clock == nil || newID == nil || policyProfile == "" || policyVersion == "" {
		return nil, errors.New("tool-call service dependencies are required")
	}
	return &ToolCallService{tools: tools, ledger: ledger, authorizer: authorizer, credentials: credentials, connector: connector, validator: validator, clock: clock, newID: newID, policyProfile: policyProfile, policyVersion: policyVersion}, nil
}

func (service *ToolCallService) Create(ctx context.Context, request ToolCallRequest) (ToolCallResult, error) {
	if service == nil || request.ToolCallID == "" || !validInvocationIdentity(request.Identity) {
		return ToolCallResult{}, domain.NewError(domain.CodeInvalidArgument, "tool-call context is invalid", nil)
	}
	if !validToolCallID(request.ToolCallID) {
		return ToolCallResult{}, domain.NewError(domain.CodeInvalidArgument, "idempotency key is invalid", nil)
	}
	if _, err := domain.ParseToolID(request.ToolID); err != nil {
		return ToolCallResult{}, domain.NewError(domain.CodeNotFound, "tool version is not available", nil)
	}
	if request.Version != "" {
		if _, err := domain.ParseSemanticVersion(request.Version); err != nil {
			return ToolCallResult{}, domain.NewError(domain.CodeNotFound, "tool version is not available", nil)
		}
	}
	resolved, err := service.tools.ResolveInvocationTool(ctx, request.Identity, request.ToolID, request.Version)
	if err != nil {
		return ToolCallResult{}, err
	}
	tool := resolved.Definition
	if err := domain.ValidateToolPublication(tool, service.validator); err != nil || string(tool.ToolID) != request.ToolID || request.Version != "" && tool.Version.String() != request.Version {
		return ToolCallResult{}, domain.NewError(domain.CodeInternal, "resolved tool version is invalid", err)
	}
	compiled, err := service.validator.Compile(tool.InputSchema)
	if err != nil {
		return ToolCallResult{}, domain.NewError(domain.CodeInternal, "published input schema is invalid", err)
	}
	normalized, err := canonicaljson.NormalizeArguments(ctx, request.Arguments, canonicaljson.Limits{MaxBytes: int(tool.Limits.RequestBytes)}, func(_ context.Context, value any) error {
		raw, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			return marshalErr
		}
		return compiled.ValidateJSON(raw)
	})
	if err != nil {
		return ToolCallResult{}, domain.NewError(domain.CodeInvalidArgument, "arguments are invalid", err)
	}
	projection, err := resourceprojection.Project(normalized, projectionDefinition(tool.ResourceProjection))
	if err != nil {
		return ToolCallResult{}, domain.NewError(domain.CodeInvalidArgument, "resource projection failed", err)
	}
	now := domain.UTCNow(service.clock)
	invocationID, err := service.newID()
	if err != nil {
		return ToolCallResult{}, domain.NewError(domain.CodeInternal, "invocation identity could not be created", err)
	}
	invocation := ports.LogicalInvocation{ID: invocationID.String(), RunID: request.Identity.RunID, ToolCallID: request.ToolCallID, ToolID: request.ToolID, ToolVersion: tool.Version.String(), ArgumentProfile: normalized.Profile, ArgumentDigest: normalized.Digest, ResourceDigest: projection.Digest, ResourceProjection: append([]byte(nil), projection.Canonical...), RetryClass: string(tool.Retry), State: "validated", CreatedAt: now, UpdatedAt: now}
	acquired, err := service.ledger.Acquire(ctx, request.Identity, invocation)
	if err != nil {
		return ToolCallResult{}, err
	}
	if acquired.Kind == ports.InvocationConflict {
		return ToolCallResult{}, domain.NewError(domain.CodeConflict, "logical invocation does not match its original request", nil)
	}
	if acquired.Kind != ports.InvocationOwned {
		return ToolCallResult{Invocation: acquired.Invocation, State: acquired.Invocation.State, Existing: true}, nil
	}
	// Recovery continues the durable invocation identity returned by the ledger;
	// a new transport request must never replace it with its provisional UUID.
	invocation = acquired.Invocation
	authRequest := authorizationRequest(request.Identity, invocation, tool, projection.Value, service.policyProfile, service.policyVersion)
	decision, err := service.authorizer.AuthorizeToolInvocation(ctx, authRequest)
	if err != nil {
		return ToolCallResult{}, domain.NewError(domain.CodeUnavailable, "authorization could not be established", err)
	}
	if err := decision.ValidateFor(authRequest); err != nil {
		return ToolCallResult{}, domain.NewError(domain.CodeForbidden, "authorization decision is invalid", err)
	}
	if err := service.ledger.RecordAuthorization(ctx, request.Identity, invocation.ID, decision, normalized.Digest, projection.Digest, now); err != nil {
		return ToolCallResult{}, err
	}
	if decision.Outcome != ports.AuthorizationAllow {
		return ToolCallResult{}, domain.NewError(domain.CodeForbidden, "tool invocation is not authorized", nil)
	}
	narrowed, err := NarrowAuthorizationConstraints(decision, ConstraintCeiling{Resources: authRequest.Resources, Actions: authRequest.Actions, ArgumentMax: map[string]int64{}, MaxResultBytes: tool.Limits.ResultBytes, MaxDuration: tool.Limits.Deadline})
	if err != nil {
		return ToolCallResult{}, domain.NewError(domain.CodeForbidden, "authorization constraints cannot be enforced", err)
	}
	decision.Constraints = narrowed
	executionCtx, cancel := context.WithTimeout(ctx, narrowed.MaxDuration)
	defer cancel()
	lease, err := service.credentials.Resolve(executionCtx, request.Identity, tool, decision)
	if err != nil {
		return ToolCallResult{}, err
	}
	if lease == nil {
		return ToolCallResult{}, domain.NewError(domain.CodeInternal, "credential broker returned no capability", nil)
	}
	defer lease.Release()
	output, err := service.connector.Execute(executionCtx, ports.ConnectorRequest{InvocationID: invocation.ID, Tool: tool, CanonicalArguments: append([]byte(nil), normalized.Canonical...), ResourceProjection: append([]byte(nil), projection.Canonical...), Decision: decision, Credential: lease})
	if err != nil {
		return ToolCallResult{}, err
	}
	state, ok := connectorState(output.Classification)
	if !ok {
		return ToolCallResult{}, domain.NewError(domain.CodeInternal, "connector returned an invalid public state", nil)
	}
	// Connector content is open-world and remains inside the application until
	// output-schema and mandatory post-tool processing finalize it.
	return ToolCallResult{Invocation: invocation, State: state}, nil
}

func connectorState(classification string) (string, bool) {
	switch classification {
	case "confirmed_success":
		return "post_tool", true
	case "definitely_rejected", "not_sent", "cancelled_pre_send":
		return "failed", true
	case "transient_safe":
		return "retry_wait", true
	case "unknown":
		return "ambiguous", true
	default:
		return "", false
	}
}

func validInvocationIdentity(identity ports.InvocationIdentity) bool {
	for _, value := range []string{identity.TenantID, identity.Subject, identity.AgentID, identity.AgentVersion, identity.RunID, identity.WorkloadID} {
		if !validInvocationText(value) {
			return false
		}
	}
	return identity.Actor == "" || validInvocationText(identity.Actor)
}

func validInvocationText(value string) bool {
	return value != "" && len(value) <= 512 && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}

func validToolCallID(value string) bool {
	if len(value) < 8 || len(value) > 128 {
		return false
	}
	for index := range value {
		character := value[index]
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || index > 0 && (character == '.' || character == '_' || character == ':' || character == '-') {
			continue
		}
		return false
	}
	return true
}

func projectionDefinition(input domain.ResourceProjectionDefinition) resourceprojection.Definition {
	fields := make([]resourceprojection.Field, len(input.Fields))
	for i, field := range input.Fields {
		fields[i] = resourceprojection.Field{Name: field.Name, Pointer: field.Pointer, Literal: field.Literal, Required: field.Required, Type: resourceprojection.Type(field.Type)}
	}
	return resourceprojection.Definition{Fields: fields, MaxFields: input.MaxFields, MaxOutputBytes: input.MaxOutputBytes}
}

func authorizationRequest(identity ports.InvocationIdentity, invocation ports.LogicalInvocation, tool domain.ToolVersionDefinition, resource map[string]any, profile, version string) ports.AuthorizationRequest {
	resources := make([]string, 0, len(resource))
	for key, value := range resource {
		encoded, _ := json.Marshal(value)
		resources = append(resources, key+":"+string(encoded))
	}
	sort.Strings(resources)
	return ports.AuthorizationRequest{RequestID: invocation.ID, TenantID: identity.TenantID, Subject: identity.Subject, Actor: identity.Actor, AgentID: identity.AgentID, AgentVersion: identity.AgentVersion, RunID: identity.RunID, WorkloadID: identity.WorkloadID, ToolID: invocation.ToolID, ToolVersion: invocation.ToolVersion, Risk: string(tool.Risk), SideEffect: fmt.Sprintf("%t", tool.SideEffect), ApprovalMode: string(tool.Approval), RetryMode: string(tool.Retry), ArgumentProfile: invocation.ArgumentProfile, ConnectorType: tool.Connector.ConnectorType, Operation: tool.Connector.Operation, ArgumentDigest: invocation.ArgumentDigest, ResourceDigest: invocation.ResourceDigest, Resources: resources, Actions: []string{tool.Connector.Operation}, RequestedDeadline: tool.Limits.Deadline, PolicyProfile: profile, PolicyVersion: version}
}
