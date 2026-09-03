package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/bdobrica/ThinkPixelTG/internal/ports"
	"github.com/bdobrica/ThinkPixelTG/internal/schema"
)

func TestToolCallServiceRunsGovernedSequenceAndCanonicalConnectorInput(t *testing.T) {
	events := []string{}
	tool := invocationTestTool(t)
	ledger := &invocationLedgerFake{events: &events}
	authorizer := authorizerFunc(func(_ context.Context, request ports.AuthorizationRequest) (ports.AuthorizationDecision, error) {
		events = append(events, "authorize")
		if request.Resources[0] != `repository:"thinkpixel/tg"` || request.ToolVersion != "1.0.0" {
			t.Fatalf("unexpected authorization request: %#v", request)
		}
		return allowedDecision(request, time.Unix(100, 0)), nil
	})
	lease := &credentialFake{events: &events}
	credentials := credentialBrokerFake(func(context.Context, ports.InvocationIdentity, domain.ToolVersionDefinition, ports.AuthorizationDecision) (ports.CredentialCapability, error) {
		events = append(events, "credential")
		return lease, nil
	})
	connector := connectorFake(func(_ context.Context, request ports.ConnectorRequest) (ports.ConnectorResult, error) {
		events = append(events, "connector")
		if string(request.CanonicalArguments) != `{"count":1,"repository":"thinkpixel/tg"}` {
			t.Fatalf("arguments = %s", request.CanonicalArguments)
		}
		return ports.ConnectorResult{Classification: "confirmed_success", Result: json.RawMessage(`{"id":"comment-1"}`)}, nil
	})
	service := newInvocationTestService(t, tool, ledger, authorizer, credentials, connector)
	result, err := service.Create(t.Context(), invocationTestRequest())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if result.State != "post_tool" || lease.releases != 1 {
		t.Fatalf("result/release = %#v/%d", result, lease.releases)
	}
	want := []string{"resolve", "acquire", "authorize", "record", "credential", "connector", "release"}
	if len(events) != len(want) {
		t.Fatalf("events = %v", events)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events = %v", events)
		}
	}
}

func TestReplayConflictAndMalformedArgumentsNeverReachAuthorityOrConnector(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*ToolCallRequest)
		kind   ports.InvocationAcquisitionKind
	}{
		{name: "invalid schema", mutate: func(r *ToolCallRequest) { r.Arguments = json.RawMessage(`{"repository":"x"}`) }, kind: ports.InvocationOwned},
		{name: "replay conflict", mutate: func(*ToolCallRequest) {}, kind: ports.InvocationConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			events := []string{}
			ledger := &invocationLedgerFake{events: &events, kind: test.kind}
			service := newInvocationTestService(t, invocationTestTool(t), ledger, authorizerFunc(func(context.Context, ports.AuthorizationRequest) (ports.AuthorizationDecision, error) {
				t.Fatal("authorization reached")
				return ports.AuthorizationDecision{}, nil
			}), credentialBrokerFake(func(context.Context, ports.InvocationIdentity, domain.ToolVersionDefinition, ports.AuthorizationDecision) (ports.CredentialCapability, error) {
				t.Fatal("credential reached")
				return nil, nil
			}), connectorFake(func(context.Context, ports.ConnectorRequest) (ports.ConnectorResult, error) {
				t.Fatal("connector reached")
				return ports.ConnectorResult{}, nil
			}))
			request := invocationTestRequest()
			test.mutate(&request)
			if _, err := service.Create(t.Context(), request); err == nil {
				t.Fatal("Create() error = nil")
			}
		})
	}
}

func TestToolCallServicePersistsDenialBeforeStopping(t *testing.T) {
	events := []string{}
	ledger := &invocationLedgerFake{events: &events}
	authorizer := authorizerFunc(func(_ context.Context, request ports.AuthorizationRequest) (ports.AuthorizationDecision, error) {
		events = append(events, "authorize")
		d := allowedDecision(request, time.Unix(100, 0))
		d.Outcome = ports.AuthorizationDeny
		d.Reasons = []ports.AuthorizationReason{ports.ReasonPolicyDenied}
		return d, nil
	})
	service := newInvocationTestService(t, invocationTestTool(t), ledger, authorizer, credentialBrokerFake(func(context.Context, ports.InvocationIdentity, domain.ToolVersionDefinition, ports.AuthorizationDecision) (ports.CredentialCapability, error) {
		t.Fatal("credential reached")
		return nil, nil
	}), connectorFake(func(context.Context, ports.ConnectorRequest) (ports.ConnectorResult, error) {
		t.Fatal("connector reached")
		return ports.ConnectorResult{}, nil
	}))
	if _, err := service.Create(t.Context(), invocationTestRequest()); err == nil {
		t.Fatal("Create() error = nil")
	}
	if events[len(events)-1] != "record" {
		t.Fatalf("events = %v", events)
	}
}

func TestReplayReturnsExistingInvocationWithoutRepeatingGovernedWork(t *testing.T) {
	events := []string{}
	ledger := &invocationLedgerFake{events: &events, kind: ports.InvocationExisting}
	service := newInvocationTestService(t, invocationTestTool(t), ledger,
		authorizerFunc(func(context.Context, ports.AuthorizationRequest) (ports.AuthorizationDecision, error) {
			t.Fatal("authorization reached on replay")
			return ports.AuthorizationDecision{}, nil
		}),
		credentialBrokerFake(func(context.Context, ports.InvocationIdentity, domain.ToolVersionDefinition, ports.AuthorizationDecision) (ports.CredentialCapability, error) {
			t.Fatal("credential broker reached on replay")
			return nil, nil
		}),
		connectorFake(func(context.Context, ports.ConnectorRequest) (ports.ConnectorResult, error) {
			t.Fatal("connector reached on replay")
			return ports.ConnectorResult{}, nil
		}),
	)

	result, err := service.Create(t.Context(), invocationTestRequest())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !result.Existing || result.Invocation.ToolCallID != invocationTestRequest().ToolCallID {
		t.Fatalf("replay result = %#v", result)
	}
	if want := []string{"resolve", "acquire"}; len(events) != len(want) || events[0] != want[0] || events[1] != want[1] {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestTimeoutAndCancellationReachConnectorAndReleaseCredential(t *testing.T) {
	for _, test := range []struct {
		name   string
		parent func() (context.Context, context.CancelFunc)
		limit  time.Duration
		want   error
	}{
		{
			name: "tool deadline",
			parent: func() (context.Context, context.CancelFunc) {
				return context.WithCancel(context.Background())
			},
			limit: 5 * time.Millisecond,
			want:  context.DeadlineExceeded,
		},
		{
			name: "caller cancellation",
			parent: func() (context.Context, context.CancelFunc) {
				return context.WithCancel(context.Background())
			},
			limit: time.Second,
			want:  context.Canceled,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			events := []string{}
			tool := invocationTestTool(t)
			tool.Limits.Deadline = test.limit
			ledger := &invocationLedgerFake{events: &events}
			lease := &credentialFake{events: &events}
			entered := make(chan struct{})
			service := newInvocationTestService(t, tool, ledger,
				authorizerFunc(func(_ context.Context, request ports.AuthorizationRequest) (ports.AuthorizationDecision, error) {
					events = append(events, "authorize")
					return allowedDecision(request, time.Unix(100, 0)), nil
				}),
				credentialBrokerFake(func(context.Context, ports.InvocationIdentity, domain.ToolVersionDefinition, ports.AuthorizationDecision) (ports.CredentialCapability, error) {
					events = append(events, "credential")
					return lease, nil
				}),
				connectorFake(func(ctx context.Context, _ ports.ConnectorRequest) (ports.ConnectorResult, error) {
					close(entered)
					<-ctx.Done()
					return ports.ConnectorResult{}, ctx.Err()
				}),
			)
			ctx, cancel := test.parent()
			defer cancel()
			if errors.Is(test.want, context.Canceled) {
				go func() {
					<-entered
					cancel()
				}()
			}
			_, err := service.Create(ctx, invocationTestRequest())
			if err == nil || !errors.Is(err, test.want) {
				t.Fatalf("Create() error = %v, want wrapping %v", err, test.want)
			}
			if lease.releases != 1 {
				t.Fatalf("credential releases = %d", lease.releases)
			}
		})
	}
}

func TestConnectorEvidenceIsBoundedAndStructured(t *testing.T) {
	for name, evidence := range map[string]ports.ConnectorEvidence{
		"oversized identifier": {ProviderRequestID: strings.Repeat("x", 257)},
		"control character":    {ResourceVersion: "version\nsecret"},
		"array metadata":       {SafeMetadata: json.RawMessage(`[]`)},
		"oversized metadata":   {SafeMetadata: json.RawMessage(`{"value":"` + strings.Repeat("x", 4090) + `"}`)},
	} {
		t.Run(name, func(t *testing.T) {
			if validConnectorEvidence(evidence) {
				t.Fatalf("accepted invalid evidence: %#v", evidence)
			}
		})
	}
	valid := ports.ConnectorEvidence{ProviderRequestID: "request-1", ProviderResultID: "result-1", ResourceVersion: `W/"version-1"`, SafeMetadata: json.RawMessage(`{"status_code":201}`)}
	if !validConnectorEvidence(valid) {
		t.Fatalf("rejected valid evidence: %#v", valid)
	}
}

type toolResolverFake struct {
	tool   domain.ToolVersionDefinition
	events *[]string
}

func (f toolResolverFake) ResolveInvocationTool(context.Context, ports.InvocationIdentity, string, string) (ports.ResolvedToolVersion, error) {
	*f.events = append(*f.events, "resolve")
	return ports.ResolvedToolVersion{Definition: f.tool}, nil
}

type invocationLedgerFake struct {
	events *[]string
	kind   ports.InvocationAcquisitionKind
}

func (f *invocationLedgerFake) Acquire(_ context.Context, _ ports.InvocationIdentity, invocation ports.LogicalInvocation) (ports.InvocationAcquisition, error) {
	*f.events = append(*f.events, "acquire")
	kind := f.kind
	if kind == "" {
		kind = ports.InvocationOwned
	}
	return ports.InvocationAcquisition{Kind: kind, Invocation: invocation}, nil
}
func (f *invocationLedgerFake) RecordAuthorization(context.Context, ports.InvocationIdentity, string, ports.AuthorizationDecision, domain.Digest, domain.Digest, time.Time) error {
	*f.events = append(*f.events, "record")
	return nil
}

type credentialBrokerFake func(context.Context, ports.InvocationIdentity, domain.ToolVersionDefinition, ports.AuthorizationDecision) (ports.CredentialCapability, error)

func (f credentialBrokerFake) Resolve(c context.Context, i ports.InvocationIdentity, t domain.ToolVersionDefinition, d ports.AuthorizationDecision) (ports.CredentialCapability, error) {
	return f(c, i, t, d)
}

type connectorFake func(context.Context, ports.ConnectorRequest) (ports.ConnectorResult, error)

func (f connectorFake) Execute(c context.Context, r ports.ConnectorRequest) (ports.ConnectorResult, error) {
	return f(c, r)
}

type credentialFake struct {
	events   *[]string
	releases int
}

func (*credentialFake) Metadata() ports.CredentialCapabilityMetadata {
	return ports.CredentialCapabilityMetadata{}
}
func (*credentialFake) UseSecret(use func([]byte) error) error { return use(nil) }
func (f *credentialFake) Release()                             { f.releases++; *f.events = append(*f.events, "release") }

type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }

func newInvocationTestService(t *testing.T, tool domain.ToolVersionDefinition, ledger ports.InvocationLedger, authorizer ports.Authorizer, credentials ports.CredentialBroker, connector ports.ConnectorExecutor) *ToolCallService {
	t.Helper()
	events := ledger.(*invocationLedgerFake).events
	service, err := NewToolCallService(toolResolverFake{tool, events}, ledger, authorizer, credentials, connector, schema.NewValidator(schema.Limits{}), fixedClock{time.Unix(100, 0)}, func() (domain.UUID, error) { return domain.ParseUUID("01890f3e-7b6d-7cc0-98c4-dc0c0c07398f") }, "default", "v1")
	if err != nil {
		t.Fatal(err)
	}
	return service
}
func invocationTestRequest() ToolCallRequest {
	return ToolCallRequest{ToolCallID: "01890f3e-7b6d-7cc0-98c4-dc0c0c073990", ToolID: "github.pull.comment", Version: "1.0.0", Arguments: json.RawMessage(`{"repository":"thinkpixel/tg","count":1}`), Identity: ports.InvocationIdentity{TenantID: "tenant", Subject: "subject", AgentID: "agent", AgentVersion: "1", RunID: "run", WorkloadID: "workload"}}
}
func invocationTestTool(t *testing.T) domain.ToolVersionDefinition {
	t.Helper()
	id, _ := domain.ParseToolID("github.pull.comment")
	version, _ := domain.ParseSemanticVersion("1.0.0")
	return domain.ToolVersionDefinition{ToolID: id, Version: version, Risk: domain.RiskBoundedWrite, SideEffect: true, Retry: domain.RetryDownstreamIdempotency, Approval: domain.ApprovalPolicy, Description: domain.ReviewedDescription{Title: "Comment", Description: "Add comment", ReviewRef: "review-1"}, InputSchema: []byte(`{"type":"object","additionalProperties":false,"required":["repository","count"],"properties":{"repository":{"type":"string"},"count":{"type":"integer"}}}`), OutputSchema: []byte(`{"type":"object"}`), CanonicalProfile: "jcs-v1", Connector: domain.ConnectorBinding{ConnectorType: "github", Operation: "pull.comment", InstanceSelector: "primary"}, CredentialSelector: "writer", RetryQualification: "qual-1", ResourceProjection: domain.ResourceProjectionDefinition{Fields: []domain.ResourceProjectionField{{Name: "repository", Pointer: "/repository", Required: true, Type: domain.ProjectionString}}}, Metering: domain.MeteringRule{Dimension: "calls", Units: "1", ChargePoint: domain.MeterAtResult, DeduplicationScope: domain.MeterPerLogicalInvocation}, Limits: domain.ToolLimits{RequestBytes: 4096, ResultBytes: 4096, Deadline: time.Second, Concurrency: 1, MaxAttempts: 1}}
}
func allowedDecision(r ports.AuthorizationRequest, now time.Time) ports.AuthorizationDecision {
	return ports.AuthorizationDecision{DecisionID: "decision", RequestID: r.RequestID, ContextDigest: "context", PolicyID: "policy", PolicyVersion: r.PolicyVersion, Outcome: ports.AuthorizationAllow, Reasons: []ports.AuthorizationReason{ports.ReasonAllowed}, IssuedAt: now, NotBefore: now, ExpiresAt: now.Add(time.Minute), RevocationCheckpoint: "checkpoint", EvidenceRef: "evidence"}
}
