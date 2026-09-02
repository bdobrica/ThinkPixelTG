package http

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/adapters/http/openapi"
	"github.com/bdobrica/ThinkPixelTG/internal/authn"
	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

const (
	defaultToolPageSize = 50
	maxToolPageSize     = 100
	maxDiscoveryCursor  = 2048
)

type ToolDiscovery interface {
	Discover(context.Context, ports.DiscoveryAuthorizationRequest) ([]ports.CatalogToolVersion, error)
	Describe(context.Context, ports.DiscoveryAuthorizationRequest, string, string) (ports.CatalogToolVersion, error)
}

type DiscoveryIdentity func(context.Context) (ports.DiscoveryAuthorizationRequest, error)

type ToolDiscoveryOptions struct {
	Discovery ToolDiscovery
	CursorKey []byte
	CursorTTL time.Duration
	Clock     func() time.Time
	Identity  DiscoveryIdentity
}

type toolDiscoveryHandler struct {
	discovery ToolDiscovery
	cursors   discoveryCursorCodec
	identity  DiscoveryIdentity
}

// NewToolDiscoveryHandler constructs the canonical GET /v1/tools adapter.
// CursorKey is deployment secret material and must contain at least 32 bytes.
func NewToolDiscoveryHandler(options ToolDiscoveryOptions) (http.Handler, error) {
	if options.Discovery == nil {
		return nil, errors.New("tool discovery service is required")
	}
	if len(options.CursorKey) < 32 {
		return nil, errors.New("discovery cursor key must be at least 32 bytes")
	}
	if options.CursorTTL <= 0 {
		return nil, errors.New("discovery cursor TTL must be positive")
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	if options.Identity == nil {
		options.Identity = discoveryIdentityFromAuthentication
	}
	handler := &toolDiscoveryHandler{
		discovery: options.Discovery,
		cursors:   discoveryCursorCodec{key: append([]byte(nil), options.CursorKey...), ttl: options.CursorTTL, now: options.Clock},
		identity:  options.Identity,
	}
	mux := http.NewServeMux()
	mux.Handle("GET /v1/tools", handler)
	mux.Handle("GET /v1/tools/{tool_id}", handler)
	return mux, nil
}

func (handler *toolDiscoveryHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.PathValue("tool_id") != "" {
		handler.describe(writer, request)
		return
	}
	identity, err := handler.identity(request.Context())
	if err != nil {
		writeDiscoveryProblem(writer, request, http.StatusUnauthorized, "invalid_context", "Authentication context is invalid")
		return
	}
	limit, cursor, err := handler.parsePage(identity, request)
	if err != nil {
		writeDiscoveryProblem(writer, request, http.StatusBadRequest, "invalid_arguments", "Pagination parameters are invalid")
		return
	}
	tools, err := handler.discovery.Discover(request.Context(), identity)
	if err != nil {
		writeDiscoveryProblem(writer, request, http.StatusServiceUnavailable, "internal", "Tool discovery is unavailable")
		return
	}
	sort.Slice(tools, func(left, right int) bool {
		if tools[left].ToolID == tools[right].ToolID {
			return tools[left].Version < tools[right].Version
		}
		return tools[left].ToolID < tools[right].ToolID
	})
	start := sort.Search(len(tools), func(index int) bool {
		return compareToolKey(tools[index], cursor.AfterToolID, cursor.AfterVersion) > 0
	})
	end := start + limit
	if end > len(tools) {
		end = len(tools)
	}
	page := openapi.ToolPage{Items: make([]openapi.Tool, 0, end-start)}
	for _, candidate := range tools[start:end] {
		tool, projectErr := projectDiscoveryTool(candidate)
		if projectErr != nil {
			writeDiscoveryProblem(writer, request, http.StatusServiceUnavailable, "internal", "Tool discovery is unavailable")
			return
		}
		page.Items = append(page.Items, tool)
	}
	if end < len(tools) {
		last := tools[end-1]
		next, encodeErr := handler.cursors.encode(discoveryCursor{
			Version: 1, AfterToolID: last.ToolID, AfterVersion: last.Version, Limit: limit,
			Binding: discoveryBinding(identity, limit), ExpiresAt: handler.cursors.now().UTC().Add(handler.cursors.ttl).Unix(),
		})
		if encodeErr != nil {
			writeDiscoveryProblem(writer, request, http.StatusServiceUnavailable, "internal", "Tool discovery is unavailable")
			return
		}
		page.NextCursor = &next
	}
	writeJSON(writer, http.StatusOK, page)
}

func (handler *toolDiscoveryHandler) describe(writer http.ResponseWriter, request *http.Request) {
	identity, err := handler.identity(request.Context())
	if err != nil {
		writeDiscoveryProblem(writer, request, http.StatusUnauthorized, "invalid_context", "Authentication context is invalid")
		return
	}
	query := request.URL.Query()
	if len(query) != 1 || !query.Has("version") || len(query["version"]) != 1 || query.Get("version") == "" {
		writeDiscoveryProblem(writer, request, http.StatusBadRequest, "invalid_arguments", "A single version parameter is required")
		return
	}
	toolID, version := request.PathValue("tool_id"), query.Get("version")
	if _, parseErr := domain.ParseToolID(toolID); parseErr != nil {
		writeDiscoveryProblem(writer, request, http.StatusNotFound, "tool_not_found", "Tool version was not found")
		return
	}
	if _, parseErr := domain.ParseSemanticVersion(version); parseErr != nil {
		writeDiscoveryProblem(writer, request, http.StatusNotFound, "tool_not_found", "Tool version was not found")
		return
	}
	candidate, err := handler.discovery.Describe(request.Context(), identity, toolID, version)
	if err != nil {
		var domainErr *domain.Error
		if errors.As(err, &domainErr) && domainErr.Code == domain.CodeNotFound {
			writeDiscoveryProblem(writer, request, http.StatusNotFound, "tool_not_found", "Tool version was not found")
			return
		}
		writeDiscoveryProblem(writer, request, http.StatusServiceUnavailable, "internal", "Tool discovery is unavailable")
		return
	}
	tool, err := projectDiscoveryTool(candidate)
	if err != nil {
		writeDiscoveryProblem(writer, request, http.StatusServiceUnavailable, "internal", "Tool discovery is unavailable")
		return
	}
	writeJSON(writer, http.StatusOK, tool)
}

func (handler *toolDiscoveryHandler) parsePage(identity ports.DiscoveryAuthorizationRequest, request *http.Request) (int, discoveryCursor, error) {
	query := request.URL.Query()
	for name, values := range query {
		if name != "limit" && name != "cursor" || len(values) != 1 {
			return 0, discoveryCursor{}, errors.New("unknown or repeated query parameter")
		}
	}
	limit := defaultToolPageSize
	if value := query.Get("limit"); query.Has("limit") {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > maxToolPageSize {
			return 0, discoveryCursor{}, errors.New("invalid page size")
		}
		limit = parsed
	}
	encoded := query.Get("cursor")
	if !query.Has("cursor") {
		return limit, discoveryCursor{}, nil
	}
	if encoded == "" {
		return 0, discoveryCursor{}, errors.New("cursor is empty")
	}
	if len(encoded) > maxDiscoveryCursor {
		return 0, discoveryCursor{}, errors.New("cursor is too large")
	}
	cursor, err := handler.cursors.decode(encoded)
	if err != nil {
		return 0, discoveryCursor{}, err
	}
	if query.Has("limit") && cursor.Limit != limit {
		return 0, discoveryCursor{}, errors.New("cursor page size mismatch")
	}
	limit = cursor.Limit
	if cursor.Binding != discoveryBinding(identity, limit) {
		return 0, discoveryCursor{}, errors.New("cursor context mismatch")
	}
	return limit, cursor, nil
}

func discoveryIdentityFromAuthentication(ctx context.Context) (ports.DiscoveryAuthorizationRequest, error) {
	governed, err := authn.DeriveGovernedContext(ctx)
	if err != nil {
		return ports.DiscoveryAuthorizationRequest{}, err
	}
	workload, ok := authn.WorkloadIdentityFromContext(ctx)
	if !ok || workload.ID == "" {
		return ports.DiscoveryAuthorizationRequest{}, errors.New("authenticated workload identity is required")
	}
	return ports.DiscoveryAuthorizationRequest{
		TenantID: governed.TenantID, Subject: governed.Subject, Actor: governed.Actor,
		AgentID: governed.AgentID, AgentVersion: governed.AgentVersion, RunID: governed.RunID, WorkloadID: workload.ID,
	}, nil
}

type discoveryCursor struct {
	Version      int    `json:"v"`
	AfterToolID  string `json:"tool_id"`
	AfterVersion string `json:"version"`
	Limit        int    `json:"limit"`
	Binding      string `json:"binding"`
	ExpiresAt    int64  `json:"expires_at"`
}

type discoveryCursorCodec struct {
	key []byte
	ttl time.Duration
	now func() time.Time
}

func (codec discoveryCursorCodec) encode(cursor discoveryCursor) (string, error) {
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, codec.key)
	_, _ = mac.Write(payload)
	encoded := base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if len(encoded) > maxDiscoveryCursor {
		return "", errors.New("encoded cursor is too large")
	}
	return encoded, nil
}

func (codec discoveryCursorCodec) decode(encoded string) (discoveryCursor, error) {
	var cursor discoveryCursor
	payloadText, signatureText, ok := strings.Cut(encoded, ".")
	if !ok {
		return cursor, errors.New("invalid cursor encoding")
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadText)
	if err != nil {
		return cursor, errors.New("invalid cursor encoding")
	}
	signature, err := base64.RawURLEncoding.DecodeString(signatureText)
	if err != nil {
		return cursor, errors.New("invalid cursor encoding")
	}
	mac := hmac.New(sha256.New, codec.key)
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return cursor, errors.New("invalid cursor signature")
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return discoveryCursor{}, errors.New("invalid cursor payload")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return discoveryCursor{}, errors.New("invalid cursor payload")
	}
	_, toolIDErr := domain.ParseToolID(cursor.AfterToolID)
	_, versionErr := domain.ParseSemanticVersion(cursor.AfterVersion)
	if cursor.Version != 1 || cursor.Limit < 1 || cursor.Limit > maxToolPageSize || toolIDErr != nil || versionErr != nil || cursor.Binding == "" || cursor.ExpiresAt <= codec.now().UTC().Unix() {
		return discoveryCursor{}, errors.New("invalid or expired cursor")
	}
	return cursor, nil
}

func discoveryBinding(identity ports.DiscoveryAuthorizationRequest, limit int) string {
	hash := sha256.New()
	for _, value := range []string{identity.TenantID, identity.Subject, identity.Actor, identity.AgentID, identity.AgentVersion, identity.RunID, identity.WorkloadID, strconv.Itoa(limit)} {
		_, _ = hash.Write([]byte(strconv.Itoa(len(value))))
		_, _ = hash.Write([]byte{':'})
		_, _ = hash.Write([]byte(value))
	}
	return base64.RawURLEncoding.EncodeToString(hash.Sum(nil))
}

func compareToolKey(tool ports.CatalogToolVersion, toolID, version string) int {
	if tool.ToolID < toolID || tool.ToolID == toolID && tool.Version < version {
		return -1
	}
	if tool.ToolID == toolID && tool.Version == version {
		return 0
	}
	return 1
}

type discoveryToolDocument struct {
	Description     string                 `json:"description"`
	InputSchema     map[string]interface{} `json:"input_schema"`
	OutputSchema    map[string]interface{} `json:"output_schema"`
	Risk            string                 `json:"risk"`
	SideEffect      *bool                  `json:"side_effect"`
	RetryClass      string                 `json:"retry_class"`
	Approval        string                 `json:"approval"`
	OpenWorldResult *bool                  `json:"open_world_result"`
}

func projectDiscoveryTool(candidate ports.CatalogToolVersion) (openapi.Tool, error) {
	var document discoveryToolDocument
	if err := json.Unmarshal(candidate.Definition, &document); err != nil || document.Description == "" || len(document.Description) > 4096 || document.InputSchema == nil || document.OutputSchema == nil || document.SideEffect == nil || document.OpenWorldResult == nil || !oneOf(document.Risk, "read", "bounded_write", "consequential_write", "privileged") || !oneOf(document.RetryClass, "safe", "downstream_idempotency", "gateway_deduplicated", "reconcile_before_retry", "at_least_once_accepted", "non_retryable") || !oneOf(document.Approval, "never", "policy", "always") {
		return openapi.Tool{}, errors.New("published discovery projection is invalid")
	}
	return openapi.Tool{
		ToolId: candidate.ToolID, Version: candidate.Version, Description: document.Description,
		InputSchema: document.InputSchema, OutputSchema: document.OutputSchema, Risk: document.Risk,
		SideEffect: *document.SideEffect, RetryClass: document.RetryClass, Approval: document.Approval,
		OpenWorldResult: *document.OpenWorldResult,
	}, nil
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func writeDiscoveryProblem(writer http.ResponseWriter, request *http.Request, status int, code, title string) {
	requestID := writer.Header().Get(RequestIDHeader)
	instance := request.URL.Path
	if code == "tool_not_found" {
		instance = "/v1/tools/{tool_id}"
	}
	writeJSONContentType(writer, status, "application/problem+json", map[string]any{
		"type": "urn:thinkpixeltg:problem:" + code, "title": title, "status": status,
		"code": code, "correlation_id": requestID, "instance": instance,
	})
}

func writeJSONContentType(writer http.ResponseWriter, status int, contentType string, value any) {
	writer.Header().Set("Content-Type", contentType)
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
