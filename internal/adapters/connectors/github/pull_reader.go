// Package github implements reviewed GitHub connector operations.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/bdobrica/ThinkPixelTG/internal/canonicaljson"
	"github.com/bdobrica/ThinkPixelTG/internal/connectors/downstreamhttp"
	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

const (
	ConnectorType = "github"
	PullGet       = "pull.get"
	PullComment   = "pull.comment"
	maximumOwner  = 39
	maximumRepo   = 100
)

var (
	ownerPattern      = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?$`)
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	ErrInvalidRequest = errors.New("GitHub connector request is invalid")
	ErrCredential     = errors.New("GitHub credential capability is invalid")
	ErrExecution      = errors.New("GitHub request execution failed")
	ErrResponse       = errors.New("GitHub response is invalid")
)

type httpClient interface {
	Do(context.Context, string, *http.Request) (*http.Response, error)
}

type destinationConfig struct {
	BaseURL string `json:"base_url"`
	Owner   string `json:"owner"`
}

type PullReader struct {
	client           httpClient
	baseURL          *url.URL
	owner            string
	instanceSelector string
}

var _ ports.ConnectorExecutor = (*PullReader)(nil)

// NewPullReader binds the operation to one immutable administrator-owned
// connector instance. Caller arguments cannot alter its authority or host.
func NewPullReader(instance domain.ConnectorInstance, client *downstreamhttp.Client) (*PullReader, error) {
	if client == nil {
		return nil, errors.New("GitHub HTTP client is required")
	}
	return newPullReader(instance, client)
}

func newPullReader(instance domain.ConnectorInstance, client httpClient) (*PullReader, error) {
	if client == nil {
		return nil, errors.New("GitHub HTTP client is required")
	}
	definition := instance.Definition()
	if err := domain.ValidateConnectorInstanceDefinition(definition); err != nil || definition.ConnectorType != ConnectorType || !definition.Enabled {
		return nil, errors.New("GitHub connector instance is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(definition.DestinationConfig))
	decoder.DisallowUnknownFields()
	var destination destinationConfig
	if err := decoder.Decode(&destination); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, errors.New("GitHub destination configuration is invalid")
	}
	baseURL, err := url.Parse(destination.BaseURL)
	if err != nil || baseURL.Scheme != "https" || baseURL.Host == "" || baseURL.User != nil || baseURL.RawQuery != "" || baseURL.Fragment != "" || baseURL.Path != strings.TrimSuffix(baseURL.Path, "/") {
		return nil, errors.New("GitHub base URL is invalid")
	}
	if !validOwner(destination.Owner) {
		return nil, errors.New("GitHub owner is invalid")
	}
	copyURL := *baseURL
	return &PullReader{client: client, baseURL: &copyURL, owner: destination.Owner, instanceSelector: definition.Selector}, nil
}

func (connector *PullReader) Execute(ctx context.Context, request ports.ConnectorRequest) (ports.ConnectorResult, error) {
	if connector == nil || ctx == nil || request.InvocationID == "" || request.Credential == nil || request.Tool.Connector.ConnectorType != ConnectorType || request.Tool.Connector.Operation != PullGet || request.Tool.Connector.InstanceSelector != connector.instanceSelector {
		return ports.ConnectorResult{}, ErrInvalidRequest
	}
	if ctx.Err() != nil {
		return ports.ConnectorResult{Classification: "cancelled_pre_send"}, nil
	}
	repository, pullNumber, err := validateTarget(request.CanonicalArguments, request.ResourceProjection)
	if err != nil {
		return ports.ConnectorResult{}, err
	}
	metadata := request.Credential.Metadata()
	if !validCredential(metadata, connector.baseURL.Hostname(), "repository:"+connector.owner+"/"+repository) {
		return ports.ConnectorResult{}, ErrCredential
	}

	target := *connector.baseURL
	target.Path = strings.TrimSuffix(target.Path, "/") + "/repos/" + connector.owner + "/" + repository + "/pulls/" + strconv.FormatInt(pullNumber, 10)
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return ports.ConnectorResult{}, ErrInvalidRequest
	}
	httpRequest.Header.Set("Accept", "application/vnd.github+json")
	httpRequest.Header.Set("User-Agent", "ThinkPixelTG")
	var response *http.Response
	attempted := false
	err = request.Credential.UseSecret(func(secret []byte) error {
		httpRequest.Header.Set("Authorization", "Bearer "+string(secret))
		defer httpRequest.Header.Del("Authorization")
		attempted = true
		var callErr error
		response, callErr = connector.client.Do(ctx, PullGet, httpRequest)
		return callErr
	})
	if err != nil {
		if !attempted {
			return ports.ConnectorResult{}, ErrCredential
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ports.ConnectorResult{}, err
		}
		return ports.ConnectorResult{}, ErrExecution
	}
	if response == nil || response.Body == nil {
		return ports.ConnectorResult{}, ErrResponse
	}
	defer func() { _ = response.Body.Close() }()

	classification := classifyStatus(response.StatusCode)
	evidence := responseEvidence(response)
	if classification != "confirmed_success" {
		return ports.ConnectorResult{Classification: classification, Evidence: evidence}, nil
	}
	result, providerResultID, err := decodePull(response.Body, repository, pullNumber)
	if err != nil {
		return ports.ConnectorResult{}, err
	}
	evidence.ProviderResultID = providerResultID
	return ports.ConnectorResult{Classification: classification, Result: result, Evidence: evidence}, nil
}

type targetDocument struct {
	Repository string      `json:"repository"`
	PullNumber json.Number `json:"pull_number"`
}

func validateTarget(arguments, projection json.RawMessage) (string, int64, error) {
	if !canonicalDocument(arguments) || !canonicalDocument(projection) {
		return "", 0, ErrInvalidRequest
	}
	argumentTarget, err := decodeTarget(arguments)
	if err != nil {
		return "", 0, err
	}
	projectedTarget, err := decodeTarget(projection)
	if err != nil || argumentTarget != projectedTarget {
		return "", 0, ErrInvalidRequest
	}
	number, err := strconv.ParseInt(argumentTarget.PullNumber.String(), 10, 64)
	if err != nil || number < 1 || number > int64(^uint32(0)>>1) || !validRepository(argumentTarget.Repository) {
		return "", 0, ErrInvalidRequest
	}
	return argumentTarget.Repository, number, nil
}

func decodeTarget(document json.RawMessage) (targetDocument, error) {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	var target targetDocument
	if err := decoder.Decode(&target); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return targetDocument{}, ErrInvalidRequest
	}
	return target, nil
}

func canonicalDocument(document []byte) bool {
	canonical, err := canonicaljson.Canonicalize(document, canonicaljson.Limits{MaxBytes: len(document)})
	return err == nil && bytes.Equal(canonical, document)
}

func validCredential(metadata ports.CredentialCapabilityMetadata, audience, resource string) bool {
	if metadata.Kind != domain.CapabilityOAuthAccessToken && metadata.Kind != domain.CapabilityAPIToken {
		return false
	}
	audienceMatches := false
	for _, candidate := range metadata.Audiences {
		if strings.EqualFold(strings.TrimSuffix(candidate, "."), strings.TrimSuffix(audience, ".")) {
			audienceMatches = true
			break
		}
	}
	if !audienceMatches {
		return false
	}
	for _, candidate := range metadata.Resources {
		if candidate == resource {
			return true
		}
	}
	return false
}

func validOwner(value string) bool {
	return len(value) > 0 && len(value) <= maximumOwner && ownerPattern.MatchString(value) && !strings.Contains(value, "--")
}

func validRepository(value string) bool {
	return len(value) > 0 && len(value) <= maximumRepo && value != "." && value != ".." && repositoryPattern.MatchString(value)
}

func classifyStatus(status int) string {
	switch {
	case status == http.StatusOK:
		return "confirmed_success"
	case status == http.StatusTooManyRequests || status >= 500 && status <= 599:
		return "transient_safe"
	default:
		return "definitely_rejected"
	}
}

type githubPull struct {
	Number    int64  `json:"number"`
	NodeID    string `json:"node_id"`
	Title     string `json:"title"`
	State     string `json:"state"`
	HTMLURL   string `json:"html_url"`
	UpdatedAt string `json:"updated_at"`
}

type pullResult struct {
	Repository string `json:"repository"`
	Number     int64  `json:"number"`
	Title      string `json:"title"`
	State      string `json:"state"`
	URL        string `json:"url"`
	UpdatedAt  string `json:"updated_at"`
}

func decodePull(body io.Reader, repository string, pullNumber int64) (json.RawMessage, string, error) {
	decoder := json.NewDecoder(body)
	var pull githubPull
	if err := decoder.Decode(&pull); err != nil || decoder.Decode(&struct{}{}) != io.EOF || pull.Number != pullNumber || pull.Title == "" || pull.State == "" || pull.HTMLURL == "" || pull.UpdatedAt == "" {
		return nil, "", ErrResponse
	}
	result, err := json.Marshal(pullResult{Repository: repository, Number: pull.Number, Title: pull.Title, State: pull.State, URL: pull.HTMLURL, UpdatedAt: pull.UpdatedAt})
	if err != nil {
		return nil, "", ErrResponse
	}
	return result, boundedHeader(pull.NodeID), nil
}
