package github

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/credentials"
	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

type httpClientFunc func(context.Context, string, *http.Request) (*http.Response, error)

func (function httpClientFunc) Do(ctx context.Context, operation string, request *http.Request) (*http.Response, error) {
	return function(ctx, operation, request)
}

type credentialStub struct {
	metadata ports.CredentialCapabilityMetadata
	uses     int
}

func (credential *credentialStub) Metadata() ports.CredentialCapabilityMetadata {
	return credential.metadata
}
func (credential *credentialStub) UseSecret(use func([]byte) error) error {
	credential.uses++
	return use([]byte("synthetic-secret-canary"))
}
func (*credentialStub) Release() {}

func TestPullReaderUsesScopedCapabilityAndReturnsBoundedProjection(t *testing.T) {
	var sent *http.Request
	client := httpClientFunc(func(_ context.Context, operation string, request *http.Request) (*http.Response, error) {
		sent = request
		if operation != PullGet || request.Method != http.MethodGet || request.URL.String() != "https://api.github.com/repos/thinkpixel/tg/pulls/17" {
			t.Fatalf("request = %s %s (%s)", request.Method, request.URL, operation)
		}
		if request.Header.Get("Authorization") != "Bearer synthetic-secret-canary" || request.Header.Get("Accept") != "application/vnd.github+json" {
			t.Fatalf("headers = %#v", request.Header)
		}
		body := `{"number":17,"node_id":"PR_kwDOExample","title":"Secure connector","state":"open","html_url":"https://github.com/thinkpixel/tg/pull/17","updated_at":"2026-09-03T12:00:00Z","body":"provider content not projected"}`
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Github-Request-Id": {"request-123"}, "Etag": {`"version-7"`}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	connector := newReader(t, client)
	capability := newCapability(t)
	result, err := connector.Execute(context.Background(), validRequest(capability))
	if err != nil {
		t.Fatal(err)
	}
	if result.Classification != "confirmed_success" || string(result.Result) != `{"repository":"tg","number":17,"title":"Secure connector","state":"open","url":"https://github.com/thinkpixel/tg/pull/17","updated_at":"2026-09-03T12:00:00Z"}` {
		t.Fatalf("result = %#v", result)
	}
	if result.Evidence.ProviderRequestID != "request-123" || result.Evidence.ProviderResultID != "PR_kwDOExample" || result.Evidence.ResourceVersion != `"version-7"` || string(result.Evidence.SafeMetadata) != `{"status_code":200}` {
		t.Fatalf("evidence = %#v", result.Evidence)
	}
	if sent.Header.Get("Authorization") != "" {
		t.Fatal("authorization header remained reachable after execution")
	}
}

func TestPullReaderOmitsUnsafeProviderEvidence(t *testing.T) {
	connector := newReader(t, httpClientFunc(func(context.Context, string, *http.Request) (*http.Response, error) {
		body := `{"number":17,"node_id":"provider\u000aid","title":"Secure connector","state":"open","html_url":"https://github.com/thinkpixel/tg/pull/17","updated_at":"2026-09-03T12:00:00Z"}`
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Github-Request-Id": {strings.Repeat("x", maximumProviderEvidenceValueBytes+1)}, "Etag": {"unsafe\nvalue"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	}))
	result, err := connector.Execute(context.Background(), validRequest(&credentialStub{metadata: validMetadata()}))
	if err != nil || result.Evidence.ProviderRequestID != "" || result.Evidence.ProviderResultID != "" || result.Evidence.ResourceVersion != "" {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
}

func TestPullReaderValidatesProjectionBeforeCredentialOrNetwork(t *testing.T) {
	called := false
	connector := newReader(t, httpClientFunc(func(context.Context, string, *http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("unexpected")
	}))
	credential := &credentialStub{metadata: validMetadata()}
	for name, mutate := range map[string]func(*ports.ConnectorRequest){
		"projection mismatch": func(request *ports.ConnectorRequest) {
			request.ResourceProjection = json.RawMessage(`{"pull_number":18,"repository":"tg"}`)
		},
		"noncanonical arguments": func(request *ports.ConnectorRequest) {
			request.CanonicalArguments = json.RawMessage(`{ "pull_number":17,"repository":"tg"}`)
		},
		"extra projection authority": func(request *ports.ConnectorRequest) {
			request.ResourceProjection = json.RawMessage(`{"owner":"attacker","pull_number":17,"repository":"tg"}`)
		},
		"path injection": func(request *ports.ConnectorRequest) {
			request.CanonicalArguments, request.ResourceProjection = json.RawMessage(`{"pull_number":17,"repository":"../admin"}`), json.RawMessage(`{"pull_number":17,"repository":"../admin"}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			request := validRequest(credential)
			mutate(&request)
			if _, err := connector.Execute(context.Background(), request); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	if credential.uses != 0 || called {
		t.Fatalf("credential uses = %d, network called = %t", credential.uses, called)
	}
}

func TestPullReaderRejectsCredentialOutsideAudienceOrResource(t *testing.T) {
	connector := newReader(t, httpClientFunc(func(context.Context, string, *http.Request) (*http.Response, error) {
		t.Fatal("invalid credential reached network")
		return nil, nil
	}))
	for name, mutate := range map[string]func(*ports.CredentialCapabilityMetadata){
		"kind":     func(metadata *ports.CredentialCapabilityMetadata) { metadata.Kind = domain.CapabilityMTLS },
		"audience": func(metadata *ports.CredentialCapabilityMetadata) { metadata.Audiences = []string{"attacker.example"} },
		"resource": func(metadata *ports.CredentialCapabilityMetadata) {
			metadata.Resources = []string{"repository:thinkpixel/other"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			metadata := validMetadata()
			mutate(&metadata)
			credential := &credentialStub{metadata: metadata}
			if _, err := connector.Execute(context.Background(), validRequest(credential)); !errors.Is(err, ErrCredential) || credential.uses != 0 {
				t.Fatalf("error = %v, uses = %d", err, credential.uses)
			}
		})
	}
}

func TestPullReaderMapsReadStatusesWithoutReturningProviderBodies(t *testing.T) {
	for status, want := range map[int]string{http.StatusNotFound: "definitely_rejected", http.StatusTooManyRequests: "transient_safe", http.StatusBadGateway: "transient_safe"} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			connector := newReader(t, httpClientFunc(func(context.Context, string, *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"token":"synthetic-secret-canary"}`))}, nil
			}))
			result, err := connector.Execute(context.Background(), validRequest(&credentialStub{metadata: validMetadata()}))
			if err != nil || result.Classification != want || len(result.Result) != 0 {
				t.Fatalf("result = %#v, error = %v", result, err)
			}
		})
	}
}

func TestPullReaderRejectsMalformedInstanceAndProviderResponse(t *testing.T) {
	badInstance := githubInstance(t, `{"base_url":"http://api.github.com","owner":"thinkpixel"}`)
	if _, err := newPullReader(badInstance, httpClientFunc(nil)); err == nil {
		t.Fatal("unsafe instance accepted")
	}
	connector := newReader(t, httpClientFunc(func(context.Context, string, *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"number":18,"title":"wrong resource","state":"open","html_url":"https://example","updated_at":"now"}`))}, nil
	}))
	if _, err := connector.Execute(context.Background(), validRequest(&credentialStub{metadata: validMetadata()})); !errors.Is(err, ErrResponse) {
		t.Fatalf("error = %v", err)
	}
}

func newReader(t *testing.T, client httpClient) *PullReader {
	t.Helper()
	connector, err := newPullReader(githubInstance(t, `{"base_url":"https://api.github.com","owner":"thinkpixel"}`), client)
	if err != nil {
		t.Fatal(err)
	}
	return connector
}

func githubInstance(t *testing.T, rawDestination string) domain.ConnectorInstance {
	t.Helper()
	destination := json.RawMessage(rawDestination)
	id, _ := domain.ParseUUID("019b0000-0000-7000-8000-000000000001")
	tenantID, _ := domain.ParseUUID("019b0000-0000-7000-8000-000000000002")
	instance, err := domain.NewConnectorInstance(domain.ConnectorInstanceDefinition{ID: id, TenantID: tenantID, Selector: "primary", ConnectorType: ConnectorType, DestinationConfig: destination, ConfigDigest: domain.DigestBytes(destination), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return instance
}

func validRequest(capability ports.CredentialCapability) ports.ConnectorRequest {
	toolID, _ := domain.ParseToolID("github.pull.get")
	version, _ := domain.ParseSemanticVersion("1.0.0")
	return ports.ConnectorRequest{InvocationID: "invocation-1", Tool: domain.ToolVersionDefinition{ToolID: toolID, Version: version, Connector: domain.ConnectorBinding{ConnectorType: ConnectorType, Operation: PullGet, InstanceSelector: "primary"}}, CanonicalArguments: json.RawMessage(`{"pull_number":17,"repository":"tg"}`), ResourceProjection: json.RawMessage(`{"pull_number":17,"repository":"tg"}`), Credential: capability}
}

func validMetadata() ports.CredentialCapabilityMetadata {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	return ports.CredentialCapabilityMetadata{Kind: domain.CapabilityOAuthAccessToken, ProviderRef: "provider:github", Audiences: []string{"api.github.com"}, Resources: []string{"repository:thinkpixel/tg"}, IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)}
}

func newCapability(t *testing.T) ports.CredentialCapability {
	t.Helper()
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	capability, err := credentials.NewCapability(validMetadata(), []byte("synthetic-secret-canary"), fixedClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(capability.Release)
	return capability
}

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }
