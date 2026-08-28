package thinkpixelag

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

const authorizationMediaType = "application/vnd.thinkpixel.authorization+json;profile=tg.ag.authorization/v1alpha1"

type AuthorizationConfig struct {
	Endpoint      string
	Client        *http.Client
	Timeout       time.Duration
	MaxBodyBytes  int64
	Clock         func() time.Time
	AllowInsecure bool // isolated tests and development only
}

type AuthorizationClient struct {
	endpoint string
	client   *http.Client
	timeout  time.Duration
	maxBody  int64
	clock    func() time.Time
}

func NewAuthorizationClient(config AuthorizationConfig) (*AuthorizationClient, error) {
	endpoint, err := url.Parse(config.Endpoint)
	if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" ||
		endpoint.Scheme != "https" && (!config.AllowInsecure || endpoint.Scheme != "http") {
		return nil, errors.New("ThinkPixelAG authorization endpoint is invalid")
	}
	if config.Client == nil || config.Timeout <= 0 {
		return nil, errors.New("ThinkPixelAG client and positive deadline are required")
	}
	if config.MaxBodyBytes <= 0 {
		config.MaxBodyBytes = 1 << 20
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	client := *config.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("ThinkPixelAG redirects are forbidden") }
	return &AuthorizationClient{endpoint: strings.TrimSuffix(config.Endpoint, "/"), client: &client,
		timeout: config.Timeout, maxBody: config.MaxBodyBytes, clock: config.Clock}, nil
}

type wireRequest struct {
	RequestID           string      `json:"request_id"`
	CurrentTime         time.Time   `json:"current_time"`
	Context             wireContext `json:"context"`
	ContextDigest       string      `json:"context_digest"`
	Tool                wireTool    `json:"tool"`
	ArgumentProfile     string      `json:"argument_profile"`
	ArgumentDigest      string      `json:"argument_digest"`
	ResourceDigest      string      `json:"resource_digest"`
	Resources           []string    `json:"resources"`
	Actions             []string    `json:"actions"`
	Operation           string      `json:"operation"`
	ConnectorType       string      `json:"connector_type"`
	RequestedDeadlineMS int64       `json:"requested_deadline_ms"`
	PolicyProfile       string      `json:"policy_profile"`
	PolicyVersion       string      `json:"policy_version"`
}

type wireContext struct {
	TenantID     string `json:"tenant_id"`
	Subject      string `json:"subject"`
	Actor        string `json:"actor,omitempty"`
	AgentID      string `json:"agent_id"`
	AgentVersion string `json:"agent_version"`
	RunID        string `json:"run_id"`
	WorkloadID   string `json:"workload_id"`
}
type wireTool struct {
	ID           string `json:"id"`
	Version      string `json:"version"`
	Risk         string `json:"risk"`
	SideEffect   string `json:"side_effect"`
	ApprovalMode string `json:"approval_mode"`
	RetryMode    string `json:"retry_mode"`
}
type wireConstraints struct {
	Repositories   []string         `json:"repositories"`
	Resources      []string         `json:"resources"`
	Actions        []string         `json:"actions"`
	ArgumentMax    map[string]int64 `json:"argument_max"`
	MaxResultBytes int64            `json:"max_result_bytes"`
	MaxDurationMS  int64            `json:"max_duration_ms"`
}
type wireApproval struct {
	Required bool   `json:"required"`
	Mode     string `json:"mode"`
}

type wireResponse struct {
	DecisionID           string                      `json:"decision_id"`
	RequestID            string                      `json:"request_id"`
	ContextDigest        string                      `json:"context_digest"`
	Outcome              ports.AuthorizationOutcome  `json:"outcome"`
	ReasonCodes          []ports.AuthorizationReason `json:"reason_codes"`
	PolicyID             string                      `json:"policy_id"`
	PolicyVersion        string                      `json:"policy_version"`
	IssuedAt             time.Time                   `json:"issued_at"`
	NotBefore            time.Time                   `json:"not_before"`
	ExpiresAt            time.Time                   `json:"expires_at"`
	RevocationEpoch      uint64                      `json:"revocation_epoch"`
	RevocationCheckpoint string                      `json:"revocation_checkpoint"`
	Constraints          wireConstraints             `json:"constraints"`
	Approval             wireApproval                `json:"approval_requirement"`
	EvidenceRef          string                      `json:"evidence_ref"`
}

func (client *AuthorizationClient) AuthorizeToolInvocation(ctx context.Context, input ports.AuthorizationRequest) (ports.AuthorizationDecision, error) {
	if client == nil {
		return ports.AuthorizationDecision{}, errors.New("ThinkPixelAG authorization client is nil")
	}
	if err := input.Validate(); err != nil {
		return ports.AuthorizationDecision{}, fmt.Errorf("validate authorization request: %w", err)
	}
	contextValue := wireContext{input.TenantID, input.Subject, input.Actor, input.AgentID, input.AgentVersion, input.RunID, input.WorkloadID}
	contextDigest, err := digestJSON(contextValue)
	if err != nil {
		return ports.AuthorizationDecision{}, err
	}
	payload := wireRequest{RequestID: input.RequestID, CurrentTime: client.clock().UTC(), Context: contextValue,
		ContextDigest: contextDigest, Tool: wireTool{input.ToolID, input.ToolVersion, input.Risk, input.SideEffect, input.ApprovalMode, input.RetryMode},
		ArgumentProfile: input.ArgumentProfile, ArgumentDigest: input.ArgumentDigest.String(), ResourceDigest: input.ResourceDigest.String(),
		Resources: input.Resources, Actions: input.Actions, Operation: input.Operation, ConnectorType: input.ConnectorType,
		RequestedDeadlineMS: input.RequestedDeadline.Milliseconds(), PolicyProfile: input.PolicyProfile, PolicyVersion: input.PolicyVersion}
	body, err := json.Marshal(payload)
	if err != nil {
		return ports.AuthorizationDecision{}, fmt.Errorf("encode authorization request: %w", err)
	}
	deadline := client.timeout
	if input.RequestedDeadline < deadline {
		deadline = input.RequestedDeadline
	}
	callCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	request, err := http.NewRequestWithContext(callCtx, http.MethodPost, client.endpoint, bytes.NewReader(body))
	if err != nil {
		return ports.AuthorizationDecision{}, fmt.Errorf("create authorization request: %w", err)
	}
	request.Header.Set("Content-Type", authorizationMediaType)
	request.Header.Set("Accept", authorizationMediaType)
	request.Header.Set("X-Request-ID", input.RequestID)
	response, err := client.client.Do(request)
	if err != nil {
		return ports.AuthorizationDecision{}, fmt.Errorf("authorize with ThinkPixelAG: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return ports.AuthorizationDecision{}, fmt.Errorf("ThinkPixelAG authorization failed with status %d", response.StatusCode)
	}
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "application/vnd.thinkpixel.authorization+json") {
		return ports.AuthorizationDecision{}, errors.New("ThinkPixelAG returned an unexpected media type")
	}
	var output wireResponse
	decoder := json.NewDecoder(io.LimitReader(response.Body, client.maxBody+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&output); err != nil {
		return ports.AuthorizationDecision{}, errors.New("ThinkPixelAG returned malformed authorization data")
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return ports.AuthorizationDecision{}, err
	}
	decision := ports.AuthorizationDecision{DecisionID: output.DecisionID, RequestID: output.RequestID, ContextDigest: output.ContextDigest,
		PolicyID: output.PolicyID, PolicyVersion: output.PolicyVersion, Outcome: output.Outcome, Reasons: output.ReasonCodes,
		IssuedAt: output.IssuedAt, NotBefore: output.NotBefore, ExpiresAt: output.ExpiresAt, RevocationEpoch: output.RevocationEpoch,
		RevocationCheckpoint: output.RevocationCheckpoint,
		Constraints: ports.AuthorizationConstraints{Repositories: output.Constraints.Repositories, Resources: output.Constraints.Resources,
			Actions: output.Constraints.Actions, ArgumentMax: output.Constraints.ArgumentMax, MaxResultBytes: output.Constraints.MaxResultBytes,
			MaxDuration: time.Duration(output.Constraints.MaxDurationMS) * time.Millisecond},
		Approval: ports.ApprovalRequirement{Required: output.Approval.Required, Mode: output.Approval.Mode}, EvidenceRef: output.EvidenceRef}
	if decision.ContextDigest != contextDigest {
		return ports.AuthorizationDecision{}, errors.New("ThinkPixelAG authorization context correlation mismatch")
	}
	if err := decision.ValidateFor(input); err != nil {
		return ports.AuthorizationDecision{}, fmt.Errorf("validate ThinkPixelAG decision: %w", err)
	}
	return decision, nil
}

func digestJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode authorization context: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("ThinkPixelAG returned trailing or oversized authorization data")
	}
	return nil
}
