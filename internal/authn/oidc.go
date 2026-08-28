package authn

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const maxTokenBytes = 32 << 10

type ErrorCode string

const (
	CodeInvalidToken          ErrorCode = "invalid_token"
	CodeUnsupportedIssuer     ErrorCode = "unsupported_issuer"
	CodeDependencyUnavailable ErrorCode = "identity_provider_unavailable"
)

type VerificationError struct {
	Code ErrorCode
	err  error
}

func (e *VerificationError) Error() string { return string(e.Code) }
func (e *VerificationError) Unwrap() error { return e.err }

type IssuerConfig struct {
	Issuer       string
	Audiences    []string
	Resources    []string
	Algorithms   []string
	RefreshAfter time.Duration
	MaxStale     time.Duration
	ClockSkew    time.Duration
	MaxKeys      int
}

type Config struct {
	Issuers       []IssuerConfig
	HTTPClient    *http.Client
	Clock         func() time.Time
	MaxBodyBytes  int64
	AllowInsecure bool // tests and explicitly isolated development only
}

type Claims struct {
	Issuer    string
	Subject   string
	Audience  []string
	Resources []string
	ExpiresAt time.Time
	NotBefore time.Time
	Raw       map[string]any
}

type Provider struct {
	client  *http.Client
	clock   func() time.Time
	maxBody int64
	issuers map[string]*issuer
}

type issuer struct {
	config IssuerConfig
	mu     sync.Mutex
	keys   map[string]crypto.PublicKey
	loaded time.Time
}

func NewOIDCProvider(config Config) (*Provider, error) {
	if len(config.Issuers) == 0 {
		return nil, errors.New("at least one OIDC issuer is required")
	}
	if config.HTTPClient == nil {
		return nil, errors.New("OIDC HTTP client is required")
	}
	if config.HTTPClient.Timeout <= 0 {
		return nil, errors.New("OIDC HTTP client timeout must be positive")
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	if config.MaxBodyBytes <= 0 {
		config.MaxBodyBytes = 1 << 20
	}
	client := *config.HTTPClient
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return errors.New("OIDC redirects are forbidden") }
	provider := &Provider{client: &client, clock: config.Clock, maxBody: config.MaxBodyBytes, issuers: make(map[string]*issuer)}
	for _, profile := range config.Issuers {
		parsed, err := url.Parse(profile.Issuer)
		if err != nil {
			return nil, fmt.Errorf("invalid OIDC issuer %q", profile.Issuer)
		}
		secureScheme := parsed.Scheme == "https" || config.AllowInsecure && parsed.Scheme == "http"
		if parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil || parsed.Host == "" || !secureScheme {
			return nil, fmt.Errorf("invalid OIDC issuer %q", profile.Issuer)
		}
		profile.Issuer = strings.TrimSuffix(profile.Issuer, "/")
		if _, exists := provider.issuers[profile.Issuer]; exists {
			return nil, fmt.Errorf("duplicate OIDC issuer %q", profile.Issuer)
		}
		if len(profile.Audiences) == 0 || len(profile.Algorithms) == 0 || profile.RefreshAfter <= 0 || profile.MaxStale < 0 || profile.ClockSkew < 0 || profile.MaxKeys < 1 {
			return nil, fmt.Errorf("OIDC issuer %q has invalid bounds", profile.Issuer)
		}
		for _, algorithm := range profile.Algorithms {
			if algorithm != "RS256" && algorithm != "ES256" && algorithm != "EdDSA" {
				return nil, fmt.Errorf("OIDC issuer %q allows unsupported algorithm %q", profile.Issuer, algorithm)
			}
		}
		provider.issuers[profile.Issuer] = &issuer{config: profile}
	}
	return provider, nil
}

func (provider *Provider) Verify(ctx context.Context, token string) (Claims, error) {
	header, payload, signature, signed, err := splitToken(token)
	if err != nil {
		return Claims{}, invalid(err)
	}
	var metadata struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
		Typ string `json:"typ"`
	}
	if err := decodeJSON(header, &metadata); err != nil || metadata.Alg == "" || metadata.Kid == "" {
		return Claims{}, invalid(errors.New("missing or malformed JOSE header"))
	}
	var raw map[string]any
	if err := decodeJSON(payload, &raw); err != nil {
		return Claims{}, invalid(err)
	}
	issuerName, ok := raw["iss"].(string)
	if !ok {
		return Claims{}, invalid(errors.New("issuer claim is required"))
	}
	profile := provider.issuers[issuerName]
	if profile == nil {
		return Claims{}, &VerificationError{Code: CodeUnsupportedIssuer}
	}
	if !contains(profile.config.Algorithms, metadata.Alg) {
		return Claims{}, invalid(errors.New("algorithm is not allowed"))
	}
	key, err := provider.key(ctx, profile, metadata.Kid)
	if err != nil {
		return Claims{}, err
	}
	if err := verifySignature(metadata.Alg, key, signed, signature); err != nil {
		return Claims{}, invalid(err)
	}
	return validateClaims(raw, profile.config, provider.clock().UTC())
}

func (provider *Provider) key(ctx context.Context, profile *issuer, kid string) (crypto.PublicKey, error) {
	profile.mu.Lock()
	defer profile.mu.Unlock()
	now := provider.clock().UTC()
	key, found := profile.keys[kid]
	fresh := !profile.loaded.IsZero() && now.Sub(profile.loaded) <= profile.config.RefreshAfter
	if found && fresh {
		return key, nil
	}
	refreshErr := provider.refresh(ctx, profile, now)
	if refreshErr == nil {
		if key, found = profile.keys[kid]; found {
			return key, nil
		}
		return nil, invalid(errors.New("signing key not found"))
	}
	// A known cached key remains deterministic and usable only within the
	// configured stale window. Unknown keys never fall back during an outage.
	if found && !profile.loaded.IsZero() && now.Sub(profile.loaded) <= profile.config.RefreshAfter+profile.config.MaxStale {
		return key, nil
	}
	return nil, &VerificationError{Code: CodeDependencyUnavailable, err: refreshErr}
}

func (provider *Provider) refresh(ctx context.Context, profile *issuer, now time.Time) error {
	discoveryURL := profile.config.Issuer + "/.well-known/openid-configuration"
	var discovery struct {
		Issuer  string `json:"issuer"`
		JWKSURI string `json:"jwks_uri"`
	}
	if err := provider.fetchJSON(ctx, discoveryURL, &discovery); err != nil {
		return err
	}
	if discovery.Issuer != profile.config.Issuer {
		return errors.New("OIDC discovery issuer mismatch")
	}
	jwksURL, err := url.Parse(discovery.JWKSURI)
	if err != nil || jwksURL.Scheme != strings.SplitN(profile.config.Issuer, ":", 2)[0] || jwksURL.Host == "" || jwksURL.User != nil || jwksURL.Fragment != "" {
		return errors.New("OIDC discovery returned an invalid JWKS URI")
	}
	var set struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := provider.fetchJSON(ctx, discovery.JWKSURI, &set); err != nil {
		return err
	}
	if len(set.Keys) == 0 || len(set.Keys) > profile.config.MaxKeys {
		return errors.New("JWKS key count is outside configured bounds")
	}
	keys := make(map[string]crypto.PublicKey, len(set.Keys))
	for _, encoded := range set.Keys {
		kid, key, err := parseJWK(encoded)
		if err != nil {
			return err
		}
		if _, duplicate := keys[kid]; duplicate {
			return errors.New("JWKS contains duplicate key IDs")
		}
		keys[kid] = key
	}
	profile.keys, profile.loaded = keys, now
	return nil
}

func (provider *Provider) fetchJSON(ctx context.Context, endpoint string, destination any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	response, err := provider.client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("identity provider returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, provider.maxBody+1))
	if err != nil {
		return err
	}
	if int64(len(body)) > provider.maxBody {
		return errors.New("identity provider response exceeds configured bound")
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("identity provider returned trailing or oversized JSON")
	}
	return nil
}

func splitToken(token string) ([]byte, []byte, []byte, []byte, error) {
	if len(token) == 0 || len(token) > maxTokenBytes {
		return nil, nil, nil, nil, errors.New("token size is invalid")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, nil, nil, nil, errors.New("compact JWT requires three segments")
	}
	decoded := make([][]byte, 3)
	for index := range parts {
		value, err := base64.RawURLEncoding.DecodeString(parts[index])
		if err != nil {
			return nil, nil, nil, nil, err
		}
		decoded[index] = value
	}
	return decoded[0], decoded[1], decoded[2], []byte(parts[0] + "." + parts[1]), nil
}

func decodeJSON(encoded []byte, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}

func validateClaims(raw map[string]any, config IssuerConfig, now time.Time) (Claims, error) {
	audience, err := stringList(raw["aud"])
	if err != nil || !intersects(audience, config.Audiences) {
		return Claims{}, invalid(errors.New("audience is not allowed"))
	}
	resources := []string{}
	if value, exists := raw["resource"]; exists {
		resources, err = stringList(value)
		if err != nil {
			return Claims{}, invalid(err)
		}
	}
	if len(config.Resources) > 0 && !intersects(resources, config.Resources) {
		return Claims{}, invalid(errors.New("resource is not allowed"))
	}
	expires, err := numericDate(raw["exp"])
	if err != nil || !now.Before(expires.Add(config.ClockSkew)) {
		return Claims{}, invalid(errors.New("token is expired or exp is missing"))
	}
	notBefore := time.Time{}
	if value, exists := raw["nbf"]; exists {
		notBefore, err = numericDate(value)
		if err != nil || now.Add(config.ClockSkew).Before(notBefore) {
			return Claims{}, invalid(errors.New("token is not yet valid"))
		}
	}
	if value, exists := raw["iat"]; exists {
		issuedAt, issuedErr := numericDate(value)
		if issuedErr != nil || now.Add(config.ClockSkew).Before(issuedAt) {
			return Claims{}, invalid(errors.New("token issue time is in the future"))
		}
	}
	subject, _ := raw["sub"].(string)
	return Claims{Issuer: config.Issuer, Subject: subject, Audience: audience, Resources: resources, ExpiresAt: expires, NotBefore: notBefore, Raw: raw}, nil
}

func numericDate(value any) (time.Time, error) {
	number, ok := value.(json.Number)
	if !ok {
		return time.Time{}, errors.New("numeric date must be a JSON number")
	}
	seconds, err := number.Int64()
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(seconds, 0).UTC(), nil
}

func stringList(value any) ([]string, error) {
	if single, ok := value.(string); ok && single != "" {
		return []string{single}, nil
	}
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return nil, errors.New("claim must be a non-empty string or string array")
	}
	result := make([]string, len(items))
	for index, item := range items {
		text, ok := item.(string)
		if !ok || text == "" {
			return nil, errors.New("claim array contains a non-string")
		}
		result[index] = text
	}
	return result, nil
}

func parseJWK(encoded json.RawMessage) (string, crypto.PublicKey, error) {
	var key struct {
		Kty string `json:"kty"`
		Kid string `json:"kid"`
		Use string `json:"use"`
		Alg string `json:"alg"`
		N   string `json:"n"`
		E   string `json:"e"`
		Crv string `json:"crv"`
		X   string `json:"x"`
		Y   string `json:"y"`
	}
	if err := json.Unmarshal(encoded, &key); err != nil || key.Kid == "" || (key.Use != "" && key.Use != "sig") {
		return "", nil, errors.New("invalid signing JWK")
	}
	switch key.Kty {
	case "RSA":
		if key.Alg != "" && key.Alg != "RS256" {
			return "", nil, errors.New("RSA JWK algorithm is not RS256")
		}
		n, err := decodeBig(key.N)
		if err != nil {
			return "", nil, err
		}
		exponentBytes, err := base64.RawURLEncoding.DecodeString(key.E)
		if err != nil || len(exponentBytes) == 0 || len(exponentBytes) > 4 {
			return "", nil, errors.New("invalid RSA exponent")
		}
		exponent := 0
		for _, value := range exponentBytes {
			exponent = exponent<<8 | int(value)
		}
		if exponent < 3 || n.BitLen() < 2048 {
			return "", nil, errors.New("RSA signing key is too weak")
		}
		return key.Kid, &rsa.PublicKey{N: n, E: exponent}, nil
	case "EC":
		if key.Alg != "" && key.Alg != "ES256" {
			return "", nil, errors.New("EC JWK algorithm is not ES256")
		}
		if key.Crv != "P-256" {
			return "", nil, errors.New("unsupported EC curve")
		}
		x, err := decodeFixed(key.X, 32)
		if err != nil {
			return "", nil, err
		}
		y, err := decodeFixed(key.Y, 32)
		if err != nil {
			return "", nil, errors.New("invalid EC key")
		}
		public, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), append(append([]byte{4}, x...), y...))
		if err != nil {
			return "", nil, errors.New("invalid EC key")
		}
		return key.Kid, public, nil
	case "OKP":
		if key.Alg != "" && key.Alg != "EdDSA" {
			return "", nil, errors.New("OKP JWK algorithm is not EdDSA")
		}
		if key.Crv != "Ed25519" {
			return "", nil, errors.New("unsupported OKP curve")
		}
		value, err := base64.RawURLEncoding.DecodeString(key.X)
		if err != nil || len(value) != ed25519.PublicKeySize {
			return "", nil, errors.New("invalid Ed25519 key")
		}
		return key.Kid, ed25519.PublicKey(value), nil
	default:
		return "", nil, errors.New("unsupported JWK type")
	}
}

func decodeBig(value string) (*big.Int, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) == 0 {
		return nil, errors.New("invalid JWK integer")
	}
	return new(big.Int).SetBytes(decoded), nil
}

func decodeFixed(value string, size int) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) != size {
		return nil, errors.New("invalid JWK coordinate")
	}
	return decoded, nil
}

func verifySignature(algorithm string, key crypto.PublicKey, signed, signature []byte) error {
	digest := sha256.Sum256(signed)
	switch algorithm {
	case "RS256":
		public, ok := key.(*rsa.PublicKey)
		if !ok {
			return errors.New("key type does not match algorithm")
		}
		return rsa.VerifyPKCS1v15(public, crypto.SHA256, digest[:], signature)
	case "ES256":
		public, ok := key.(*ecdsa.PublicKey)
		if !ok || len(signature) != 64 {
			return errors.New("key type does not match algorithm")
		}
		if !ecdsa.Verify(public, digest[:], new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:])) {
			return errors.New("invalid signature")
		}
		return nil
	case "EdDSA":
		public, ok := key.(ed25519.PublicKey)
		if !ok || !ed25519.Verify(public, signed, signature) {
			return errors.New("invalid signature")
		}
		return nil
	default:
		return errors.New("unsupported algorithm")
	}
}

func invalid(err error) error { return &VerificationError{Code: CodeInvalidToken, err: err} }
func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
func intersects(left, right []string) bool {
	for _, value := range left {
		if contains(right, value) {
			return true
		}
	}
	return false
}
