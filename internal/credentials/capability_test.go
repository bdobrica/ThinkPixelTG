package credentials

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

type mutableClock struct{ now time.Time }

func (clock *mutableClock) Now() time.Time { return clock.now }

func TestCapabilityProvidesScopedSecretAndErasesCopies(t *testing.T) {
	clock := &mutableClock{now: time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)}
	input := []byte("synthetic-secret-canary")
	capability, err := NewCapability(validCapabilityMetadata(clock.now), input, clock)
	if err != nil {
		t.Fatal(err)
	}
	input[0] = 'X'
	var retained []byte
	if err := capability.UseSecret(func(secret []byte) error {
		if string(secret) != "synthetic-secret-canary" {
			t.Fatalf("secret = %q", secret)
		}
		retained = secret
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(retained, make([]byte, len(retained))) {
		t.Fatal("ephemeral callback copy was not erased")
	}
	capability.Release()
	capability.Release()
	if metadata := capability.Metadata(); metadata.LeaseID != "" || metadata.RefreshID != "" {
		t.Fatal("released capability retained lease metadata")
	}
	if err := capability.UseSecret(func([]byte) error { return nil }); err == nil {
		t.Fatal("released capability remained usable")
	}
}

func TestCapabilityExpiresAndMetadataIsDefensivelyCopied(t *testing.T) {
	clock := &mutableClock{now: time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)}
	metadata := validCapabilityMetadata(clock.now)
	capability, err := NewCapability(metadata, []byte("synthetic-secret-canary"), clock)
	if err != nil {
		t.Fatal(err)
	}
	metadata.Scopes[0] = "admin"
	returned := capability.Metadata()
	returned.Audiences[0] = "attacker"
	if got := capability.Metadata(); got.Scopes[0] != "pull_request:write" || got.Audiences[0] != "api.github.com" {
		t.Fatalf("metadata was mutable: %#v", got)
	}
	clock.now = returned.ExpiresAt
	if err := capability.UseSecret(func([]byte) error { return nil }); err == nil {
		t.Fatal("expired capability remained usable")
	}
}

func TestCapabilityRedactsEveryRepresentation(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	capability, err := NewCapability(validCapabilityMetadata(now), []byte("synthetic-secret-canary"), &mutableClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(capability)
	if err != nil {
		t.Fatal(err)
	}
	representations := []string{
		capability.String(), capability.GoString(), capability.Error(), string(encoded),
		fmt.Sprintf("%s", capability), fmt.Sprintf("%v", capability), fmt.Sprintf("%+v", capability), fmt.Sprintf("%#v", capability),
		fmt.Errorf("wrapped: %w", capability).Error(),
	}
	for _, representation := range representations {
		if strings.Contains(representation, "synthetic-secret-canary") || strings.Contains(representation, "provider:github") || !strings.Contains(representation, redacted) {
			t.Fatalf("unsafe representation %q", representation)
		}
	}
}

func TestCapabilityRejectsInvalidMaterialAndMetadata(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(*ports.CredentialCapabilityMetadata, *[]byte, *domain.Clock){
		"nil clock":    func(_ *ports.CredentialCapabilityMetadata, _ *[]byte, clock *domain.Clock) { *clock = nil },
		"empty secret": func(_ *ports.CredentialCapabilityMetadata, secret *[]byte, _ *domain.Clock) { *secret = nil },
		"kind": func(metadata *ports.CredentialCapabilityMetadata, _ *[]byte, _ *domain.Clock) {
			metadata.Kind = "raw_header"
		},
		"provider": func(metadata *ports.CredentialCapabilityMetadata, _ *[]byte, _ *domain.Clock) {
			metadata.ProviderRef = "bad\nref"
		},
		"lifetime": func(metadata *ports.CredentialCapabilityMetadata, _ *[]byte, _ *domain.Clock) {
			metadata.ExpiresAt = metadata.IssuedAt
		},
		"expired": func(metadata *ports.CredentialCapabilityMetadata, _ *[]byte, _ *domain.Clock) {
			metadata.ExpiresAt = now
		},
		"OAuth target": func(metadata *ports.CredentialCapabilityMetadata, _ *[]byte, _ *domain.Clock) {
			metadata.Audiences = nil
			metadata.Resources = nil
		},
		"duplicate scope": func(metadata *ports.CredentialCapabilityMetadata, _ *[]byte, _ *domain.Clock) {
			metadata.Scopes = []string{"repo", "repo"}
		},
		"lease": func(metadata *ports.CredentialCapabilityMetadata, _ *[]byte, _ *domain.Clock) {
			metadata.LeaseID = "bad\rlease"
		},
	} {
		t.Run(name, func(t *testing.T) {
			metadata := validCapabilityMetadata(now)
			secret := []byte("synthetic-secret-canary")
			var clock domain.Clock = &mutableClock{now: now}
			mutate(&metadata, &secret, &clock)
			if _, err := NewCapability(metadata, secret, clock); err == nil {
				t.Fatal("invalid capability accepted")
			}
		})
	}
}

func validCapabilityMetadata(now time.Time) ports.CredentialCapabilityMetadata {
	return ports.CredentialCapabilityMetadata{
		Kind: domain.CapabilityOAuthAccessToken, ProviderRef: "provider:github", Issuer: "https://identity.example",
		Audiences: []string{"api.github.com"}, Resources: []string{"repository:owner/name"}, Scopes: []string{"pull_request:write"},
		IssuedAt: now.Add(-time.Minute), ExpiresAt: now.Add(10 * time.Minute), LeaseID: "lease-42", RefreshID: "refresh-42", RevocationEpoch: "epoch-42",
	}
}
