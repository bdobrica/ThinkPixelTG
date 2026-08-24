//go:build integration

package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
)

func TestAttemptClaimingConcurrencyAndFencingIntegration(t *testing.T) {
	ctx := context.Background()
	pool := repositoryTestPool(t, ctx)
	const tenantID = "019b0000-0000-7000-8000-000000000301"
	const invocationID = "019b0000-0000-7000-8000-000000000302"
	now := time.Now().UTC().Truncate(time.Microsecond)
	digest := make([]byte, 32)
	seedAcquisitionCatalog(t, ctx, pool, tenantID, now, digest)
	if _, err := pool.Exec(ctx, `INSERT INTO invocations (tenant_id,invocation_id,run_id,tool_call_id,tool_id,tool_version,
		 argument_profile,argument_digest,resource_projection,resource_digest,retry_class,state,created_at,updated_at)
		VALUES ($1,$4,'run-1','call-1','github.issue_create','1','jcs-v1',$3,'{}',$3,'safe','ready',$2,$2)`,
		tenantID, now, digest, invocationID); err != nil {
		t.Fatalf("seed attempt claim: %v", err)
	}
	claimer, err := NewAttemptClaimer(pool, tenantID)
	if err != nil {
		t.Fatalf("create attempt claimer: %v", err)
	}

	const workers = 8
	start := make(chan struct{})
	results := make(chan AttemptClaim, workers)
	errorsCh := make(chan error, workers)
	var wait sync.WaitGroup
	for i := 0; i < workers; i++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			<-start
			claim, claimErr := claimer.Claim(ctx, AttemptClaimRequest{InvocationID: invocationID,
				OwnerID: "worker-" + string(rune('a'+worker)), Now: now, LeaseDuration: time.Minute})
			results <- claim
			errorsCh <- claimErr
		}(i)
	}
	close(start)
	wait.Wait()
	close(results)
	close(errorsCh)
	for claimErr := range errorsCh {
		if claimErr != nil {
			t.Fatalf("concurrent claim: %v", claimErr)
		}
	}
	var winner Attempt
	claimed, busy := 0, 0
	for result := range results {
		switch result.Kind {
		case AttemptClaimed:
			claimed++
			winner = result.Attempt
		case AttemptClaimBusy:
			busy++
		default:
			t.Fatalf("unexpected claim kind %q", result.Kind)
		}
	}
	if claimed != 1 || busy != workers-1 || winner.AttemptNo != 1 || winner.Fence != 1 {
		t.Fatalf("claimed=%d busy=%d winner=%#v", claimed, busy, winner)
	}

	current, err := claimer.IsCurrent(ctx, invocationID, winner.AttemptNo, winner.Fence, *winner.OwnerID, now)
	if err != nil || !current {
		t.Fatalf("winning fence current=%v err=%v", current, err)
	}
	recovered, err := claimer.Claim(ctx, AttemptClaimRequest{InvocationID: invocationID,
		OwnerID: "recovery-worker", Now: now.Add(time.Minute), LeaseDuration: time.Minute})
	if err != nil || recovered.Kind != AttemptClaimed || recovered.Attempt.AttemptNo != 2 || recovered.Attempt.Fence != 2 {
		t.Fatalf("recovered claim=%#v err=%v", recovered, err)
	}
	current, err = claimer.IsCurrent(ctx, invocationID, winner.AttemptNo, winner.Fence, *winner.OwnerID, now.Add(time.Minute))
	if err != nil || current {
		t.Fatalf("stale fence current=%v err=%v", current, err)
	}

	stale := AttemptFinalization{InvocationID: invocationID, AttemptNo: winner.AttemptNo, Fence: winner.Fence,
		OwnerID: *winner.OwnerID, Now: now.Add(time.Minute), OutcomeClassification: "confirmed_success"}
	var domainErr *domain.Error
	if err := claimer.Finalize(ctx, stale); !errors.As(err, &domainErr) || domainErr.Code != domain.CodeConflict {
		t.Fatalf("stale finalization error=%v", err)
	}
	final := AttemptFinalization{InvocationID: invocationID, AttemptNo: recovered.Attempt.AttemptNo,
		Fence: recovered.Attempt.Fence, OwnerID: *recovered.Attempt.OwnerID,
		Now: now.Add(90 * time.Second), OutcomeClassification: "confirmed_success"}
	if err := claimer.Finalize(ctx, final); err != nil {
		t.Fatalf("current finalization: %v", err)
	}
	if err := claimer.Finalize(ctx, final); !errors.As(err, &domainErr) || domainErr.Code != domain.CodeConflict {
		t.Fatalf("duplicate finalization error=%v", err)
	}
}
