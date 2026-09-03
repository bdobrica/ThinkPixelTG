// Package http implements the bounded HTTP transport adapter.
package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	stdhttp "net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/config"
	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/bdobrica/ThinkPixelTG/internal/telemetry"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const RequestIDHeader = "X-Request-ID"

const (
	maxPublicRequestBytes       = 1 << 20
	maxPublicRequestDuration    = 30 * time.Second
	maxConcurrentPublicRequests = 1000
	maxPublicResultBytes        = 4 << 20
)

type Authenticator func(context.Context, *stdhttp.Request) (context.Context, error)
type Readiness func(context.Context) error
type IDGenerator func() (domain.UUID, error)

type Options struct {
	Config        config.HTTP
	Logger        *slog.Logger
	Observability *telemetry.Observability
	Authenticator Authenticator
	// AdminAuthenticator is deliberately distinct from Authenticator. Ordinary
	// harness credentials must never reach administrative handlers.
	AdminAuthenticator Authenticator
	Readiness          Readiness
	IDGenerator        IDGenerator
	Application        stdhttp.Handler
	AdminApplication   stdhttp.Handler
}

type Server struct {
	httpServer      *stdhttp.Server
	shutdownTimeout time.Duration
}

func New(options Options) (*Server, error) {
	if options.Logger == nil {
		return nil, errors.New("HTTP logger is required")
	}
	if options.Observability == nil {
		return nil, errors.New("HTTP observability is required")
	}
	if options.Application == nil {
		options.Application = stdhttp.NotFoundHandler()
	}
	if options.Authenticator == nil {
		options.Authenticator = func(ctx context.Context, _ *stdhttp.Request) (context.Context, error) { return ctx, nil }
	}
	if options.Readiness == nil {
		options.Readiness = func(context.Context) error { return nil }
	}
	if options.IDGenerator == nil {
		options.IDGenerator = func() (domain.UUID, error) { return domain.NewUUIDv7(domain.SystemClock{}) }
	}
	if err := validateHTTPConfig(options.Config); err != nil {
		return nil, err
	}

	mux := stdhttp.NewServeMux()
	mux.HandleFunc("GET /livez", func(writer stdhttp.ResponseWriter, _ *stdhttp.Request) {
		writeJSON(writer, stdhttp.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
		if err := options.Readiness(request.Context()); err != nil {
			writePublicProblem(writer, request, domain.CodeNotReady)
			return
		}
		writeJSON(writer, stdhttp.StatusOK, map[string]string{"status": "ready"})
	})
	mux.Handle("GET /metrics", options.Observability.MetricsHandler)
	var adminApplication stdhttp.Handler = stdhttp.HandlerFunc(func(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
		writePublicProblem(writer, request, domain.CodeUnauthenticated)
	})
	if options.AdminApplication != nil {
		if options.AdminAuthenticator == nil {
			return nil, errors.New("admin authenticator is required when the admin application is configured")
		}
		adminApplication = authentication(options.AdminAuthenticator, options.AdminApplication)
	}
	mux.Handle("/v1/admin/", adminApplication)
	mux.Handle("/", authentication(options.Authenticator, options.Application))

	handler := stdhttp.Handler(mux)
	handler = publicRequestLimits(options.Config.MaxBodyBytes, maxConcurrentPublicRequests, handler)
	handler = bodyLimit(options.Config.MaxBodyBytes, handler)
	handler = requestDeadline(options.Config.WriteTimeout, handler)
	handler = accessTelemetry(options.Logger, options.Observability, handler)
	handler = traceContext(options.Observability.Propagator, handler)
	handler = requestID(options.IDGenerator, handler)
	handler = recoverPanics(options.Logger, handler)

	return &Server{
		httpServer: &stdhttp.Server{
			Addr: options.Config.Address, Handler: handler,
			ReadHeaderTimeout: options.Config.ReadHeaderTimeout, ReadTimeout: options.Config.ReadTimeout,
			WriteTimeout: options.Config.WriteTimeout, IdleTimeout: options.Config.IdleTimeout,
			MaxHeaderBytes: options.Config.MaxHeaderBytes,
		},
		shutdownTimeout: options.Config.ShutdownTimeout,
	}, nil
}

func validateHTTPConfig(cfg config.HTTP) error {
	if strings.TrimSpace(cfg.Address) == "" || cfg.ReadHeaderTimeout <= 0 || cfg.ReadTimeout <= 0 || cfg.WriteTimeout <= 0 || cfg.WriteTimeout > maxPublicRequestDuration || cfg.IdleTimeout <= 0 || cfg.ShutdownTimeout <= 0 || cfg.MaxHeaderBytes < 1024 || cfg.MaxBodyBytes < 1 || cfg.MaxBodyBytes > maxPublicRequestBytes {
		return errors.New("invalid HTTP server limits")
	}
	return nil
}

func (server *Server) Handler() stdhttp.Handler { return server.httpServer.Handler }

// Run serves until ctx is cancelled, then performs a bounded graceful drain.
func (server *Server) Run(ctx context.Context, listener net.Listener) error {
	serveResult := make(chan error, 1)
	go func() { serveResult <- server.httpServer.Serve(listener) }()
	select {
	case err := <-serveResult:
		if errors.Is(err, stdhttp.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), server.shutdownTimeout)
		defer cancel()
		if err := server.httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown HTTP server: %w", err)
		}
		err := <-serveResult
		if err != nil && !errors.Is(err, stdhttp.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func recoverPanics(logger *slog.Logger, next stdhttp.Handler) stdhttp.Handler {
	return stdhttp.HandlerFunc(func(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				_ = telemetry.LogEvent(request.Context(), logger, slog.LevelError, "http.panic_recovered", slog.String("panic.type", fmt.Sprintf("%T", recovered)))
				writePublicProblem(writer, request, domain.CodeInternal)
			}
		}()
		next.ServeHTTP(writer, request)
	})
}

func requestID(generate IDGenerator, next stdhttp.Handler) stdhttp.Handler {
	return stdhttp.HandlerFunc(func(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
		id := request.Header.Get(RequestIDHeader)
		if parsed, err := domain.ParseUUID(id); err != nil || parsed[6]>>4 != 7 {
			generated, generateErr := generate()
			if generateErr != nil {
				writePublicProblem(writer, request, domain.CodeInternal)
				return
			}
			id = generated.String()
		}
		writer.Header().Set(RequestIDHeader, id)
		request = request.WithContext(telemetry.WithCorrelation(request.Context(), id, ""))
		next.ServeHTTP(writer, request)
	})
}

func traceContext(propagator propagation.TextMapPropagator, next stdhttp.Handler) stdhttp.Handler {
	return stdhttp.HandlerFunc(func(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
		ctx := propagator.Extract(request.Context(), propagation.HeaderCarrier(request.Header))
		traceID := trace.SpanContextFromContext(ctx).TraceID().String()
		if traceID == "00000000000000000000000000000000" {
			traceID = ""
		}
		requestID, _ := telemetry.Correlation(ctx)
		ctx = telemetry.WithCorrelation(ctx, requestID, traceID)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

type statusWriter struct {
	stdhttp.ResponseWriter
	status int
}

func (writer *statusWriter) WriteHeader(status int) {
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}
func (writer *statusWriter) Write(body []byte) (int, error) {
	if writer.status == 0 {
		writer.WriteHeader(200)
	}
	return writer.ResponseWriter.Write(body)
}

func accessTelemetry(logger *slog.Logger, observability *telemetry.Observability, next stdhttp.Handler) stdhttp.Handler {
	return stdhttp.HandlerFunc(func(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
		started := time.Now()
		tracked := &statusWriter{ResponseWriter: writer}
		next.ServeHTTP(tracked, request)
		status := tracked.status
		if status == 0 {
			status = 200
		}
		route := request.Pattern
		if route == "" {
			route = "unknown"
		}
		observability.ObserveRequest(request.Method, route, status, time.Since(started))
		_ = telemetry.LogEvent(request.Context(), logger, slog.LevelInfo, "http.request_completed", slog.String("http.method", request.Method), slog.String("http.route", telemetry.StableRoute(route)), slog.String("http.status_class", telemetry.StatusClass(status)))
	})
}

func requestDeadline(timeout time.Duration, next stdhttp.Handler) stdhttp.Handler {
	return stdhttp.HandlerFunc(func(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
		ctx, cancel := context.WithTimeout(request.Context(), timeout)
		defer cancel()
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func bodyLimit(limit int64, next stdhttp.Handler) stdhttp.Handler {
	return stdhttp.HandlerFunc(func(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
		request.Body = stdhttp.MaxBytesReader(writer, request.Body, limit)
		next.ServeHTTP(writer, request)
	})
}

// publicRequestLimits applies the deployment-wide admission envelope before a
// public request can allocate an application-sized body or reach a dependency.
// Tool-version limits may only narrow these bounds inside the application.
func publicRequestLimits(maxBodyBytes int64, maxConcurrent int, next stdhttp.Handler) stdhttp.Handler {
	permits := make(chan struct{}, maxConcurrent)
	return stdhttp.HandlerFunc(func(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
		if !strings.HasPrefix(request.URL.Path, "/v1/") {
			next.ServeHTTP(writer, request)
			return
		}
		select {
		case permits <- struct{}{}:
			defer func() { <-permits }()
		default:
			writePublicProblem(writer, request, domain.CodeRateLimited)
			return
		}
		if request.ContentLength > maxBodyBytes {
			writePublicProblem(writer, request, domain.CodeInvalidArguments)
			return
		}
		if request.Method == stdhttp.MethodGet || request.Method == stdhttp.MethodHead {
			if request.ContentLength > 0 {
				writePublicProblem(writer, request, domain.CodeInvalidArguments)
				return
			}
		}
		next.ServeHTTP(writer, request)
	})
}

func authentication(authenticate Authenticator, next stdhttp.Handler) stdhttp.Handler {
	return stdhttp.HandlerFunc(func(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
		ctx, err := authenticate(request.Context(), request)
		if err != nil {
			status := stdhttp.StatusUnauthorized
			var classified interface{ HTTPStatus() int }
			if errors.As(err, &classified) && classified.HTTPStatus() == stdhttp.StatusServiceUnavailable {
				status = stdhttp.StatusServiceUnavailable
			}
			code := domain.CodeUnauthenticated
			if status == stdhttp.StatusServiceUnavailable {
				code = domain.CodeIdentityProviderUnavailable
			}
			writePublicProblem(writer, request, code)
			return
		}
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

type Problem struct {
	Type          string `json:"type"`
	Title         string `json:"title"`
	Status        int    `json:"status"`
	Detail        string `json:"detail,omitempty"`
	Instance      string `json:"instance,omitempty"`
	Code          string `json:"code"`
	CorrelationID string `json:"correlation_id"`
}

func WriteProblem(writer stdhttp.ResponseWriter, request *stdhttp.Request, problem Problem) {
	if problem.Code == "" {
		problem.Code = string(domain.CodeInternal)
	}
	if problem.Instance == "" {
		problem.Instance = request.URL.Path
	}
	problem.CorrelationID = writer.Header().Get(RequestIDHeader)
	writer.Header().Set("Content-Type", "application/problem+json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(problem.Status)
	_ = json.NewEncoder(writer).Encode(problem)
}

func writeJSON(writer stdhttp.ResponseWriter, status int, value any) {
	writeBoundedJSON(writer, status, "application/json", false, value)
}

func writeBoundedJSON(writer stdhttp.ResponseWriter, status int, contentType string, noStore bool, value any) {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded)+1 > maxPublicResultBytes {
		correlationID := writer.Header().Get(RequestIDHeader)
		problem, _ := json.Marshal(Problem{Type: "urn:thinkpixeltg:problem:result_blocked", Title: "Tool result was blocked", Status: stdhttp.StatusForbidden, Code: string(domain.CodeResultBlocked), CorrelationID: correlationID})
		writer.Header().Set("Content-Type", "application/problem+json")
		writer.Header().Set("Cache-Control", "no-store")
		writer.WriteHeader(stdhttp.StatusForbidden)
		_, _ = writer.Write(append(problem, '\n'))
		return
	}
	writer.Header().Set("Content-Type", contentType)
	if noStore {
		writer.Header().Set("Cache-Control", "no-store")
	}
	writer.Header().Set("Content-Length", strconv.Itoa(len(encoded)+1))
	writer.WriteHeader(status)
	_, _ = writer.Write(append(encoded, '\n'))
}
