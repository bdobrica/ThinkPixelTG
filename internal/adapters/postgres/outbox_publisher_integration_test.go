//go:build integration

package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestOutboxPublisherIntegrationRetryDeadLetterAndVisibility(t *testing.T) {
	ctx := context.Background()
	pool := repositoryTestPool(t, ctx)
	const tenantID = "019b0000-0000-7000-8000-000000000401"
	const outboxID = "019b0000-0000-7000-8000-000000000402"
	seedPublisherMessage(t, ctx, pool, tenantID, outboxID)
	now := time.Now().UTC()
	sink := &recordingSink{err: &PublicationError{Class: PublicationTransient, Err: errors.New("temporary")}}
	publisher := newIntegrationPublisher(t, pool, sink, "publisher-retry", &now, 2)

	processed, err := publisher.PublishOne(ctx)
	if err != nil || !processed {
		t.Fatalf("first publication processed=%v err=%v", processed, err)
	}
	now = now.Add(2 * time.Second)
	processed, err = publisher.PublishOne(ctx)
	if err != nil || !processed {
		t.Fatalf("second publication processed=%v err=%v", processed, err)
	}

	dead, err := publisher.DeadLetters(ctx, tenantID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(dead) != 1 || dead[0].OutboxID != outboxID || dead[0].AttemptCount != 2 ||
		dead[0].ErrorClass != PublicationTransient || dead[0].Reason != "delivery_transient" {
		t.Fatalf("dead letters = %#v", dead)
	}
	processed, err = publisher.PublishOne(ctx)
	if err != nil || processed {
		t.Fatalf("dead letter reclaimed: processed=%v err=%v", processed, err)
	}
}

func TestOutboxPublisherIntegrationCrashAfterSendReplaysStableIdentity(t *testing.T) {
	pool := repositoryTestPool(t, context.Background())
	const tenantID = "019b0000-0000-7000-8000-000000000411"
	const outboxID = "019b0000-0000-7000-8000-000000000412"
	seedPublisherMessage(t, context.Background(), pool, tenantID, outboxID)
	now := time.Now().UTC()
	crashCtx, cancel := context.WithCancel(context.Background())
	sink := &recordingSink{afterPublish: cancel}
	first := newIntegrationPublisher(t, pool, sink, "publisher-crashed", &now, 3)
	processed, err := first.PublishOne(crashCtx)
	if !processed || !errors.Is(err, context.Canceled) {
		t.Fatalf("crash simulation processed=%v err=%v", processed, err)
	}

	// The sink accepted the event, but the claimed row was not acknowledged.
	// After lease expiry another owner must replay the identical outbox identity.
	now = now.Add(2 * time.Minute)
	sink.afterPublish = nil
	second := newIntegrationPublisher(t, pool, sink, "publisher-recovery", &now, 3)
	processed, err = second.PublishOne(context.Background())
	if err != nil || !processed {
		t.Fatalf("recovery processed=%v err=%v", processed, err)
	}
	if got := sink.ids(); len(got) != 2 || got[0] != outboxID || got[1] != outboxID {
		t.Fatalf("sink identities = %v", got)
	}
	var attempts int
	var published *time.Time
	if err := pool.QueryRow(context.Background(), `SELECT attempt_count,published_at FROM outbox_messages WHERE tenant_id=$1 AND outbox_id=$2`, tenantID, outboxID).Scan(&attempts, &published); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || published == nil {
		t.Fatalf("attempts=%d published=%v", attempts, published)
	}
}

func seedPublisherMessage(t *testing.T, ctx context.Context, db DBTX, tenantID, outboxID string) {
	t.Helper()
	if _, err := db.Exec(ctx, `INSERT INTO tenants (tenant_id,created_at) VALUES ($1,now())`, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO outbox_messages
		(tenant_id,outbox_id,event_id,topic,event_type,safe_payload,payload_digest,created_at,available_at)
		VALUES ($1,$2,$3,'evidence','test.event','{"safe":true}',$4,now(),now())`, tenantID, outboxID,
		outboxID, make([]byte, 32)); err != nil {
		t.Fatal(err)
	}
}

func newIntegrationPublisher(t *testing.T, db AcquisitionDatabase, sink PublicationSink, owner string, now *time.Time, attempts int) *OutboxPublisher {
	t.Helper()
	publisher, err := NewOutboxPublisher(db, sink, OutboxPublisherConfig{OwnerID: owner, LeaseDuration: time.Minute,
		MaxAttempts: attempts, BaseBackoff: time.Second, MaxBackoff: time.Minute, Jitter: 0,
		Now: func() time.Time { return *now }, Random: func() float64 { return .5 }}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return publisher
}

type recordingSink struct {
	mu           sync.Mutex
	published    []string
	err          error
	afterPublish func()
}

func (s *recordingSink) Publish(_ context.Context, message Publication) (string, error) {
	s.mu.Lock()
	s.published = append(s.published, message.OutboxID)
	after, err := s.afterPublish, s.err
	s.mu.Unlock()
	if after != nil {
		after()
	}
	if err != nil {
		return "", err
	}
	return "sink:" + message.OutboxID, nil
}

func (s *recordingSink) ids() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.published...)
}
