package ports

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
)

// Authorizer is the application-owned authorization boundary. Adapters must
// translate their wire representation into these types before returning.
type Authorizer interface {
	AuthorizeToolInvocation(context.Context, AuthorizationRequest) (AuthorizationDecision, error)
}

type AuthorizationOutcome string

const (
	AuthorizationAllow AuthorizationOutcome = "allow"
	AuthorizationDeny  AuthorizationOutcome = "deny"
)

type AuthorizationReason string

const (
	ReasonAllowed            AuthorizationReason = "allowed"
	ReasonPolicyDenied       AuthorizationReason = "policy_denied"
	ReasonRunInactive        AuthorizationReason = "run_inactive"
	ReasonAgentVersionDenied AuthorizationReason = "agent_version_denied"
	ReasonToolDenied         AuthorizationReason = "tool_denied"
	ReasonResourceDenied     AuthorizationReason = "resource_denied"
	ReasonBudgetExhausted    AuthorizationReason = "budget_exhausted"
	ReasonApprovalRequired   AuthorizationReason = "approval_required"
	ReasonRevoked            AuthorizationReason = "revoked"
	ReasonDecisionStale      AuthorizationReason = "decision_stale"
	ReasonDependencyDown     AuthorizationReason = "dependency_unavailable"
	ReasonMalformedDecision  AuthorizationReason = "malformed_decision"
)

type AuthorizationRequest struct {
	RequestID, TenantID, Subject, Actor, AgentID, AgentVersion, RunID, WorkloadID string
	ToolID, ToolVersion, Risk, SideEffect, ApprovalMode, RetryMode                string
	ArgumentProfile, ConnectorType, Operation                                     string
	ArgumentDigest, ResourceDigest                                                domain.Digest
	Resources, Actions                                                            []string
	RequestedDeadline                                                             time.Duration
	PolicyProfile                                                                 string
	PolicyVersion                                                                 string
}

type AuthorizationConstraints struct {
	Repositories   []string
	Resources      []string
	Actions        []string
	ArgumentMax    map[string]int64
	MaxResultBytes int64
	MaxDuration    time.Duration
}

type ApprovalRequirement struct {
	Required bool
	Mode     string
}

type AuthorizationDecision struct {
	DecisionID, RequestID, ContextDigest, PolicyID, PolicyVersion string
	Outcome                                                       AuthorizationOutcome
	Reasons                                                       []AuthorizationReason
	IssuedAt, NotBefore, ExpiresAt                                time.Time
	RevocationEpoch                                               uint64
	RevocationCheckpoint                                          string
	Constraints                                                   AuthorizationConstraints
	Approval                                                      ApprovalRequirement
	EvidenceRef                                                   string
}

func (request AuthorizationRequest) Validate() error {
	required := []string{request.RequestID, request.TenantID, request.Subject, request.AgentID,
		request.AgentVersion, request.RunID, request.WorkloadID, request.ToolID, request.ToolVersion,
		request.Risk, request.SideEffect, request.ApprovalMode, request.RetryMode,
		request.ArgumentProfile, request.ConnectorType, request.Operation, request.PolicyProfile, request.PolicyVersion}
	for _, value := range required {
		if !validAuthorizationText(value) {
			return errors.New("authorization request has missing or invalid fields")
		}
	}
	if request.Actor != "" && !validAuthorizationText(request.Actor) || request.RequestedDeadline <= 0 {
		return errors.New("authorization request has invalid bounds")
	}
	if !validSet(request.Resources) || !validSet(request.Actions) {
		return errors.New("authorization request sets must be normalized")
	}
	return nil
}

func (decision AuthorizationDecision) ValidateFor(request AuthorizationRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if !validAuthorizationText(decision.DecisionID) || decision.RequestID != request.RequestID ||
		!validAuthorizationText(decision.ContextDigest) || !validAuthorizationText(decision.PolicyID) ||
		decision.PolicyVersion != request.PolicyVersion || !validAuthorizationText(decision.EvidenceRef) ||
		!validAuthorizationText(decision.RevocationCheckpoint) || decision.IssuedAt.IsZero() ||
		decision.NotBefore.IsZero() || decision.ExpiresAt.IsZero() || decision.NotBefore.Before(decision.IssuedAt) ||
		!decision.ExpiresAt.After(decision.NotBefore) {
		return errors.New("authorization decision is incomplete or contradictory")
	}
	if decision.Outcome != AuthorizationAllow && decision.Outcome != AuthorizationDeny || len(decision.Reasons) == 0 {
		return errors.New("authorization decision outcome is invalid")
	}
	for _, reason := range decision.Reasons {
		if !reason.Valid() {
			return errors.New("authorization decision reason is unknown")
		}
	}
	if decision.Outcome == AuthorizationAllow && !containsReason(decision.Reasons, ReasonAllowed) ||
		decision.Outcome == AuthorizationDeny && containsReason(decision.Reasons, ReasonAllowed) {
		return errors.New("authorization decision reasons contradict outcome")
	}
	return decision.Constraints.Validate()
}

func (reason AuthorizationReason) Valid() bool {
	switch reason {
	case ReasonAllowed, ReasonPolicyDenied, ReasonRunInactive, ReasonAgentVersionDenied,
		ReasonToolDenied, ReasonResourceDenied, ReasonBudgetExhausted, ReasonApprovalRequired,
		ReasonRevoked, ReasonDecisionStale, ReasonDependencyDown, ReasonMalformedDecision:
		return true
	default:
		return false
	}
}

func (constraints AuthorizationConstraints) Validate() error {
	if !validSet(constraints.Repositories) || !validSet(constraints.Resources) || !validSet(constraints.Actions) ||
		constraints.MaxResultBytes < 0 || constraints.MaxDuration < 0 {
		return errors.New("authorization constraints are invalid")
	}
	for name, maximum := range constraints.ArgumentMax {
		if !validAuthorizationText(name) || maximum < 0 {
			return errors.New("authorization argument constraint is invalid")
		}
	}
	return nil
}

func validAuthorizationText(value string) bool {
	return value != "" && len(value) <= 512 && strings.TrimSpace(value) == value
}

func validSet(values []string) bool {
	previous := ""
	for _, value := range values {
		if !validAuthorizationText(value) || previous != "" && value <= previous {
			return false
		}
		previous = value
	}
	return true
}

func containsReason(reasons []AuthorizationReason, wanted AuthorizationReason) bool {
	for _, reason := range reasons {
		if reason == wanted {
			return true
		}
	}
	return false
}
