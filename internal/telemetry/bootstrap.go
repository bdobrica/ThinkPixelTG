package telemetry

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/connectors/downstreamhttp"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

type TraceMode string

const (
	TraceNoop  TraceMode = "noop"
	TraceLocal TraceMode = "local"
	TraceOTLP  TraceMode = "otlp"
)

const maxAttributeValueBytes = 256

type BootstrapConfig struct {
	ServiceName    string
	ServiceVersion string
	TraceMode      TraceMode
	OTLPEndpoint   string
	OTLPInsecure   bool
	LocalWriter    io.Writer
}

type Observability struct {
	Registry           *prometheus.Registry
	MetricsHandler     http.Handler
	TracerProvider     trace.TracerProvider
	Propagator         propagation.TextMapPropagator
	Requests           *prometheus.CounterVec
	Duration           *prometheus.HistogramVec
	DownstreamRequests *prometheus.CounterVec
	DownstreamDuration *prometheus.HistogramVec
	shutdown           func(context.Context) error
	once               sync.Once
	shutdownErr        error
}

func Bootstrap(ctx context.Context, cfg BootstrapConfig) (*Observability, error) {
	if strings.TrimSpace(cfg.ServiceName) == "" {
		return nil, errors.New("telemetry service name is required")
	}
	registry := prometheus.NewRegistry()
	requests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "thinkpixeltg", Name: "http_requests_total", Help: "Completed HTTP requests by bounded outcome.",
	}, []string{"method", "route", "status_class"})
	duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "thinkpixeltg", Name: "http_request_duration_seconds", Help: "HTTP request duration by stable route.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route"})
	downstreamRequests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "thinkpixeltg", Name: "downstream_http_requests_total", Help: "Completed downstream HTTP requests by compiled operation and bounded outcome.",
	}, []string{"operation", "method", "outcome", "status_class"})
	downstreamDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "thinkpixeltg", Name: "downstream_http_request_duration_seconds", Help: "Downstream HTTP request duration by compiled operation and bounded outcome.",
		Buckets: prometheus.DefBuckets,
	}, []string{"operation", "method", "outcome"})
	if err := registry.Register(requests); err != nil {
		return nil, fmt.Errorf("register request metric: %w", err)
	}
	if err := registry.Register(duration); err != nil {
		return nil, fmt.Errorf("register duration metric: %w", err)
	}
	if err := registry.Register(downstreamRequests); err != nil {
		return nil, fmt.Errorf("register downstream request metric: %w", err)
	}
	if err := registry.Register(downstreamDuration); err != nil {
		return nil, fmt.Errorf("register downstream duration metric: %w", err)
	}

	provider, shutdown, err := traceProvider(ctx, cfg)
	if err != nil {
		return nil, err
	}
	propagator := propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagator)

	return &Observability{
		Registry: registry, MetricsHandler: promhttp.HandlerFor(registry, promhttp.HandlerOpts{}),
		TracerProvider: provider, Propagator: propagator, Requests: requests, Duration: duration,
		DownstreamRequests: downstreamRequests, DownstreamDuration: downstreamDuration, shutdown: shutdown,
	}, nil
}

func traceProvider(ctx context.Context, cfg BootstrapConfig) (trace.TracerProvider, func(context.Context) error, error) {
	if cfg.TraceMode == "" {
		cfg.TraceMode = TraceNoop
	}
	if cfg.TraceMode == TraceNoop {
		provider := noop.NewTracerProvider()
		return provider, func(context.Context) error { return nil }, nil
	}

	var exporter sdktrace.SpanExporter
	var err error
	switch cfg.TraceMode {
	case TraceLocal:
		writer := cfg.LocalWriter
		if writer == nil {
			writer = io.Discard
		}
		exporter, err = stdouttrace.New(stdouttrace.WithWriter(writer), stdouttrace.WithoutTimestamps())
	case TraceOTLP:
		if strings.TrimSpace(cfg.OTLPEndpoint) == "" {
			return nil, nil, errors.New("OTLP endpoint is required")
		}
		options := []otlptracehttp.Option{otlptracehttp.WithEndpoint(cfg.OTLPEndpoint)}
		if cfg.OTLPInsecure {
			options = append(options, otlptracehttp.WithInsecure())
		}
		exporter, err = otlptracehttp.New(ctx, options...)
	default:
		return nil, nil, fmt.Errorf("unsupported trace mode %q", cfg.TraceMode)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("create trace exporter: %w", err)
	}

	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes("",
		attribute.String("service.name", BoundedAttributeValue(cfg.ServiceName)),
		attribute.String("service.version", BoundedAttributeValue(cfg.ServiceVersion))))
	if err != nil {
		return nil, nil, fmt.Errorf("create telemetry resource: %w", err)
	}
	provider := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter), sdktrace.WithResource(res))
	return provider, provider.Shutdown, nil
}

func BoundedAttributeValue(value string) string {
	if len(value) <= maxAttributeValueBytes {
		return value
	}
	return value[:maxAttributeValueBytes]
}

func StableRoute(route string) string {
	if route == "" || len(route) > 128 || strings.ContainsAny(route, "?#") {
		return "unknown"
	}
	return route
}

func StatusClass(status int) string {
	if status < 100 || status > 599 {
		return "unknown"
	}
	return fmt.Sprintf("%dxx", status/100)
}

func (observability *Observability) Shutdown(ctx context.Context) error {
	observability.once.Do(func() { observability.shutdownErr = observability.shutdown(ctx) })
	return observability.shutdownErr
}

func (observability *Observability) ObserveRequest(method, route string, status int, elapsed time.Duration) {
	method = strings.ToUpper(method)
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions, http.MethodHead:
	default:
		method = "OTHER"
	}
	route = StableRoute(route)
	observability.Requests.WithLabelValues(method, route, StatusClass(status)).Inc()
	observability.Duration.WithLabelValues(method, route).Observe(elapsed.Seconds())
}

// ObserveDownstreamHTTP implements downstreamhttp.Observer. The transport has
// already reduced these fields to compiled, bounded dimensions.
func (observability *Observability) ObserveDownstreamHTTP(_ context.Context, event downstreamhttp.Event) {
	observability.DownstreamRequests.WithLabelValues(event.Operation, event.Method, event.Outcome, event.StatusClass).Inc()
	observability.DownstreamDuration.WithLabelValues(event.Operation, event.Method, event.Outcome).Observe(event.Duration.Seconds())
}
