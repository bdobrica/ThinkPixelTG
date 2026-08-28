package authn

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestLocalWorkloadSourceIsExplicitAndSeparate(t *testing.T) {
	source, err := NewLocalWorkloadSource("local://thinkpixeltg/developer")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := source.Resolve(context.Background(), nil)
	if err != nil || identity.ID != "local://thinkpixeltg/developer" || identity.Source != "local" {
		t.Fatalf("identity = %#v, error = %v", identity, err)
	}
	if _, err := NewLocalWorkloadSource(" "); err == nil {
		t.Fatal("empty local identity accepted")
	}
}

func TestMTLSWorkloadSourceRequiresVerifiedAllowedSPIFFEID(t *testing.T) {
	source, err := NewMTLSWorkloadSource("example.org")
	if err != nil {
		t.Fatal(err)
	}
	spiffeID, _ := url.Parse("spiffe://example.org/ns/default/sa/thinkpixeltg")
	leaf := &x509.Certificate{Raw: []byte("leaf"), URIs: []*url.URL{spiffeID}}
	request := httptest.NewRequest("GET", "https://tg.example/v1/tools", nil)
	request.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{leaf}}}
	identity, err := source.Resolve(context.Background(), request)
	if err != nil || identity.ID != spiffeID.String() || identity.Source != "spiffe-mtls" {
		t.Fatalf("identity = %#v, error = %v", identity, err)
	}

	tests := []struct {
		name string
		tls  *tls.ConnectionState
	}{
		{name: "no TLS"},
		{name: "unverified peer", tls: &tls.ConnectionState{PeerCertificates: []*x509.Certificate{leaf}}},
		{name: "wrong trust domain", tls: verifiedTLSCertificate("spiffe://attacker.example/workload")},
		{name: "non SPIFFE", tls: verifiedTLSCertificate("https://example.org/workload")},
		{name: "multiple IDs", tls: &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{Raw: []byte("multiple"), URIs: []*url.URL{spiffeID, spiffeID}}}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := httptest.NewRequest("GET", "https://tg.example/v1/tools", nil)
			candidate.TLS = test.tls
			if _, err := source.Resolve(context.Background(), candidate); err == nil {
				t.Fatal("Resolve() error = nil")
			}
		})
	}
}

func verifiedTLSCertificate(rawID string) *tls.ConnectionState {
	id, _ := url.Parse(rawID)
	return &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{Raw: []byte(rawID), URIs: []*url.URL{id}}}}}
}
