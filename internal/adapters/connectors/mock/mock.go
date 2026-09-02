// Package mock provides a hermetic connector for application and integration
// tests. Its behavior is entirely configured in memory and it performs no I/O.
package mock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

type OperationKind string

const (
	Read  OperationKind = "read"
	Write OperationKind = "write"
)

type Outcome struct {
	Classification string
	Result         json.RawMessage
	TransportError bool
	// Applied controls whether an unknown write is visible to reconciliation.
	Applied bool
}

type Operation struct {
	Kind           OperationKind
	Delay          time.Duration
	Outcomes       []Outcome
	Reconciliation string
}

type Config struct {
	Operations map[string]Operation
}

// TransportError is a deterministic, content-free injected transport failure.
type TransportError struct{ Operation string }

func (e *TransportError) Error() string { return "mock connector transport error for " + e.Operation }

type record struct {
	operation string
	result    json.RawMessage
	applied   bool
}

type Connector struct {
	mu         sync.Mutex
	operations map[string]Operation
	calls      map[string]int
	records    map[string]record
}

var _ ports.ConnectorExecutor = (*Connector)(nil)
var _ ports.ConnectorReconciler = (*Connector)(nil)

func New(config Config) (*Connector, error) {
	if len(config.Operations) == 0 {
		return nil, errors.New("mock connector operations are required")
	}
	operations := make(map[string]Operation, len(config.Operations))
	for name, operation := range config.Operations {
		if name == "" || operation.Kind != Read && operation.Kind != Write || operation.Delay < 0 || len(operation.Outcomes) == 0 {
			return nil, fmt.Errorf("invalid mock connector operation %q", name)
		}
		if operation.Reconciliation != "" && !validReconciliation(operation.Reconciliation) {
			return nil, fmt.Errorf("invalid mock reconciliation outcome for %q", name)
		}
		operation.Outcomes = append([]Outcome(nil), operation.Outcomes...)
		for i := range operation.Outcomes {
			outcome := &operation.Outcomes[i]
			if !validClassification(outcome.Classification) || len(outcome.Result) != 0 && !json.Valid(outcome.Result) {
				return nil, fmt.Errorf("invalid mock outcome for %q", name)
			}
			outcome.Result = append(json.RawMessage(nil), outcome.Result...)
		}
		operations[name] = operation
	}
	return &Connector{operations: operations, calls: map[string]int{}, records: map[string]record{}}, nil
}

func (connector *Connector) Execute(ctx context.Context, request ports.ConnectorRequest) (ports.ConnectorResult, error) {
	if connector == nil || request.InvocationID == "" || request.Tool.Connector.ConnectorType != "mock" || !json.Valid(request.CanonicalArguments) {
		return ports.ConnectorResult{}, errors.New("invalid mock connector request")
	}
	operationName := request.Tool.Connector.Operation
	connector.mu.Lock()
	operation, ok := connector.operations[operationName]
	if !ok {
		connector.mu.Unlock()
		return ports.ConnectorResult{}, errors.New("unknown mock connector operation")
	}
	index := connector.calls[operationName]
	connector.calls[operationName] = index + 1
	if index >= len(operation.Outcomes) {
		index = len(operation.Outcomes) - 1
	}
	outcome := operation.Outcomes[index]
	connector.mu.Unlock()

	if operation.Delay > 0 {
		timer := time.NewTimer(operation.Delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ports.ConnectorResult{}, ctx.Err()
		case <-timer.C:
		}
	}

	result := append(json.RawMessage(nil), outcome.Result...)
	if operation.Kind == Write {
		connector.mu.Lock()
		connector.records[request.InvocationID] = record{operation: operationName, result: result, applied: outcome.Applied || outcome.Classification == "confirmed_success"}
		connector.mu.Unlock()
	}
	if outcome.TransportError {
		return ports.ConnectorResult{}, &TransportError{Operation: operationName}
	}
	return ports.ConnectorResult{Classification: outcome.Classification, Result: result}, nil
}

func (connector *Connector) Reconcile(ctx context.Context, request ports.ConnectorReconciliationRequest) (ports.ConnectorReconciliationResult, error) {
	if err := ctx.Err(); err != nil {
		return ports.ConnectorReconciliationResult{}, err
	}
	if connector == nil || request.InvocationID == "" || request.Tool.Connector.ConnectorType != "mock" || len(request.Evidence) != 0 && !json.Valid(request.Evidence) {
		return ports.ConnectorReconciliationResult{}, errors.New("invalid mock reconciliation request")
	}
	operationName := request.Tool.Connector.Operation
	connector.mu.Lock()
	operation, ok := connector.operations[operationName]
	recorded, found := connector.records[request.InvocationID]
	connector.mu.Unlock()
	if !ok || operation.Kind != Write || operation.Reconciliation == "" || found && recorded.operation != operationName {
		return ports.ConnectorReconciliationResult{}, errors.New("mock operation is not reconcilable")
	}
	outcome := operation.Reconciliation
	result := json.RawMessage(nil)
	if outcome == "confirmed_success" {
		if !found || !recorded.applied {
			outcome = "confirmed_not_applied"
		} else {
			result = append(result, recorded.result...)
		}
	}
	evidence, _ := json.Marshal(map[string]any{"mock_operation": operationName, "recorded": found, "applied": found && recorded.applied})
	return ports.ConnectorReconciliationResult{Outcome: outcome, Result: result, Evidence: evidence}, nil
}

func (connector *Connector) Calls(operation string) int {
	connector.mu.Lock()
	defer connector.mu.Unlock()
	return connector.calls[operation]
}

func validClassification(value string) bool {
	switch value {
	case "confirmed_success", "definitely_rejected", "not_sent", "transient_safe", "unknown", "cancelled_pre_send":
		return true
	default:
		return false
	}
}

func validReconciliation(value string) bool {
	switch value {
	case "confirmed_success", "confirmed_not_applied", "still_unknown", "unsafe_to_retry":
		return true
	default:
		return false
	}
}
