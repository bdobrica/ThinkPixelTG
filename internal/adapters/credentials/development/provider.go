// Package development provides an explicitly non-production credential source
// for local development and isolated tests.
package development

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/config"
	credentialcore "github.com/bdobrica/ThinkPixelTG/internal/credentials"
	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

const maximumSecretBytes = 64 << 10

var environmentName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,127}$`)

type Config struct {
	Mode       config.Mode
	Clock      domain.Clock
	LookupEnv  func(string) (string, bool)
	ReadFile   func(string) ([]byte, error)
	DefaultTTL time.Duration
}

type Provider struct {
	clock      domain.Clock
	lookupEnv  func(string) (string, bool)
	readFile   func(string) ([]byte, error)
	defaultTTL time.Duration
}

var _ ports.CredentialProvider = (*Provider)(nil)

func New(providerConfig Config) (*Provider, error) {
	if providerConfig.Mode != config.ModeDevelopment {
		return nil, errors.New("development credential provider is forbidden outside development mode")
	}
	if providerConfig.Clock == nil {
		return nil, errors.New("development credential provider requires a clock")
	}
	if providerConfig.DefaultTTL == 0 {
		providerConfig.DefaultTTL = 5 * time.Minute
	}
	if providerConfig.DefaultTTL <= 0 || providerConfig.DefaultTTL > 24*time.Hour {
		return nil, errors.New("development credential provider TTL is invalid")
	}
	if providerConfig.LookupEnv == nil {
		providerConfig.LookupEnv = os.LookupEnv
	}
	if providerConfig.ReadFile == nil {
		providerConfig.ReadFile = readBoundedFile
	}
	return &Provider{clock: providerConfig.Clock, lookupEnv: providerConfig.LookupEnv, readFile: providerConfig.ReadFile, defaultTTL: providerConfig.DefaultTTL}, nil
}

func (provider *Provider) Resolve(ctx context.Context, binding domain.CredentialBinding, governed ports.CredentialProviderContext) (ports.CredentialCapability, error) {
	if provider == nil || ctx == nil {
		return nil, errors.New("development credential provider is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, errors.New("development credential resolution was cancelled")
	}
	definition := binding.Definition()
	if !definition.Enabled || definition.Provider.ProviderType != "development" {
		return nil, errors.New("development credential binding is invalid")
	}
	if definition.Delegation.Mode != domain.DelegationNone || !validGovernedValue(governed.Subject) || !validGovernedValue(governed.RunID) {
		return nil, errors.New("development credential subject policy is invalid")
	}

	secret, err := provider.load(definition.Provider.ProviderRef)
	if err != nil {
		return nil, err
	}
	defer erase(secret)
	now := domain.UTCNow(provider.clock)
	ttl := provider.defaultTTL
	if definition.Cache.MaximumTTL > 0 && definition.Cache.MaximumTTL < ttl {
		ttl = definition.Cache.MaximumTTL
	}
	metadata := ports.CredentialCapabilityMetadata{
		Kind: definition.Provider.Capability, ProviderRef: definition.Provider.ProviderRef,
		Audiences: append([]string(nil), definition.Provider.Audiences...), Resources: append([]string(nil), definition.Provider.Resources...), Scopes: append([]string(nil), definition.Provider.Scopes...),
		IssuedAt: now, ExpiresAt: now.Add(ttl), RevocationEpoch: definition.RevocationEpoch,
	}
	capability, err := credentialcore.NewCapability(metadata, secret, provider.clock)
	if err != nil {
		return nil, errors.New("development credential capability is invalid")
	}
	return capability, nil
}

func validGovernedValue(value string) bool {
	return value != "" && len(value) <= 512 && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}

func (provider *Provider) load(reference string) ([]byte, error) {
	switch {
	case strings.HasPrefix(reference, "env:"):
		name := strings.TrimPrefix(reference, "env:")
		if !environmentName.MatchString(name) {
			return nil, errors.New("development credential environment reference is invalid")
		}
		value, ok := provider.lookupEnv(name)
		if !ok || value == "" || len(value) > maximumSecretBytes {
			return nil, errors.New("development credential environment value is unavailable")
		}
		return []byte(value), nil
	case strings.HasPrefix(reference, "file:"):
		path := strings.TrimPrefix(reference, "file:")
		if len(path) > 4096 || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, '\x00') {
			return nil, errors.New("development credential file reference is invalid")
		}
		value, err := provider.readFile(path)
		if err != nil || len(value) == 0 || len(value) > maximumSecretBytes {
			erase(value)
			return nil, errors.New("development credential file value is unavailable")
		}
		return value, nil
	default:
		return nil, errors.New("development credential source is invalid")
	}
}

func readBoundedFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return io.ReadAll(io.LimitReader(file, maximumSecretBytes+1))
}

func erase(value []byte) {
	for index := range value {
		value[index] = 0
	}
	runtime.KeepAlive(value)
}
