//go:build integration

package postgres

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func TestLogicalInvocationAcquisitionReplayConflictAndRecoveryIntegration(t *testing.T) {
	ctx := context.Background()
	pool := repositoryTestPool(t, ctx)
	const tenantID = "019b0000-0000-7000-8000-000000000201"
	now := time.Now().UTC().Truncate(time.Microsecond)
	digest := bytes.Repeat([]byte{1}, 32)
	if _, err := pool.Exec(ctx, `INSERT INTO tenants VALUES ($1,$2);
		INSERT INTO tools VALUES ('github.issue_create',$2);
		INSERT INTO tool_versions VALUES ('github.issue_create','1','published','{}',$3,$2)`, tenantID, now, digest); err != nil {
		t.Fatalf("seed acquisition catalog: %v", err)
	}

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
