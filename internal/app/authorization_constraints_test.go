package app

import (
	"slices"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

func TestNarrowAuthorizationConstraintsCannotExpandCeiling(t *testing.T) {
	ceiling := ConstraintCeiling{Repositories: []string{"a", "b"}, Resources: []string{"issue", "pull"}, Actions: []string{"read", "write"},
		ArgumentMax: map[string]int64{"body_bytes": 100, "items": 10}, MaxResultBytes: 1000, MaxDuration: 10 * time.Second}
	decision := ports.AuthorizationDecision{Outcome: ports.AuthorizationAllow, Constraints: ports.AuthorizationConstraints{
		Repositories: []string{"b", "outside"}, Resources: []string{"issue", "outside"}, Actions: []string{"outside", "write"},
		ArgumentMax: map[string]int64{"body_bytes": 50}, MaxResultBytes: 500, MaxDuration: time.Second}}
	effective, err := NarrowAuthorizationConstraints(decision, ceiling)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(effective.Repositories, []string{"b"}) || !slices.Equal(effective.Resources, []string{"issue"}) || !slices.Equal(effective.Actions, []string{"write"}) {
		t.Fatalf("sets expanded: %#v", effective)
	}
	if effective.ArgumentMax["body_bytes"] != 50 || effective.ArgumentMax["items"] != 10 || effective.MaxResultBytes != 500 || effective.MaxDuration != time.Second {
		t.Fatalf("bounds not narrowed: %#v", effective)
	}
}

func TestNarrowAuthorizationConstraintsFailsClosed(t *testing.T) {
	ceiling := ConstraintCeiling{Repositories: []string{"a"}, Actions: []string{"read"}, ArgumentMax: map[string]int64{"items": 10}}
	tests := map[string]ports.AuthorizationDecision{
		"denial":             {Outcome: ports.AuthorizationDeny},
		"empty intersection": {Outcome: ports.AuthorizationAllow, Constraints: ports.AuthorizationConstraints{Repositories: []string{"outside"}}},
		"unknown argument":   {Outcome: ports.AuthorizationAllow, Constraints: ports.AuthorizationConstraints{ArgumentMax: map[string]int64{"unknown": 1}}},
	}
	for name, decision := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := NarrowAuthorizationConstraints(decision, ceiling); err == nil {
				t.Fatal("unsafe constraints accepted")
			}
		})
	}
}

func TestNarrowAuthorizationConstraintsNeverExceedsEitherBound(t *testing.T) {
	for local := int64(1); local <= 20; local++ {
		for policy := int64(1); policy <= 20; policy++ {
			effective, err := NarrowAuthorizationConstraints(ports.AuthorizationDecision{Outcome: ports.AuthorizationAllow,
				Constraints: ports.AuthorizationConstraints{ArgumentMax: map[string]int64{"value": policy}, MaxResultBytes: policy}},
				ConstraintCeiling{ArgumentMax: map[string]int64{"value": local}, MaxResultBytes: local})
			if err != nil {
				t.Fatal(err)
			}
			if effective.ArgumentMax["value"] > local || effective.ArgumentMax["value"] > policy || effective.MaxResultBytes > local || effective.MaxResultBytes > policy {
				t.Fatalf("expanded local=%d policy=%d", local, policy)
			}
		}
	}
}
