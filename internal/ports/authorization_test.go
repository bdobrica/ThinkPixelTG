package ports

import (
	"testing"
	"time"
)

func TestAuthorizationDecisionValidation(t *testing.T) {
	now := time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC)
	request := validAuthorizationRequest()
	decision := AuthorizationDecision{DecisionID: "decision-1", RequestID: request.RequestID,
		ContextDigest: "sha256:context", PolicyID: "policy", PolicyVersion: "7", Outcome: AuthorizationAllow,
		Reasons: []AuthorizationReason{ReasonAllowed}, IssuedAt: now, NotBefore: now,
		ExpiresAt: now.Add(time.Minute), RevocationCheckpoint: "checkpoint-3", EvidenceRef: "evidence-1"}
	if err := decision.ValidateFor(request); err != nil {
		t.Fatalf("valid decision: %v", err)
	}

	for name, mutate := range map[string]func(*AuthorizationDecision){
		"correlation":   func(value *AuthorizationDecision) { value.RequestID = "other" },
		"outcome":       func(value *AuthorizationDecision) { value.Outcome = "maybe" },
		"reason":        func(value *AuthorizationDecision) { value.Reasons = []AuthorizationReason{"new_reason"} },
		"contradiction": func(value *AuthorizationDecision) { value.Outcome = AuthorizationDeny },
		"time":          func(value *AuthorizationDecision) { value.ExpiresAt = now },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := decision
			mutate(&candidate)
			if candidate.ValidateFor(request) == nil {
				t.Fatal("invalid decision accepted")
			}
		})
	}
}

func TestAuthorizationRequestRequiresNormalizedSets(t *testing.T) {
	request := validAuthorizationRequest()
	request.Actions = []string{"write", "read"}
	if request.Validate() == nil {
		t.Fatal("unsorted actions accepted")
	}
}

func validAuthorizationRequest() AuthorizationRequest {
	return AuthorizationRequest{RequestID: "request-1", TenantID: "tenant-1", Subject: "subject-1", AgentID: "agent-1",
		AgentVersion: "v1", RunID: "run-1", WorkloadID: "spiffe://example/tg", ToolID: "github.issue",
		ToolVersion: "v1", Risk: "medium", SideEffect: "write", ApprovalMode: "policy", RetryMode: "idempotent",
		ArgumentProfile: "jcs-v1", ConnectorType: "github", Operation: "create", Resources: []string{"repo:a"},
		Actions: []string{"write"}, RequestedDeadline: time.Second, PolicyProfile: "default"}
}
