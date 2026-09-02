package schema

import (
	"errors"
	"strings"
	"testing"
)

func TestCompileAndValidateJSONSchema202012(t *testing.T) {
	validator := NewValidator(Limits{})
	compiled, err := validator.Compile([]byte(`{
		"$schema":"https://json-schema.org/draft/2020-12/schema",
		"type":"object",
		"properties":{"count":{"type":"integer","minimum":1},"name":{"type":"string"}},
		"required":["count","name"],
		"additionalProperties":false
	}`))
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if err := compiled.ValidateJSON([]byte(`{"name":"pixel","count":2}`)); err != nil {
		t.Fatalf("valid instance: %v", err)
	}

	err = compiled.ValidateJSON([]byte(`{"extra":true,"count":0}`))
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected ValidationError, got %v", err)
	}
	if len(validationErr.Violations) != 3 {
		t.Fatalf("violations = %#v", validationErr.Violations)
	}
	for index := 1; index < len(validationErr.Violations); index++ {
		if validationErr.Violations[index-1].InstanceLocation > validationErr.Violations[index].InstanceLocation {
			t.Fatalf("violations are not deterministic: %#v", validationErr.Violations)
		}
	}
	if strings.Contains(err.Error(), "extra") || strings.Contains(err.Error(), "pixel") {
		t.Fatalf("error leaked instance content: %v", err)
	}
}

func TestCompilationCacheIsBoundedAndReusesEntries(t *testing.T) {
	validator := NewValidator(Limits{MaxCacheEntries: 2})
	first, err := validator.Compile([]byte(`{"type":"string"}`))
	if err != nil {
		t.Fatal(err)
	}
	again, err := validator.Compile([]byte(`{"type":"string"}`))
	if err != nil {
		t.Fatal(err)
	}
	if first != again {
		t.Fatal("cache did not reuse compiled schema")
	}
	for _, raw := range []string{`{"type":"number"}`, `{"type":"boolean"}`} {
		if _, err := validator.Compile([]byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	if got := validator.CacheLen(); got != 2 {
		t.Fatalf("cache length = %d, want 2", got)
	}
}

func TestSchemaAndInstanceLimits(t *testing.T) {
	tests := []struct {
		name   string
		limits Limits
		raw    string
		want   error
	}{
		{"oversize", Limits{MaxSchemaBytes: 20}, `{"description":"too large"}`, ErrSchemaTooLarge},
		{"malformed", Limits{}, `{`, ErrMalformedSchema},
		{"depth", Limits{MaxDepth: 3}, `{"a":{"b":{"c":1}}}`, ErrSchemaComplexity},
		{"nodes", Limits{MaxNodes: 5}, `{"a":1,"b":2,"c":3,"d":4,"e":5}`, ErrSchemaComplexity},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewValidator(test.limits).Compile([]byte(test.raw))
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
	compiled, err := NewValidator(Limits{MaxInstanceBytes: 64, MaxDepth: 2, MaxNodes: 3}).Compile([]byte(`true`))
	if err != nil {
		t.Fatal(err)
	}
	if err := compiled.ValidateJSON([]byte(`"` + strings.Repeat("1", 64) + `"`)); !errors.Is(err, ErrInstanceTooLarge) {
		t.Fatalf("oversize instance: %v", err)
	}
	if err := compiled.ValidateJSON([]byte(`{"a":{"b":1}}`)); !errors.Is(err, ErrInstanceComplexity) {
		t.Fatalf("deep instance: %v", err)
	}
}

func TestHostileSchemasFailClosed(t *testing.T) {
	validator := NewValidator(Limits{})
	tests := []struct {
		name string
		raw  string
		want error
	}{
		{"external ref", `{"$ref":"https://attacker.invalid/schema"}`, ErrExternalReference},
		{"relative ref", `{"$ref":"other.json"}`, ErrExternalReference},
		{"non-string ref", `{"$ref":42}`, ErrExternalReference},
		{"invalid regex", `{"pattern":"["}`, ErrInvalidSchema},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := validator.Compile([]byte(test.raw))
			if err == nil {
				t.Fatal("hostile schema compiled")
			}
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestLimitsCannotExceedCapacityEnvelope(t *testing.T) {
	validator := NewValidator(Limits{
		MaxSchemaBytes:   DefaultMaxSchemaBytes + 1,
		MaxInstanceBytes: DefaultMaxInstanceBytes + 1,
		MaxDepth:         DefaultMaxDepth + 1,
		MaxNodes:         DefaultMaxNodes + 1,
		MaxStringBytes:   DefaultMaxStringBytes + 1,
		MaxObjectMembers: DefaultMaxObjectMembers + 1,
		MaxCacheEntries:  maximumCacheEntries + 1,
		MaxErrors:        maximumErrors + 1,
	})
	if validator.limits.MaxSchemaBytes != DefaultMaxSchemaBytes ||
		validator.limits.MaxInstanceBytes != DefaultMaxInstanceBytes ||
		validator.limits.MaxDepth != DefaultMaxDepth ||
		validator.limits.MaxNodes != DefaultMaxNodes ||
		validator.limits.MaxStringBytes != DefaultMaxStringBytes ||
		validator.limits.MaxObjectMembers != DefaultMaxObjectMembers ||
		validator.limits.MaxCacheEntries != DefaultMaxCacheEntries ||
		validator.limits.MaxErrors != DefaultMaxErrors {
		t.Fatalf("limits escaped capacity envelope: %#v", validator.limits)
	}

	compiled, err := NewValidator(Limits{MaxStringBytes: 3}).Compile([]byte(`true`))
	if err != nil {
		t.Fatal(err)
	}
	if err := compiled.ValidateJSON([]byte(`"four"`)); !errors.Is(err, ErrInstanceComplexity) {
		t.Fatalf("long string: %v", err)
	}
}

func TestLocalRecursiveSchemaIsBoundedByInstanceLimits(t *testing.T) {
	validator := NewValidator(Limits{MaxDepth: 8})
	compiled, err := validator.Compile([]byte(`{"$defs":{"node":{"type":"object","properties":{"next":{"$ref":"#/$defs/node"}}}},"$ref":"#/$defs/node"}`))
	if err != nil {
		t.Fatalf("compile recursive schema: %v", err)
	}
	if err := compiled.ValidateJSON([]byte(`{"next":{"next":{}}}`)); err != nil {
		t.Fatalf("validate bounded recursion: %v", err)
	}
	if err := compiled.ValidateJSON([]byte(`{"next":{"next":{"next":{"next":{"next":{"next":{"next":{"next":{}}}}}}}}}`)); !errors.Is(err, ErrInstanceComplexity) {
		t.Fatalf("deep recursive instance: %v", err)
	}
}
