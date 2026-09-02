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
		if response.Code != 503 || !strings.Contains(response.Body.String(), `"code":"identity_provider_unavailable"`) {
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

func TestPublicRequestAdmissionLimits(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	handler := publicRequestLimits(4, 1, stdhttp.HandlerFunc(func(writer stdhttp.ResponseWriter, _ *stdhttp.Request) {
		close(entered)
		<-release
		writer.WriteHeader(stdhttp.StatusNoContent)
	}))
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(stdhttp.MethodGet, "/v1/tools", nil))
	}()
	<-entered

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(stdhttp.MethodGet, "/v1/tools", nil))
	if response.Code != stdhttp.StatusTooManyRequests || !strings.Contains(response.Body.String(), `"code":"rate_limited"`) {
		t.Fatalf("overload response = %d %s", response.Code, response.Body.String())
	}
	close(release)
	<-firstDone

	for name, request := range map[string]*stdhttp.Request{
		"declared oversize": httptest.NewRequest(stdhttp.MethodPost, "/v1/tool-calls", strings.NewReader("12345")),
		"GET body":          httptest.NewRequest(stdhttp.MethodGet, "/v1/tools", strings.NewReader("x")),
	} {
		t.Run(name, func(t *testing.T) {
			response := httptest.NewRecorder()
			publicRequestLimits(4, 1, stdhttp.HandlerFunc(func(stdhttp.ResponseWriter, *stdhttp.Request) { t.Fatal("application reached") })).ServeHTTP(response, request)
			if response.Code != stdhttp.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_arguments"`) {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestPublicResultLimitFailsClosedBeforeWritingContent(t *testing.T) {
	response := httptest.NewRecorder()
	response.Header().Set(RequestIDHeader, "request-1")
	writeJSON(response, stdhttp.StatusOK, map[string]string{"result": strings.Repeat("x", maxPublicResultBytes)})
	if response.Code != stdhttp.StatusForbidden || response.Header().Get("Content-Type") != "application/problem+json" || !strings.Contains(response.Body.String(), `"code":"result_blocked"`) || strings.Contains(response.Body.String(), "xxxx") {
		t.Fatalf("response = %d %#v %.200s", response.Code, response.Header(), response.Body.String())
	}
}

func TestServerRejectsLimitsAbovePublicEnvelope(t *testing.T) {
	for name, mutate := range map[string]func(*Options){
		"request bytes": func(options *Options) { options.Config.MaxBodyBytes = maxPublicRequestBytes + 1 },
		"deadline":      func(options *Options) { options.Config.WriteTimeout = maxPublicRequestDuration + time.Second },
	} {
		t.Run(name, func(t *testing.T) {
			observability, err := telemetry.Bootstrap(context.Background(), telemetry.BootstrapConfig{ServiceName: "test", TraceMode: telemetry.TraceNoop})
			if err != nil {
				t.Fatal(err)
			}
			options := Options{Config: config.Default().HTTP, Logger: telemetry.NewLogger(slog.NewTextHandler(io.Discard, nil)), Observability: observability}
			mutate(&options)
			if _, err := New(options); err == nil {
				t.Fatal("expected invalid public limit")
			}
		})
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
