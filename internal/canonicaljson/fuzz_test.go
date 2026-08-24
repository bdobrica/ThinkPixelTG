package canonicaljson

import (
	"bytes"
	"testing"
)

func FuzzCanonicalizeIdempotent(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`{"b":2,"a":1}`), []byte(`[1.0,"€",null]`),
		[]byte(`{"\uD834\uDD1E":true}`), []byte(`{"a":1,"a":2}`),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		canonical, err := Canonicalize(input, Limits{MaxBytes: 16 << 10})
		if err != nil {
			return
		}
		repeated, err := Canonicalize(canonical, Limits{MaxBytes: 16 << 10})
		if err != nil {
			t.Fatalf("canonical output rejected: %v", err)
		}
		if !bytes.Equal(canonical, repeated) {
			t.Fatalf("canonicalization is not idempotent: %q != %q", canonical, repeated)
		}
	})
}

func FuzzNormalizeArgumentsDeterministic(f *testing.F) {
	f.Add([]byte(`{"z":0,"a":1}`))
	f.Add([]byte(`9007199254740993`))
	f.Fuzz(func(t *testing.T, input []byte) {
		first, firstErr := NormalizeArguments(t.Context(), input, Limits{MaxBytes: 16 << 10}, acceptAll)
		second, secondErr := NormalizeArguments(t.Context(), input, Limits{MaxBytes: 16 << 10}, acceptAll)
		if (firstErr == nil) != (secondErr == nil) {
			t.Fatalf("same input produced inconsistent errors: %v / %v", firstErr, secondErr)
		}
		if firstErr == nil && (!bytes.Equal(first.Canonical, second.Canonical) || first.Digest != second.Digest) {
			t.Fatalf("same input produced inconsistent identity")
		}
	})
}
