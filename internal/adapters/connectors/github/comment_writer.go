package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/bdobrica/ThinkPixelTG/internal/connectors/downstreamhttp"
	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

const maximumCommentBytes = 65536

type CommentWriter struct {
	client           httpClient
	baseURL          *url.URL
	owner            string
	instanceSelector string
}

var _ ports.ConnectorExecutor = (*CommentWriter)(nil)

// NewCommentWriter binds pull.comment to an immutable administrator-owned
// instance and the secured downstream transport. GitHub does not document a
// native idempotency key for this operation, so its tool version must be
// published as non_retryable.
func NewCommentWriter(instance domain.ConnectorInstance, client *downstreamhttp.Client) (*CommentWriter, error) {
	if client == nil {
		return nil, errors.New("GitHub HTTP client is required")
	}
	return newCommentWriter(instance, client)
}

func newCommentWriter(instance domain.ConnectorInstance, client httpClient) (*CommentWriter, error) {
	reader, err := newPullReader(instance, client)
	if err != nil {
		return nil, err
	}
	baseURL := *reader.baseURL
	return &CommentWriter{client: client, baseURL: &baseURL, owner: reader.owner, instanceSelector: reader.instanceSelector}, nil
}

func (connector *CommentWriter) Execute(ctx context.Context, request ports.ConnectorRequest) (ports.ConnectorResult, error) {
	if connector == nil || ctx == nil || request.InvocationID == "" || request.Credential == nil || request.Tool.Connector.ConnectorType != ConnectorType || request.Tool.Connector.Operation != PullComment || request.Tool.Connector.InstanceSelector != connector.instanceSelector || !request.Tool.SideEffect || request.Tool.Retry != domain.RetryNonRetryable {
		return ports.ConnectorResult{}, ErrInvalidRequest
	}
	if ctx.Err() != nil {
		return ports.ConnectorResult{Classification: "cancelled_pre_send"}, nil
	}
	repository, pullNumber, body, err := validateComment(request.CanonicalArguments, request.ResourceProjection)
	if err != nil {
		return ports.ConnectorResult{}, err
	}
	if !validCredential(request.Credential.Metadata(), connector.baseURL.Hostname(), "repository:"+connector.owner+"/"+repository) {
		return ports.ConnectorResult{}, ErrCredential
	}
	payload, err := json.Marshal(struct {
		Body string `json:"body"`
	}{Body: body})
	if err != nil {
		return ports.ConnectorResult{}, ErrInvalidRequest
	}
	target := *connector.baseURL
	target.Path = strings.TrimSuffix(target.Path, "/") + "/repos/" + connector.owner + "/" + repository + "/issues/" + strconv.FormatInt(pullNumber, 10) + "/comments"
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(payload))
	if err != nil {
		return ports.ConnectorResult{}, ErrInvalidRequest
	}
	httpRequest.Header.Set("Accept", "application/vnd.github+json")
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("User-Agent", "ThinkPixelTG")

	attempted := false
	var response *http.Response
	err = request.Credential.UseSecret(func(secret []byte) error {
		httpRequest.Header.Set("Authorization", "Bearer "+string(secret))
		defer httpRequest.Header.Del("Authorization")
		attempted = true
		var callErr error
		response, callErr = connector.client.Do(ctx, PullComment, httpRequest)
		return callErr
	})
	if err != nil {
		if attempted {
			return ports.ConnectorResult{Classification: "unknown"}, nil
		}
		return ports.ConnectorResult{}, ErrCredential
	}
	if response == nil || response.Body == nil {
		return ports.ConnectorResult{Classification: "unknown"}, nil
	}
	defer func() { _ = response.Body.Close() }()

	classification := classifyCommentStatus(response.StatusCode)
	if classification != "confirmed_success" {
		return ports.ConnectorResult{Classification: classification}, nil
	}
	result, err := decodeComment(response.Body, repository, pullNumber)
	if err != nil {
		return ports.ConnectorResult{}, err
	}
	return ports.ConnectorResult{Classification: classification, Result: result}, nil
}

type commentArguments struct {
	Repository string      `json:"repository"`
	PullNumber json.Number `json:"pull_number"`
	Body       string      `json:"body"`
}

func validateComment(arguments, projection json.RawMessage) (string, int64, string, error) {
	if !canonicalDocument(arguments) || !canonicalDocument(projection) {
		return "", 0, "", ErrInvalidRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	var comment commentArguments
	if err := decoder.Decode(&comment); err != nil || decoder.Decode(&struct{}{}) != io.EOF || comment.Body == "" || len([]byte(comment.Body)) > maximumCommentBytes {
		return "", 0, "", ErrInvalidRequest
	}
	projected, err := decodeTarget(projection)
	if err != nil || comment.Repository != projected.Repository || comment.PullNumber.String() != projected.PullNumber.String() {
		return "", 0, "", ErrInvalidRequest
	}
	number, err := strconv.ParseInt(comment.PullNumber.String(), 10, 64)
	if err != nil || number < 1 || number > int64(^uint32(0)>>1) || !validRepository(comment.Repository) {
		return "", 0, "", ErrInvalidRequest
	}
	return comment.Repository, number, comment.Body, nil
}

func classifyCommentStatus(status int) string {
	switch status {
	case http.StatusCreated:
		return "confirmed_success"
	case http.StatusForbidden, http.StatusNotFound, http.StatusGone, http.StatusUnprocessableEntity, http.StatusTooManyRequests:
		return "definitely_rejected"
	default:
		// Only provider-documented rejection statuses prove this non-idempotent
		// write was not applied. An unfamiliar response must not enable replay.
		return "unknown"
	}
}

type githubComment struct {
	ID        int64  `json:"id"`
	HTMLURL   string `json:"html_url"`
	CreatedAt string `json:"created_at"`
}

type commentResult struct {
	Repository string `json:"repository"`
	PullNumber int64  `json:"pull_number"`
	CommentID  int64  `json:"comment_id"`
	URL        string `json:"url"`
	CreatedAt  string `json:"created_at"`
}

func decodeComment(body io.Reader, repository string, pullNumber int64) (json.RawMessage, error) {
	decoder := json.NewDecoder(body)
	var comment githubComment
	if err := decoder.Decode(&comment); err != nil || decoder.Decode(&struct{}{}) != io.EOF || comment.ID < 1 || comment.HTMLURL == "" || comment.CreatedAt == "" {
		return nil, ErrResponse
	}
	result, err := json.Marshal(commentResult{Repository: repository, PullNumber: pullNumber, CommentID: comment.ID, URL: comment.HTMLURL, CreatedAt: comment.CreatedAt})
	if err != nil {
		return nil, ErrResponse
	}
	return result, nil
}
