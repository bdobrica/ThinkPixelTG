// Package fakes provides contract-shaped local AG and GR test doubles.
package fakes

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

type Server struct {
	Kind  string
	Clock func() time.Time
}

func (server Server) Handler() (http.Handler, error) {
	if server.Kind != "ag" && server.Kind != "gr" {
		return nil, fmt.Errorf("unsupported fake kind %q", server.Kind)
	}
	if server.Clock == nil {
		server.Clock = time.Now
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		write(writer, 200, map[string]string{"status": "ok", "kind": server.Kind})
	})
	if server.Kind == "ag" {
		mux.HandleFunc("POST /v1/authorize", server.authorize)
		mux.HandleFunc("POST /v1/approvals", server.approve)
	} else {
		mux.HandleFunc("POST /v1/evaluate", server.evaluate)
	}
	return mux, nil
}

type authorizationRequest struct {
	RequestID     string `json:"request_id"`
	ContextDigest string `json:"context_digest"`
	Deadline      string `json:"deadline"`
}

func (server Server) authorize(writer http.ResponseWriter, request *http.Request) {
	var input authorizationRequest
	if err := decode(request, &input); err != nil || input.RequestID == "" || input.ContextDigest == "" {
		problem(writer, 400, "malformed authorization request")
		return
	}
	now := server.Clock().UTC()
	write(writer, 200, map[string]any{
		"decision_id": "fake-decision-" + input.RequestID, "request_id": input.RequestID,
		"context_digest": input.ContextDigest, "outcome": "allow", "reason_codes": []string{"allowed"},
		"policy_id": "local-compose", "policy_version": "1", "issued_at": now.Format(time.RFC3339Nano),
		"not_before": now.Format(time.RFC3339Nano), "expires_at": now.Add(time.Minute).Format(time.RFC3339Nano),
		"revocation_epoch": 1, "revocation_checkpoint": "local-1", "constraints": map[string]any{},
		"approval_requirement": "none", "evidence_ref": "fake-ag:" + input.RequestID,
	})
}

type approvalRequest struct {
	RequestID     string `json:"request_id"`
	BindingDigest string `json:"binding_digest"`
}

func (server Server) approve(writer http.ResponseWriter, request *http.Request) {
	var input approvalRequest
	if err := decode(request, &input); err != nil || input.RequestID == "" || input.BindingDigest == "" {
		problem(writer, 400, "malformed approval request")
		return
	}
	write(writer, 200, map[string]any{"approval_id": "fake-approval-" + input.RequestID, "binding_digest": input.BindingDigest, "status": "approved", "evidence_ref": "fake-approval:" + input.RequestID})
}

type evaluationRequest struct {
	EvaluationID  string `json:"evaluation_id"`
	Phase         string `json:"phase"`
	ContentDigest string `json:"content_digest"`
	CorrelationID string `json:"correlation_id"`
}

func (server Server) evaluate(writer http.ResponseWriter, request *http.Request) {
	var input evaluationRequest
	if err := decode(request, &input); err != nil || input.EvaluationID == "" || input.ContentDigest == "" || (input.Phase != "pre_tool" && input.Phase != "post_tool") {
		problem(writer, 400, "malformed evaluation request")
		return
	}
	now := server.Clock().UTC()
	write(writer, 200, map[string]any{
		"evaluation_id": input.EvaluationID, "correlation_id": input.CorrelationID,
		"content_digest": input.ContentDigest, "decision": "allow", "reason_codes": []string{"local_allow"},
		"policy_id": "local-compose", "profile": "mandatory", "policy_version": "1",
		"issued_at": now.Format(time.RFC3339Nano), "evidence_ref": "fake-gr:" + input.EvaluationID,
	})
}

func decode(request *http.Request, target any) error {
	if request.Header.Get("Content-Type") != "application/json" {
		return errors.New("content type must be application/json")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(nil, request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func problem(writer http.ResponseWriter, status int, detail string) {
	write(writer, status, map[string]any{"type": "urn:thinkpixeltg:fake:invalid-request", "title": "Invalid request", "status": status, "detail": detail})
}
func write(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
