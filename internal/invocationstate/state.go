package invocationstate

import (
	"errors"
	"fmt"
	"math"
)

type State string

const (
	Received           State = "received"
	Validated          State = "validated"
	Authorized         State = "authorized"
	PreToolPassed      State = "pre_tool_passed"
	WaitingForApproval State = "waiting_for_approval"
	Ready              State = "ready"
	Executing          State = "executing"
	RetryWait          State = "retry_wait"
	Reconciling        State = "reconciling"
	Ambiguous          State = "ambiguous"
	ManualReview       State = "manual_review"
	PostTool           State = "post_tool"
	Succeeded          State = "succeeded"
	Failed             State = "failed"
	Denied             State = "denied"
	Blocked            State = "blocked"
	Cancelled          State = "cancelled"
)

type Actor string

const (
	Ingress      Actor = "ingress"
	Orchestrator Actor = "orchestrator"
	Worker       Actor = "worker"
	Reconciler   Actor = "reconciler"
	Operator     Actor = "operator"
	AGAdapter    Actor = "ag_adapter"
	GRAdapter    Actor = "gr_adapter"
)

var (
	ErrInvalidState      = errors.New("invalid invocation state")
	ErrInvalidActor      = errors.New("invalid invocation actor")
	ErrVersionConflict   = errors.New("invocation state version conflict")
	ErrVersionExhausted  = errors.New("invocation state version exhausted")
	ErrTerminalImmutable = errors.New("terminal invocation state is immutable")
	ErrIllegalTransition = errors.New("illegal invocation state transition")
	ErrActorForbidden    = errors.New("actor may not perform invocation transition")
	ErrApprovalRequired  = errors.New("current approval is required")
	ErrCurrentFence      = errors.New("current attempt fence is required")
	ErrResolution        = errors.New("append-only manual resolution is required")
)

type Snapshot struct {
	State   State
	Version int64
}

// Command contains facts already authenticated or verified by the caller.
// Transition still fails closed when a transition requiring one is missing it.
type Command struct {
	Actor              Actor
	Target             State
	ExpectedVersion    int64
	ApprovalSatisfied  bool
	CurrentFence       bool
	ResolutionRecorded bool
}

func (state State) Valid() bool {
	switch state {
	case Received, Validated, Authorized, PreToolPassed, WaitingForApproval,
		Ready, Executing, RetryWait, Reconciling, Ambiguous, ManualReview,
		PostTool, Succeeded, Failed, Denied, Blocked, Cancelled:
		return true
	default:
		return false
	}
}

func (state State) Terminal() bool {
	switch state {
	case Succeeded, Failed, Denied, Blocked, Cancelled:
		return true
	default:
		return false
	}
}

func (actor Actor) Valid() bool {
	switch actor {
	case Ingress, Orchestrator, Worker, Reconciler, Operator, AGAdapter, GRAdapter:
		return true
	default:
		return false
	}
}

// Transition validates a command and returns a new snapshot. The input is never
// mutated. Even a same-state command is illegal and cannot consume a version.
func Transition(current Snapshot, command Command) (Snapshot, error) {
	if !current.State.Valid() || !command.Target.Valid() || current.Version < 0 {
		return current, ErrInvalidState
	}
	if !command.Actor.Valid() {
		return current, ErrInvalidActor
	}
	if command.ExpectedVersion != current.Version {
		return current, ErrVersionConflict
	}
	if current.State.Terminal() {
		return current, ErrTerminalImmutable
	}
	if current.Version == math.MaxInt64 {
		return current, ErrVersionExhausted
	}
	rule, ok := rules[edge{current.State, command.Target}]
	if !ok {
		return current, ErrIllegalTransition
	}
	if !rule.actors[command.Actor] {
		return current, ErrActorForbidden
	}
	if rule.approval && !command.ApprovalSatisfied {
		return current, ErrApprovalRequired
	}
	if rule.fence && !command.CurrentFence {
		return current, ErrCurrentFence
	}
	if rule.resolution && !command.ResolutionRecorded {
		return current, ErrResolution
	}
	return Snapshot{State: command.Target, Version: current.Version + 1}, nil
}

type edge struct{ from, to State }
type rule struct {
	actors     map[Actor]bool
	approval   bool
	fence      bool
	resolution bool
}

func allow(actors ...Actor) rule {
	set := make(map[Actor]bool, len(actors))
	for _, actor := range actors {
		set[actor] = true
	}
	return rule{actors: set}
}

func withApproval(value rule) rule   { value.approval = true; return value }
func withFence(value rule) rule      { value.fence = true; return value }
func withResolution(value rule) rule { value.resolution = true; return value }

var rules = map[edge]rule{
	{Received, Validated}:               allow(Ingress, Orchestrator),
	{Received, Failed}:                  allow(Ingress, Orchestrator),
	{Received, Cancelled}:               allow(Ingress, Orchestrator),
	{Validated, Authorized}:             allow(Orchestrator),
	{Validated, Failed}:                 allow(Ingress, Orchestrator),
	{Validated, Denied}:                 allow(Orchestrator),
	{Validated, Cancelled}:              allow(Ingress, Orchestrator),
	{Authorized, PreToolPassed}:         allow(Orchestrator),
	{Authorized, Validated}:             allow(Orchestrator),
	{Authorized, Failed}:                allow(Orchestrator),
	{Authorized, Blocked}:               allow(Orchestrator),
	{Authorized, Cancelled}:             allow(Ingress, Orchestrator),
	{PreToolPassed, WaitingForApproval}: allow(Orchestrator),
	{PreToolPassed, Ready}:              allow(Orchestrator),
	{PreToolPassed, Validated}:          allow(Orchestrator),
	{PreToolPassed, Failed}:             allow(Orchestrator),
	{PreToolPassed, Blocked}:            allow(Orchestrator),
	{PreToolPassed, Cancelled}:          allow(Ingress, Orchestrator),
	{WaitingForApproval, Ready}:         withApproval(allow(Orchestrator)),
	{WaitingForApproval, Validated}:     allow(Orchestrator),
	{WaitingForApproval, Failed}:        allow(Orchestrator),
	{WaitingForApproval, Blocked}:       allow(Orchestrator),
	{WaitingForApproval, Cancelled}:     allow(Ingress, Orchestrator),
	{Ready, Executing}:                  withFence(allow(Worker)),
	{Ready, Failed}:                     allow(Orchestrator),
	{Ready, Cancelled}:                  allow(Ingress, Orchestrator),
	{Executing, PostTool}:               withFence(allow(Worker)),
	{Executing, RetryWait}:              withFence(allow(Worker)),
	{Executing, Ambiguous}:              withFence(allow(Worker)),
	{Executing, Failed}:                 withFence(allow(Worker)),
	{RetryWait, Executing}:              withFence(allow(Worker)),
	{RetryWait, Failed}:                 allow(Orchestrator),
	{RetryWait, Cancelled}:              allow(Orchestrator),
	{Ambiguous, Reconciling}:            withFence(allow(Reconciler)),
	{Ambiguous, ManualReview}:           allow(Orchestrator, Reconciler),
	{Reconciling, PostTool}:             withFence(allow(Reconciler)),
	{Reconciling, RetryWait}:            withFence(allow(Reconciler)),
	{Reconciling, ManualReview}:         withFence(allow(Reconciler)),
	{Reconciling, Failed}:               withFence(allow(Reconciler)),
	{Reconciling, Cancelled}:            withFence(allow(Reconciler)),
	{ManualReview, PostTool}:            withResolution(allow(Operator)),
	{ManualReview, RetryWait}:           withResolution(allow(Operator)),
	{ManualReview, Failed}:              withResolution(allow(Operator)),
	{ManualReview, Cancelled}:           withResolution(allow(Operator)),
	{PostTool, Succeeded}:               allow(Orchestrator),
	{PostTool, Failed}:                  allow(Orchestrator),
	{PostTool, Blocked}:                 allow(Orchestrator),
}

func (snapshot Snapshot) String() string {
	return fmt.Sprintf("%s@%d", snapshot.State, snapshot.Version)
}
