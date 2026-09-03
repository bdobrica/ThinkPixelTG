package downstreamhttp

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type staticResolver struct{ addresses []net.IPAddr }

func (resolver staticResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return resolver.addresses, nil
}

type recordingObserver struct {
	mu     sync.Mutex
	events []Event
}

func (observer *recordingObserver) ObserveDownstreamHTTP(_ context.Context, event Event) {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.events = append(observer.events, event)
}

func TestClientEnforcesTLSDestinationBodyAndTelemetry(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Host == "" {
			t.Fatal("missing host")
		}
		response.WriteHeader(http.StatusCreated)
		response.(http.Flusher).Flush()
		_, _ = io.WriteString(response, "12345")
	}))
	defer server.Close()

	client, observer, target := testClient(t, server, 4, 0)
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(context.Background(), "github.pull_read", request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	if _, err = io.ReadAll(response.Body); !errors.Is(err, ErrResponseBodyTooLarge) {
		t.Fatalf("body error = %v", err)
	}
	if len(observer.events) != 1 || observer.events[0].Operation != "github.pull_read" || observer.events[0].Method != "GET" || observer.events[0].Outcome != "response" || observer.events[0].StatusClass != "2xx" {
		t.Fatalf("events = %#v", observer.events)
	}
}

func TestClientEnforcesOverallDeadline(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		response.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, observer, target := testClient(t, server, 1024, 0)
	client.http.Timeout = 5 * time.Millisecond
	request, _ := http.NewRequest(http.MethodGet, target, nil)
	if _, err := client.Do(context.Background(), "github.pull_read", request); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
	if len(observer.events) != 1 || observer.events[0].Outcome != "transport_error" {
		t.Fatalf("events = %#v", observer.events)
	}
}

func TestClientRejectsKnownOversizedResponseBeforeReturningIt(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(response, "12345")
	}))
	defer server.Close()
	client, observer, target := testClient(t, server, 4, 0)
	request, _ := http.NewRequest(http.MethodGet, target, nil)
	response, err := client.Do(context.Background(), "github.pull_read", request)
	if response != nil || !errors.Is(err, ErrResponseBodyTooLarge) {
		t.Fatalf("response = %#v, error = %v", response, err)
	}
	if len(observer.events) != 1 || observer.events[0].Outcome != "response_too_large" {
		t.Fatalf("events = %#v", observer.events)
	}
}

func TestClientRejectsUnsafeDestinationsAndHeadersBeforeSend(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("unsafe request reached server")
	}))
	defer server.Close()
	client, _, target := testClient(t, server, 1024, 0)

	for name, rawURL := range map[string]string{
		"scheme":     strings.Replace(target, "https://", "http://", 1),
		"host":       strings.Replace(target, "example.com", "attacker.example", 1),
		"query":      target + "?token=secret",
		"userinfo":   strings.Replace(target, "https://", "https://user:pass@", 1),
		"wrong port": strings.Replace(target, target[strings.LastIndex(target, ":"):], ":444", 1),
	} {
		t.Run(name, func(t *testing.T) {
			request, _ := http.NewRequest(http.MethodGet, rawURL, nil)
			if _, err := client.Do(context.Background(), "github.pull_read", request); !errors.Is(err, ErrDestinationDenied) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	request, _ := http.NewRequest(http.MethodGet, target, nil)
	request.Header.Set("X-Caller-Selected", "value")
	if _, err := client.Do(context.Background(), "github.pull_read", request); !errors.Is(err, ErrUnsafeHeader) {
		t.Fatalf("error = %v", err)
	}
}

func TestClientRejectsRedirectEscapeAndBoundsRedirects(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/escape":
			http.Redirect(response, request, "https://attacker.example/collect", http.StatusFound)
		default:
			http.Redirect(response, request, "/again", http.StatusFound)
		}
	}))
	defer server.Close()
	client, _, target := testClient(t, server, 1024, 1)

	for _, path := range []string{"/escape", "/again"} {
		request, _ := http.NewRequest(http.MethodGet, target+path, nil)
		if _, err := client.Do(context.Background(), "github.pull_read", request); !errors.Is(err, ErrDestinationDenied) {
			t.Fatalf("path %s: error = %v", path, err)
		}
	}
}

func TestSecureDialRejectsPrivateMetadataAndMixedDNSAnswers(t *testing.T) {
	for name, addresses := range map[string][]net.IPAddr{
		"loopback": {{IP: net.ParseIP("127.0.0.1")}},
		"private":  {{IP: net.ParseIP("10.0.0.1")}},
		"metadata": {{IP: net.ParseIP("169.254.169.254")}},
		"mixed":    {{IP: net.ParseIP("192.0.2.1")}, {IP: net.ParseIP("10.0.0.1")}},
	} {
		t.Run(name, func(t *testing.T) {
			dialed := false
			dial := dialerFunc(func(context.Context, string, string) (net.Conn, error) {
				dialed = true
				return nil, errors.New("unexpected")
			})
			policy := compiledPolicy{allowPrivate: false}
			_, err := secureDialContext(staticResolver{addresses: addresses}, dial, policy)(context.Background(), "tcp", "connector.example:443")
			if !errors.Is(err, ErrDestinationDenied) || dialed {
				t.Fatalf("error = %v, dialed = %t", err, dialed)
			}
		})
	}
}

func TestClientRequiresVerifiedTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	parsed, _ := url.Parse(server.URL)
	port, _ := strconv.ParseUint(parsed.Port(), 10, 16)
	client, err := NewClient(validPolicy("example.com", uint16(port), x509.NewCertPool()), Options{Resolver: staticResolver{addresses: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}}})
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodGet, "https://example.com:"+parsed.Port(), nil)
	if _, err = client.Do(context.Background(), "github.pull_read", request); err == nil {
		t.Fatal("untrusted TLS certificate accepted")
	}
}

func TestPolicyRejectsUnsafeOrUnboundedConfiguration(t *testing.T) {
	base := validPolicy("connector.example", 443, x509.NewCertPool())
	tests := map[string]func(*Policy){
		"scheme":        func(policy *Policy) { policy.AllowedSchemes = []string{"http"} },
		"wildcard host": func(policy *Policy) { policy.AllowedHosts = []string{"*.example"} },
		"header":        func(policy *Policy) { policy.AllowedRequestHeaders = []string{"Connection"} },
		"timeout":       func(policy *Policy) { policy.RequestTimeout = 0 },
		"body":          func(policy *Policy) { policy.MaxResponseBodyBytes = maximumResponseBody + 1 },
		"redirect":      func(policy *Policy) { policy.MaxRedirects = maximumRedirects + 1 },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			policy := base
			mutate(&policy)
			if _, err := NewClient(policy, Options{}); err == nil {
				t.Fatal("unsafe policy accepted")
			}
		})
	}
}

func testClient(t *testing.T, server *httptest.Server, maxBody int64, redirects int) (*Client, *recordingObserver, string) {
	t.Helper()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.ParseUint(parsed.Port(), 10, 16)
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	observer := &recordingObserver{}
	client, err := NewClient(validPolicy("example.com", uint16(port), roots), Options{Resolver: staticResolver{addresses: []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}}, Observer: observer})
	if err != nil {
		t.Fatal(err)
	}
	client.policy.maxBody = maxBody
	client.policy.maxRedirects = redirects
	return client, observer, "https://example.com:" + parsed.Port()
}

func validPolicy(host string, port uint16, roots *x509.CertPool) Policy {
	return Policy{AllowedSchemes: []string{"https"}, AllowedHosts: []string{host}, AllowedPorts: []uint16{port}, AllowedRequestHeaders: []string{"Accept", "Authorization", "Content-Type", "Idempotency-Key", "User-Agent"}, AllowPrivateAddresses: true, MaxRedirects: 0, RequestTimeout: time.Second, ConnectTimeout: time.Second, ResponseHeaderTimeout: time.Second, MaxResponseBodyBytes: 1024, MaxConnectionsPerHost: 2, RootCAs: roots}
}

type dialerFunc func(context.Context, string, string) (net.Conn, error)

func (function dialerFunc) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return function(ctx, network, address)
}
