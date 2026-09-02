package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/bdobrica/ThinkPixelTG/internal/schema"
)

func TestToolIDValidation(t *testing.T) {
	for _, valid := range []string{"github.pull.comment", "service.operation_v2"} {
		if _, err := ParseToolID(valid); err != nil {
			t.Errorf("ParseToolID(%q): %v", valid, err)
		}
	}
	for _, invalid := range []string{"github", "GitHub.pull", "github..pull", "github.pull-comment", ".github.pull", strings.Repeat("a", 256) + ".b"} {
		if _, err := ParseToolID(invalid); err == nil {
			t.Errorf("ParseToolID(%q) accepted invalid ID", invalid)
		}
	}
}

func TestSemanticVersionValidationAndPrecedence(t *testing.T) {
	ordered := []string{"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.1.0", "2.0.0"}
	for index := 1; index < len(ordered); index++ {
		previous, err := ParseSemanticVersion(ordered[index-1])
		if err != nil {
			t.Fatal(err)
		}
		next, err := ParseSemanticVersion(ordered[index])
		if err != nil {
			t.Fatal(err)
		}
		if err := RequireMonotonicVersion(previous, next); err != nil {
			t.Errorf("%s -> %s: %v", previous, next, err)
		}
	}
	for _, invalid := range []string{"v1.0.0", "1", "1.0", "01.0.0", "1.0.0-01", "1.0.0+"} {
		if _, err := ParseSemanticVersion(invalid); err == nil {
			t.Errorf("accepted invalid SemVer %q", invalid)
		}
	}
	left, _ := ParseSemanticVersion("1.0.0+one")
	right, _ := ParseSemanticVersion("1.0.0+two")
	if left.Compare(right) != 0 {
		t.Error("build metadata affected precedence")
	}
	if err := RequireMonotonicVersion(left, right); err == nil {
		t.Error("equal precedence accepted as monotonic")
	}
}

func TestToolVersionLifecycleAndImmutableDefinition(t *testing.T) {
	definition := validToolDefinition(t)
	version, err := NewToolVersion(definition)
	if err != nil {
		t.Fatal(err)
	}
	definition.ResourceProjection.Fields[0].Name = "mutated"
	if version.Definition().ResourceProjection.Fields[0].Name != "repository" {
		t.Fatal("constructor retained mutable metadata")
	}
	returned := version.Definition()
	returned.ResourceProjection.Fields[0].Name = "mutated_again"
	if version.Definition().ResourceProjection.Fields[0].Name != "repository" {
		t.Fatal("getter exposed mutable metadata")
	}
	if _, err := NewToolExposure(version, true); err == nil {
		t.Fatal("enabled draft exposure accepted")
	}
	published, err := version.Publish(schema.NewValidator(schema.Limits{}))
	if err != nil {
		t.Fatal(err)
	}
	if published.State() != ToolVersionPublished {
		t.Fatalf("state = %s", published.State())
	}
	exposure, err := NewToolExposure(published, true)
	if err != nil || !exposure.Enabled() {
		t.Fatalf("enable published: %v", err)
	}
	if _, err := published.Publish(schema.NewValidator(schema.Limits{})); err == nil {
		t.Fatal("published twice")
	}
	retired, err := published.Retire()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exposure.SetEnabled(retired, true); err == nil {
		t.Fatal("enabled retired version")
	}
	if _, err := exposure.SetEnabled(retired, false); err != nil {
		t.Fatalf("disable retired version: %v", err)
	}
}

func TestToolVersionDefinitionRejectsUntrustedOrUnsafeMetadata(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ToolVersionDefinition)
	}{
		{"risk", func(d *ToolVersionDefinition) { d.Risk = "caller_supplied" }},
		{"retry", func(d *ToolVersionDefinition) { d.Retry = "best_effort" }},
		{"approval", func(d *ToolVersionDefinition) { d.Approval = "optional" }},
		{"read side effect", func(d *ToolVersionDefinition) { d.SideEffect = true }},
		{"safe write", func(d *ToolVersionDefinition) { d.Risk = RiskBoundedWrite; d.SideEffect = true; d.Retry = RetrySafe }},
		{"connector type", func(d *ToolVersionDefinition) { d.Connector.ConnectorType = "https://caller.invalid" }},
		{"operation", func(d *ToolVersionDefinition) { d.Connector.Operation = "" }},
		{"selector", func(d *ToolVersionDefinition) { d.Connector.InstanceSelector = "tenant/../../other" }},
		{"projection", func(d *ToolVersionDefinition) { d.ResourceProjection.Fields = nil }},
		{"projection pointer", func(d *ToolVersionDefinition) { d.ResourceProjection.Fields[0].Pointer = "/bad~2escape" }},
		{"duplicate projection", func(d *ToolVersionDefinition) {
			d.ResourceProjection.Fields = append(d.ResourceProjection.Fields, d.ResourceProjection.Fields[0])
		}},
		{"request limit", func(d *ToolVersionDefinition) { d.Limits.RequestBytes = 1<<20 + 1 }},
		{"result limit", func(d *ToolVersionDefinition) { d.Limits.ResultBytes = 4<<20 + 1 }},
		{"deadline", func(d *ToolVersionDefinition) { d.Limits.Deadline = 31 * time.Second }},
		{"concurrency", func(d *ToolVersionDefinition) { d.Limits.Concurrency = 0 }},
		{"attempts", func(d *ToolVersionDefinition) { d.Limits.MaxAttempts = 4 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			definition := validToolDefinition(t)
			test.mutate(&definition)
			if err := ValidateToolVersionDefinition(definition); err == nil {
				t.Fatal("invalid definition accepted")
			}
		})
	}
}

func TestToolPublicationRejectsIncompleteSecurityMetadata(t *testing.T) {
	validator := schema.NewValidator(schema.Limits{})
	tests := []struct {
		name   string
		mutate func(*ToolVersionDefinition)
	}{
		{"title", func(d *ToolVersionDefinition) { d.Description.Title = "" }},
		{"description review", func(d *ToolVersionDefinition) { d.Description.ReviewRef = "" }},
		{"input schema", func(d *ToolVersionDefinition) { d.InputSchema = []byte(`{"type":`) }},
		{"output schema", func(d *ToolVersionDefinition) { d.OutputSchema = nil }},
		{"external schema reference", func(d *ToolVersionDefinition) { d.InputSchema = []byte(`{"$ref":"https://attacker.invalid/schema"}`) }},
		{"canonical profile", func(d *ToolVersionDefinition) { d.CanonicalProfile = "caller-json" }},
		{"credential selector", func(d *ToolVersionDefinition) { d.CredentialSelector = "" }},
		{"retry qualification", func(d *ToolVersionDefinition) { d.RetryQualification = "" }},
		{"required projection", func(d *ToolVersionDefinition) { d.ResourceProjection.Fields[0].Required = false }},
		{"metering dimension", func(d *ToolVersionDefinition) { d.Metering.Dimension = "" }},
		{"metering units", func(d *ToolVersionDefinition) { d.Metering.Units = "1e9" }},
		{"metering charge point", func(d *ToolVersionDefinition) { d.Metering.ChargePoint = "request" }},
		{"metering deduplication", func(d *ToolVersionDefinition) { d.Metering.DeduplicationScope = "transport_request" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			definition := validToolDefinition(t)
			test.mutate(&definition)
			version, err := NewToolVersion(definition)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := version.Publish(validator); err == nil {
				t.Fatal("incomplete publication accepted")
			}
			if version.State() != ToolVersionDraft {
				t.Fatal("failed publication changed draft state")
			}
		})
	}
}

func TestToolPublicationRequiresValidatorAndClonesSchemas(t *testing.T) {
	definition := validToolDefinition(t)
	version, err := NewToolVersion(definition)
	if err != nil {
		t.Fatal(err)
	}
	definition.InputSchema[0] = 'x'
	if version.Definition().InputSchema[0] != '{' {
		t.Fatal("constructor retained mutable schema bytes")
	}
	returned := version.Definition()
	returned.OutputSchema[0] = 'x'
	if version.Definition().OutputSchema[0] != '{' {
		t.Fatal("getter exposed mutable schema bytes")
	}
	if _, err := version.Publish(nil); err == nil {
		t.Fatal("publication without bounded schema validator accepted")
	}
}

func validToolDefinition(t *testing.T) ToolVersionDefinition {
	t.Helper()
	id, err := ParseToolID("github.pull.comment")
	if err != nil {
		t.Fatal(err)
	}
	version, err := ParseSemanticVersion("1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	return ToolVersionDefinition{ToolID: id, Version: version, Risk: RiskRead, Retry: RetrySafe, Approval: ApprovalPolicy, OpenWorldResult: true,
		Description:        ReviewedDescription{Title: "Read pull request", Description: "Read an existing pull request.", ReviewRef: "review:catalog-42"},
		InputSchema:        []byte(`{"type":"object","properties":{"repository":{"type":"string"}},"required":["repository"]}`),
		OutputSchema:       []byte(`{"type":"object"}`),
		CanonicalProfile:   "jcs-v1",
		Connector:          ConnectorBinding{ConnectorType: "github", Operation: "pull.comment", InstanceSelector: "github_primary"},
		CredentialSelector: "github_user_delegated",
		RetryQualification: "qualification:github-pull-read-v1",
		ResourceProjection: ResourceProjectionDefinition{Fields: []ResourceProjectionField{{Name: "repository", Pointer: "/repository", Required: true, Type: ProjectionString}}},
		Metering:           MeteringRule{Dimension: "tool_calls", Units: "1", ChargePoint: MeterAtResult, DeduplicationScope: MeterPerLogicalInvocation},
		Limits:             ToolLimits{RequestBytes: 1 << 20, ResultBytes: 1 << 20, Deadline: 10 * time.Second, Concurrency: 10, MaxAttempts: 3}}
}
