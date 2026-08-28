package authn

import (
	"context"
	"crypto/x509"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// WorkloadIdentity identifies the authenticated process or pod. It is
// provenance, not tenant, end-user, agent, or run authority.
type WorkloadIdentity struct {
	ID     string
	Source string
}

type workloadContextKey struct{}

func withWorkloadIdentity(ctx context.Context, identity WorkloadIdentity) context.Context {
	return context.WithValue(ctx, workloadContextKey{}, identity)
}

func WorkloadIdentityFromContext(ctx context.Context) (WorkloadIdentity, bool) {
	identity, ok := ctx.Value(workloadContextKey{}).(WorkloadIdentity)
	return identity, ok
}

// WorkloadIdentitySource authenticates the process/pod independently of the
// bearer credential.
type WorkloadIdentitySource interface {
	Resolve(context.Context, *http.Request) (WorkloadIdentity, error)
}

type LocalWorkloadSource struct{ identity WorkloadIdentity }

// NewLocalWorkloadSource creates an explicit development-only identity source.
func NewLocalWorkloadSource(id string) (*LocalWorkloadSource, error) {
	if !validIdentityValue(id) {
		return nil, errors.New("local workload identity is required")
	}
	return &LocalWorkloadSource{identity: WorkloadIdentity{ID: id, Source: "local"}}, nil
}

func (source *LocalWorkloadSource) Resolve(context.Context, *http.Request) (WorkloadIdentity, error) {
	if source == nil {
		return WorkloadIdentity{}, errors.New("local workload source is nil")
	}
	return source.identity, nil
}

type MTLSWorkloadSource struct{ trustDomains map[string]struct{} }

// NewMTLSWorkloadSource accepts SPIFFE IDs only from verified client
// certificate chains in one of the explicitly configured trust domains.
func NewMTLSWorkloadSource(trustDomains ...string) (*MTLSWorkloadSource, error) {
	configured := make(map[string]struct{}, len(trustDomains))
	for _, domain := range trustDomains {
		if domain == "" || strings.TrimSpace(domain) != domain || strings.ContainsAny(domain, "/:@") {
			return nil, errors.New("invalid SPIFFE trust domain")
		}
		configured[strings.ToLower(domain)] = struct{}{}
	}
	if len(configured) == 0 {
		return nil, errors.New("at least one SPIFFE trust domain is required")
	}
	return &MTLSWorkloadSource{trustDomains: configured}, nil
}

func (source *MTLSWorkloadSource) Resolve(_ context.Context, request *http.Request) (WorkloadIdentity, error) {
	if source == nil || request == nil || request.TLS == nil || len(request.TLS.VerifiedChains) == 0 {
		return WorkloadIdentity{}, errors.New("verified workload certificate is required")
	}
	leaf, err := verifiedLeaf(request.TLS.VerifiedChains)
	if err != nil {
		return WorkloadIdentity{}, err
	}
	if len(leaf.URIs) != 1 || !validSPIFFEID(leaf.URIs[0], source.trustDomains) {
		return WorkloadIdentity{}, errors.New("exactly one allowed SPIFFE ID is required")
	}
	return WorkloadIdentity{ID: leaf.URIs[0].String(), Source: "spiffe-mtls"}, nil
}

func verifiedLeaf(chains [][]*x509.Certificate) (*x509.Certificate, error) {
	var leaf *x509.Certificate
	for _, chain := range chains {
		if len(chain) == 0 {
			return nil, errors.New("verified certificate chain is empty")
		}
		if leaf == nil {
			leaf = chain[0]
		} else if !leaf.Equal(chain[0]) {
			return nil, errors.New("verified certificate chains disagree on leaf")
		}
	}
	return leaf, nil
}

func validSPIFFEID(id *url.URL, trustDomains map[string]struct{}) bool {
	if id == nil || id.Scheme != "spiffe" || id.Host == "" || id.User != nil || id.RawQuery != "" || id.Fragment != "" || id.Path == "" || id.Path == "/" {
		return false
	}
	_, allowed := trustDomains[strings.ToLower(id.Host)]
	return allowed && id.String() == strings.TrimSpace(id.String())
}
