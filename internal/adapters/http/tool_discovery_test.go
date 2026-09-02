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
	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

func TestListToolsOrdersAndPaginatesAuthorizedResults(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	discovery := toolDiscoveryFunc(func(_ context.Context, request ports.DiscoveryAuthorizationRequest) ([]ports.CatalogToolVersion, error) {
		wantIdentity, _ := discoveryTestIdentity(context.Background())
		if request.TenantID != wantIdentity.TenantID || len(request.Candidates) != 0 {
			t.Fatalf("discovery request = %#v", request)
		}
		return []ports.CatalogToolVersion{
			discoveryCandidate("slack.message.send", "2.0.0"),
			discoveryCandidate("github.pull.comment", "1.1.0"),
			discoveryCandidate("github.pull.comment", "1.0.0"),
		}, nil
	})
	handler := newDiscoveryTestHandler(t, discovery, func() time.Time { return now }, discoveryTestIdentity)

	first := performDiscoveryRequest(handler, "/v1/tools?limit=2")
	if first.Code != http.StatusOK || first.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("first response = %d %s", first.Code, first.Body.String())
	}
	var page openapi.ToolPage
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].ToolId != "github.pull.comment" || page.Items[0].Version != "1.0.0" || page.Items[1].Version != "1.1.0" || page.NextCursor == nil {
		t.Fatalf("first page = %#v", page)
	}

	second := performDiscoveryRequest(handler, "/v1/tools?cursor="+*page.NextCursor)
	var next openapi.ToolPage
	if err := json.Unmarshal(second.Body.Bytes(), &next); err != nil {
		t.Fatal(err)
	}
	if second.Code != http.StatusOK || len(next.Items) != 1 || next.Items[0].ToolId != "slack.message.send" || next.NextCursor != nil {
		t.Fatalf("second page = %d %#v", second.Code, next)
	}
}

func TestListToolsRejectsInvalidBoundOrExpiredCursor(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	discovery := toolDiscoveryFunc(func(context.Context, ports.DiscoveryAuthorizationRequest) ([]ports.CatalogToolVersion, error) {
		return []ports.CatalogToolVersion{discoveryCandidate("github.pull.comment", "1.0.0"), discoveryCandidate("slack.message.send", "1.0.0")}, nil
	})
	handler := newDiscoveryTestHandler(t, discovery, func() time.Time { return now }, discoveryTestIdentity)
	first := performDiscoveryRequest(handler, "/v1/tools?limit=1")
	var page openapi.ToolPage
	_ = json.Unmarshal(first.Body.Bytes(), &page)
	if page.NextCursor == nil {
		t.Fatal("expected next cursor")
	}

	otherIdentity := func(context.Context) (ports.DiscoveryAuthorizationRequest, error) {
		identity, _ := discoveryTestIdentity(context.Background())
		identity.RunID = "different-run"
		return identity, nil
	}
	boundHandler := newDiscoveryTestHandler(t, discovery, func() time.Time { return now }, otherIdentity)
	tests := []struct {
		name    string
		handler http.Handler
		url     string
	}{
		{"tampered", handler, "/v1/tools?cursor=" + *page.NextCursor + "x"},
		{"limit changed", handler, "/v1/tools?limit=2&cursor=" + *page.NextCursor},
		{"identity changed", boundHandler, "/v1/tools?cursor=" + *page.NextCursor},
		{"expired", newDiscoveryTestHandler(t, discovery, func() time.Time { return now.Add(6 * time.Minute) }, discoveryTestIdentity), "/v1/tools?cursor=" + *page.NextCursor},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := performDiscoveryRequest(test.handler, test.url)
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_arguments"`) {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestListToolsEnforcesPageBoundsAndFailsClosedOnInvalidProjection(t *testing.T) {
	t.Parallel()
	invalid := toolDiscoveryFunc(func(context.Context, ports.DiscoveryAuthorizationRequest) ([]ports.CatalogToolVersion, error) {
		return []ports.CatalogToolVersion{{ToolID: "private.tool.read", Version: "1.0.0", Definition: json.RawMessage(`{"description":"unsafe"}`)}}, nil
	})
	handler := newDiscoveryTestHandler(t, invalid, time.Now, discoveryTestIdentity)
	for _, url := range []string{"/v1/tools?limit=", "/v1/tools?limit=0", "/v1/tools?limit=101", "/v1/tools?cursor=", "/v1/tools?limit=1&limit=2", "/v1/tools?unknown=true"} {
		if response := performDiscoveryRequest(handler, url); response.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d", url, response.Code)
		}
	}
	response := performDiscoveryRequest(handler, "/v1/tools")
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "private.tool.read") {
		t.Fatalf("invalid catalog response = %d %s", response.Code, response.Body.String())
	}
}

func TestNewToolDiscoveryHandlerValidatesSecurityConfiguration(t *testing.T) {
	t.Parallel()
	discovery := toolDiscoveryFunc(func(context.Context, ports.DiscoveryAuthorizationRequest) ([]ports.CatalogToolVersion, error) {
		return nil, nil
	})
	for _, options := range []ToolDiscoveryOptions{
		{},
		{Discovery: discovery, CursorKey: []byte("short"), CursorTTL: time.Minute},
		{Discovery: discovery, CursorKey: make([]byte, 32)},
	} {
		if _, err := NewToolDiscoveryHandler(options); err == nil {
			t.Fatalf("options %#v accepted", options)
		}
	}
}

func newDiscoveryTestHandler(t *testing.T, discovery ToolDiscovery, now func() time.Time, identity DiscoveryIdentity) http.Handler {
	t.Helper()
	handler, err := NewToolDiscoveryHandler(ToolDiscoveryOptions{
		Discovery: discovery, CursorKey: []byte("0123456789abcdef0123456789abcdef"), CursorTTL: 5 * time.Minute,
		Clock: now, Identity: identity,
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func performDiscoveryRequest(handler http.Handler, url string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, url, nil)
	response := httptest.NewRecorder()
	response.Header().Set(RequestIDHeader, "019b0000-0000-7000-8000-000000000099")
	handler.ServeHTTP(response, request)
	return response
}

func discoveryTestIdentity(context.Context) (ports.DiscoveryAuthorizationRequest, error) {
	return ports.DiscoveryAuthorizationRequest{
		TenantID: "019b0000-0000-7000-8000-000000000001", Subject: "subject-1", AgentID: "agent-1",
		AgentVersion: "1.0.0", RunID: "run-1", WorkloadID: "spiffe://example.test/workload/tg",
	}, nil
}

func discoveryCandidate(toolID, version string) ports.CatalogToolVersion {
	return ports.CatalogToolVersion{ToolID: toolID, Version: version, Definition: json.RawMessage(`{
		"description":"A reviewed tool description.","input_schema":{"type":"object"},"output_schema":{"type":"object"},
		"risk":"read","side_effect":false,"retry_class":"safe","approval":"policy","open_world_result":true
	}`)}
}

type toolDiscoveryFunc func(context.Context, ports.DiscoveryAuthorizationRequest) ([]ports.CatalogToolVersion, error)

func (function toolDiscoveryFunc) Discover(ctx context.Context, request ports.DiscoveryAuthorizationRequest) ([]ports.CatalogToolVersion, error) {
	return function(ctx, request)
}
