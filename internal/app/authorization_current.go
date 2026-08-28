package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

// HighRiskWrite identifies an exact trusted risk/side-effect combination whose
// authorization must be obtained live immediately before protected execution.
type HighRiskWrite struct {
	Risk       string
	SideEffect string
}

type CurrentAuthorizationConfig struct {
	HighRiskWrites []HighRiskWrite
	MaxDecisionAge time.Duration
	Clock          func() time.Time
	Revocations    RevocationState
}

// CurrentAuthorizer routes ordinary requests through regular, but never lets a
// configured high-risk write use that path's cache or stale fallback.
type CurrentAuthorizer struct {
	regular        ports.Authorizer
	live           ports.Authorizer
	highRiskWrites map[HighRiskWrite]struct{}
	maxDecisionAge time.Duration
	clock          func() time.Time
	revocations    RevocationState
}

func NewCurrentAuthorizer(regular, live ports.Authorizer, config CurrentAuthorizationConfig) (*CurrentAuthorizer, error) {
	if regular == nil || live == nil || config.MaxDecisionAge <= 0 || config.Revocations == nil || len(config.HighRiskWrites) == 0 {
		return nil, errors.New("current authorization configuration is invalid")
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	rules := make(map[HighRiskWrite]struct{}, len(config.HighRiskWrites))
	for _, rule := range config.HighRiskWrites {
		if strings.TrimSpace(rule.Risk) == "" || strings.TrimSpace(rule.SideEffect) == "" {
			return nil, errors.New("high-risk write rule is invalid")
		}
		rules[rule] = struct{}{}
	}
	return &CurrentAuthorizer{regular: regular, live: live, highRiskWrites: rules, maxDecisionAge: config.MaxDecisionAge,
		clock: config.Clock, revocations: config.Revocations}, nil
}

func (authorizer *CurrentAuthorizer) AuthorizeToolInvocation(ctx context.Context, request ports.AuthorizationRequest) (ports.AuthorizationDecision, error) {
	if authorizer == nil {
		return ports.AuthorizationDecision{}, errors.New("current authorizer is nil")
	}
	if _, required := authorizer.highRiskWrites[HighRiskWrite{Risk: request.Risk, SideEffect: request.SideEffect}]; !required {
		return authorizer.regular.AuthorizeToolInvocation(ctx, request)
	}

	// Observe revocations before the live decision so that decision must be at
	// least as current as the observation which triggered it.
	epoch, checkpoint, err := authorizer.revocations.Current(ctx, request.TenantID, request.PolicyProfile)
	if err != nil {
		return ports.AuthorizationDecision{}, errors.New("mandatory authorization freshness unavailable")
	}
	decision, err := authorizer.live.AuthorizeToolInvocation(ctx, request)
	if err != nil {
		return ports.AuthorizationDecision{}, err
	}
	now := authorizer.clock().UTC()
	if err := validateFreshDecision(decision, request, now); err != nil {
		return ports.AuthorizationDecision{}, err
	}
	if now.Sub(decision.IssuedAt) > authorizer.maxDecisionAge || !matchesRevocation(decision, epoch, checkpoint) {
		return ports.AuthorizationDecision{}, errors.New("mandatory authorization freshness cannot be established")
	}
	return cloneDecision(decision), nil
}
