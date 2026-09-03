package github

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

func TestCommentWriterSendsBoundedWriteAndReturnsSafeIdentifiers(t *testing.T) {
	var sent *http.Request
	connector := newWriter(t, httpClientFunc(func(_ context.Context, operation string, request *http.Request) (*http.Response, error) {
		sent = request
		payload, _ := io.ReadAll(request.Body)
		if operation != PullComment || request.Method != http.MethodPost || request.URL.String() != "https://api.github.com/repos/thinkpixel/tg/issues/17/comments" || string(payload) != `{"body":"Looks good."}` {
			t.Fatalf("request = %s %s (%s), body = %s", request.Method, request.URL, operation, payload)
		}
		if request.Header.Get("Authorization") != "Bearer synthetic-secret-canary" || request.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("headers = %#v", request.Header)
		}
		body := `{"id":91,"html_url":"https://github.com/thinkpixel/tg/pull/17#issuecomment-91","created_at":"2026-09-03T12:00:00Z","body":"Looks good.","user":{"login":"sensitive"}}`
		return &http.Response{StatusCode: http.StatusCreated, Header: http.Header{"X-Github-Request-Id": {"request-456"}, "Etag": {`W/"comment-91"`}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	}))
	result, err := connector.Execute(context.Background(), validCommentRequest(newCapability(t)))
	if err != nil {
		t.Fatal(err)
	}
	if result.Classification != "confirmed_success" || string(result.Result) != `{"repository":"tg","pull_number":17,"comment_id":91,"url":"https://github.com/thinkpixel/tg/pull/17#issuecomment-91","created_at":"2026-09-03T12:00:00Z"}` {
		t.Fatalf("result = %#v", result)
	}
	if result.Evidence.ProviderRequestID != "request-456" || result.Evidence.ProviderResultID != "91" || result.Evidence.ResourceVersion != `W/"comment-91"` || string(result.Evidence.SafeMetadata) != `{"status_code":201}` {
		t.Fatalf("evidence = %#v", result.Evidence)
	}
	if sent.Header.Get("Authorization") != "" {
		t.Fatal("authorization header remained reachable after execution")
	}
}

func TestCommentWriterValidatesAuthorityBeforeCredentialOrNetwork(t *testing.T) {
	called := false
	connector := newWriter(t, httpClientFunc(func(context.Context, string, *http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("unexpected")
	}))
	credential := &credentialStub{metadata: validMetadata()}
	for name, mutate := range map[string]func(*ports.ConnectorRequest){
		"projection mismatch": func(request *ports.ConnectorRequest) {
			request.ResourceProjection = json.RawMessage(`{"pull_number":18,"repository":"tg"}`)
		},
		"body in projection": func(request *ports.ConnectorRequest) {
			request.ResourceProjection = json.RawMessage(`{"body":"expanded authority","pull_number":17,"repository":"tg"}`)
		},
		"noncanonical": func(request *ports.ConnectorRequest) {
			request.CanonicalArguments = json.RawMessage(`{ "body":"Looks good.","pull_number":17,"repository":"tg"}`)
		},
		"extra argument": func(request *ports.ConnectorRequest) {
			request.CanonicalArguments = json.RawMessage(`{"body":"Looks good.","owner":"attacker","pull_number":17,"repository":"tg"}`)
		},
		"wrong retry":       func(request *ports.ConnectorRequest) { request.Tool.Retry = domain.RetryDownstreamIdempotency },
		"not a side effect": func(request *ports.ConnectorRequest) { request.Tool.SideEffect = false },
	} {
		t.Run(name, func(t *testing.T) {
			request := validCommentRequest(credential)
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

func TestCommentWriterNeverBlindlyRetriesAmbiguousOutcomes(t *testing.T) {
	for name, test := range map[string]struct {
		client httpClient
		want   string
	}{
		"transport": {httpClientFunc(func(context.Context, string, *http.Request) (*http.Response, error) {
			return nil, errors.New("reset after send")
		}), "unknown"},
		"server error": {httpClientFunc(func(context.Context, string, *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}), "unknown"},
		"rate limited": {httpClientFunc(func(context.Context, string, *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusTooManyRequests, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}), "definitely_rejected"},
		"undocumented client status": {httpClientFunc(func(context.Context, string, *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusTeapot, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}), "unknown"},
		"validation": {httpClientFunc(func(context.Context, string, *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusUnprocessableEntity, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		}), "definitely_rejected"},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := newWriter(t, test.client).Execute(context.Background(), validCommentRequest(&credentialStub{metadata: validMetadata()}))
			if err != nil || result.Classification != test.want || len(result.Result) != 0 {
				t.Fatalf("result = %#v, error = %v", result, err)
			}
		})
	}
}

func TestCommentWriterCancellationBeforeSendDoesNotResolveCredential(t *testing.T) {
	connector := newWriter(t, httpClientFunc(func(context.Context, string, *http.Request) (*http.Response, error) {
		t.Fatal("cancelled request reached network")
		return nil, nil
	}))
	credential := &credentialStub{metadata: validMetadata()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := connector.Execute(ctx, validCommentRequest(credential))
	if err != nil || result.Classification != "cancelled_pre_send" || credential.uses != 0 {
		t.Fatalf("result = %#v, error = %v, credential uses = %d", result, err, credential.uses)
	}
}

func newWriter(t *testing.T, client httpClient) *CommentWriter {
	t.Helper()
	connector, err := newCommentWriter(githubInstance(t, `{"base_url":"https://api.github.com","owner":"thinkpixel"}`), client)
	if err != nil {
		t.Fatal(err)
	}
	return connector
}

func validCommentRequest(capability ports.CredentialCapability) ports.ConnectorRequest {
	toolID, _ := domain.ParseToolID("github.pull.comment")
	version, _ := domain.ParseSemanticVersion("1.0.0")
	return ports.ConnectorRequest{InvocationID: "invocation-1", Tool: domain.ToolVersionDefinition{ToolID: toolID, Version: version, SideEffect: true, Retry: domain.RetryNonRetryable, Connector: domain.ConnectorBinding{ConnectorType: ConnectorType, Operation: PullComment, InstanceSelector: "primary"}}, CanonicalArguments: json.RawMessage(`{"body":"Looks good.","pull_number":17,"repository":"tg"}`), ResourceProjection: json.RawMessage(`{"pull_number":17,"repository":"tg"}`), Credential: capability}
}
