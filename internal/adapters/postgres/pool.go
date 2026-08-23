package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

type PoolConfig struct {
	URL                                                             string
	MaxConnections, MinConnections                                  int32
	MaxConnectionLifetime, MaxConnectionIdleTime, HealthCheckPeriod time.Duration
	ConnectTimeout, ReadinessTimeout, StatementTimeout, LockTimeout time.Duration
	IdleTransactionTimeout, TransactionTimeout, ShutdownTimeout     time.Duration
}

type Pool struct {
	*pgxpool.Pool
	readinessTimeout   time.Duration
	transactionTimeout time.Duration
	shutdownTimeout    time.Duration
	closeOnce          sync.Once
}

func Open(ctx context.Context, cfg PoolConfig, registerer prometheus.Registerer) (*Pool, error) {
	if err := validatePoolConfig(cfg); err != nil {
		return nil, err
	}
	parsed, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parse PostgreSQL pool configuration: %w", err)
	}
	parsed.MaxConns, parsed.MinConns = cfg.MaxConnections, cfg.MinConnections
	parsed.MaxConnLifetime, parsed.MaxConnIdleTime, parsed.HealthCheckPeriod = cfg.MaxConnectionLifetime, cfg.MaxConnectionIdleTime, cfg.HealthCheckPeriod
	parsed.ConnConfig.ConnectTimeout = cfg.ConnectTimeout
	parsed.ConnConfig.RuntimeParams["statement_timeout"] = milliseconds(cfg.StatementTimeout)
	parsed.ConnConfig.RuntimeParams["lock_timeout"] = milliseconds(cfg.LockTimeout)
	parsed.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = milliseconds(cfg.IdleTransactionTimeout)
	metrics := newDatabaseMetrics(registerer)
	parsed.ConnConfig.Tracer = &queryTracer{metrics: metrics}
	connectCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	inner, err := pgxpool.NewWithConfig(connectCtx, parsed)
	if err != nil {
		return nil, fmt.Errorf("create PostgreSQL pool: %w", err)
	}
	pool := &Pool{Pool: inner, readinessTimeout: cfg.ReadinessTimeout, transactionTimeout: cfg.TransactionTimeout, shutdownTimeout: cfg.ShutdownTimeout}
	metrics.registerPool(inner)
	if err := pool.Ready(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("initialize PostgreSQL pool: %w", err)
	}
	return pool, nil
}

func (pool *Pool) Ready(ctx context.Context) error {
	readyCtx, cancel := context.WithTimeout(ctx, pool.readinessTimeout)
	defer cancel()
	if err := pool.Ping(readyCtx); err != nil {
		return fmt.Errorf("PostgreSQL readiness: %w", err)
	}
	return nil
}

func (pool *Pool) Transactor() (*Transactor, error) {
	return NewTransactorWithTimeout(pool, pool.transactionTimeout, pool.shutdownTimeout)
}

// Close is concurrency-safe. pgxpool rejects new acquisitions and waits for
// checked-out connections; transaction deadlines bound that wait in service use.
func (pool *Pool) Close() { pool.closeOnce.Do(pool.Pool.Close) }

func (pool *Pool) Shutdown(ctx context.Context) error {
	done := make(chan struct{})
	go func() { pool.Close(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("shutdown PostgreSQL pool: %w", ctx.Err())
	}
}

func validatePoolConfig(cfg PoolConfig) error {
	if strings.TrimSpace(cfg.URL) == "" {
		return errors.New("PostgreSQL URL is required")
	}
	if cfg.MinConnections < 0 || cfg.MaxConnections < 1 || cfg.MinConnections > cfg.MaxConnections {
		return errors.New("PostgreSQL connection limits are invalid")
	}
	for name, value := range map[string]time.Duration{"connection lifetime": cfg.MaxConnectionLifetime, "connection idle": cfg.MaxConnectionIdleTime, "health check": cfg.HealthCheckPeriod, "connect": cfg.ConnectTimeout, "readiness": cfg.ReadinessTimeout, "statement": cfg.StatementTimeout, "lock": cfg.LockTimeout, "idle transaction": cfg.IdleTransactionTimeout, "transaction": cfg.TransactionTimeout, "shutdown": cfg.ShutdownTimeout} {
		if value <= 0 {
			return fmt.Errorf("PostgreSQL %s timeout must be positive", name)
		}
	}
	return nil
}

func milliseconds(value time.Duration) string { return strconv.FormatInt(value.Milliseconds(), 10) }

type databaseMetrics struct {
	queries    *prometheus.CounterVec
	duration   *prometheus.HistogramVec
	registerer prometheus.Registerer
}

func newDatabaseMetrics(registerer prometheus.Registerer) *databaseMetrics {
	metrics := &databaseMetrics{registerer: registerer}
	if registerer == nil {
		return metrics
	}
	metrics.queries = prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "thinkpixeltg", Subsystem: "postgres", Name: "queries_total", Help: "PostgreSQL queries by bounded operation and outcome."}, []string{"operation", "error_class"})
	metrics.duration = prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: "thinkpixeltg", Subsystem: "postgres", Name: "query_duration_seconds", Help: "PostgreSQL query duration by bounded operation.", Buckets: prometheus.DefBuckets}, []string{"operation"})
	registerer.MustRegister(metrics.queries, metrics.duration)
	return metrics
}

func (metrics *databaseMetrics) registerPool(pool *pgxpool.Pool) {
	if metrics.registerer == nil {
		return
	}
	for name, gauge := range map[string]struct {
		help  string
		value func() float64
	}{
		"connections_acquired": {"Currently acquired PostgreSQL connections.", func() float64 { return float64(pool.Stat().AcquiredConns()) }},
		"connections_idle":     {"Currently idle PostgreSQL connections.", func() float64 { return float64(pool.Stat().IdleConns()) }},
		"connections_total":    {"Current total PostgreSQL connections.", func() float64 { return float64(pool.Stat().TotalConns()) }},
	} {
		metrics.registerer.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Namespace: "thinkpixeltg", Subsystem: "postgres", Name: name, Help: gauge.help}, gauge.value))
	}
}

type queryTraceKey struct{}
type queryTraceState struct {
	started   time.Time
	operation string
}
type queryTracer struct{ metrics *databaseMetrics }

func (tracer *queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, queryTraceKey{}, queryTraceState{started: time.Now(), operation: queryOperation(data.SQL)})
}
func (tracer *queryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if tracer.metrics.queries == nil {
		return
	}
	state, ok := ctx.Value(queryTraceKey{}).(queryTraceState)
	if !ok {
		return
	}
	tracer.metrics.queries.WithLabelValues(state.operation, string(ClassifyError(data.Err))).Inc()
	tracer.metrics.duration.WithLabelValues(state.operation).Observe(time.Since(state.started).Seconds())
}
func queryOperation(sql string) string {
	fields := strings.Fields(sql)
	if len(fields) == 0 {
		return "other"
	}
	switch operation := strings.ToLower(fields[0]); operation {
	case "select", "insert", "update", "delete", "begin", "commit", "rollback":
		return operation
	}
	return "other"
}
