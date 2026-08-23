package telemetry

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/propagation"
)

func TestBootstrapNoopMetricsAndShutdown(t *testing.T) {
	observability, err := Bootstrap(context.Background(), BootstrapConfig{ServiceName: "thinkpixeltg", TraceMode: TraceNoop})
	if err != nil {
		t.Fatal(err)
	}
	observability.ObserveRequest("GET", "/livez", 200, 1)
	response := httptest.NewRecorder()
	observability.MetricsHandler.ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), "thinkpixeltg_http_requests_total") {
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
