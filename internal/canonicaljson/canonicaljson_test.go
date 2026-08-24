package canonicaljson

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
)

type fixture struct {
	Profile string `json:"profile"`
	Domain  string `json:"domain"`
	Vectors []struct {
		Name         string `json:"name"`
		Input        string `json:"input"`
		Canonical    string `json:"canonical"`
		CanonicalHex string `json:"canonical_hex"`
		DigestHex    string `json:"digest_hex"`
	} `json:"vectors"`
	Reject []struct {
		Name  string `json:"name"`
		Input string `json:"input"`
	} `json:"reject"`
}

func TestContractFixtures(t *testing.T) {
	t.Parallel()
	encoded, err := os.ReadFile("../../docs/contracts/testdata/canonical-json-v1.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var vectors fixture
	if err := json.Unmarshal(encoded, &vectors); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if vectors.Profile != Profile || vectors.Domain != ArgumentDomain {
		t.Fatalf("fixture profile/domain = %q/%q, want %q/%q", vectors.Profile, vectors.Domain, Profile, ArgumentDomain)
	}
	for _, vector := range vectors.Vectors {
		vector := vector
		t.Run(vector.Name, func(t *testing.T) {
			t.Parallel()
			result, err := NormalizeArguments(t.Context(), []byte(vector.Input), Limits{}, acceptAll)
			if err != nil {
				t.Fatalf("NormalizeArguments() error = %v", err)
			}
			if string(result.Canonical) != vector.Canonical {
				t.Errorf("canonical = %q, want %q", result.Canonical, vector.Canonical)
			}
			if hex.EncodeToString(result.Canonical) != vector.CanonicalHex {
				t.Errorf("canonical hex = %x, want %s", result.Canonical, vector.CanonicalHex)
			}
			if result.Digest.String() != vector.DigestHex {
				t.Errorf("digest = %s, want %s", result.Digest, vector.DigestHex)
			}
		})
	}
	for _, vector := range vectors.Reject {
		vector := vector
		t.Run("reject-"+vector.Name, func(t *testing.T) {
			t.Parallel()
			if _, err := Canonicalize([]byte(vector.Input), Limits{}); err == nil {
				t.Fatalf("Canonicalize(%q) succeeded", vector.Input)
			}
		})
	}
}

func TestUnicodeUTF16KeyOrderingAndEscapes(t *testing.T) {
	t.Parallel()
	input := []byte(`{"\uE000":"bmp","\uD834\uDD1E":"supplementary","line":"a\nb"}`)
	got, err := Canonicalize(input, Limits{})
	if err != nil {
		t.Fatalf("Canonicalize() error = %v", err)
	}
	want := `{"line":"a\nb","𝄞":"supplementary","":"bmp"}`
	if string(got) != want {
		t.Fatalf("Canonicalize() = %q, want %q", got, want)
	}
}

func TestMalformedAndUnsafeNumbers(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  error
	}{
		{"+1", ErrInvalidJSON}, {".1", ErrInvalidJSON}, {"1.", ErrInvalidJSON},
		{"1e", ErrInvalidJSON}, {"00", ErrInvalidJSON}, {"1e9999", ErrUnsafeNumber},
		{"1e-9999", ErrUnsafeNumber}, {"-9007199254740992", ErrUnsafeNumber},
	}
	for _, test := range tests {
		_, err := Canonicalize([]byte(test.input), Limits{})
		if !errors.Is(err, test.want) {
			t.Errorf("Canonicalize(%q) error = %v, want %v", test.input, err, test.want)
		}
	}
}

func TestRFC8785NumberSerializationBoundaries(t *testing.T) {
	t.Parallel()
	input := []byte(`[333333333.33333329,4.50,2e-3,1e-27,-0,0.000001,1e-7]`)
	got, err := Canonicalize(input, Limits{})
	if err != nil {
		t.Fatalf("Canonicalize() error = %v", err)
	}
	want := `[333333333.3333333,4.5,0.002,1e-27,0,0.000001,1e-7]`
	if string(got) != want {
		t.Fatalf("Canonicalize() = %s, want %s", got, want)
	}
}

func TestDuplicateDecodedKeyAndInvalidUTF8(t *testing.T) {
	t.Parallel()
	for _, input := range [][]byte{
		[]byte(`{"a":1,"\u0061":2}`),
		{'"', 0xff, '"'},
	} {
		if _, err := Canonicalize(input, Limits{}); !errors.Is(err, ErrInvalidJSON) {
			t.Errorf("Canonicalize(%q) error = %v, want invalid JSON", input, err)
		}
	}
}

func TestSchemaValidationBoundary(t *testing.T) {
	t.Parallel()
	rejected := errors.New("required name is absent")
	called := false
	result, err := NormalizeArguments(t.Context(), []byte(`{"count":1}`), Limits{}, func(_ context.Context, value any) error {
		called = true
		object, ok := value.(map[string]any)
		if !ok || object["count"] != float64(1) {
			t.Fatalf("validator value = %#v", value)
		}
		return rejected
	})
	if !called || !errors.Is(err, ErrSchema) || !errors.Is(err, rejected) || result.Profile != "" || result.Canonical != nil || result.Digest != (domain.Digest{}) {
		t.Fatalf("schema rejection: called=%v result=%#v error=%v", called, result, err)
	}
	if _, err := NormalizeArguments(t.Context(), []byte(`{}`), Limits{}, nil); err == nil {
		t.Fatal("NormalizeArguments() without validator succeeded")
	}
	if _, err := NormalizeArguments(t.Context(), []byte(`{"a":1,"a":2}`), Limits{}, func(context.Context, any) error {
		t.Fatal("validator called for structurally invalid JSON")
		return nil
	}); !errors.Is(err, ErrInvalidJSON) {
		t.Fatalf("duplicate member error = %v, want invalid JSON", err)
	}
}

func TestLimits(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		limit Limits
	}{
		{"bytes", `{"a":1}`, Limits{MaxBytes: 2}},
		{"depth", `[[0]]`, Limits{MaxDepth: 2}},
		{"members", `{"a":1,"b":2}`, Limits{MaxMembers: 1}},
		{"string", `"long"`, Limits{MaxStringBytes: 3}},
		{"number", `1234`, Limits{MaxNumberBytes: 3}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Canonicalize([]byte(test.input), test.limit); !errors.Is(err, ErrLimit) {
				t.Fatalf("Canonicalize() error = %v, want limit error", err)
			}
		})
	}
}

func TestDigestDomainSeparation(t *testing.T) {
	t.Parallel()
	canonical := []byte(`{"a":1}`)
	argument := Digest(ArgumentDomain, canonical)
	resource := Digest(ResourceDomain, canonical)
	if argument == resource {
		t.Fatal("argument and resource digests are equal")
	}
	if argument != domain.DigestBytes(append([]byte(ArgumentDomain), canonical...)) {
		t.Fatal("argument digest does not include the normative domain prefix")
	}
}

func TestPropertyObjectOrderAndIdempotence(t *testing.T) {
	t.Parallel()
	random := rand.New(rand.NewPCG(1, 2))
	for iteration := 0; iteration < 500; iteration++ {
		keys := []string{"alpha", "βeta", "𝄞", "z", "nested"}
		random.Shuffle(len(keys), func(left, right int) { keys[left], keys[right] = keys[right], keys[left] })
		members := make([]string, 0, len(keys))
		for index, key := range keys {
			members = append(members, fmt.Sprintf("%q:%d", key, index))
		}
		input := []byte("{" + strings.Join(members, ",") + "}")
		first, err := Canonicalize(input, Limits{})
		if err != nil {
			t.Fatalf("iteration %d: Canonicalize() error = %v", iteration, err)
		}
		second, err := Canonicalize(first, Limits{})
		if err != nil || !slices.Equal(first, second) {
			t.Fatalf("iteration %d: canonicalization is not idempotent: %q / %q (%v)", iteration, first, second, err)
		}
	}
}

func acceptAll(context.Context, any) error { return nil }
