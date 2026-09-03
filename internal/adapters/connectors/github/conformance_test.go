package github

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bdobrica/ThinkPixelTG/internal/connectors/downstreamhttp"
	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

func TestGitHubConnectorConformanceStatusAndRateLimitMapping(t *testing.T) {
	tests := []struct {
		name      string
		connector func(*testing.T, httpClient) ports.ConnectorExecutor
		request   func(ports.CredentialCapability) ports.ConnectorRequest
		statuses  map[int]string
	}{
		{
			name:      "pull read",
			connector: func(t *testing.T, client httpClient) ports.ConnectorExecutor { return newReader(t, client) },
			request:   validRequest,
			statuses: map[int]string{
				http.StatusBadRequest:          "definitely_rejected",
				http.StatusNotFound:            "definitely_rejected",
				http.StatusTooManyRequests:     "transient_safe",
				http.StatusInternalServerError: "transient_safe",
			},
		},
		{
			name:      "pull comment",
			connector: func(t *testing.T, client httpClient) ports.ConnectorExecutor { return newWriter(t, client) },
			request:   validCommentRequest,
			statuses: map[int]string{
				http.StatusForbidden:           "definitely_rejected",
				http.StatusNotFound:            "definitely_rejected",
				http.StatusUnprocessableEntity: "definitely_rejected",
				http.StatusTooManyRequests:     "definitely_rejected",
				http.StatusInternalServerError: "unknown",
				http.StatusTeapot:              "unknown",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for status, want := range test.statuses {
				t.Run(http.StatusText(status), func(t *testing.T) {
					client := httpClientFunc(func(context.Context, string, *http.Request) (*http.Response, error) {
						return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"credential":"must-not-escape"}`))}, nil
					})
					result, err := test.connector(t, client).Execute(context.Background(), test.request(&credentialStub{metadata: validMetadata()}))
					if err != nil || result.Classification != want || len(result.Result) != 0 {
						t.Fatalf("status %d: result = %#v, error = %v", status, result, err)
					}
				})
			}
		})
	}
}

func TestGitHubConnectorConformanceCancellationAndResponseLimit(t *testing.T) {
	t.Run("cancellation before dispatch", func(t *testing.T) {
		for _, test := range []struct {
			name      string
			connector func(*testing.T, httpClient) ports.ConnectorExecutor
			request   func(ports.CredentialCapability) ports.ConnectorRequest
		}{
			{"pull read", func(t *testing.T, client httpClient) ports.ConnectorExecutor { return newReader(t, client) }, validRequest},
			{"pull comment", func(t *testing.T, client httpClient) ports.ConnectorExecutor { return newWriter(t, client) }, validCommentRequest},
		} {
			t.Run(test.name, func(t *testing.T) {
				called := false
				client := httpClientFunc(func(context.Context, string, *http.Request) (*http.Response, error) {
					called = true
					return nil, nil
				})
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				result, err := test.connector(t, client).Execute(ctx, test.request(&credentialStub{metadata: validMetadata()}))
				if err != nil || result.Classification != "cancelled_pre_send" || called {
					t.Fatalf("result = %#v, error = %v, network = %t", result, err, called)
				}
			})
		}
	})
	t.Run("read cancellation is preserved", func(t *testing.T) {
		for _, want := range []error{context.Canceled, context.DeadlineExceeded} {
			connector := newReader(t, httpClientFunc(func(context.Context, string, *http.Request) (*http.Response, error) {
				return nil, want
			}))
			_, err := connector.Execute(context.Background(), validRequest(&credentialStub{metadata: validMetadata()}))
			if !errors.Is(err, want) {
				t.Fatalf("error = %v, want %v", err, want)
			}
		}
	})
	t.Run("read response limit is a safe execution error", func(t *testing.T) {
		connector := newReader(t, httpClientFunc(func(context.Context, string, *http.Request) (*http.Response, error) {
			return nil, downstreamhttp.ErrResponseBodyTooLarge
		}))
		_, err := connector.Execute(context.Background(), validRequest(&credentialStub{metadata: validMetadata()}))
		if !errors.Is(err, ErrExecution) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("write cancellation after dispatch is ambiguous", func(t *testing.T) {
		for _, transportErr := range []error{context.Canceled, context.DeadlineExceeded, downstreamhttp.ErrResponseBodyTooLarge} {
			connector := newWriter(t, httpClientFunc(func(context.Context, string, *http.Request) (*http.Response, error) {
				return nil, transportErr
			}))
			result, err := connector.Execute(context.Background(), validCommentRequest(&credentialStub{metadata: validMetadata()}))
			if err != nil || result.Classification != "unknown" {
				t.Fatalf("result = %#v, error = %v", result, err)
			}
		}
	})
}

func TestGitHubConnectorConformanceRejectsExpiredCredentialBeforeNetwork(t *testing.T) {
	for _, test := range []struct {
		name      string
		connector func(*testing.T, httpClient) ports.ConnectorExecutor
		request   func(ports.CredentialCapability) ports.ConnectorRequest
	}{
		{"pull read", func(t *testing.T, client httpClient) ports.ConnectorExecutor { return newReader(t, client) }, validRequest},
		{"pull comment", func(t *testing.T, client httpClient) ports.ConnectorExecutor { return newWriter(t, client) }, validCommentRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			client := httpClientFunc(func(context.Context, string, *http.Request) (*http.Response, error) {
				called = true
				return nil, nil
			})
			capability := &rejectingCapability{metadata: validMetadata(), err: errors.New("credential capability is expired")}
			_, err := test.connector(t, client).Execute(context.Background(), test.request(capability))
			if !errors.Is(err, ErrCredential) || called {
				t.Fatalf("error = %v, network = %t", err, called)
			}
		})
	}
}

type rejectingCapability struct {
	metadata ports.CredentialCapabilityMetadata
	err      error
}

func (capability *rejectingCapability) Metadata() ports.CredentialCapabilityMetadata {
	return capability.metadata
}
func (capability *rejectingCapability) UseSecret(func([]byte) error) error { return capability.err }
func (*rejectingCapability) Release()                                      {}
