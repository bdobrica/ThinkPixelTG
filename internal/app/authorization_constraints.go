package app

import (
	"errors"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

// ConstraintCeiling is the immutable tool-version and deployment envelope. An
// AG decision can only remove members or lower numeric limits from this value.
type ConstraintCeiling struct {
	Repositories   []string
	Resources      []string
	Actions        []string
	ArgumentMax    map[string]int64
	MaxResultBytes int64
	MaxDuration    time.Duration
}

// NarrowAuthorizationConstraints intersects an allow decision with local
// immutable limits. Empty AG sets mean no additional set restriction; a
// non-empty set whose intersection is empty fails closed.
func NarrowAuthorizationConstraints(decision ports.AuthorizationDecision, ceiling ConstraintCeiling) (ports.AuthorizationConstraints, error) {
	if decision.Outcome != ports.AuthorizationAllow {
		return ports.AuthorizationConstraints{}, errors.New("authorization allow decision is required")
	}
	local := ports.AuthorizationConstraints{Repositories: ceiling.Repositories, Resources: ceiling.Resources,
		Actions: ceiling.Actions, ArgumentMax: ceiling.ArgumentMax, MaxResultBytes: ceiling.MaxResultBytes, MaxDuration: ceiling.MaxDuration}
	if err := local.Validate(); err != nil {
		return ports.AuthorizationConstraints{}, errors.New("local authorization ceiling is invalid")
	}
	if err := decision.Constraints.Validate(); err != nil {
		return ports.AuthorizationConstraints{}, err
	}
	repositories, err := narrowSet(ceiling.Repositories, decision.Constraints.Repositories)
	if err != nil {
		return ports.AuthorizationConstraints{}, err
	}
	resources, err := narrowSet(ceiling.Resources, decision.Constraints.Resources)
	if err != nil {
		return ports.AuthorizationConstraints{}, err
	}
	actions, err := narrowSet(ceiling.Actions, decision.Constraints.Actions)
	if err != nil {
		return ports.AuthorizationConstraints{}, err
	}
	arguments := make(map[string]int64, len(ceiling.ArgumentMax))
	for name, maximum := range ceiling.ArgumentMax {
		arguments[name] = maximum
	}
	for name, maximum := range decision.Constraints.ArgumentMax {
		localMaximum, exists := ceiling.ArgumentMax[name]
		if !exists {
			return ports.AuthorizationConstraints{}, errors.New("authorization constrained an unknown argument")
		}
		if maximum < localMaximum {
			arguments[name] = maximum
		}
	}
	return ports.AuthorizationConstraints{Repositories: repositories, Resources: resources, Actions: actions,
		ArgumentMax: arguments, MaxResultBytes: narrowInt64(ceiling.MaxResultBytes, decision.Constraints.MaxResultBytes),
		MaxDuration: narrowDuration(ceiling.MaxDuration, decision.Constraints.MaxDuration)}, nil
}

func narrowSet(ceiling, restriction []string) ([]string, error) {
	if len(restriction) == 0 {
		return append([]string(nil), ceiling...), nil
	}
	allowed := make(map[string]struct{}, len(restriction))
	for _, value := range restriction {
		allowed[value] = struct{}{}
	}
	result := make([]string, 0, len(ceiling))
	for _, value := range ceiling {
		if _, ok := allowed[value]; ok {
			result = append(result, value)
		}
	}
	if len(ceiling) != 0 && len(result) == 0 {
		return nil, errors.New("authorization constraint intersection is empty")
	}
	return result, nil
}

func narrowInt64(ceiling, restriction int64) int64 {
	if restriction > 0 && (ceiling == 0 || restriction < ceiling) {
		return restriction
	}
	return ceiling
}

func narrowDuration(ceiling, restriction time.Duration) time.Duration {
	if restriction > 0 && (ceiling == 0 || restriction < ceiling) {
		return restriction
	}
	return ceiling
}
