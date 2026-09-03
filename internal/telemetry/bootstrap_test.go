package telemetry

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/connectors/downstreamhttp"
	"go.opentelemetry.io/otel/propagation"
)

type credentialCanaryContextKey struct{}

func TestBootstrapNoopMetricsAndShutdown(t *testing.T) {
	observability, err := Bootstrap(context.Background(), BootstrapConfig{ServiceName: "thinkpixeltg", TraceMode: TraceNoop})
	if err != nil {
		t.Fatal(err)
	}
	observability.ObserveRequest("GET", "/livez", 200, 1)
	observability.ObserveDownstreamHTTP(context.Background(), downstreamhttp.Event{Operation: "github.pull_read", Method: "GET", Outcome: "response", StatusClass: "2xx", Duration: time.Millisecond})
	response := httptest.NewRecorder()
	observability.MetricsHandler.ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), "thinkpixeltg_http_requests_total") || !strings.Contains(response.Body.String(), `thinkpixeltg_downstream_http_requests_total{method="GET",operation="github.pull_read",outcome="response",status_class="2xx"} 1`) {
		t.Fatalf("metrics response: %d %s", response.Code, response.Body.String())
	}
	if err := observability.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := observability.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestBootstrapLocalAndW3CPropagation(t *testing.T) {
	var output bytes.Buffer
	observability, err := Bootstrap(context.Background(), BootstrapConfig{ServiceName: "thinkpixeltg", TraceMode: TraceLocal, LocalWriter: &output})
	if err != nil {
		t.Fatal(err)
	}
	ctx, span := observability.TracerProvider.Tracer("test").Start(context.Background(), "operation")
	carrier := propagation.MapCarrier{}
	observability.Propagator.Inject(ctx, carrier)
	span.End()
	if carrier.Get("traceparent") == "" {
		t.Fatal("traceparent was not injected")
	}
	if err := observability.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "operation") {
		t.Fatalf("local trace missing: %s", output.String())
	}
}

func TestCredentialCanaryIsExcludedFromTracesAndMetrics(t *testing.T) {
	const canary = "SYNTHETIC_CREDENTIAL_CANARY_telemetry_013"
	var traces bytes.Buffer
	observability, err := Bootstrap(context.Background(), BootstrapConfig{ServiceName: "thinkpixeltg", TraceMode: TraceLocal, LocalWriter: &traces})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), credentialCanaryContextKey{}, canary)
	_, span := observability.TracerProvider.Tracer("test").Start(ctx, "credential.resolve")
	span.End()
	observability.ObserveRequest("GET", "/v1/tools/{tool_id}", 200, time.Millisecond)
	metrics := httptest.NewRecorder()
	observability.MetricsHandler.ServeHTTP(metrics, httptest.NewRequest("GET", "/metrics", nil))
	if err := observability.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(traces.String(), canary) || strings.Contains(metrics.Body.String(), canary) {
		t.Fatalf("telemetry leaked credential canary: traces=%s metrics=%s", traces.String(), metrics.Body.String())
	}
}

func TestBoundedTelemetryValues(t *testing.T) {
	if got := len(BoundedAttributeValue(strings.Repeat("x", 300))); got != maxAttributeValueBytes {
		t.Fatalf("length = %d", got)
	}
	if got := StableRoute("/things?id=caller"); got != "unknown" {
		t.Fatalf("route = %q", got)
	}
	if got := StatusClass(999); got != "unknown" {
		t.Fatalf("status = %q", got)
	}
}

func TestOTLPRequiresEndpoint(t *testing.T) {
	if _, err := Bootstrap(context.Background(), BootstrapConfig{ServiceName: "thinkpixeltg", TraceMode: TraceOTLP}); err == nil {
		t.Fatal("expected error")
	}
}
