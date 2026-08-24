package resourceprojection

import (
	"context"
	"errors"
	"testing"

	"github.com/bdobrica/ThinkPixelTG/internal/canonicaljson"
)

func TestProjectProducesTrustedCanonicalResourceAndDigest(t *testing.T) {
	arguments := normalize(t, `{
		"repository":{"owner":"thinkpixel","name":"tg"},
		"issue":17,
		"resource":{"owner":"attacker","name":"other"}
	}`)
	definition := Definition{Fields: []Field{
		{Name: "type", Literal: "github_issue", Type: String},
		{Name: "owner", Pointer: "/repository/owner", Required: true, Type: String},
		{Name: "repository", Pointer: "/repository/name", Required: true, Type: String},
		{Name: "number", Pointer: "/issue", Required: true, Type: Number},
	}}

	got, err := Project(arguments, definition)
	if err != nil {
		t.Fatalf("Project() error = %v", err)
	}
	want := `{"number":17,"owner":"thinkpixel","repository":"tg","type":"github_issue"}`
	if string(got.Canonical) != want {
		t.Fatalf("Canonical = %s, want %s", got.Canonical, want)
	}
	wantDigest := canonicaljson.Digest(canonicaljson.ResourceDomain, []byte(want))
	if got.Digest != wantDigest {
		t.Fatalf("Digest = %s, want %s", got.Digest, wantDigest)
	}
	if got.Value["owner"] != "thinkpixel" {
		t.Fatalf("caller-controlled resource object affected projection: %#v", got.Value)
	}
}

func TestProjectHandlesEscapedPointersAndExactArrayIndex(t *testing.T) {
	arguments := normalize(t, `{"a/b":{"~key":["first","second"]}}`)
	got, err := Project(arguments, Definition{Fields: []Field{
		{Name: "selected", Pointer: "/a~1b/~0key/1", Required: true, Type: String},
	}})
	if err != nil {
		t.Fatalf("Project() error = %v", err)
	}
	if string(got.Canonical) != `{"selected":"second"}` {
		t.Fatalf("Canonical = %s", got.Canonical)
	}
}

func TestProjectOptionalMissingFieldIsOmitted(t *testing.T) {
	got, err := Project(normalize(t, `{"owner":"tg"}`), Definition{Fields: []Field{
		{Name: "owner", Pointer: "/owner", Required: true, Type: String},
		{Name: "branch", Pointer: "/branch", Type: String},
	}})
	if err != nil {
		t.Fatalf("Project() error = %v", err)
	}
	if string(got.Canonical) != `{"owner":"tg"}` {
		t.Fatalf("Canonical = %s", got.Canonical)
	}
}

func TestProjectRejectsMissingRequiredAndTypeMismatch(t *testing.T) {
	tests := []struct {
		name       string
		arguments  string
		field      Field
		wantTarget error
	}{
		{"missing", `{}`, Field{Name: "owner", Pointer: "/owner", Required: true, Type: String}, ErrMissing},
		{"wrong type", `{"owner":["tg"]}`, Field{Name: "owner", Pointer: "/owner", Required: true, Type: String}, ErrType},
		{"array wildcard", `{"repos":["a","b"]}`, Field{Name: "repo", Pointer: "/repos/*", Required: true, Type: String}, ErrMissing},
		{"ambiguous append token", `{"repos":["a"]}`, Field{Name: "repo", Pointer: "/repos/-", Required: true, Type: String}, ErrMissing},
		{"noncanonical array index", `{"repos":["a","b"]}`, Field{Name: "repo", Pointer: "/repos/01", Required: true, Type: String}, ErrMissing},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Project(normalize(t, test.arguments), Definition{Fields: []Field{test.field}})
			if !errors.Is(err, test.wantTarget) {
				t.Fatalf("Project() error = %v, want %v", err, test.wantTarget)
			}
		})
	}
}

func TestProjectRejectsAmbiguousOrUnsafeDefinitions(t *testing.T) {
	tests := []struct {
		name       string
		definition Definition
	}{
		{"no fields", Definition{}},
		{"duplicate output", Definition{Fields: []Field{{Name: "repo", Pointer: "/a"}, {Name: "repo", Pointer: "/b"}}}},
		{"no source", Definition{Fields: []Field{{Name: "repo", Required: true}}}},
		{"two sources", Definition{Fields: []Field{{Name: "repo", Pointer: "/repo", Literal: "fixed"}}}},
		{"relative pointer", Definition{Fields: []Field{{Name: "repo", Pointer: "repo"}}}},
		{"bad escape", Definition{Fields: []Field{{Name: "repo", Pointer: "/repo~2name"}}}},
		{"unknown type", Definition{Fields: []Field{{Name: "repo", Pointer: "/repo", Type: "integer"}}}},
		{"newline name", Definition{Fields: []Field{{Name: "repo\ninjected", Pointer: "/repo"}}}},
		{"too many fields", Definition{MaxFields: 1, Fields: []Field{{Name: "a", Pointer: "/a"}, {Name: "b", Pointer: "/b"}}}},
	}
	arguments := normalize(t, `{"a":"a","b":"b","repo":"tg"}`)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Project(arguments, test.definition)
			if !errors.Is(err, ErrInvalidDefinition) {
				t.Fatalf("Project() error = %v, want ErrInvalidDefinition", err)
			}
		})
	}
}

func TestProjectRejectsForgedNormalizedResults(t *testing.T) {
	valid := normalize(t, `{"repo":"tg"}`)
	tests := []struct {
		name   string
		mutate func(*canonicaljson.Result)
	}{
		{"profile", func(result *canonicaljson.Result) { result.Profile = "other" }},
		{"noncanonical", func(result *canonicaljson.Result) { result.Canonical = []byte(`{ "repo": "tg" }`) }},
		{"digest", func(result *canonicaljson.Result) { result.Digest[0] ^= 0xff }},
	}
	definition := Definition{Fields: []Field{{Name: "repo", Pointer: "/repo", Required: true, Type: String}}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			_, err := Project(candidate, definition)
			if !errors.Is(err, ErrInvalidArguments) {
				t.Fatalf("Project() error = %v, want ErrInvalidArguments", err)
			}
		})
	}
}

func TestProjectEnforcesOutputLimit(t *testing.T) {
	_, err := Project(normalize(t, `{"repo":"thinkpixel"}`), Definition{
		MaxOutputBytes: 2,
		Fields:         []Field{{Name: "repo", Pointer: "/repo", Required: true, Type: String}},
	})
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("Project() error = %v, want ErrLimit", err)
	}
}

func normalize(t *testing.T, input string) canonicaljson.Result {
	t.Helper()
	result, err := canonicaljson.NormalizeArguments(context.Background(), []byte(input), canonicaljson.Limits{}, func(context.Context, any) error { return nil })
	if err != nil {
		t.Fatalf("NormalizeArguments() error = %v", err)
	}
	return result
}
