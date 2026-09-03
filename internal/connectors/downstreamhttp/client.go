// Package downstreamhttp provides the shared, fail-closed HTTP boundary used by
// networked connector adapters.
package downstreamhttp

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	maximumRedirects       = 5
	maximumResponseBody    = 16 << 20
	maximumIdleConnections = 100
)

var (
	ErrDestinationDenied    = errors.New("downstream HTTP destination denied")
	ErrUnsafeHeader         = errors.New("downstream HTTP header denied")
	ErrRequestFailed        = errors.New("downstream HTTP request failed")
	ErrResponseBodyTooLarge = errors.New("downstream HTTP response body exceeds limit")
)

// Resolver is injectable so DNS policy can be tested without external access.
type Resolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

// Dialer is the narrow connection primitive used after address validation.
type Dialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

// Event contains only bounded, low-cardinality telemetry. It deliberately
// excludes URLs, headers, bodies, tenant identifiers, and credentials.
type Event struct {
	Operation, Method, Outcome, StatusClass string
	Duration                                time.Duration
}

type Observer interface {
	ObserveDownstreamHTTP(context.Context, Event)
}

type Policy struct {
	AllowedSchemes        []string
	AllowedHosts          []string
	AllowedPorts          []uint16
	AllowedRequestHeaders []string
	AllowPrivateAddresses bool
	MaxRedirects          int
	RequestTimeout        time.Duration
	ConnectTimeout        time.Duration
	ResponseHeaderTimeout time.Duration
	MaxResponseBodyBytes  int64
	MaxConnectionsPerHost int
	RootCAs               *x509.CertPool
}

type Options struct {
	Resolver Resolver
	Dialer   Dialer
	Observer Observer
	Now      func() time.Time
}

type Client struct {
	http     *http.Client
	policy   compiledPolicy
	observer Observer
	now      func() time.Time
}

type compiledPolicy struct {
	schemes, hosts, headers map[string]struct{}
	ports                   map[uint16]struct{}
	allowPrivate            bool
	maxRedirects            int
	maxBody                 int64
}

func NewClient(policy Policy, options Options) (*Client, error) {
	compiled, err := compilePolicy(policy)
	if err != nil {
		return nil, err
	}
	resolver := options.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	dialer := options.Dialer
	if dialer == nil {
		dialer = &net.Dialer{Timeout: policy.ConnectTimeout, KeepAlive: 30 * time.Second}
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           secureDialContext(resolver, dialer, compiled),
		ForceAttemptHTTP2:     true,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: policy.RootCAs},
		TLSHandshakeTimeout:   policy.ConnectTimeout,
		ResponseHeaderTimeout: policy.ResponseHeaderTimeout,
		ExpectContinueTimeout: time.Second,
		MaxIdleConns:          maximumIdleConnections,
		MaxIdleConnsPerHost:   policy.MaxConnectionsPerHost,
		MaxConnsPerHost:       policy.MaxConnectionsPerHost,
		IdleConnTimeout:       90 * time.Second,
	}
	client := &Client{policy: compiled, observer: options.Observer, now: options.Now}
	if client.now == nil {
		client.now = time.Now
	}
	client.http = &http.Client{Transport: transport, Timeout: policy.RequestTimeout}
	client.http.CheckRedirect = client.checkRedirect
	return client, nil
}

// Do performs one connector request. operation must be a compiled stable name,
// suitable for a bounded telemetry dimension.
func (client *Client) Do(ctx context.Context, operation string, request *http.Request) (*http.Response, error) {
	if client == nil || ctx == nil || request == nil || request.URL == nil {
		return nil, errors.New("downstream HTTP request is invalid")
	}
	if !stableName(operation) {
		return nil, errors.New("downstream HTTP operation is invalid")
	}
	if err := client.policy.validateURL(request.URL); err != nil {
		return nil, err
	}
	if err := client.policy.validateHeaders(request.Header); err != nil {
		return nil, err
	}
	request = request.Clone(ctx)
	started := client.now()
	response, err := client.http.Do(request)
	event := Event{Operation: operation, Method: stableMethod(request.Method), Outcome: "transport_error", Duration: client.now().Sub(started)}
	if err == nil {
		event.Outcome = "response"
		event.StatusClass = statusClass(response.StatusCode)
		if response.ContentLength > client.policy.maxBody {
			_ = response.Body.Close()
			response = nil
			err = ErrResponseBodyTooLarge
			event.Outcome = "response_too_large"
		} else {
			response.Body = &boundedBody{reader: io.LimitReader(response.Body, client.policy.maxBody+1), body: response.Body, remaining: client.policy.maxBody}
		}
	}
	if client.observer != nil {
		client.observer.ObserveDownstreamHTTP(ctx, event)
	}
	if err != nil {
		switch {
		case errors.Is(err, ErrResponseBodyTooLarge):
			err = ErrResponseBodyTooLarge
		case errors.Is(err, ErrDestinationDenied):
			err = ErrDestinationDenied
		case errors.Is(err, ErrUnsafeHeader):
			err = ErrUnsafeHeader
		case errors.Is(err, context.Canceled):
			err = context.Canceled
		case errors.Is(err, context.DeadlineExceeded):
			err = context.DeadlineExceeded
		default:
			err = ErrRequestFailed
		}
	}
	return response, err
}

func (client *Client) CloseIdleConnections() {
	if client != nil {
		client.http.CloseIdleConnections()
	}
}

func (client *Client) checkRedirect(request *http.Request, via []*http.Request) error {
	if len(via) > client.policy.maxRedirects {
		return ErrDestinationDenied
	}
	if err := client.policy.validateURL(request.URL); err != nil {
		return err
	}
	// net/http synthesizes Referer during redirect processing. Connector
	// destinations never need it, and paths can contain confidential resource
	// identifiers, so do not propagate it even within one authority.
	request.Header.Del("Referer")
	if len(via) > 0 && !sameAuthority(via[0].URL, request.URL) {
		request.Header.Del("Authorization")
		request.Header.Del("Cookie")
		request.Header.Del("Proxy-Authorization")
	}
	return client.policy.validateHeaders(request.Header)
}

func compilePolicy(policy Policy) (compiledPolicy, error) {
	if policy.RequestTimeout <= 0 || policy.ConnectTimeout <= 0 || policy.ResponseHeaderTimeout <= 0 || policy.MaxResponseBodyBytes <= 0 || policy.MaxResponseBodyBytes > maximumResponseBody || policy.MaxConnectionsPerHost <= 0 {
		return compiledPolicy{}, errors.New("downstream HTTP limits are invalid")
	}
	if policy.MaxRedirects < 0 || policy.MaxRedirects > maximumRedirects {
		return compiledPolicy{}, errors.New("downstream HTTP redirect limit is invalid")
	}
	compiled := compiledPolicy{schemes: map[string]struct{}{}, hosts: map[string]struct{}{}, headers: map[string]struct{}{}, ports: map[uint16]struct{}{}, allowPrivate: policy.AllowPrivateAddresses, maxRedirects: policy.MaxRedirects, maxBody: policy.MaxResponseBodyBytes}
	for _, scheme := range policy.AllowedSchemes {
		scheme = strings.ToLower(strings.TrimSpace(scheme))
		if scheme != "https" {
			return compiledPolicy{}, errors.New("only HTTPS downstream destinations are supported")
		}
		compiled.schemes[scheme] = struct{}{}
	}
	for _, host := range policy.AllowedHosts {
		host = normalizeHost(host)
		if host == "" || strings.ContainsAny(host, "/?#@:%*[]") {
			return compiledPolicy{}, errors.New("downstream HTTP host allowlist is invalid")
		}
		compiled.hosts[host] = struct{}{}
	}
	for _, port := range policy.AllowedPorts {
		if port == 0 {
			return compiledPolicy{}, errors.New("downstream HTTP port allowlist is invalid")
		}
		compiled.ports[port] = struct{}{}
	}
	for _, header := range policy.AllowedRequestHeaders {
		header = http.CanonicalHeaderKey(strings.TrimSpace(header))
		if header == "" || forbiddenHeader(header) {
			return compiledPolicy{}, errors.New("downstream HTTP header allowlist is invalid")
		}
		compiled.headers[header] = struct{}{}
	}
	if len(compiled.schemes) == 0 || len(compiled.hosts) == 0 || len(compiled.ports) == 0 {
		return compiledPolicy{}, errors.New("downstream HTTP destination allowlist is required")
	}
	return compiled, nil
}

func (policy compiledPolicy) validateURL(target *url.URL) error {
	if target == nil || target.User != nil || target.RawQuery != "" || target.Fragment != "" || target.Host == "" {
		return ErrDestinationDenied
	}
	scheme := strings.ToLower(target.Scheme)
	host := normalizeHost(target.Hostname())
	if _, ok := policy.schemes[scheme]; !ok {
		return ErrDestinationDenied
	}
	if _, ok := policy.hosts[host]; !ok {
		return ErrDestinationDenied
	}
	port, err := effectivePort(target)
	if err != nil {
		return ErrDestinationDenied
	}
	if _, ok := policy.ports[port]; !ok {
		return ErrDestinationDenied
	}
	if address, err := netip.ParseAddr(host); err == nil && !policy.allowPrivate && prohibitedAddress(address) {
		return ErrDestinationDenied
	}
	return nil
}

func (policy compiledPolicy) validateHeaders(headers http.Header) error {
	for name, values := range headers {
		canonical := http.CanonicalHeaderKey(name)
		if forbiddenHeader(canonical) {
			return ErrUnsafeHeader
		}
		if _, ok := policy.headers[canonical]; !ok {
			return ErrUnsafeHeader
		}
		for _, value := range values {
			if strings.ContainsAny(value, "\r\n") {
				return ErrUnsafeHeader
			}
		}
	}
	return nil
}

func secureDialContext(resolver Resolver, dialer Dialer, policy compiledPolicy) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, ErrDestinationDenied
		}
		if literal, parseErr := netip.ParseAddr(host); parseErr == nil {
			if !policy.allowPrivate && prohibitedAddress(literal) {
				return nil, ErrDestinationDenied
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(literal.String(), port))
		}
		addresses, err := resolver.LookupIPAddr(ctx, host)
		if err != nil || len(addresses) == 0 {
			return nil, fmt.Errorf("resolve downstream host: %w", ErrDestinationDenied)
		}
		validated := make([]netip.Addr, 0, len(addresses))
		for _, candidate := range addresses {
			parsed, ok := netip.AddrFromSlice(candidate.IP)
			if !ok || !policy.allowPrivate && prohibitedAddress(parsed) {
				return nil, ErrDestinationDenied
			}
			validated = append(validated, parsed.Unmap())
		}
		var dialErr error
		for _, candidate := range validated {
			connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.String(), port))
			if err == nil {
				return connection, nil
			}
			dialErr = err
		}
		return nil, fmt.Errorf("connect downstream host: %w", dialErr)
	}
}

func prohibitedAddress(address netip.Addr) bool {
	address = address.Unmap()
	return !address.IsValid() || address.IsUnspecified() || address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast()
}

func forbiddenHeader(name string) bool {
	switch strings.ToLower(name) {
	case "connection", "proxy-connection", "keep-alive", "transfer-encoding", "te", "trailer", "upgrade", "host", "proxy-authorization", "forwarded", "x-forwarded-for", "x-forwarded-host", "x-forwarded-proto":
		return true
	default:
		return false
	}
}

func effectivePort(target *url.URL) (uint16, error) {
	value := target.Port()
	if value == "" {
		if strings.EqualFold(target.Scheme, "https") {
			return 443, nil
		}
		return 0, ErrDestinationDenied
	}
	port, err := strconv.ParseUint(value, 10, 16)
	if err != nil || port == 0 {
		return 0, ErrDestinationDenied
	}
	return uint16(port), nil
}

func normalizeHost(host string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
}
func sameAuthority(left, right *url.URL) bool {
	leftPort, _ := effectivePort(left)
	rightPort, _ := effectivePort(right)
	return strings.EqualFold(left.Scheme, right.Scheme) && normalizeHost(left.Hostname()) == normalizeHost(right.Hostname()) && leftPort == rightPort
}
func stableName(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '.' || char == '_' {
			continue
		}
		return false
	}
	return true
}
func stableMethod(value string) string {
	value = strings.ToUpper(value)
	switch value {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead:
		return value
	default:
		return "OTHER"
	}
}
func statusClass(status int) string {
	if status < 100 || status > 599 {
		return "unknown"
	}
	return strconv.Itoa(status/100) + "xx"
}

type boundedBody struct {
	reader    io.Reader
	body      io.ReadCloser
	remaining int64
}

func (body *boundedBody) Read(buffer []byte) (int, error) {
	n, err := body.reader.Read(buffer)
	body.remaining -= int64(n)
	if body.remaining < 0 {
		return n, ErrResponseBodyTooLarge
	}
	return n, err
}
func (body *boundedBody) Close() error { return body.body.Close() }
