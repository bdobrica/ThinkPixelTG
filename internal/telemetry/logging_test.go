package telemetry

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestLoggerCorrelatesAndRecursivelyRedacts(t *testing.T) {
	const canary = "SECRET_CANARY_6ac8"
	var output bytes.Buffer
	logger := NewLogger(slog.NewJSONHandler(&output, nil))
	ctx := WithCorrelation(context.Background(), "request-7", "trace-9")
	err := LogEvent(ctx, logger, slog.LevelInfo, "http.request_completed",
		slog.Any("payload", map[string]any{
			"safe":   "visible",
			"nested": map[string]any{"access_token": canary},
		}),
		slog.Group("headers", slog.String("Authorization", "Bearer "+canary)),
	)
	if err != nil {
		t.Fatal(err)
	}
	logged := output.String()
	for _, expected := range []string{"request-7", "trace-9", "http.request_completed", "visible", Redacted} {
		if !strings.Contains(logged, expected) {
			t.Errorf("missing %q in %s", expected, logged)
		}
	}
	if strings.Contains(logged, canary) || strings.Contains(logged, "Bearer") {
		t.Fatalf("secret leaked: %s", logged)
	}
}

func TestLoggerRedactsWithAttrs(t *testing.T) {
	const canary = "SECRET_CANARY_b811"
	var output bytes.Buffer
	logger := NewLogger(slog.NewJSONHandler(&output, nil)).With("credentials", map[string]string{"password": canary})
	if err := LogEvent(context.Background(), logger, slog.LevelInfo, "service.started"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), canary) {
		t.Fatalf("secret leaked: %s", output.String())
	}
}

func TestLogEventRejectsUnstableName(t *testing.T) {
	logger := NewLogger(slog.NewTextHandler(&bytes.Buffer{}, nil))
	if err := LogEvent(context.Background(), logger, slog.LevelInfo, "User supplied event"); err == nil {
		t.Fatal("expected error")
	}
}

func TestCorrelationBoundsCallerValues(t *testing.T) {
	ctx := WithCorrelation(context.Background(), strings.Repeat("r", 200), strings.Repeat("t", 100))
	requestID, traceID := Correlation(ctx)
	if len(requestID) != 128 || len(traceID) != 64 {
		t.Fatalf("lengths = %d, %d", len(requestID), len(traceID))
	}
}
