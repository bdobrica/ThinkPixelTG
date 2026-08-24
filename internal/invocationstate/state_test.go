package invocationstate

import (
	"errors"
	"math"
	"testing"
)

func TestLegalTransitions(t *testing.T) {
	tests := []struct {
		name  string
		from  State
		to    State
		actor Actor
		facts Command
	}{
		{"ingress validates", Received, Validated, Ingress, Command{}},
		{"authorization", Validated, Authorized, Orchestrator, Command{}},
		{"approval requested", PreToolPassed, WaitingForApproval, Orchestrator, Command{}},
		{"approval accepted", WaitingForApproval, Ready, Orchestrator, Command{ApprovalSatisfied: true}},
		{"approval not required", PreToolPassed, Ready, Orchestrator, Command{}},
		{"worker claims", Ready, Executing, Worker, Command{CurrentFence: true}},
		{"safe retry", Executing, RetryWait, Worker, Command{CurrentFence: true}},
		{"retry execution", RetryWait, Executing, Worker, Command{CurrentFence: true}},
		{"unknown outcome", Executing, Ambiguous, Worker, Command{CurrentFence: true}},
		{"reconciliation begins", Ambiguous, Reconciling, Reconciler, Command{CurrentFence: true}},
		{"reconciled success", Reconciling, PostTool, Reconciler, Command{CurrentFence: true}},
		{"manual escalation", Ambiguous, ManualReview, Orchestrator, Command{}},
		{"operator proves unapplied", ManualReview, RetryWait, Operator, Command{ResolutionRecorded: true}},
		{"post tool success", PostTool, Succeeded, Orchestrator, Command{}},
		{"guardrail transformation", Authorized, Validated, Orchestrator, Command{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := test.facts
			command.Actor, command.Target, command.ExpectedVersion = test.actor, test.to, 7
			next, err := Transition(Snapshot{State: test.from, Version: 7}, command)
			if err != nil {
				t.Fatal(err)
			}
			if next != (Snapshot{State: test.to, Version: 8}) {
				t.Fatalf("next = %v", next)
			}
		})
	}
}

func TestIllegalTransitionsAndPermissionsFailClosed(t *testing.T) {
	tests := []struct {
		name     string
		from, to State
		actor    Actor
		facts    Command
		want     error
	}{
		{"cannot skip validation", Received, Ready, Orchestrator, Command{}, ErrIllegalTransition},
		{"same state", Ready, Ready, Worker, Command{CurrentFence: true}, ErrIllegalTransition},
		{"adapter cannot authorize", Validated, Authorized, AGAdapter, Command{}, ErrActorForbidden},
		{"adapter cannot apply guardrail", Authorized, PreToolPassed, GRAdapter, Command{}, ErrActorForbidden},
		{"orchestrator cannot execute", Ready, Executing, Orchestrator, Command{CurrentFence: true}, ErrActorForbidden},
		{"worker requires fence", Ready, Executing, Worker, Command{}, ErrCurrentFence},
		{"approval cannot be assumed", WaitingForApproval, Ready, Orchestrator, Command{}, ErrApprovalRequired},
		{"manual review requires operator", ManualReview, Failed, Reconciler, Command{ResolutionRecorded: true}, ErrActorForbidden},
		{"manual review requires record", ManualReview, Failed, Operator, Command{}, ErrResolution},
		{"ambiguity cannot become failure", Ambiguous, Failed, Reconciler, Command{CurrentFence: true}, ErrIllegalTransition},
		{"ambiguity cannot retry blindly", Ambiguous, RetryWait, Reconciler, Command{CurrentFence: true}, ErrIllegalTransition},
		{"post send cannot be cancelled by ingress", Executing, Cancelled, Ingress, Command{}, ErrIllegalTransition},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := test.facts
			command.Actor, command.Target, command.ExpectedVersion = test.actor, test.to, 3
			current := Snapshot{State: test.from, Version: 3}
			next, err := Transition(current, command)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if next != current {
				t.Fatalf("failure mutated snapshot: %v", next)
			}
		})
	}
}

func TestVersioningAndTerminalImmutability(t *testing.T) {
	current := Snapshot{State: Received, Version: 4}
	if next, err := Transition(current, Command{Actor: Ingress, Target: Validated, ExpectedVersion: 3}); !errors.Is(err, ErrVersionConflict) || next != current {
		t.Fatalf("stale transition = %v, %v", next, err)
	}

	for _, terminal := range []State{Succeeded, Failed, Denied, Blocked, Cancelled} {
		t.Run(string(terminal), func(t *testing.T) {
			value := Snapshot{State: terminal, Version: 9}
			next, err := Transition(value, Command{Actor: Orchestrator, Target: Failed, ExpectedVersion: 9})
			if !errors.Is(err, ErrTerminalImmutable) || next != value {
				t.Fatalf("transition = %v, %v", next, err)
			}
		})
	}

	exhausted := Snapshot{State: Received, Version: math.MaxInt64}
	if _, err := Transition(exhausted, Command{Actor: Ingress, Target: Validated, ExpectedVersion: math.MaxInt64}); !errors.Is(err, ErrVersionExhausted) {
		t.Fatalf("error = %v", err)
	}
	if _, err := Transition(Snapshot{State: Received, Version: -1}, Command{Actor: Ingress, Target: Validated, ExpectedVersion: -1}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("negative version error = %v", err)
	}
}

func FuzzTransitionNeverMutatesOnErrorOrSkipsVersion(f *testing.F) {
	f.Add("received", int64(0), "ingress", "validated", int64(0), false, false, false)
	f.Add("ambiguous", int64(8), "worker", "retry_wait", int64(8), true, false, false)
	f.Fuzz(func(t *testing.T, stateText string, version int64, actorText, targetText string,
		expected int64, fence, approval, resolution bool) {
		current := Snapshot{State: State(stateText), Version: version}
		next, err := Transition(current, Command{Actor: Actor(actorText), Target: State(targetText),
			ExpectedVersion: expected, CurrentFence: fence, ApprovalSatisfied: approval,
			ResolutionRecorded: resolution})
		if err != nil {
			if next != current {
				t.Fatalf("error mutated %v into %v", current, next)
			}
			return
		}
		if next.State != State(targetText) || next.Version != version+1 || next.State == current.State {
			t.Fatalf("invalid success %v -> %v", current, next)
		}
	})
}
