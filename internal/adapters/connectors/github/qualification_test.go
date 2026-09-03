package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/app"
	"github.com/bdobrica/ThinkPixelTG/internal/credentials"
	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/bdobrica/ThinkPixelTG/internal/ports"
	"github.com/bdobrica/ThinkPixelTG/internal/schema"
)

const qualificationCanary = "SYNTHETIC_GITHUB_CREDENTIAL_CANARY_CRED_014"

func TestIsolatedGitHubReadWriteQualification(t *testing.T) {
	t.Run("unauthorized call stops before credential resolution", func(t *testing.T) {
		ledger := &qualificationLedger{}
		broker := &qualificationBroker{t: t}
		service := qualificationService(t, qualificationTool(t, PullGet, false), ledger,
			qualificationAuthorizer{outcome: ports.AuthorizationDeny}, broker,
			&qualificationConnector{t: t})

		result, err := service.Create(t.Context(), qualificationRequest(PullGet))
		if err == nil {
			t.Fatal("unauthorized invocation succeeded")
		}
		assertHarnessHasNoCredential(t, result, err)
		if broker.resolutions != 0 {
			t.Fatalf("credential resolutions = %d, want 0", broker.resolutions)
		}
	})

	for _, operation := range []string{PullGet, PullComment} {
		t.Run(operation, func(t *testing.T) {
			ledger := &qualificationLedger{}
			broker := &qualificationBroker{t: t}
			providerCalls := 0
			var providerRequest *http.Request
			client := httpClientFunc(func(_ context.Context, gotOperation string, request *http.Request) (*http.Response, error) {
				providerCalls++
				providerRequest = request
				if gotOperation != operation || request.Header.Get("Authorization") != "Bearer "+qualificationCanary {
					t.Fatalf("provider request operation/header = %q/%q", gotOperation, request.Header.Get("Authorization"))
				}
				if operation == PullGet {
					return qualificationResponse(http.StatusOK, "github-request-read", `{"number":17,"node_id":"PR_17","title":"Qualified","state":"open","html_url":"https://github.com/thinkpixel/tg/pull/17","updated_at":"2026-09-03T12:00:00Z"}`), nil
				}
				return qualificationResponse(http.StatusCreated, "github-request-write", `{"id":91,"html_url":"https://github.com/thinkpixel/tg/pull/17#issuecomment-91","created_at":"2026-09-03T12:00:00Z"}`), nil
			})

			var connector ports.ConnectorExecutor
			instance := githubInstance(t, `{"base_url":"https://api.github.com","owner":"thinkpixel"}`)
			if operation == PullGet {
				connector, _ = newPullReader(instance, client)
			} else {
				connector, _ = newCommentWriter(instance, client)
			}
			correlated := &qualificationConnector{t: t, next: connector}
			service := qualificationService(t, qualificationTool(t, operation, operation == PullComment), ledger,
				qualificationAuthorizer{outcome: ports.AuthorizationAllow}, broker, correlated)

			result, err := service.Create(t.Context(), qualificationRequest(operation))
			if err != nil || result.State != "post_tool" {
				t.Fatalf("qualification result = %#v, error = %v", result, err)
			}
			assertHarnessHasNoCredential(t, result, err)
			if providerCalls != 1 || broker.resolutions != 1 {
				t.Fatalf("provider calls/resolutions = %d/%d", providerCalls, broker.resolutions)
			}
			if providerRequest == nil || providerRequest.Header.Get("Authorization") != "" {
				t.Fatal("credential remained reachable after provider execution")
			}
			if ledger.authorizedInvocationID == "" || correlated.invocationID != ledger.authorizedInvocationID {
				t.Fatalf("connector invocation %q is not correlated to authorization %q", correlated.invocationID, ledger.authorizedInvocationID)
			}
		})
	}
}

func assertHarnessHasNoCredential(t *testing.T, result app.ToolCallResult, err error) {
	t.Helper()
	harnessView := fmt.Sprintf("%#v %v", result, err)
	if strings.Contains(harnessView, qualificationCanary) {
		t.Fatalf("credential reached harness view: %s", harnessView)
	}
}

func qualificationResponse(status int, requestID, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"X-Github-Request-Id": {requestID}}, Body: io.NopCloser(strings.NewReader(body))}
}

type qualificationResolver struct{ tool domain.ToolVersionDefinition }

func (resolver qualificationResolver) ResolveInvocationTool(context.Context, ports.InvocationIdentity, string, string) (ports.ResolvedToolVersion, error) {
	return ports.ResolvedToolVersion{Definition: resolver.tool}, nil
}

type qualificationLedger struct{ authorizedInvocationID string }

func (*qualificationLedger) Acquire(_ context.Context, _ ports.InvocationIdentity, invocation ports.LogicalInvocation) (ports.InvocationAcquisition, error) {
	return ports.InvocationAcquisition{Kind: ports.InvocationOwned, Invocation: invocation}, nil
}
func (ledger *qualificationLedger) RecordAuthorization(_ context.Context, _ ports.InvocationIdentity, invocationID string, _ ports.AuthorizationDecision, _, _ domain.Digest, _ time.Time) error {
	ledger.authorizedInvocationID = invocationID
	return nil
}

type qualificationAuthorizer struct{ outcome ports.AuthorizationOutcome }

func (authorizer qualificationAuthorizer) AuthorizeToolInvocation(_ context.Context, request ports.AuthorizationRequest) (ports.AuthorizationDecision, error) {
	now := time.Unix(100, 0)
	reasons := []ports.AuthorizationReason{ports.ReasonAllowed}
	if authorizer.outcome == ports.AuthorizationDeny {
		reasons = []ports.AuthorizationReason{ports.ReasonPolicyDenied}
	}
	return ports.AuthorizationDecision{DecisionID: "decision-014", RequestID: request.RequestID, ContextDigest: "context-014", PolicyID: "policy", PolicyVersion: request.PolicyVersion, Outcome: authorizer.outcome, Reasons: reasons, IssuedAt: now, NotBefore: now, ExpiresAt: now.Add(time.Minute), RevocationCheckpoint: "checkpoint", EvidenceRef: "authorization-evidence-014"}, nil
}

type qualificationBroker struct {
	t           *testing.T
	resolutions int
}

func (broker *qualificationBroker) Resolve(_ context.Context, _ ports.InvocationIdentity, _ domain.ToolVersionDefinition, _ ports.AuthorizationDecision) (ports.CredentialCapability, error) {
	broker.resolutions++
	capability, err := credentials.NewCapability(validMetadata(), []byte(qualificationCanary), fixedClock{now: time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		broker.t.Fatal(err)
	}
	return capability, err
}

type qualificationConnector struct {
	t            *testing.T
	next         ports.ConnectorExecutor
	invocationID string
}

func (connector *qualificationConnector) Execute(ctx context.Context, request ports.ConnectorRequest) (ports.ConnectorResult, error) {
	connector.invocationID = request.InvocationID
	if connector.next == nil {
		connector.t.Fatal("unauthorized call reached connector")
	}
	return connector.next.Execute(ctx, request)
}

func qualificationService(t *testing.T, tool domain.ToolVersionDefinition, ledger ports.InvocationLedger, authorizer ports.Authorizer, broker ports.CredentialBroker, connector ports.ConnectorExecutor) *app.ToolCallService {
	t.Helper()
	service, err := app.NewToolCallService(qualificationResolver{tool: tool}, ledger, authorizer, broker, connector, schema.NewValidator(schema.Limits{}), fixedClock{now: time.Unix(100, 0)}, func() (domain.UUID, error) {
		return domain.ParseUUID("01890f3e-7b6d-7cc0-98c4-dc0c0c014000")
	}, "qualification", "v1")
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func qualificationRequest(operation string) app.ToolCallRequest {
	arguments := json.RawMessage(`{"pull_number":17,"repository":"tg"}`)
	if operation == PullComment {
		arguments = json.RawMessage(`{"body":"CRED-014 isolated qualification","pull_number":17,"repository":"tg"}`)
	}
	return app.ToolCallRequest{ToolCallID: "credential-qualification-014", ToolID: "github." + operation, Version: "1.0.0", Arguments: arguments, Identity: ports.InvocationIdentity{TenantID: "tenant", Subject: "subject", AgentID: "agent", AgentVersion: "1", RunID: "run-014", WorkloadID: "workload"}}
}

func qualificationTool(t *testing.T, operation string, write bool) domain.ToolVersionDefinition {
	t.Helper()
	toolID, _ := domain.ParseToolID("github." + operation)
	version, _ := domain.ParseSemanticVersion("1.0.0")
	retry := domain.RetrySafe
	risk := domain.RiskRead
	properties := `"pull_number":{"type":"integer"},"repository":{"type":"string"}`
	required := `"pull_number","repository"`
	if write {
		retry, risk = domain.RetryNonRetryable, domain.RiskBoundedWrite
		properties = `"body":{"type":"string"},` + properties
		required = `"body",` + required
	}
	return domain.ToolVersionDefinition{ToolID: toolID, Version: version, Risk: risk, SideEffect: write, Retry: retry, Approval: domain.ApprovalPolicy, Description: domain.ReviewedDescription{Title: "GitHub qualification", Description: "Isolated connector qualification", ReviewRef: "CRED-014"}, InputSchema: []byte(`{"type":"object","additionalProperties":false,"required":[` + required + `],"properties":{` + properties + `}}`), OutputSchema: []byte(`{"type":"object"}`), CanonicalProfile: "jcs-v1", Connector: domain.ConnectorBinding{ConnectorType: ConnectorType, Operation: operation, InstanceSelector: "primary"}, CredentialSelector: "github-machine", RetryQualification: "CRED-014", ResourceProjection: domain.ResourceProjectionDefinition{Fields: []domain.ResourceProjectionField{{Name: "pull_number", Pointer: "/pull_number", Required: true, Type: domain.ProjectionNumber}, {Name: "repository", Pointer: "/repository", Required: true, Type: domain.ProjectionString}}}, Metering: domain.MeteringRule{Dimension: "calls", Units: "1", ChargePoint: domain.MeterAtResult, DeduplicationScope: domain.MeterPerLogicalInvocation}, Limits: domain.ToolLimits{RequestBytes: 4096, ResultBytes: 4096, Deadline: time.Second, Concurrency: 1, MaxAttempts: 1}}
}
