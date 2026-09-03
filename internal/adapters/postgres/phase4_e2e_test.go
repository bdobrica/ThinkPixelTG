//go:build integration && e2e

package postgres

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/adapters/connectors/mock"
	gatewayhttp "github.com/bdobrica/ThinkPixelTG/internal/adapters/http"
	"github.com/bdobrica/ThinkPixelTG/internal/app"
	"github.com/bdobrica/ThinkPixelTG/internal/authn"
	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/bdobrica/ThinkPixelTG/internal/ports"
	"github.com/bdobrica/ThinkPixelTG/internal/schema"
)

func TestPhase4GovernedMockInvocationE2E(t *testing.T) {
	ctx := context.Background()
	pool := repositoryTestPool(t, ctx)
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	const tenantID = "019b0000-0000-7000-8000-000000000501"
	tool := phase4E2ETool(t)
	if _, err := pool.Exec(ctx, `INSERT INTO tenants VALUES ($1,$2)`, tenantID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO tools VALUES ($1,$2)`, string(tool.ToolID), now); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO tool_versions VALUES ($1,$2,'published','{}',$3,$4)`, string(tool.ToolID), tool.Version.String(), make([]byte, 32), now); err != nil {
		t.Fatal(err)
	}

	repositories, err := NewTenantRepositories(pool, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	acquirer, err := NewLogicalInvocationAcquirer(pool, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	clock := phase4Clock{now}
	ledger, err := NewInvocationLedger(acquirer, repositories.Decisions, "phase4-worker", time.Minute, 1, clock)
	if err != nil {
		t.Fatal(err)
	}
	connector, err := mock.New(mock.Config{Operations: map[string]mock.Operation{
		"comment.create": {Kind: mock.Write, Outcomes: []mock.Outcome{{Classification: "confirmed_success", Result: json.RawMessage(`{"comment_id":"mock-1"}`)}}, Reconciliation: "confirmed_success"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	lease := &phase4Lease{}
	service, err := app.NewToolCallService(phase4Resolver{tool}, ledger, phase4Authorizer{now}, phase4Broker{lease}, connector, schema.NewValidator(schema.Limits{}), clock,
		func() (domain.UUID, error) { return domain.ParseUUID("019b0000-0000-7000-8000-000000000502") }, "default", "v1")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := gatewayhttp.NewToolCallHandler(gatewayhttp.ToolCallOptions{Service: service})
	if err != nil {
		t.Fatal(err)
	}
	workload, _ := authn.NewLocalWorkloadSource("spiffe://local/phase4-harness")
	authenticator, err := authn.NewHTTPAuthenticatorWithWorkload(phase4Verifier{tenantID: tenantID, now: now}, workload)
	if err != nil {
		t.Fatal(err)
	}
	protected := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		authenticated, authenticateErr := authenticator.Authenticate(request.Context(), request)
		if authenticateErr != nil {
			t.Fatalf("authenticate request: %v", authenticateErr)
		}
		handler.ServeHTTP(writer, request.WithContext(authenticated))
	})

	for attempt, wantStatus := range []int{http.StatusAccepted, http.StatusOK} {
		request := httptest.NewRequest(http.MethodPost, "/v1/tool-calls", strings.NewReader(`{"tool_id":"mock.comment.create","version":"1.0.0","arguments":{"repository":"thinkpixel/tg","body":"phase 4"}}`))
		request.Header.Set("Authorization", "Bearer phase4-token")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "phase4-call-0001")
		response := httptest.NewRecorder()
		protected.ServeHTTP(response, request)
		if response.Code != wantStatus {
			t.Fatalf("attempt %d response = %d %s", attempt+1, response.Code, response.Body.String())
		}
	}
	if connector.Calls("comment.create") != 1 || lease.releases != 1 {
		t.Fatalf("connector calls=%d credential releases=%d", connector.Calls("comment.create"), lease.releases)
	}
	for table, want := range map[string]int{"invocations": 1, "authorization_decisions": 1, "audit_events": 1, "outbox_messages": 1} {
		var count int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE tenant_id=$1", tenantID).Scan(&count); err != nil || count != want {
			t.Fatalf("%s count=%d error=%v, want %d", table, count, err, want)
		}
	}
}

type phase4Clock struct{ now time.Time }

func (clock phase4Clock) Now() time.Time { return clock.now }

type phase4Resolver struct{ tool domain.ToolVersionDefinition }

func (resolver phase4Resolver) ResolveInvocationTool(context.Context, ports.InvocationIdentity, string, string) (ports.ResolvedToolVersion, error) {
	return ports.ResolvedToolVersion{Definition: resolver.tool}, nil
}

type phase4Authorizer struct{ now time.Time }

func (authorizer phase4Authorizer) AuthorizeToolInvocation(_ context.Context, request ports.AuthorizationRequest) (ports.AuthorizationDecision, error) {
	digest := domain.DigestBytes([]byte("phase4-context"))
	return ports.AuthorizationDecision{DecisionID: "phase4-decision", RequestID: request.RequestID, ContextDigest: digest.String(), PolicyID: "phase4-policy", PolicyVersion: request.PolicyVersion, Outcome: ports.AuthorizationAllow, Reasons: []ports.AuthorizationReason{ports.ReasonAllowed}, IssuedAt: authorizer.now, NotBefore: authorizer.now, ExpiresAt: authorizer.now.Add(time.Minute), RevocationCheckpoint: "phase4-checkpoint", Constraints: ports.AuthorizationConstraints{Resources: request.Resources, Actions: request.Actions, MaxResultBytes: 4096, MaxDuration: time.Second}, EvidenceRef: "phase4-ag-evidence"}, nil
}

type phase4Lease struct{ releases int }

func (*phase4Lease) Metadata() ports.CredentialCapabilityMetadata {
	return ports.CredentialCapabilityMetadata{}
}
func (*phase4Lease) UseSecret(use func([]byte) error) error { return use(nil) }
func (lease *phase4Lease) Release()                         { lease.releases++ }

type phase4Broker struct{ lease *phase4Lease }

func (broker phase4Broker) Resolve(context.Context, ports.InvocationIdentity, domain.ToolVersionDefinition, ports.AuthorizationDecision) (ports.CredentialCapability, error) {
	return broker.lease, nil
}

type phase4Verifier struct {
	tenantID string
	now      time.Time
}

func (verifier phase4Verifier) Verify(context.Context, string) (authn.Claims, error) {
	return authn.Claims{Issuer: "https://issuer.example", Subject: "phase4-user", Audience: []string{"thinkpixeltg"}, ExpiresAt: verifier.now.Add(time.Hour), Raw: map[string]any{"tenant_id": verifier.tenantID, "agent_id": "phase4-agent", "agent_version": "1.0.0", "run_id": "phase4-run"}}, nil
}

func phase4E2ETool(t *testing.T) domain.ToolVersionDefinition {
	t.Helper()
	id, _ := domain.ParseToolID("mock.comment.create")
	version, _ := domain.ParseSemanticVersion("1.0.0")
	return domain.ToolVersionDefinition{ToolID: id, Version: version, Risk: domain.RiskBoundedWrite, SideEffect: true, Retry: domain.RetryDownstreamIdempotency, Approval: domain.ApprovalPolicy, Description: domain.ReviewedDescription{Title: "Mock comment", Description: "Create a hermetic comment", ReviewRef: "phase4-review"}, InputSchema: []byte(`{"type":"object","additionalProperties":false,"required":["repository","body"],"properties":{"repository":{"type":"string"},"body":{"type":"string"}}}`), OutputSchema: []byte(`{"type":"object"}`), CanonicalProfile: "jcs-v1", Connector: domain.ConnectorBinding{ConnectorType: "mock", Operation: "comment.create", InstanceSelector: "phase4"}, CredentialSelector: "phase4", RetryQualification: "mock-native-idempotency", ResourceProjection: domain.ResourceProjectionDefinition{Fields: []domain.ResourceProjectionField{{Name: "repository", Pointer: "/repository", Required: true, Type: domain.ProjectionString}}}, Metering: domain.MeteringRule{Dimension: "calls", Units: "1", ChargePoint: domain.MeterAtResult, DeduplicationScope: domain.MeterPerLogicalInvocation}, Limits: domain.ToolLimits{RequestBytes: 4096, ResultBytes: 4096, Deadline: time.Second, Concurrency: 1, MaxAttempts: 1}}
}
