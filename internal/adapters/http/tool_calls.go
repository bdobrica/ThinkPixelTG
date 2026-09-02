package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/bdobrica/ThinkPixelTG/internal/app"
	"github.com/bdobrica/ThinkPixelTG/internal/authn"
	"github.com/bdobrica/ThinkPixelTG/internal/canonicaljson"
	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

type ToolCallCreator interface {
	Create(context.Context, app.ToolCallRequest) (app.ToolCallResult, error)
}

type ToolCallReader interface {
	Get(context.Context, ports.InvocationIdentity, string) (ports.ToolCallView, error)
}

type InvocationIdentity func(context.Context) (ports.InvocationIdentity, error)

type ToolCallOptions struct {
	Service  ToolCallCreator
	Reader   ToolCallReader
	Identity InvocationIdentity
}

func NewToolCallHandler(options ToolCallOptions) (http.Handler, error) {
	if options.Service == nil {
		return nil, errors.New("tool-call service is required")
	}
	if options.Identity == nil {
		options.Identity = invocationIdentityFromAuthentication
	}
	handler := &toolCallHandler{service: options.Service, reader: options.Reader, identity: options.Identity}
	mux := http.NewServeMux()
	mux.Handle("POST /v1/tool-calls", handler)
	mux.Handle("GET /v1/tool-calls/{tool_call_id}", handler)
	return mux, nil
}

type toolCallHandler struct {
	service  ToolCallCreator
	reader   ToolCallReader
	identity InvocationIdentity
}

type createToolCallDocument struct {
	ToolID    string          `json:"tool_id"`
	Version   *string         `json:"version,omitempty"`
	Arguments json.RawMessage `json:"arguments"`
}

func (handler *toolCallHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodGet {
		handler.get(writer, request)
		return
	}
	mediaType, _, mediaErr := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if mediaErr != nil || mediaType != "application/json" {
		writeToolCallProblem(writer, request, http.StatusUnsupportedMediaType, "invalid_arguments", "JSON content is required")
		return
	}
	keys := request.Header.Values("Idempotency-Key")
	if len(keys) != 1 || strings.TrimSpace(keys[0]) == "" || strings.Contains(keys[0], ",") {
		writeToolCallProblem(writer, request, http.StatusBadRequest, "invalid_arguments", "A single idempotency key is required")
		return
	}
	identity, err := handler.identity(request.Context())
	if err != nil {
		writeToolCallProblem(writer, request, http.StatusUnauthorized, "invalid_context", "Authentication context is invalid")
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		writeToolCallProblem(writer, request, http.StatusBadRequest, "invalid_arguments", "Tool-call document is invalid")
		return
	}
	if _, err := canonicaljson.Parse(body, canonicaljson.Limits{MaxBytes: 1 << 20}); err != nil {
		writeToolCallProblem(writer, request, http.StatusBadRequest, "invalid_arguments", "Tool-call document is invalid")
		return
	}
	var document createToolCallDocument
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil || len(document.Arguments) == 0 {
		writeToolCallProblem(writer, request, http.StatusBadRequest, "invalid_arguments", "Tool-call document is invalid")
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		writeToolCallProblem(writer, request, http.StatusBadRequest, "invalid_arguments", "Tool-call document is invalid")
		return
	}
	version := ""
	if document.Version != nil {
		version = *document.Version
	}
	result, err := handler.service.Create(request.Context(), app.ToolCallRequest{ToolCallID: keys[0], ToolID: document.ToolID, Version: version, Arguments: document.Arguments, Identity: identity})
	if err != nil {
		writeToolCallError(writer, request, err)
		return
	}
	status := http.StatusAccepted
	if result.Existing {
		status = http.StatusOK
	}
	response := map[string]any{"tool_call_id": result.Invocation.ToolCallID, "tool_id": result.Invocation.ToolID, "version": result.Invocation.ToolVersion, "state": result.State, "created_at": result.Invocation.CreatedAt, "updated_at": result.Invocation.UpdatedAt}
	writeJSONContentType(writer, status, "application/json", response)
}

func (handler *toolCallHandler) get(writer http.ResponseWriter, request *http.Request) {
	identity, err := handler.identity(request.Context())
	if err != nil {
		writeToolCallProblem(writer, request, http.StatusUnauthorized, "invalid_context", "Authentication context is invalid")
		return
	}
	if handler.reader == nil {
		writeToolCallProblem(writer, request, http.StatusServiceUnavailable, "internal", "Tool call is unavailable")
		return
	}
	if request.URL.RawQuery != "" {
		writeToolCallProblem(writer, request, http.StatusNotFound, "tool_call_not_found", "Tool call was not found")
		return
	}
	view, err := handler.reader.Get(request.Context(), identity, request.PathValue("tool_call_id"))
	if err != nil {
		var classified *domain.Error
		if errors.As(err, &classified) && classified.Code == domain.CodeNotFound {
			writeToolCallProblem(writer, request, http.StatusNotFound, "tool_call_not_found", "Tool call was not found")
			return
		}
		writeToolCallProblem(writer, request, http.StatusServiceUnavailable, "internal", "Tool call is unavailable")
		return
	}
	response := map[string]any{"tool_call_id": view.ToolCallID, "tool_id": view.ToolID, "version": view.ToolVersion, "state": view.State, "created_at": view.CreatedAt, "updated_at": view.UpdatedAt}
	if len(view.Result) != 0 {
		response["result"] = json.RawMessage(view.Result)
	}
	if view.ErrorCode != nil {
		response["error_code"] = *view.ErrorCode
	}
	writeJSONContentType(writer, http.StatusOK, "application/json", response)
}

func invocationIdentityFromAuthentication(ctx context.Context) (ports.InvocationIdentity, error) {
	governed, err := authn.DeriveGovernedContext(ctx)
	if err != nil {
		return ports.InvocationIdentity{}, err
	}
	workload, ok := authn.WorkloadIdentityFromContext(ctx)
	if !ok || workload.ID == "" {
		return ports.InvocationIdentity{}, errors.New("authenticated workload identity is required")
	}
	return ports.InvocationIdentity{TenantID: governed.TenantID, Subject: governed.Subject, Actor: governed.Actor, AgentID: governed.AgentID, AgentVersion: governed.AgentVersion, RunID: governed.RunID, WorkloadID: workload.ID}, nil
}

func writeToolCallError(writer http.ResponseWriter, request *http.Request, err error) {
	status, code, title := http.StatusServiceUnavailable, "internal", "Tool call is unavailable"
	var classified *domain.Error
	if errors.As(err, &classified) {
		switch classified.Code {
		case domain.CodeInvalidArgument:
			status, code, title = http.StatusBadRequest, "invalid_arguments", "Tool-call arguments are invalid"
		case domain.CodeNotFound:
			status, code, title = http.StatusNotFound, "tool_not_found", "Tool version is not available"
		case domain.CodeConflict:
			status, code, title = http.StatusConflict, "replay_conflict", "Idempotency key conflicts with an existing tool call"
		case domain.CodeForbidden:
			status, code, title = http.StatusForbidden, "authorization_denied", "Tool call is not authorized"
		}
	}
	writeToolCallProblem(writer, request, status, code, title)
}

func writeToolCallProblem(writer http.ResponseWriter, request *http.Request, status int, code, title string) {
	writeJSONContentType(writer, status, "application/problem+json", map[string]any{"type": "urn:thinkpixeltg:problem:" + code, "title": title, "status": status, "code": code, "correlation_id": writer.Header().Get(RequestIDHeader), "instance": "/v1/tool-calls"})
}
