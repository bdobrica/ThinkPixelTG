package postgres

import (
	"errors"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
)

func TestValidateAttemptClaimRequest(t *testing.T) {
	t.Parallel()
	valid := AttemptClaimRequest{InvocationID: "invocation", OwnerID: "worker", Now: time.Now(), LeaseDuration: time.Minute, Evidence: []byte(`{}`)}
	for _, test := range []struct {
		name string
		edit func(*AttemptClaimRequest)
	}{
		{"missing invocation", func(v *AttemptClaimRequest) { v.InvocationID = "" }},
		{"missing owner", func(v *AttemptClaimRequest) { v.OwnerID = "" }},
		{"zero time", func(v *AttemptClaimRequest) { v.Now = time.Time{} }},
		{"zero lease", func(v *AttemptClaimRequest) { v.LeaseDuration = 0 }},
		{"excess lease", func(v *AttemptClaimRequest) { v.LeaseDuration = maxAttemptLease + time.Nanosecond }},
		{"non-object evidence", func(v *AttemptClaimRequest) { v.Evidence = []byte(`[]`) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.edit(&candidate)
			assertInvalidArgument(t, validateAttemptClaimRequest(candidate))
		})
	}
}

func TestValidateAttemptFinalization(t *testing.T) {
	t.Parallel()
	valid := AttemptFinalization{InvocationID: "invocation", AttemptNo: 1, Fence: 1, OwnerID: "worker", Now: time.Now(), OutcomeClassification: "confirmed_success"}
	if err := validateAttemptFinalization(valid); err != nil {
		t.Fatalf("valid finalization rejected: %v", err)
	}
	unknown := valid
	unknown.OutcomeClassification = "unknown"
	assertInvalidArgument(t, validateAttemptFinalization(unknown))
	ambiguity := "possibly_applied"
	unknown.AmbiguityClassification = &ambiguity
	if err := validateAttemptFinalization(unknown); err != nil {
		t.Fatalf("classified unknown outcome rejected: %v", err)
	}
}

func assertInvalidArgument(t *testing.T, err error) {
	t.Helper()
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) || domainErr.Code != domain.CodeInvalidArgument {
		t.Fatalf("error = %v, want invalid_argument", err)
	}
}
