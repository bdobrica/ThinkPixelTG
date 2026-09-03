package fakes

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAGFakeCorrelatesAuthorization(t *testing.T) {
	handler, err := (Server{Kind: "ag", Clock: func() time.Time { return time.Unix(0, 0) }}).Handler()
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/v1/authorize", bytes.NewBufferString(`{"request_id":"r1","context_digest":"sha256:x","deadline":"1970-01-01T00:01:00Z"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"request_id":"r1"`) || !strings.Contains(response.Body.String(), `"context_digest":"sha256:x"`) {
		t.Fatalf("response: %d %s", response.Code, response.Body.String())
	}
}

func TestGRFakeRejectsUnknownAndInvalidPhase(t *testing.T) {
	handler, err := (Server{Kind: "gr"}).Handler()
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/v1/evaluate", bytes.NewBufferString(`{"evaluation_id":"e1","phase":"other","content_digest":"sha256:x","unknown":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestGRRequestRejectsCredentialCanary(t *testing.T) {
	const canary = "SYNTHETIC_CREDENTIAL_CANARY_gr_013"
	handler, err := (Server{Kind: "gr"}).Handler()
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/v1/evaluate", bytes.NewBufferString(`{"evaluation_id":"e1","phase":"pre_tool","content_digest":"sha256:x","credential":"`+canary+`"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || strings.Contains(response.Body.String(), canary) {
		t.Fatalf("GR boundary accepted or disclosed credential: %d %s", response.Code, response.Body.String())
	}
}
