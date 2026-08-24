package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
)

type Publication struct {
	TenantID, OutboxID, EventID, Topic, EventType string
	SafePayload                                   json.RawMessage
	PayloadDigest                                 []byte
	CreatedAt                                     time.Time
}

// PublicationSink must deduplicate by (tenant_id, outbox_id). A publisher can
// crash after the sink accepts a message but before PostgreSQL records success;
// the next lease therefore deliberately sends the same stable identity again.
type PublicationSink interface {
	Publish(context.Context, Publication) (publicationRef string, err error)
}

type PublicationErrorClass string

const (
	PublicationTransient PublicationErrorClass = "transient"
	PublicationPermanent PublicationErrorClass = "permanent"
	PublicationPoison    PublicationErrorClass = "poison"
	PublicationUnknown   PublicationErrorClass = "unknown"
)

type PublicationError struct {
	Class PublicationErrorClass
	Err   error
}

func (e *PublicationError) Error() string {
	return fmt.Sprintf("outbox publication %s: %v", e.Class, e.Err)
}
func (e *PublicationError) Unwrap() error { return e.Err }

type OutboxPublisherConfig struct {
	OwnerID       string
	LeaseDuration time.Duration
	MaxAttempts   int
	BaseBackoff   time.Duration
	MaxBackoff    time.Duration
	Jitter        float64
	Now           func() time.Time
	Random        func() float64
}

type OutboxPublisher struct {
	db      DBTX
	tx      *Transactor
	sink    PublicationSink
	cfg     OutboxPublisherConfig
	metrics *outboxMetrics
}

func NewOutboxPublisher(database AcquisitionDatabase, sink PublicationSink, cfg OutboxPublisherConfig, registerer prometheus.Registerer) (*OutboxPublisher, error) {
	if database == nil || sink == nil {
		return nil, errors.New("outbox database and sink are required")
	}
	if strings.TrimSpace(cfg.OwnerID) == "" || cfg.LeaseDuration <= 0 || cfg.MaxAttempts < 1 ||
		cfg.BaseBackoff <= 0 || cfg.MaxBackoff < cfg.BaseBackoff || cfg.Jitter < 0 || cfg.Jitter > 1 {
		return nil, errors.New("outbox publisher configuration is invalid")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Random == nil {
		cfg.Random = rand.Float64
	}
	tx, err := NewTransactor(database)
	if err != nil {
		return nil, err
	}
	metrics, err := newOutboxMetrics(registerer)
	if err != nil {
		return nil, err
	}
	return &OutboxPublisher{db: database, tx: tx, sink: sink, cfg: cfg, metrics: metrics}, nil
}

// PublishOne claims and handles at most one available message. The boolean is
// false when no work was ready. Sink failures are persisted and are not returned;
// infrastructure/ownership failures are returned to the worker loop.
func (p *OutboxPublisher) PublishOne(ctx context.Context) (bool, error) {
	now := p.cfg.Now().UTC()
	message, err := p.claim(ctx, now)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	p.metrics.claims.Inc()
	started := time.Now()
	ref, publishErr := p.sink.Publish(ctx, message.Publication)
	p.metrics.duration.Observe(time.Since(started).Seconds())
	if publishErr == nil {
		if strings.TrimSpace(ref) == "" {
			publishErr = &PublicationError{Class: PublicationPoison, Err: errors.New("sink returned an empty publication reference")}
		} else {
			err = p.markPublished(ctx, message, ref, p.cfg.Now().UTC())
			if err == nil {
				p.metrics.results.WithLabelValues("published").Inc()
			}
			return true, err
		}
	}
	class := classifyPublicationError(publishErr)
	dead := class == PublicationPermanent || class == PublicationPoison || message.AttemptCount >= p.cfg.MaxAttempts
	if dead {
		err = p.markDeadLetter(ctx, message, class, p.cfg.Now().UTC())
		if err == nil {
			p.metrics.results.WithLabelValues("dead_lettered").Inc()
		}
	} else {
		err = p.releaseForRetry(ctx, message, class, p.cfg.Now().UTC())
		if err == nil {
			p.metrics.results.WithLabelValues("retry_scheduled").Inc()
		}
	}
	return true, err
}

type claimedOutbox struct {
	Publication
	AttemptCount int
	ClaimUntil   time.Time
}

func (p *OutboxPublisher) claim(ctx context.Context, now time.Time) (claimedOutbox, error) {
	var result claimedOutbox
	err := p.tx.WithinTransaction(ctx, pgx.TxOptions{}, func(txCtx context.Context, db DBTX) error {
		const query = `WITH candidate AS (
			SELECT tenant_id,outbox_id FROM outbox_messages
			WHERE published_at IS NULL AND dead_lettered_at IS NULL AND available_at <= $1
			  AND (claim_until IS NULL OR claim_until <= $1)
			ORDER BY available_at,tenant_id,outbox_id FOR UPDATE SKIP LOCKED LIMIT 1
		) UPDATE outbox_messages o SET claim_owner=$2,claim_until=$3,attempt_count=o.attempt_count+1
		FROM candidate c WHERE o.tenant_id=c.tenant_id AND o.outbox_id=c.outbox_id
		RETURNING o.tenant_id,o.outbox_id,o.event_id,o.topic,o.event_type,o.safe_payload,o.payload_digest,
			o.created_at,o.attempt_count,o.claim_until`
		return db.QueryRow(txCtx, query, now, p.cfg.OwnerID, now.Add(p.cfg.LeaseDuration)).Scan(
			&result.TenantID, &result.OutboxID, &result.EventID, &result.Topic, &result.EventType,
			&result.SafePayload, &result.PayloadDigest, &result.CreatedAt, &result.AttemptCount, &result.ClaimUntil)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return claimedOutbox{}, pgx.ErrNoRows
		}
		return claimedOutbox{}, repositoryError("claim outbox message", err)
	}
	p.metrics.lag.Observe(max(0, now.Sub(result.CreatedAt).Seconds()))
	return result, nil
}

func (p *OutboxPublisher) markPublished(ctx context.Context, m claimedOutbox, ref string, now time.Time) error {
	const query = `UPDATE outbox_messages SET published_at=$6,publication_ref=$7,claim_owner=NULL,claim_until=NULL
		WHERE tenant_id=$1 AND outbox_id=$2 AND claim_owner=$3 AND claim_until=$4 AND attempt_count=$5
		  AND published_at IS NULL AND dead_lettered_at IS NULL`
	return p.ownedUpdate(ctx, query, "mark outbox published", m, now, ref)
}

func (p *OutboxPublisher) markDeadLetter(ctx context.Context, m claimedOutbox, class PublicationErrorClass, now time.Time) error {
	const query = `UPDATE outbox_messages SET dead_lettered_at=$6,dead_letter_reason=$7,last_error_class=$8,last_error_at=$6,
		claim_owner=NULL,claim_until=NULL WHERE tenant_id=$1 AND outbox_id=$2 AND claim_owner=$3 AND claim_until=$4
		AND attempt_count=$5 AND published_at IS NULL AND dead_lettered_at IS NULL`
	return p.ownedUpdate(ctx, query, "dead-letter outbox message", m, now, "delivery_"+string(class), string(class))
}

func (p *OutboxPublisher) releaseForRetry(ctx context.Context, m claimedOutbox, class PublicationErrorClass, now time.Time) error {
	delay := p.backoff(m.AttemptCount)
	const query = `UPDATE outbox_messages SET available_at=$6,last_error_class=$7,last_error_at=$8,
		claim_owner=NULL,claim_until=NULL WHERE tenant_id=$1 AND outbox_id=$2 AND claim_owner=$3 AND claim_until=$4
		AND attempt_count=$5 AND published_at IS NULL AND dead_lettered_at IS NULL`
	return p.ownedUpdate(ctx, query, "release outbox message for retry", m, now.Add(delay), string(class), now)
}

func (p *OutboxPublisher) ownedUpdate(ctx context.Context, query, operation string, m claimedOutbox, args ...any) error {
	base := []any{m.TenantID, m.OutboxID, p.cfg.OwnerID, m.ClaimUntil, m.AttemptCount}
	tag, err := p.db.Exec(ctx, query, append(base, args...)...)
	if err != nil {
		return repositoryError(operation, err)
	}
	if tag.RowsAffected() != 1 {
		return domain.NewError(domain.CodeConflict, "outbox lease is no longer current", nil)
	}
	return nil
}

func (p *OutboxPublisher) backoff(attempt int) time.Duration {
	delay := p.cfg.BaseBackoff
	for i := 1; i < attempt && delay < p.cfg.MaxBackoff; i++ {
		if delay > p.cfg.MaxBackoff/2 {
			delay = p.cfg.MaxBackoff
		} else {
			delay *= 2
		}
	}
	if delay > p.cfg.MaxBackoff {
		delay = p.cfg.MaxBackoff
	}
	factor := 1 - p.cfg.Jitter + 2*p.cfg.Jitter*p.cfg.Random()
	return time.Duration(float64(delay) * factor)
}

func classifyPublicationError(err error) PublicationErrorClass {
	var typed *PublicationError
	if errors.As(err, &typed) {
		switch typed.Class {
		case PublicationTransient, PublicationPermanent, PublicationPoison, PublicationUnknown:
			return typed.Class
		}
	}
	return PublicationUnknown
}

type DeadLetter struct {
	Publication
	AttemptCount   int
	ErrorClass     PublicationErrorClass
	DeadLetteredAt time.Time
	Reason         string
}

func (p *OutboxPublisher) DeadLetters(ctx context.Context, tenantID string, limit int) ([]DeadLetter, error) {
	id, err := domain.ParseUUID(tenantID)
	if err != nil || limit < 1 || limit > 1000 {
		return nil, domain.NewError(domain.CodeInvalidArgument, "dead-letter query is invalid", err)
	}
	const query = `SELECT tenant_id,outbox_id,event_id,topic,event_type,safe_payload,payload_digest,created_at,
		attempt_count,last_error_class,dead_lettered_at,dead_letter_reason FROM outbox_messages
		WHERE tenant_id=$1 AND dead_lettered_at IS NOT NULL ORDER BY dead_lettered_at DESC,outbox_id LIMIT $2`
	rows, err := p.db.Query(ctx, query, id.String(), limit)
	if err != nil {
		return nil, repositoryError("list outbox dead letters", err)
	}
	defer rows.Close()
	result := make([]DeadLetter, 0)
	for rows.Next() {
		var item DeadLetter
		if err := rows.Scan(&item.TenantID, &item.OutboxID, &item.EventID, &item.Topic, &item.EventType, &item.SafePayload,
			&item.PayloadDigest, &item.CreatedAt, &item.AttemptCount, &item.ErrorClass, &item.DeadLetteredAt, &item.Reason); err != nil {
			return nil, repositoryError("scan outbox dead letter", err)
		}
		result = append(result, item)
	}
	return result, repositoryError("list outbox dead letters", rows.Err())
}

type outboxMetrics struct {
	claims        prometheus.Counter
	results       *prometheus.CounterVec
	duration, lag prometheus.Observer
}

func newOutboxMetrics(registerer prometheus.Registerer) (*outboxMetrics, error) {
	claims := prometheus.NewCounter(prometheus.CounterOpts{Namespace: "thinkpixeltg", Subsystem: "outbox", Name: "claims_total", Help: "Outbox messages claimed for publication."})
	results := prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "thinkpixeltg", Subsystem: "outbox", Name: "results_total", Help: "Outbox processing results."}, []string{"result"})
	duration := prometheus.NewHistogram(prometheus.HistogramOpts{Namespace: "thinkpixeltg", Subsystem: "outbox", Name: "publish_duration_seconds", Help: "Outbox sink publication duration."})
	lag := prometheus.NewHistogram(prometheus.HistogramOpts{Namespace: "thinkpixeltg", Subsystem: "outbox", Name: "lag_seconds", Help: "Age of an outbox message when claimed."})
	if registerer != nil {
		for _, collector := range []prometheus.Collector{claims, results, duration, lag} {
			if err := registerer.Register(collector); err != nil {
				return nil, fmt.Errorf("register outbox metric: %w", err)
			}
		}
	}
	return &outboxMetrics{claims: claims, results: results, duration: duration, lag: lag}, nil
}
