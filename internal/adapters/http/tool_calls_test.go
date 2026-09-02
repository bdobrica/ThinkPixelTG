package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/app"
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

type toolCallCreatorFunc func(context.Context, app.ToolCallRequest) (app.ToolCallResult, error)

func (f toolCallCreatorFunc) Create(ctx context.Context, r app.ToolCallRequest) (app.ToolCallResult, error) {
	return f(ctx, r)
}
func jsonReader(value string) *strings.Reader { return strings.NewReader(value) }
