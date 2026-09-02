package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

func TestToolCallQueryUsesTrustedScope(t *testing.T) {
	identity := queryIdentity()
	reader := toolCallReaderFunc(func(_ context.Context, got ports.InvocationIdentity, id string) (ports.ToolCallView, error) {
		if got != identity || id != "call-0001" {
			t.Fatalf("scope = %#v, %q", got, id)
		}
		return ports.ToolCallView{ToolCallID: id, State: "executing"}, nil
	})
	service, err := NewToolCallQueryService(reader)
	if err != nil {
		t.Fatal(err)
	}
	view, err := service.Get(context.Background(), identity, "call-0001")
	if err != nil || view.State != "executing" {
		t.Fatalf("Get() = %#v, %v", view, err)
	}
}

func TestToolCallQueryMakesMalformedAndMissingIDsEquivalent(t *testing.T) {
	reader := toolCallReaderFunc(func(context.Context, ports.InvocationIdentity, string) (ports.ToolCallView, error) {
		return ports.ToolCallView{}, domain.NewError(domain.CodeNotFound, "database miss", nil)
	})
	service, _ := NewToolCallQueryService(reader)
	for _, id := range []string{"bad", "missing-0001"} {
		_, err := service.Get(context.Background(), queryIdentity(), id)
		var classified *domain.Error
		if !errors.As(err, &classified) || classified.Code != domain.CodeToolCallNotFound || classified.Message != "tool call was not found" {
			t.Fatalf("Get(%q) error = %v", id, err)
		}
	}
}

func TestToolCallQueryRejectsMismatchedReaderProjection(t *testing.T) {
	service, _ := NewToolCallQueryService(toolCallReaderFunc(func(context.Context, ports.InvocationIdentity, string) (ports.ToolCallView, error) {
		return ports.ToolCallView{ToolCallID: "other-0001", CreatedAt: time.Now()}, nil
	}))
	_, err := service.Get(context.Background(), queryIdentity(), "call-0001")
	var classified *domain.Error
	if !errors.As(err, &classified) || classified.Code != domain.CodeInternal {
		t.Fatalf("Get() error = %v", err)
	}
}

func TestToolCallQuerySuppressesResultBeforeSuccess(t *testing.T) {
	service, _ := NewToolCallQueryService(toolCallReaderFunc(func(context.Context, ports.InvocationIdentity, string) (ports.ToolCallView, error) {
		return ports.ToolCallView{ToolCallID: "call-0001", State: "post_tool", Result: []byte(`{"unreviewed":true}`)}, nil
	}))
	view, err := service.Get(context.Background(), queryIdentity(), "call-0001")
	if err != nil || len(view.Result) != 0 {
		t.Fatalf("Get() = %#v, %v", view, err)
	}
}

func queryIdentity() ports.InvocationIdentity {
	return ports.InvocationIdentity{TenantID: "tenant", Subject: "subject", AgentID: "agent", AgentVersion: "1", RunID: "run", WorkloadID: "workload"}
}

type toolCallReaderFunc func(context.Context, ports.InvocationIdentity, string) (ports.ToolCallView, error)

func (function toolCallReaderFunc) GetToolCall(ctx context.Context, identity ports.InvocationIdentity, id string) (ports.ToolCallView, error) {
	return function(ctx, identity, id)
}
