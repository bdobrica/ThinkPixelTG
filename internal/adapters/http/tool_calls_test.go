package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/adapters/http/openapi"
	"github.com/bdobrica/ThinkPixelTG/internal/app"
	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

func TestCreateToolCallStrictlyProjectsAuthenticatedRequest(t *testing.T) {
	creator := toolCallCreatorFunc(func(_ context.Context, request app.ToolCallRequest) (app.ToolCallResult, error) {
		if request.Identity.TenantID != "trusted-tenant" || request.ToolCallID != "01890f3e-7b6d-7cc0-98c4-dc0c0c073990" || string(request.Arguments) != `{"b":2,"a":1}` {
			t.Fatalf("request = %#v", request)
		}
		now := time.Unix(100, 0).UTC()
		return app.ToolCallResult{Invocation: ports.LogicalInvocation{ToolCallID: request.ToolCallID, ToolID: request.ToolID, ToolVersion: "1.0.0", CreatedAt: now, UpdatedAt: now}, State: "post_tool"}, nil
	})
	handler, err := NewToolCallHandler(ToolCallOptions{Service: creator, Identity: func(context.Context) (ports.InvocationIdentity, error) {
		return ports.InvocationIdentity{TenantID: "trusted-tenant"}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/tool-calls", jsonReader(`{"tool_id":"github.pull.comment","arguments":{"b":2,"a":1}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "01890f3e-7b6d-7cc0-98c4-dc0c0c073990")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted || res.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response = %d %#v %s", res.Code, res.Header(), res.Body.String())
	}
}

func TestGetToolCallReturnsOnlySafeProjection(t *testing.T) {
	reader := toolCallQueryFunc(func(_ context.Context, identity ports.InvocationIdentity, id string) (ports.ToolCallView, error) {
		if identity.TenantID != "trusted-tenant" || identity.RunID != "trusted-run" || id != "call-0001" {
			t.Fatalf("lookup = %#v, %q", identity, id)
		}
		now := time.Unix(100, 0).UTC()
		return ports.ToolCallView{ToolCallID: id, ToolID: "github.pull.comment", ToolVersion: "1.0.0", State: "succeeded", Result: json.RawMessage(`{"comment_id":"safe"}`), CreatedAt: now, UpdatedAt: now}, nil
	})
	handler, err := NewToolCallHandler(ToolCallOptions{Service: toolCallCreatorFunc(func(context.Context, app.ToolCallRequest) (app.ToolCallResult, error) {
		return app.ToolCallResult{}, nil
	}), Reader: reader, Identity: func(context.Context) (ports.InvocationIdentity, error) {
		return ports.InvocationIdentity{TenantID: "trusted-tenant", RunID: "trusted-run"}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/tool-calls/call-0001", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response = %d %#v %s", response.Code, response.Header(), response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, leaked := body["invocation_id"]; leaked || len(body) != 7 {
		t.Fatalf("body = %#v", body)
	}
}

func TestGetToolCallIsEnumerationSafe(t *testing.T) {
	reader := toolCallQueryFunc(func(context.Context, ports.InvocationIdentity, string) (ports.ToolCallView, error) {
		return ports.ToolCallView{}, domain.NewError(domain.CodeNotFound, "scope mismatch details", nil)
	})
	handler, _ := NewToolCallHandler(ToolCallOptions{Service: toolCallCreatorFunc(func(context.Context, app.ToolCallRequest) (app.ToolCallResult, error) {
		return app.ToolCallResult{}, nil
	}), Reader: reader, Identity: func(context.Context) (ports.InvocationIdentity, error) {
		return ports.InvocationIdentity{TenantID: "tenant", RunID: "run"}, nil
	}})
	for _, target := range []string{"/v1/tool-calls/missing-0001", "/v1/tool-calls/missing-0001?run_id=other"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "scope mismatch") || !strings.Contains(response.Body.String(), `"code":"tool_call_not_found"`) {
			t.Fatalf("response = %d %s", response.Code, response.Body.String())
		}
	}
}

func TestCreateToolCallRejectsUntrustedShapeBeforeApplication(t *testing.T) {
	creator := toolCallCreatorFunc(func(context.Context, app.ToolCallRequest) (app.ToolCallResult, error) {
		t.Fatal("application reached")
		return app.ToolCallResult{}, nil
	})
	handler, _ := NewToolCallHandler(ToolCallOptions{Service: creator, Identity: func(context.Context) (ports.InvocationIdentity, error) {
		return ports.InvocationIdentity{TenantID: "tenant"}, nil
	}})
	for _, test := range []struct{ name, contentType, key, body string }{
		{"content type", "text/plain", "01890f3e-7b6d-7cc0-98c4-dc0c0c073990", `{}`},
		{"missing key", "application/json", "", `{"tool_id":"a.b","arguments":{}}`},
		{"governance field", "application/json", "01890f3e-7b6d-7cc0-98c4-dc0c0c073990", `{"tool_id":"a.b","arguments":{},"tenant_id":"forged"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/tool-calls", jsonReader(test.body))
			req.Header.Set("Content-Type", test.contentType)
			if test.key != "" {
				req.Header.Set("Idempotency-Key", test.key)
			}
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code < 400 {
				t.Fatalf("status=%d", res.Code)
			}
		})
	}
}

func TestMalformedArgumentsConformToPublicProblemContract(t *testing.T) {
	creator := toolCallCreatorFunc(func(context.Context, app.ToolCallRequest) (app.ToolCallResult, error) {
		t.Fatal("application reached")
		return app.ToolCallResult{}, nil
	})
	handler, _ := NewToolCallHandler(ToolCallOptions{Service: creator, Identity: func(context.Context) (ports.InvocationIdentity, error) {
		return ports.InvocationIdentity{TenantID: "tenant"}, nil
	}})
	for _, body := range []string{
		``,
		`null`,
		`{"tool_id":"github.pull.comment"}`,
		`{"tool_id":"github.pull.comment","arguments":{"count":01}}`,
		`{"tool_id":"github.pull.comment","arguments":{},"arguments":{}}`,
		`{"tool_id":"github.pull.comment","arguments":{}} trailing`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/v1/tool-calls", jsonReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "call-0001")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		var problem openapi.Problem
		if response.Code != http.StatusBadRequest || response.Header().Get("Content-Type") != "application/problem+json" || json.Unmarshal(response.Body.Bytes(), &problem) != nil || problem.Code != "invalid_arguments" {
			t.Fatalf("body %q response = %d %#v %s", body, response.Code, response.Header(), response.Body.String())
		}
	}
}

func TestOpenAPIConformanceToolCallSuccessAndReplayResponses(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name     string
		existing bool
		status   int
	}{
		{name: "accepted", status: http.StatusAccepted},
		{name: "matching replay", existing: true, status: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler, _ := NewToolCallHandler(ToolCallOptions{Service: toolCallCreatorFunc(func(context.Context, app.ToolCallRequest) (app.ToolCallResult, error) {
				return app.ToolCallResult{Existing: test.existing, State: "post_tool", Invocation: ports.LogicalInvocation{ToolCallID: "call-0001", ToolID: "github.pull.comment", ToolVersion: "1.0.0", CreatedAt: now, UpdatedAt: now}}, nil
			}), Identity: func(context.Context) (ports.InvocationIdentity, error) {
				return ports.InvocationIdentity{TenantID: "tenant"}, nil
			}})
			request := httptest.NewRequest(http.MethodPost, "/v1/tool-calls", jsonReader(`{"tool_id":"github.pull.comment","arguments":{}}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", "call-0001")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			var document openapi.ToolCall
			if response.Code != test.status || response.Header().Get("Content-Type") != "application/json" || json.Unmarshal(response.Body.Bytes(), &document) != nil {
				t.Fatalf("response = %d %#v %s", response.Code, response.Header(), response.Body.String())
			}
			if document.ToolCallId != "call-0001" || document.ToolId != "github.pull.comment" || document.Version != "1.0.0" || document.State != "post_tool" {
				t.Fatalf("OpenAPI tool call = %#v", document)
			}
		})
	}
}

type toolCallCreatorFunc func(context.Context, app.ToolCallRequest) (app.ToolCallResult, error)

type toolCallQueryFunc func(context.Context, ports.InvocationIdentity, string) (ports.ToolCallView, error)

func (function toolCallQueryFunc) Get(ctx context.Context, identity ports.InvocationIdentity, id string) (ports.ToolCallView, error) {
	return function(ctx, identity, id)
}

func (f toolCallCreatorFunc) Create(ctx context.Context, r app.ToolCallRequest) (app.ToolCallResult, error) {
	return f(ctx, r)
}
func jsonReader(value string) *strings.Reader { return strings.NewReader(value) }
