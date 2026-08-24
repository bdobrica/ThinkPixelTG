package postgres

import (
	"errors"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
)

func TestValidateAcquisitionRequest(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	valid := AcquisitionRequest{
		Invocation: Invocation{RunID: "run", ToolCallID: "call", ToolID: "tool", ToolVersion: "1", ArgumentDigest: make([]byte, 32)},
		OwnerID:    "owner", Now: now, LeaseDuration: time.Minute, MaxRecoveries: 2,
	}
	if err := validateAcquisitionRequest(valid); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}

	tests := []struct {
		name string
		edit func(*AcquisitionRequest)
	}{
		{"missing owner", func(v *AcquisitionRequest) { v.OwnerID = "" }},
		{"bad digest", func(v *AcquisitionRequest) { v.Invocation.ArgumentDigest = make([]byte, 31) }},
		{"zero lease", func(v *AcquisitionRequest) { v.LeaseDuration = 0 }},
		{"unbounded lease", func(v *AcquisitionRequest) { v.LeaseDuration = 5*time.Minute + time.Nanosecond }},
		{"negative recoveries", func(v *AcquisitionRequest) { v.MaxRecoveries = -1 }},
		{"excess recoveries", func(v *AcquisitionRequest) { v.MaxRecoveries = 11 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.edit(&candidate)
			var domainErr *domain.Error
			if err := validateAcquisitionRequest(candidate); !errors.As(err, &domainErr) || domainErr.Code != domain.CodeInvalidArgument {
				t.Fatalf("error = %v, want invalid_argument", err)
			}
		})
	}
}
