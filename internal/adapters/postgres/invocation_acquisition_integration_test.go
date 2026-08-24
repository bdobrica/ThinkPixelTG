//go:build integration

package postgres

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestLogicalInvocationAcquisitionReplayConflictAndRecoveryIntegration(t *testing.T) {
	ctx := context.Background()
	pool := repositoryTestPool(t, ctx)
	const tenantID = "019b0000-0000-7000-8000-000000000201"
	now := time.Now().UTC().Truncate(time.Microsecond)
	digest := bytes.Repeat([]byte{1}, 32)
	seedAcquisitionCatalog(t, ctx, pool, tenantID, now, digest)

	acquirer, err := NewLogicalInvocationAcquirer(pool, tenantID)
	if err != nil {
		t.Fatalf("create acquirer: %v", err)
	}
	request := AcquisitionRequest{
		Invocation: Invocation{
			ID: "019b0000-0000-7000-8000-000000000202", RunID: "run-1", ToolCallID: "call-1",
			ToolID: "github.issue_create", ToolVersion: "1", ArgumentProfile: "jcs-v1",
			ArgumentDigest: digest, ResourceProjection: []byte(`{}`), ResourceDigest: digest,
			RetryClass: "safe", State: "received", CreatedAt: now, UpdatedAt: now,
		},
		OwnerID: "owner-a", Now: now, LeaseDuration: time.Minute, MaxRecoveries: 1,
	}

	owned, err := acquirer.Acquire(ctx, request)
	if err != nil || owned.Kind != AcquisitionOwned {
		t.Fatalf("initial acquisition = %#v, %v", owned, err)
	}
	request.OwnerID = "owner-b"
	pending, err := acquirer.Acquire(ctx, request)
	if err != nil || pending.Kind != AcquisitionPending || pending.Invocation.ID != owned.Invocation.ID {
		t.Fatalf("active replay = %#v, %v", pending, err)
	}

	conflicting := request
	conflicting.Invocation.ToolVersion = "2"
	conflict, err := acquirer.Acquire(ctx, conflicting)
	if err != nil || conflict.Kind != AcquisitionConflict {
		t.Fatalf("mismatched replay = %#v, %v", conflict, err)
	}

	request.Now = now.Add(time.Minute)
	recovered, err := acquirer.Acquire(ctx, request)
	if err != nil || recovered.Kind != AcquisitionRecovered || recovered.Invocation.ID != owned.Invocation.ID {
		t.Fatalf("abandoned recovery = %#v, %v", recovered, err)
	}
	request.OwnerID = "owner-c"
	request.Now = request.Now.Add(time.Minute)
	exhausted, err := acquirer.Acquire(ctx, request)
	if err != nil || exhausted.Kind != AcquisitionExhausted {
		t.Fatalf("bounded recovery = %#v, %v", exhausted, err)
	}

	resultDigest := bytes.Repeat([]byte{2}, 32)
	if err := acquirer.Complete(ctx, "run-1", "call-1", "owner-b", 201, resultDigest, []byte(`{"ok":true}`), request.Now); err != nil {
		t.Fatalf("complete recovered acquisition: %v", err)
	}
	request.Now = request.Now.Add(time.Second)
	replay, err := acquirer.Acquire(ctx, request)
	if err != nil || replay.Kind != AcquisitionReplay || replay.ReplayStatusCode == nil || *replay.ReplayStatusCode != 201 ||
		!bytes.Equal(replay.ReplayResultDigest, resultDigest) {
		t.Fatalf("completed replay = %#v, %v", replay, err)
	}
}

func TestLogicalInvocationAcquisitionConcurrencyPropertiesIntegration(t *testing.T) {
	ctx := context.Background()
	pool := repositoryTestPool(t, ctx)
	const tenantID = "019b0000-0000-7000-8000-000000000211"
	now := time.Now().UTC().Truncate(time.Microsecond)
	digest := bytes.Repeat([]byte{3}, 32)
	seedAcquisitionCatalog(t, ctx, pool, tenantID, now, digest)
	acquirer, err := NewLogicalInvocationAcquirer(pool, tenantID)
	if err != nil {
		t.Fatalf("create acquirer: %v", err)
	}

	for scenario, workers := range []int{2, 3, 8, 16} {
		scenario, workers := scenario, workers
		t.Run(fmt.Sprintf("workers_%d", workers), func(t *testing.T) {
			request := AcquisitionRequest{Invocation: Invocation{
				ID:    fmt.Sprintf("019b0000-0000-7000-8000-%012d", 220+scenario),
				RunID: fmt.Sprintf("concurrent-run-%d", scenario), ToolCallID: "call-1",
				ToolID: "github.issue_create", ToolVersion: "1", ArgumentProfile: "jcs-v1",
				ArgumentDigest: digest, ResourceProjection: []byte(`{}`), ResourceDigest: digest,
				RetryClass: "safe", State: "received", CreatedAt: now, UpdatedAt: now,
			}, Now: now, LeaseDuration: time.Minute, MaxRecoveries: 1}

			results := concurrentAcquisitions(t, ctx, acquirer, request, workers)
			owned, pending := 0, 0
			var ownerID string
			for _, result := range results {
				switch result.kind {
				case AcquisitionOwned:
					owned++
					ownerID = result.ownerID
				case AcquisitionPending:
					pending++
				default:
					t.Fatalf("initial concurrent acquisition kind = %q", result.kind)
				}
			}
			if owned != 1 || pending != workers-1 {
				t.Fatalf("owned=%d pending=%d workers=%d", owned, pending, workers)
			}

			conflicting := request
			conflicting.OwnerID = "conflicting-worker"
			conflicting.Invocation.ArgumentDigest = bytes.Repeat([]byte{4}, 32)
			conflict, err := acquirer.Acquire(ctx, conflicting)
			if err != nil || conflict.Kind != AcquisitionConflict {
				t.Fatalf("conflicting replay = %#v, %v", conflict, err)
			}

			resultDigest := bytes.Repeat([]byte{byte(10 + scenario)}, 32)
			if err := acquirer.Complete(ctx, request.Invocation.RunID, request.Invocation.ToolCallID,
				ownerID, 200, resultDigest, []byte(`{"ok":true}`), now.Add(time.Second)); err != nil {
				t.Fatalf("complete winner: %v", err)
			}
			for _, result := range concurrentAcquisitions(t, ctx, acquirer, request, workers) {
				if result.kind != AcquisitionReplay || !bytes.Equal(result.replayDigest, resultDigest) {
					t.Fatalf("completed concurrent replay kind=%q digest=%x", result.kind, result.replayDigest)
				}
			}
		})
	}
}

func seedAcquisitionCatalog(t *testing.T, ctx context.Context, db DBTX, tenantID string, now time.Time, digest []byte) {
	t.Helper()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO tenants VALUES ($1,$2)`, []any{tenantID, now}},
		{`INSERT INTO tools VALUES ('github.issue_create',$1)`, []any{now}},
		{`INSERT INTO tool_versions VALUES ('github.issue_create','1','published','{}',$1,$2)`, []any{digest, now}},
	}
	for _, statement := range statements {
		if _, err := db.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("seed acquisition catalog: %v", err)
		}
	}
}

type concurrentAcquisitionResult struct {
	kind         AcquisitionKind
	ownerID      string
	replayDigest []byte
}

func concurrentAcquisitions(t *testing.T, ctx context.Context, acquirer *LogicalInvocationAcquirer, base AcquisitionRequest, workers int) []concurrentAcquisitionResult {
	t.Helper()
	start := make(chan struct{})
	results := make(chan concurrentAcquisitionResult, workers)
	errorsCh := make(chan error, workers)
	var wait sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			<-start
			request := base
			request.OwnerID = fmt.Sprintf("worker-%d", worker)
			result, err := acquirer.Acquire(ctx, request)
			results <- concurrentAcquisitionResult{result.Kind, request.OwnerID, result.ReplayResultDigest}
			errorsCh <- err
		}(worker)
	}
	close(start)
	wait.Wait()
	close(results)
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatalf("concurrent acquisition: %v", err)
		}
	}
	collected := make([]concurrentAcquisitionResult, 0, workers)
	for result := range results {
		collected = append(collected, result)
	}
	return collected
}
