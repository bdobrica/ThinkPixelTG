package domain

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

type fixedClock time.Time

func (clock fixedClock) Now() time.Time { return time.Time(clock) }

func TestUUIDv7Deterministic(t *testing.T) {
	instant := time.Date(2026, 8, 23, 10, 30, 0, 123000000, time.FixedZone("test", 7200))
	id, err := NewUUIDv7From(fixedClock(instant), bytes.NewReader(bytes.Repeat([]byte{0xff}, 10)))
	if err != nil {
		t.Fatal(err)
	}
	if id[6]>>4 != 7 || id[8]>>6 != 2 {
		t.Fatalf("wrong version/variant: %s", id)
	}
	milliseconds, err := MillisecondsFromUUIDv7(id)
	if err != nil {
		t.Fatal(err)
	}
	if milliseconds != instant.UTC().UnixMilli() {
		t.Fatalf("timestamp = %d", milliseconds)
	}
	parsed, err := ParseUUID(id.String())
	if err != nil || parsed != id {
		t.Fatalf("round trip: %v %s", err, parsed)
	}
}

func TestQuantitiesAreExactAndChecked(t *testing.T) {
	left, _ := NewQuantity(math.MaxInt64, "bytes")
	right, _ := NewQuantity(1, "bytes")
	if _, err := left.Add(right); err == nil {
		t.Fatal("expected overflow")
	}
	if _, err := right.Multiply(-1); err == nil {
		t.Fatal("expected negative multiplier error")
	}
	requests, _ := NewQuantity(2, "requests")
	if _, err := right.Add(requests); err == nil {
		t.Fatal("expected unit error")
	}
}

func TestDigestRoundTrip(t *testing.T) {
	digest := DigestBytes([]byte("thinkpixeltg"))
	parsed, err := ParseDigest(digest.String())
	if err != nil || parsed != digest {
		t.Fatalf("round trip: %v", err)
	}
}

func TestCursorRoundTripAndTamperRejection(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	encoded, err := EncodeCursor(Cursor{Version: 1, After: "tool-7", Limit: 25}, key)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeCursor(encoded, key)
	if err != nil || decoded.After != "tool-7" || decoded.Limit != 25 {
		t.Fatalf("decode: %#v %v", decoded, err)
	}
	tampered := strings.Replace(encoded, "A", "B", 1)
	if tampered == encoded {
		tampered = "A" + encoded[1:]
	}
	if _, err := DecodeCursor(tampered, key); err == nil {
		t.Fatal("expected tamper error")
	}
}

func TestDomainErrorDoesNotRenderCause(t *testing.T) {
	const canary = "SECRET_CANARY"
	err := NewError(CodeInternal, "operation failed", errors.New(canary))
	if strings.Contains(err.Error(), canary) {
		t.Fatalf("cause leaked: %s", err)
	}
	if !errors.Is(err, err.Cause) {
		t.Fatal("cause not available to internal inspection")
	}
}

func FuzzParseUUID(f *testing.F) {
	f.Add("018f1f10-7b5c-7abc-8def-0123456789ab")
	f.Add("not-a-uuid")
	f.Fuzz(func(t *testing.T, input string) { _, _ = ParseUUID(input) })
}

func FuzzDecodeCursor(f *testing.F) {
	key := bytes.Repeat([]byte{0x42}, 32)
	f.Add("")
	f.Add("payload.signature")
	f.Fuzz(func(t *testing.T, input string) { _, _ = DecodeCursor(input, key) })
}

func FuzzCheckedAdd(f *testing.F) {
	f.Add(int64(math.MaxInt64), int64(1))
	f.Add(int64(2), int64(3))
	f.Fuzz(func(t *testing.T, left, right int64) {
		value, err := CheckedAdd(left, right)
		if err == nil && ((right > 0 && value < left) || (right < 0 && value > left)) {
			t.Fatalf("unchecked overflow: %d + %d", left, right)
		}
	})
}
