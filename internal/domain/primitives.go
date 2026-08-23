package domain

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
)

type Clock interface{ Now() time.Time }

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }

func UTCNow(clock Clock) time.Time { return clock.Now().UTC() }

type UUID [16]byte

func NewUUIDv7(clock Clock) (UUID, error) { return NewUUIDv7From(clock, rand.Reader) }

func NewUUIDv7From(clock Clock, random io.Reader) (UUID, error) {
	var id UUID
	milliseconds := UTCNow(clock).UnixMilli()
	if milliseconds < 0 || milliseconds > 1<<48-1 {
		return id, errors.New("UUIDv7 timestamp is out of range")
	}
	var entropy [10]byte
	if _, err := io.ReadFull(random, entropy[:]); err != nil {
		return id, fmt.Errorf("read UUID entropy: %w", err)
	}
	id[0] = byte(milliseconds >> 40)
	id[1] = byte(milliseconds >> 32)
	id[2] = byte(milliseconds >> 24)
	id[3] = byte(milliseconds >> 16)
	id[4] = byte(milliseconds >> 8)
	id[5] = byte(milliseconds)
	id[6] = 0x70 | entropy[0]&0x0f
	id[7] = entropy[1]
	id[8] = 0x80 | entropy[2]&0x3f
	copy(id[9:], entropy[3:])
	return id, nil
}

func ParseUUID(input string) (UUID, error) {
	var id UUID
	if len(input) != 36 || input[8] != '-' || input[13] != '-' || input[18] != '-' || input[23] != '-' {
		return id, errors.New("invalid UUID format")
	}
	decoded, err := hex.DecodeString(strings.ReplaceAll(input, "-", ""))
	if err != nil || len(decoded) != len(id) {
		return id, errors.New("invalid UUID format")
	}
	copy(id[:], decoded)
	return id, nil
}

func (id UUID) String() string {
	var output [36]byte
	hex.Encode(output[0:8], id[0:4])
	output[8] = '-'
	hex.Encode(output[9:13], id[4:6])
	output[13] = '-'
	hex.Encode(output[14:18], id[6:8])
	output[18] = '-'
	hex.Encode(output[19:23], id[8:10])
	output[23] = '-'
	hex.Encode(output[24:36], id[10:16])
	return string(output[:])
}

type Digest [sha256.Size]byte

func DigestBytes(input []byte) Digest { return sha256.Sum256(input) }
func (digest Digest) String() string  { return hex.EncodeToString(digest[:]) }
func ParseDigest(input string) (Digest, error) {
	var digest Digest
	decoded, err := hex.DecodeString(input)
	if err != nil || len(decoded) != len(digest) {
		return digest, errors.New("invalid SHA-256 digest")
	}
	copy(digest[:], decoded)
	return digest, nil
}

type Quantity struct {
	amount int64
	unit   string
}

func NewQuantity(amount int64, unit string) (Quantity, error) {
	if amount < 0 {
		return Quantity{}, errors.New("quantity cannot be negative")
	}
	unit = strings.TrimSpace(unit)
	if unit == "" || len(unit) > 32 {
		return Quantity{}, errors.New("quantity unit is invalid")
	}
	return Quantity{amount: amount, unit: unit}, nil
}
func (quantity Quantity) Amount() int64 { return quantity.amount }
func (quantity Quantity) Unit() string  { return quantity.unit }
func (quantity Quantity) Add(other Quantity) (Quantity, error) {
	if quantity.unit != other.unit {
		return Quantity{}, errors.New("quantity units differ")
	}
	if other.amount > math.MaxInt64-quantity.amount {
		return Quantity{}, errors.New("quantity overflow")
	}
	return Quantity{amount: quantity.amount + other.amount, unit: quantity.unit}, nil
}
func (quantity Quantity) Multiply(multiplier int64) (Quantity, error) {
	if multiplier < 0 {
		return Quantity{}, errors.New("quantity multiplier cannot be negative")
	}
	if quantity.amount != 0 && multiplier > math.MaxInt64/quantity.amount {
		return Quantity{}, errors.New("quantity overflow")
	}
	return Quantity{amount: quantity.amount * multiplier, unit: quantity.unit}, nil
}

type Cursor struct {
	Version uint8  `json:"v"`
	After   string `json:"after"`
	Limit   uint16 `json:"limit"`
}

func EncodeCursor(cursor Cursor, key []byte) (string, error) {
	if len(key) < 32 {
		return "", errors.New("cursor signing key must be at least 32 bytes")
	}
	if cursor.Version != 1 || cursor.Limit < 1 || cursor.Limit > 100 || len(cursor.After) > 256 {
		return "", errors.New("invalid cursor")
	}
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("marshal cursor: %w", err)
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func DecodeCursor(encoded string, key []byte) (Cursor, error) {
	var cursor Cursor
	if len(key) < 32 {
		return cursor, errors.New("cursor signing key must be at least 32 bytes")
	}
	payloadText, signatureText, found := strings.Cut(encoded, ".")
	if !found || len(encoded) > 1024 {
		return cursor, errors.New("invalid cursor encoding")
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadText)
	if err != nil {
		return cursor, errors.New("invalid cursor encoding")
	}
	signature, err := base64.RawURLEncoding.DecodeString(signatureText)
	if err != nil {
		return cursor, errors.New("invalid cursor encoding")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return cursor, errors.New("invalid cursor signature")
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return cursor, errors.New("invalid cursor payload")
	}
	if cursor.Version != 1 || cursor.Limit < 1 || cursor.Limit > 100 || len(cursor.After) > 256 {
		return Cursor{}, errors.New("invalid cursor")
	}
	return cursor, nil
}

func CheckedAdd(left, right int64) (int64, error) {
	if (right > 0 && left > math.MaxInt64-right) || (right < 0 && left < math.MinInt64-right) {
		return 0, errors.New("integer overflow")
	}
	return left + right, nil
}

func MillisecondsFromUUIDv7(id UUID) (int64, error) {
	if id[6]>>4 != 7 || id[8]>>6 != 2 {
		return 0, errors.New("not an RFC 9562 UUIDv7")
	}
	var buffer [8]byte
	copy(buffer[2:], id[0:6])
	return int64(binary.BigEndian.Uint64(buffer[:])), nil
}
