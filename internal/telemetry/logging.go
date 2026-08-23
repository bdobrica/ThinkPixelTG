// Package telemetry provides safe structured logging and observability bootstrap.
package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"regexp"
	"strings"
)

type correlationKey uint8

const (
	requestIDKey correlationKey = iota
	traceIDKey
)

const (
	AttrEvent     = "event.name"
	AttrRequestID = "request.id"
	AttrTraceID   = "trace.id"
	Redacted      = "[REDACTED]"
)

var eventNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z][a-z0-9_]*)+$`)

func WithCorrelation(ctx context.Context, requestID, traceID string) context.Context {
	ctx = context.WithValue(ctx, requestIDKey, bounded(requestID, 128))
	return context.WithValue(ctx, traceIDKey, bounded(traceID, 64))
}

func Correlation(ctx context.Context) (requestID, traceID string) {
	requestID, _ = ctx.Value(requestIDKey).(string)
	traceID, _ = ctx.Value(traceIDKey).(string)
	return requestID, traceID
}

// NewLogger wraps every sink with recursive redaction.
func NewLogger(handler slog.Handler) *slog.Logger {
	return slog.New(&redactingHandler{next: handler})
}

// LogEvent emits a stable event name and bounded correlation attributes.
func LogEvent(ctx context.Context, logger *slog.Logger, level slog.Level, event string, attrs ...slog.Attr) error {
	if !eventNamePattern.MatchString(event) {
		return fmt.Errorf("invalid event name %q", event)
	}
	requestID, traceID := Correlation(ctx)
	base := []slog.Attr{slog.String(AttrEvent, event)}
	if requestID != "" {
		base = append(base, slog.String(AttrRequestID, requestID))
	}
	if traceID != "" {
		base = append(base, slog.String(AttrTraceID, traceID))
	}
	base = append(base, attrs...)
	logger.LogAttrs(ctx, level, event, base...)
	return nil
}

func bounded(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

type redactingHandler struct{ next slog.Handler }

func (handler *redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return handler.next.Enabled(ctx, level)
}

func (handler *redactingHandler) Handle(ctx context.Context, record slog.Record) error {
	clean := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	record.Attrs(func(attr slog.Attr) bool { clean.AddAttrs(redactAttr(attr)); return true })
	return handler.next.Handle(ctx, clean)
}

func (handler *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		clean = append(clean, redactAttr(attr))
	}
	return &redactingHandler{next: handler.next.WithAttrs(clean)}
}

func (handler *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{next: handler.next.WithGroup(name)}
}

func redactAttr(attr slog.Attr) slog.Attr {
	if sensitiveKey(attr.Key) {
		return slog.String(attr.Key, Redacted)
	}
	value := attr.Value.Resolve()
	if value.Kind() == slog.KindGroup {
		group := value.Group()
		clean := make([]slog.Attr, 0, len(group))
		for _, nested := range group {
			clean = append(clean, redactAttr(nested))
		}
		return slog.Group(attr.Key, attrsToAny(clean)...)
	}
	if value.Kind() == slog.KindAny {
		return slog.Any(attr.Key, redactAny(value.Any(), map[uintptr]bool{}))
	}
	return slog.Attr{Key: attr.Key, Value: value}
}

func attrsToAny(attrs []slog.Attr) []any {
	values := make([]any, len(attrs))
	for index := range attrs {
		values[index] = attrs[index]
	}
	return values
}

func sensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "_", ".", "_").Replace(key))
	for _, fragment := range []string{"authorization", "password", "passwd", "secret", "token", "api_key", "cookie", "credential", "private_key"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func redactAny(input any, seen map[uintptr]bool) any {
	return redactReflect(reflect.ValueOf(input), seen)
}

func redactReflect(value reflect.Value, seen map[uintptr]bool) any {
	if !value.IsValid() {
		return nil
	}
	if value.Kind() == reflect.Interface {
		return redactReflect(value.Elem(), seen)
	}
	switch value.Kind() {
	case reflect.Pointer:
		if value.IsNil() {
			return nil
		}
		pointer := value.Pointer()
		if seen[pointer] {
			return "[CYCLE]"
		}
		seen[pointer] = true
		defer delete(seen, pointer)
		return redactReflect(value.Elem(), seen)
	case reflect.Map:
		clean := make(map[string]any, value.Len())
		iterator := value.MapRange()
		for iterator.Next() {
			key := fmt.Sprint(iterator.Key().Interface())
			if sensitiveKey(key) {
				clean[key] = Redacted
			} else {
				clean[key] = redactReflect(iterator.Value(), seen)
			}
		}
		return clean
	case reflect.Struct:
		clean := make(map[string]any, value.NumField())
		typeOf := value.Type()
		for index := 0; index < value.NumField(); index++ {
			field := typeOf.Field(index)
			if !value.Field(index).CanInterface() {
				continue
			}
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "" {
				name = field.Name
			}
			if name == "-" {
				continue
			}
			if sensitiveKey(name) {
				clean[name] = Redacted
			} else {
				clean[name] = redactReflect(value.Field(index), seen)
			}
		}
		return clean
	case reflect.Slice, reflect.Array:
		clean := make([]any, value.Len())
		for index := 0; index < value.Len(); index++ {
			clean[index] = redactReflect(value.Index(index), seen)
		}
		return clean
	default:
		if value.CanInterface() {
			return value.Interface()
		}
		return "[UNAVAILABLE]"
	}
}
