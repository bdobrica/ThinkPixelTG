package thinkpixelag

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

func TestAuthorizationClientCorrelatesAndValidatesDecision(t *testing.T) {
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("X-Request-ID") != "request-1" {
			t.Error("missing correlation header")
		}
		var input wireRequest
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(wireResponse{DecisionID: "decision-1", RequestID: input.RequestID,
			ContextDigest: input.ContextDigest, Outcome: ports.AuthorizationAllow, ReasonCodes: []ports.AuthorizationReason{ports.ReasonAllowed},
			PolicyID: "policy", PolicyVersion: "1", IssuedAt: now, NotBefore: now, ExpiresAt: now.Add(time.Minute),
			RevocationCheckpoint: "cp-1", EvidenceRef: "evidence-1"})
		return response(string(body)), nil
	})
	client, err := NewAuthorizationClient(AuthorizationConfig{Endpoint: "https://ag.example/authorize", Client: &http.Client{Transport: transport}, Timeout: time.Second, Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := client.AuthorizeToolInvocation(t.Context(), adapterRequest())
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if decision.DecisionID != "decision-1" {
		t.Fatalf("decision ID = %q", decision.DecisionID)
	}
}

func TestAuthorizationClientRejectsMalformedAndMismatchedResponses(t *testing.T) {
	for name, body := range map[string]string{
		"unknown field":    `{"unknown":true}`,
		"context mismatch": `{"decision_id":"d","request_id":"request-1","context_digest":"wrong","outcome":"allow"}`,
	} {
		t.Run(name, func(t *testing.T) {
			client, _ := NewAuthorizationClient(AuthorizationConfig{Endpoint: "https://ag.example/authorize", Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return response(body), nil })}, Timeout: time.Second})
			if _, err := client.AuthorizeToolInvocation(t.Context(), adapterRequest()); err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func response(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{authorizationMediaType}}, Body: io.NopCloser(strings.NewReader(body))}
}

func adapterRequest() ports.AuthorizationRequest {
	return ports.AuthorizationRequest{RequestID: "request-1", TenantID: "tenant", Subject: "subject", AgentID: "agent", AgentVersion: "v1",
		RunID: "run", WorkloadID: "workload", ToolID: "tool", ToolVersion: "v1", Risk: "low", SideEffect: "read",
		ApprovalMode: "none", RetryMode: "safe", ArgumentProfile: "jcs-v1", Resources: []string{"repo:a"}, Actions: []string{"read"},
		ConnectorType: "github", Operation: "get", RequestedDeadline: time.Second, PolicyProfile: "default", PolicyVersion: "1"}
}
