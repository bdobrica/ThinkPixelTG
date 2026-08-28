package http

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/config"
	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/bdobrica/ThinkPixelTG/internal/telemetry"
)

func testServer(t *testing.T, mutate func(*Options)) *Server {
	t.Helper()
	observability, err := telemetry.Bootstrap(context.Background(), telemetry.BootstrapConfig{ServiceName: "test", TraceMode: telemetry.TraceNoop})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default().HTTP
	options := Options{Config: cfg, Logger: telemetry.NewLogger(slog.NewTextHandler(io.Discard, nil)), Observability: observability}
	if mutate != nil {
		mutate(&options)
	}
	server, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func TestHealthMetricsAndRequestID(t *testing.T) {
	server := testServer(t, nil)
	for _, path := range []string{"/livez", "/readyz", "/metrics"} {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != 200 {
			t.Errorf("%s status = %d", path, response.Code)
		}
		if _, err := domain.ParseUUID(response.Header().Get(RequestIDHeader)); err != nil {
			t.Errorf("%s request id: %v", path, err)
		}
	}
}

func TestMiddlewareOrdersCorrelationBeforeAuthentication(t *testing.T) {
	server := testServer(t, func(options *Options) {
		options.Authenticator = func(ctx context.Context, request *stdhttp.Request) (context.Context, error) {
			requestID, traceID := telemetry.Correlation(ctx)
			if requestID == "" || traceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
				return ctx, errors.New("missing correlation")
			}
			return ctx, nil
		}
		options.Application = stdhttp.HandlerFunc(func(writer stdhttp.ResponseWriter, _ *stdhttp.Request) { writer.WriteHeader(204) })
	})
	request := httptest.NewRequest("GET", "/v1/tools", nil)
	request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != 204 {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
}

func TestPanicAndAuthenticationUseProblems(t *testing.T) {
	t.Run("panic", func(t *testing.T) {
		server := testServer(t, func(options *Options) {
			options.Application = stdhttp.HandlerFunc(func(stdhttp.ResponseWriter, *stdhttp.Request) { panic("canary") })
		})
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/panic", nil))
		if response.Code != 500 || response.Header().Get("Content-Type") != "application/problem+json" {
			t.Fatalf("response = %d %s", response.Code, response.Body.String())
		}
	})
	t.Run("authentication", func(t *testing.T) {
		server := testServer(t, func(options *Options) {
			options.Authenticator = func(context.Context, *stdhttp.Request) (context.Context, error) { return nil, errors.New("denied") }
		})
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/v1/tools", nil))
		if response.Code != 401 || !strings.Contains(response.Body.String(), `"status":401`) {
			t.Fatalf("response = %d %s", response.Code, response.Body.String())
		}
	})
	t.Run("identity provider unavailable", func(t *testing.T) {
		server := testServer(t, func(options *Options) {
			options.Authenticator = func(context.Context, *stdhttp.Request) (context.Context, error) {
				return nil, unavailable{}
			}
		})
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/v1/tools", nil))
		if response.Code != 503 || !strings.Contains(response.Body.String(), "identity-provider-unavailable") {
			t.Fatalf("response = %d %s", response.Code, response.Body.String())
		}
	})
}

type unavailable struct{}

func (unavailable) Error() string   { return "unavailable" }
func (unavailable) HTTPStatus() int { return stdhttp.StatusServiceUnavailable }

func TestBodyLimitAndCancellation(t *testing.T) {
	server := testServer(t, func(options *Options) {
		options.Config.MaxBodyBytes = 4
		options.Config.WriteTimeout = 20 * time.Millisecond
		options.Application = stdhttp.HandlerFunc(func(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
			if request.URL.Path == "/body" {
				_, err := io.ReadAll(request.Body)
				if err == nil {
					t.Error("expected body limit")
				}
				writer.WriteHeader(204)
				return
			}
			<-request.Context().Done()
			writer.WriteHeader(504)
		})
	})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest("POST", "/body", bytes.NewBufferString("too large")))
	if response.Code != 204 {
		t.Fatalf("body status = %d", response.Code)
	}
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/wait", nil))
	if response.Code != 504 {
		t.Fatalf("timeout status = %d", response.Code)
	}
}

func TestReadinessFailure(t *testing.T) {
	server := testServer(t, func(options *Options) {
		options.Readiness = func(context.Context) error { return errors.New("database unavailable") }
	})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/readyz", nil))
	if response.Code != 503 || strings.Contains(response.Body.String(), "database") {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}
