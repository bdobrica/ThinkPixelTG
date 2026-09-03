package credentials

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

const redacted = "[REDACTED]"

var _ ports.CredentialCapability = (*Capability)(nil)
var _ error = (*Capability)(nil)

// Capability owns one short-lived credential value. It is intentionally not
// constructible without a clock so expiry remains deterministic in tests and
// provider adapters.
type Capability struct {
	mu       sync.Mutex
	metadata ports.CredentialCapabilityMetadata
	secret   []byte
	clock    domain.Clock
	released bool
}

func NewCapability(metadata ports.CredentialCapabilityMetadata, secret []byte, clock domain.Clock) (*Capability, error) {
	if clock == nil || len(secret) == 0 || len(secret) > 64<<10 {
		return nil, errors.New("credential capability material is invalid")
	}
	if err := validateMetadata(metadata, domain.UTCNow(clock)); err != nil {
		return nil, err
	}
	return &Capability{metadata: cloneMetadata(metadata), secret: append([]byte(nil), secret...), clock: clock}, nil
}

func (capability *Capability) Metadata() ports.CredentialCapabilityMetadata {
	if capability == nil {
		return ports.CredentialCapabilityMetadata{}
	}
	capability.mu.Lock()
	defer capability.mu.Unlock()
	return cloneMetadata(capability.metadata)
}

// UseSecret supplies an ephemeral copy and erases it immediately after the
// callback. Keeping the callback under the lock prevents concurrent release.
func (capability *Capability) UseSecret(use func([]byte) error) error {
	if capability == nil || use == nil {
		return errors.New("credential capability use is invalid")
	}
	capability.mu.Lock()
	defer capability.mu.Unlock()
	if capability.released {
		return errors.New("credential capability is released")
	}
	if !domain.UTCNow(capability.clock).Before(capability.metadata.ExpiresAt) {
		return errors.New("credential capability is expired")
	}
	material := append([]byte(nil), capability.secret...)
	defer zero(material)
	return use(material)
}

// Release is idempotent and erases the owned credential bytes.
func (capability *Capability) Release() {
	if capability == nil {
		return
	}
	capability.mu.Lock()
	defer capability.mu.Unlock()
	zero(capability.secret)
	capability.secret = nil
	capability.metadata.LeaseID = ""
	capability.metadata.RefreshID = ""
	capability.released = true
}

func (*Capability) String() string                 { return redacted }
func (*Capability) GoString() string               { return redacted }
func (*Capability) Error() string                  { return redacted }
func (*Capability) MarshalText() ([]byte, error)   { return []byte(redacted), nil }
func (*Capability) MarshalJSON() ([]byte, error)   { return json.Marshal(redacted) }
func (*Capability) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, redacted) }

func validateMetadata(metadata ports.CredentialCapabilityMetadata, now time.Time) error {
	if metadata.Kind != domain.CapabilityOAuthAccessToken && metadata.Kind != domain.CapabilityAPIToken && metadata.Kind != domain.CapabilityMTLS && metadata.Kind != domain.CapabilitySignedRequest {
		return errors.New("credential capability kind is invalid")
	}
	if !safe(metadata.ProviderRef, 512) || !optionalSafe(metadata.Issuer, 512) || metadata.IssuedAt.IsZero() || metadata.ExpiresAt.IsZero() || !metadata.ExpiresAt.After(metadata.IssuedAt) || !now.Before(metadata.ExpiresAt) {
		return errors.New("credential capability lifetime or provider metadata is invalid")
	}
	if err := validateSet(metadata.Audiences, 32, 512); err != nil {
		return err
	}
	if err := validateSet(metadata.Resources, 32, 512); err != nil {
		return err
	}
	if err := validateSet(metadata.Scopes, 64, 256); err != nil {
		return err
	}
	if metadata.Kind == domain.CapabilityOAuthAccessToken && len(metadata.Audiences) == 0 && len(metadata.Resources) == 0 {
		return errors.New("OAuth capability requires an audience or resource")
	}
	for _, reference := range []string{metadata.LeaseID, metadata.RefreshID, metadata.RevocationEpoch} {
		if !optionalSafe(reference, 512) {
			return errors.New("credential capability lifecycle metadata is invalid")
		}
	}
	return nil
}

func validateSet(values []string, maximumCount, maximumLength int) error {
	if len(values) > maximumCount {
		return errors.New("credential capability target metadata exceeds bounds")
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !safe(value, maximumLength) {
			return errors.New("credential capability target metadata is invalid")
		}
		if _, exists := seen[value]; exists {
			return errors.New("credential capability target metadata contains duplicates")
		}
		seen[value] = struct{}{}
	}
	return nil
}

func safe(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}

func optionalSafe(value string, maximum int) bool { return value == "" || safe(value, maximum) }

func cloneMetadata(metadata ports.CredentialCapabilityMetadata) ports.CredentialCapabilityMetadata {
	metadata.Audiences = append([]string(nil), metadata.Audiences...)
	metadata.Resources = append([]string(nil), metadata.Resources...)
	metadata.Scopes = append([]string(nil), metadata.Scopes...)
	return metadata
}

func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
	runtime.KeepAlive(value)
}
