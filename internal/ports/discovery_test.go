package ports

import "testing"

func TestDiscoveryDecisionCanOnlyNarrowCandidates(t *testing.T) {
	t.Parallel()
	request := validDiscoveryRequest()
	request.Candidates = []ToolVersionKey{{ToolID: "github.pull.comment", Version: "1.0.0"}}
	if err := (DiscoveryAuthorizationDecision{Allowed: request.Candidates}).ValidateFor(request); err != nil {
		t.Fatalf("valid decision rejected: %v", err)
	}
	decision := DiscoveryAuthorizationDecision{Allowed: []ToolVersionKey{{ToolID: "slack.message.send", Version: "1.0.0"}}}
	if err := decision.ValidateFor(request); err == nil {
		t.Fatal("visibility-expanding decision accepted")
	}
}

func TestDiscoveryRequestRequiresCompleteGovernedIdentityAndUniqueValidCandidates(t *testing.T) {
	t.Parallel()
	request := validDiscoveryRequest()
	request.Subject = ""
	if err := request.Validate(); err == nil {
		t.Fatal("incomplete governed identity accepted")
	}
	request = validDiscoveryRequest()
	request.Candidates = []ToolVersionKey{
		{ToolID: "github.pull.comment", Version: "1.0.0"},
		{ToolID: "github.pull.comment", Version: "1.0.0"},
	}
	if err := request.Validate(); err == nil {
		t.Fatal("duplicate discovery candidates accepted")
	}
}

func validDiscoveryRequest() DiscoveryAuthorizationRequest {
	return DiscoveryAuthorizationRequest{
		TenantID: "019b0000-0000-7000-8000-000000000001", Subject: "subject-1",
		AgentID: "agent-1", AgentVersion: "1.0.0", RunID: "run-1", WorkloadID: "spiffe://example.test/tg",
	}
}
