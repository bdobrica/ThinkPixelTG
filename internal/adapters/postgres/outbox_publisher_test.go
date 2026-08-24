package postgres

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestOutboxPublisherConfiguration(t *testing.T) {
	t.Parallel()
	valid := OutboxPublisherConfig{OwnerID: "publisher-1", LeaseDuration: time.Minute, MaxAttempts: 3,
		BaseBackoff: time.Second, MaxBackoff: time.Minute, Jitter: .2}
	for name, mutate := range map[string]func(*OutboxPublisherConfig){
		"owner":          func(c *OutboxPublisherConfig) { c.OwnerID = "" },
		"lease":          func(c *OutboxPublisherConfig) { c.LeaseDuration = 0 },
		"attempts":       func(c *OutboxPublisherConfig) { c.MaxAttempts = 0 },
		"backoff":        func(c *OutboxPublisherConfig) { c.BaseBackoff = 0 },
		"backoff bounds": func(c *OutboxPublisherConfig) { c.MaxBackoff = time.Millisecond },
		"jitter":         func(c *OutboxPublisherConfig) { c.Jitter = 1.1 },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := valid
			mutate(&cfg)
			if _, err := NewOutboxPublisher(&protectedDatabase{fakeBeginner: &fakeBeginner{}}, sinkFunc(nil), cfg, nil); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
}

func TestOutboxBackoffIsBoundedAndJittered(t *testing.T) {
	t.Parallel()
	p := &OutboxPublisher{cfg: OutboxPublisherConfig{BaseBackoff: time.Second, MaxBackoff: 8 * time.Second, Jitter: .25, Random: func() float64 { return 1 }}}
	for attempt, want := range map[int]time.Duration{1: 1250 * time.Millisecond, 2: 2500 * time.Millisecond, 4: 10 * time.Second, 30: 10 * time.Second} {
		if got := p.backoff(attempt); got != want {
			t.Errorf("attempt %d backoff = %s, want %s", attempt, got, want)
		}
	}
}

func TestPublicationErrorClassification(t *testing.T) {
	t.Parallel()
	if got := classifyPublicationError(errors.New("opaque")); got != PublicationUnknown {
		t.Fatalf("opaque = %q", got)
	}
	wrapped := errors.Join(errors.New("context"), &PublicationError{Class: PublicationPoison, Err: errors.New("malformed")})
	if got := classifyPublicationError(wrapped); got != PublicationPoison {
		t.Fatalf("typed = %q", got)
	}
	invalid := &PublicationError{Class: "secret-dependent-class", Err: errors.New("bad")}
	if got := classifyPublicationError(invalid); got != PublicationUnknown {
		t.Fatalf("unbounded class = %q", got)
	}
}

type sinkFunc func(Publication) (string, error)

func (fn sinkFunc) Publish(_ context.Context, publication Publication) (string, error) {
	return fn(publication)
}
