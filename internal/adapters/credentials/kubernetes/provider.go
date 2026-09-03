// Package kubernetes resolves audience-bound service-account tokens projected
// by kubelet from explicitly allowlisted paths.
package kubernetes

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/config"
	credentialcore "github.com/bdobrica/ThinkPixelTG/internal/credentials"
	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

const maximumTokenBytes = 64 << 10

type Config struct {
	Mode                 config.Mode
	Clock                domain.Clock
	AllowedTokenPaths    []string
	TrustedIssuers       []string
	ReadFile             func(string) ([]byte, error)
	MaximumTokenLifetime time.Duration
	ClockSkew            time.Duration
}

type Provider struct {
	clock           domain.Clock
	allowedPaths    map[string]struct{}
	trustedIssuers  map[string]struct{}
	readFile        func(string) ([]byte, error)
	maximumLifetime time.Duration
	clockSkew       time.Duration
}

var _ ports.CredentialProvider = (*Provider)(nil)

func New(providerConfig Config) (*Provider, error) {
	if providerConfig.Mode != config.ModeProduction && providerConfig.Mode != config.ModeDevelopment {
		return nil, errors.New("kubernetes projected-token provider mode is invalid")
	}
	if providerConfig.Clock == nil || len(providerConfig.AllowedTokenPaths) == 0 || len(providerConfig.TrustedIssuers) == 0 {
		return nil, errors.New("kubernetes projected-token provider configuration is incomplete")
	}
	if providerConfig.MaximumTokenLifetime == 0 {
		providerConfig.MaximumTokenLifetime = time.Hour
	}
	if providerConfig.MaximumTokenLifetime <= 0 || providerConfig.MaximumTokenLifetime > 24*time.Hour || providerConfig.ClockSkew < 0 || providerConfig.ClockSkew > 5*time.Minute {
		return nil, errors.New("kubernetes projected-token provider lifetime policy is invalid")
	}
	allowedPaths := make(map[string]struct{}, len(providerConfig.AllowedTokenPaths))
	for _, path := range providerConfig.AllowedTokenPaths {
		if !validAbsolutePath(path) {
			return nil, errors.New("kubernetes projected-token path allowlist is invalid")
		}
		if _, exists := allowedPaths[path]; exists {
			return nil, errors.New("kubernetes projected-token path allowlist contains duplicates")
		}
		allowedPaths[path] = struct{}{}
	}
	trustedIssuers := make(map[string]struct{}, len(providerConfig.TrustedIssuers))
	for _, issuer := range providerConfig.TrustedIssuers {
		if !safeText(issuer, 512) {
			return nil, errors.New("kubernetes projected-token issuer allowlist is invalid")
		}
		if _, exists := trustedIssuers[issuer]; exists {
			return nil, errors.New("kubernetes projected-token issuer allowlist contains duplicates")
		}
		trustedIssuers[issuer] = struct{}{}
	}
	if providerConfig.ReadFile == nil {
		providerConfig.ReadFile = readBoundedFile
	}
	return &Provider{clock: providerConfig.Clock, allowedPaths: allowedPaths, trustedIssuers: trustedIssuers, readFile: providerConfig.ReadFile, maximumLifetime: providerConfig.MaximumTokenLifetime, clockSkew: providerConfig.ClockSkew}, nil
}

func (provider *Provider) Resolve(ctx context.Context, binding domain.CredentialBinding, governedSubject string) (ports.CredentialCapability, error) {
	if provider == nil || ctx == nil {
		return nil, errors.New("kubernetes projected credential provider is unavailable")
	}
	if ctx.Err() != nil {
		return nil, errors.New("kubernetes projected credential resolution was cancelled")
	}
	definition := binding.Definition()
	if !definition.Enabled || definition.Provider.ProviderType != "kubernetes_projected" || definition.Provider.Capability != domain.CapabilityAPIToken {
		return nil, errors.New("kubernetes projected credential binding is invalid")
	}
	if definition.Delegation.Mode != domain.DelegationNone || !safeText(governedSubject, 512) {
		return nil, errors.New("kubernetes projected credential subject policy is invalid")
	}
	path, ok := strings.CutPrefix(definition.Provider.ProviderRef, "projected:")
	if !ok || !validAbsolutePath(path) {
		return nil, errors.New("kubernetes projected credential reference is invalid")
	}
	if _, allowed := provider.allowedPaths[path]; !allowed {
		return nil, errors.New("kubernetes projected credential path is not allowed")
	}
	token, err := provider.readFile(path)
	if err != nil || len(token) == 0 || len(token) > maximumTokenBytes {
		erase(token)
		return nil, errors.New("kubernetes projected credential is unavailable")
	}
	defer erase(token)

	claims, err := parseClaims(token)
	if err != nil {
		return nil, err
	}
	if _, trusted := provider.trustedIssuers[claims.issuer]; !trusted {
		return nil, errors.New("kubernetes projected credential issuer is not trusted")
	}
	if !containsAll(claims.audiences, definition.Provider.Audiences) {
		return nil, errors.New("kubernetes projected credential audience does not match binding")
	}
	now := domain.UTCNow(provider.clock)
	if claims.issuedAt.After(now.Add(provider.clockSkew)) || !now.Before(claims.expiresAt) || claims.expiresAt.Sub(claims.issuedAt) > provider.maximumLifetime {
		return nil, errors.New("kubernetes projected credential lifetime is invalid")
	}
	expiresAt := claims.expiresAt
	if definition.Cache.MaximumTTL > 0 && now.Add(definition.Cache.MaximumTTL).Before(expiresAt) {
		expiresAt = now.Add(definition.Cache.MaximumTTL)
	}
	metadata := ports.CredentialCapabilityMetadata{
		Kind: domain.CapabilityAPIToken, ProviderRef: definition.Provider.ProviderRef, Issuer: claims.issuer,
		Audiences: append([]string(nil), definition.Provider.Audiences...), Resources: append([]string(nil), definition.Provider.Resources...), Scopes: append([]string(nil), definition.Provider.Scopes...),
		IssuedAt: claims.issuedAt, ExpiresAt: expiresAt, RevocationEpoch: definition.RevocationEpoch,
	}
	capability, err := credentialcore.NewCapability(metadata, token, provider.clock)
	if err != nil {
		return nil, errors.New("kubernetes projected credential capability is invalid")
	}
	return capability, nil
}

type tokenClaims struct {
	issuer    string
	audiences []string
	issuedAt  time.Time
	expiresAt time.Time
}

func parseClaims(token []byte) (tokenClaims, error) {
	parts := bytes.Split(token, []byte{'.'})
	if len(parts) != 3 || len(parts[0]) == 0 || len(parts[1]) == 0 || len(parts[2]) == 0 {
		return tokenClaims{}, errors.New("kubernetes projected credential format is invalid")
	}
	payload := make([]byte, base64.RawURLEncoding.DecodedLen(len(parts[1])))
	decoded, err := base64.RawURLEncoding.Decode(payload, parts[1])
	if err != nil || decoded == 0 || decoded > maximumTokenBytes {
		erase(payload)
		return tokenClaims{}, errors.New("kubernetes projected credential claims are invalid")
	}
	payload = payload[:decoded]
	defer erase(payload)
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var values map[string]any
	if err := decoder.Decode(&values); err != nil {
		return tokenClaims{}, errors.New("kubernetes projected credential claims are invalid")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return tokenClaims{}, errors.New("kubernetes projected credential claims are invalid")
	}
	issuer, ok := values["iss"].(string)
	if !ok || !safeText(issuer, 512) {
		return tokenClaims{}, errors.New("kubernetes projected credential claims are invalid")
	}
	audiences, err := claimAudiences(values["aud"])
	if err != nil {
		return tokenClaims{}, err
	}
	issuedAt, err := numericDate(values["iat"])
	if err != nil {
		return tokenClaims{}, err
	}
	expiresAt, err := numericDate(values["exp"])
	if err != nil || !expiresAt.After(issuedAt) {
		return tokenClaims{}, errors.New("kubernetes projected credential claims are invalid")
	}
	return tokenClaims{issuer: issuer, audiences: audiences, issuedAt: issuedAt, expiresAt: expiresAt}, nil
}

func claimAudiences(value any) ([]string, error) {
	var audiences []string
	switch typed := value.(type) {
	case string:
		audiences = []string{typed}
	case []any:
		for _, item := range typed {
			audience, ok := item.(string)
			if !ok {
				return nil, errors.New("kubernetes projected credential claims are invalid")
			}
			audiences = append(audiences, audience)
		}
	default:
		return nil, errors.New("kubernetes projected credential claims are invalid")
	}
	if len(audiences) == 0 || len(audiences) > 32 {
		return nil, errors.New("kubernetes projected credential claims are invalid")
	}
	sort.Strings(audiences)
	for index, audience := range audiences {
		if !safeText(audience, 512) || index > 0 && audience == audiences[index-1] {
			return nil, errors.New("kubernetes projected credential claims are invalid")
		}
	}
	return audiences, nil
}

func numericDate(value any) (time.Time, error) {
	number, ok := value.(json.Number)
	if !ok {
		return time.Time{}, errors.New("kubernetes projected credential claims are invalid")
	}
	seconds, err := number.Int64()
	if err != nil || seconds < 0 {
		return time.Time{}, errors.New("kubernetes projected credential claims are invalid")
	}
	return time.Unix(seconds, 0).UTC(), nil
}

func containsAll(actual, expected []string) bool {
	available := make(map[string]struct{}, len(actual))
	for _, value := range actual {
		available[value] = struct{}{}
	}
	for _, value := range expected {
		if _, ok := available[value]; !ok {
			return false
		}
	}
	return len(expected) > 0
}

func validAbsolutePath(path string) bool {
	return path != "" && len(path) <= 4096 && filepath.IsAbs(path) && filepath.Clean(path) == path && !strings.ContainsAny(path, "\x00\r\n")
}

func safeText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}

func readBoundedFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(io.LimitReader(file, maximumTokenBytes+1))
}

func erase(value []byte) {
	for index := range value {
		value[index] = 0
	}
	runtime.KeepAlive(value)
}
