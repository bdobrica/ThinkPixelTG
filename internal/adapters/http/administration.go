package http

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

type AdministrationOptions struct {
	Authorizer ports.AdministrativeAuthorizer
	Service    ports.ToolAdministrator
	Compiler   domain.SchemaCompiler
}

type administrationHandler struct{ options AdministrationOptions }

func NewAdministrationHandler(options AdministrationOptions) (http.Handler, error) {
	if options.Authorizer == nil || options.Service == nil || options.Compiler == nil {
		return nil, errors.New("administration authorizer, service, and schema compiler are required")
	}
	handler := &administrationHandler{options: options}
	mux := http.NewServeMux()
	mux.Handle("POST /v1/admin/tool-versions", handler)
	mux.Handle("PUT /v1/admin/tenant-tool-exposures", handler)
	return mux, nil
}

type publishToolVersionDocument struct {
	ToolID                  string               `json:"tool_id"`
	Version                 string               `json:"version"`
	Title                   string               `json:"title"`
	Description             string               `json:"description"`
	ReviewReference         string               `json:"review_reference"`
	InputSchema             json.RawMessage      `json:"input_schema"`
	OutputSchema            json.RawMessage      `json:"output_schema"`
	Risk                    domain.RiskClass     `json:"risk"`
	SideEffect              *bool                `json:"side_effect"`
	RetryClass              domain.RetryClass    `json:"retry_class"`
	RetryQualification      string               `json:"retry_qualification"`
	Approval                domain.ApprovalClass `json:"approval"`
	OpenWorldResult         *bool                `json:"open_world_result"`
	CanonicalizationProfile string               `json:"canonicalization_profile"`
	Connector               struct {
		Type             string `json:"type"`
		Operation        string `json:"operation"`
		InstanceSelector string `json:"instance_selector"`
	} `json:"connector"`
	CredentialBindingSelector string                              `json:"credential_binding_selector"`
	ResourceProjection        domain.ResourceProjectionDefinition `json:"resource_projection"`
	Limits                    struct {
		RequestBytes         int64 `json:"request_bytes"`
		ResultBytes          int64 `json:"result_bytes"`
		DeadlineMilliseconds int64 `json:"deadline_ms"`
		Concurrency          int   `json:"concurrency"`
		MaxAttempts          int   `json:"max_attempts"`
	} `json:"limits"`
	Metering domain.MeteringRule `json:"metering"`
}

type tenantToolExposureDocument struct {
	TenantID string `json:"tenant_id"`
	ToolID   string `json:"tool_id"`
	Version  string `json:"version"`
	Enabled  *bool  `json:"enabled"`
}

func (handler *administrationHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	key, ok := administrativeRequest(writer, request)
	if !ok {
		return
	}
	if request.URL.Path == "/v1/admin/tool-versions" {
		handler.publish(writer, request, key)
		return
	}
	handler.setExposure(writer, request, key)
}

func administrativeRequest(writer http.ResponseWriter, request *http.Request) (string, bool) {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writePublicProblem(writer, request, domain.CodeInvalidArguments)
		return "", false
	}
	keys := request.Header.Values("Idempotency-Key")
	if len(keys) != 1 || strings.TrimSpace(keys[0]) == "" || strings.Contains(keys[0], ",") || len(keys[0]) > 128 {
		writePublicProblem(writer, request, domain.CodeInvalidArguments)
		return "", false
	}
	return keys[0], true
}

func decodeAdministrativeJSON(request *http.Request, target any) error {
	body, err := io.ReadAll(io.LimitReader(request.Body, maxPublicRequestBytes+1))
	if err != nil || len(body) > maxPublicRequestBytes {
		return errors.New("administrative document exceeds the request limit")
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values")
	}
	return nil
}

func (handler *administrationHandler) publish(writer http.ResponseWriter, request *http.Request, key string) {
	if err := handler.options.Authorizer.AuthorizeAdministration(request.Context(), ports.AdminPublishToolVersion, ""); err != nil {
		writePublicProblem(writer, request, domain.CodeAuthorizationDenied)
		return
	}
	var document publishToolVersionDocument
	if err := decodeAdministrativeJSON(request, &document); err != nil {
		writePublicProblem(writer, request, domain.CodeInvalidArguments)
		return
	}
	toolID, err := domain.ParseToolID(document.ToolID)
	if err != nil {
		writePublicProblem(writer, request, domain.CodeInvalidArguments)
		return
	}
	version, err := domain.ParseSemanticVersion(document.Version)
	if err != nil {
		writePublicProblem(writer, request, domain.CodeInvalidArguments)
		return
	}
	if document.SideEffect == nil || document.OpenWorldResult == nil || document.Limits.DeadlineMilliseconds < 1 || document.Limits.DeadlineMilliseconds > 30_000 {
		writePublicProblem(writer, request, domain.CodeInvalidArguments)
		return
	}
	definition := domain.ToolVersionDefinition{
		ToolID: toolID, Version: version, Risk: document.Risk, SideEffect: *document.SideEffect,
		Retry: document.RetryClass, Approval: document.Approval, OpenWorldResult: *document.OpenWorldResult,
		Description: domain.ReviewedDescription{Title: document.Title, Description: document.Description, ReviewRef: document.ReviewReference},
		InputSchema: document.InputSchema, OutputSchema: document.OutputSchema, CanonicalProfile: document.CanonicalizationProfile,
		Connector:          domain.ConnectorBinding{ConnectorType: document.Connector.Type, Operation: document.Connector.Operation, InstanceSelector: document.Connector.InstanceSelector},
		CredentialSelector: document.CredentialBindingSelector, RetryQualification: document.RetryQualification,
		ResourceProjection: document.ResourceProjection, Metering: document.Metering,
		Limits: domain.ToolLimits{RequestBytes: document.Limits.RequestBytes, ResultBytes: document.Limits.ResultBytes, Deadline: time.Duration(document.Limits.DeadlineMilliseconds) * time.Millisecond, Concurrency: document.Limits.Concurrency, MaxAttempts: document.Limits.MaxAttempts},
	}
	if err := domain.ValidateToolPublication(definition, handler.options.Compiler); err != nil {
		writePublicProblem(writer, request, domain.CodeInvalidArguments)
		return
	}
	if err := handler.options.Service.PublishToolVersion(request.Context(), ports.AdministrativeMutation{IdempotencyKey: key, Definition: definition}); err != nil {
		writeDomainProblem(writer, request, err, domain.CodeServiceUnavailable)
		return
	}
	writeJSONContentType(writer, http.StatusCreated, "application/json", map[string]any{
		"tool_id": document.ToolID, "version": document.Version, "description": document.Description,
		"input_schema": document.InputSchema, "output_schema": document.OutputSchema, "risk": document.Risk,
		"side_effect": *document.SideEffect, "retry_class": document.RetryClass, "approval": document.Approval,
		"open_world_result": *document.OpenWorldResult,
	})
}

func (handler *administrationHandler) setExposure(writer http.ResponseWriter, request *http.Request, key string) {
	var document tenantToolExposureDocument
	if err := decodeAdministrativeJSON(request, &document); err != nil {
		writePublicProblem(writer, request, domain.CodeInvalidArguments)
		return
	}
	if _, err := domain.ParseUUID(document.TenantID); err != nil {
		writePublicProblem(writer, request, domain.CodeInvalidArguments)
		return
	}
	if _, err := domain.ParseToolID(document.ToolID); err != nil {
		writePublicProblem(writer, request, domain.CodeInvalidArguments)
		return
	}
	if _, err := domain.ParseSemanticVersion(document.Version); err != nil {
		writePublicProblem(writer, request, domain.CodeInvalidArguments)
		return
	}
	if document.Enabled == nil {
		writePublicProblem(writer, request, domain.CodeInvalidArguments)
		return
	}
	if err := handler.options.Authorizer.AuthorizeAdministration(request.Context(), ports.AdminSetTenantExposure, document.TenantID); err != nil {
		writePublicProblem(writer, request, domain.CodeAuthorizationDenied)
		return
	}
	mutation := ports.AdministrativeMutation{IdempotencyKey: key, TenantID: document.TenantID, ToolID: document.ToolID, Version: document.Version, Enabled: *document.Enabled}
	if err := handler.options.Service.SetTenantToolExposure(request.Context(), mutation); err != nil {
		writeDomainProblem(writer, request, err, domain.CodeServiceUnavailable)
		return
	}
	writer.WriteHeader(http.StatusOK)
}
