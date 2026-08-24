package canonicaljson

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"strconv"
	"unicode/utf8"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/ucarion/jcs"
)

const (
	Profile = "tg-cjson-v1"

	ArgumentDomain = "ThinkPixelTG:arguments:tg-cjson-v1\x00"
	ResourceDomain = "ThinkPixelTG:resource:tg-cjson-v1\x00"

	maxSafeInteger = int64(1<<53 - 1)
)

var (
	ErrInvalidJSON  = errors.New("invalid JSON")
	ErrLimit        = errors.New("canonical JSON limit exceeded")
	ErrUnsafeNumber = errors.New("JSON number is not safely interoperable")
	ErrSchema       = errors.New("JSON schema validation failed")
)

// Limits bounds work before schema validation and canonicalization. Zero-valued
// fields receive the secure defaults from DefaultLimits.
type Limits struct {
	MaxBytes       int
	MaxDepth       int
	MaxMembers     int
	MaxStringBytes int
	MaxNumberBytes int
}

func DefaultLimits() Limits {
	return Limits{
		MaxBytes:       1 << 20,
		MaxDepth:       64,
		MaxMembers:     10_000,
		MaxStringBytes: 1 << 20,
		MaxNumberBytes: 128,
	}
}

// Validator validates the parsed value against the immutable tool schema. It
// must not mutate value. Canonical argument identity is never produced unless
// this callback succeeds.
type Validator func(context.Context, any) error

type Result struct {
	Profile   string
	Canonical []byte
	Digest    domain.Digest
}

// NormalizeArguments strictly parses, schema-validates, canonicalizes, and
// hashes arguments in the normative order defined by the tg-cjson-v1 contract.
func NormalizeArguments(ctx context.Context, input []byte, limits Limits, validate Validator) (Result, error) {
	if validate == nil {
		return Result{}, errors.New("canonical JSON schema validator is required")
	}
	value, err := Parse(input, limits)
	if err != nil {
		return Result{}, err
	}
	if err := validate(ctx, value); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrSchema, err)
	}
	canonical, err := format(value)
	if err != nil {
		return Result{}, err
	}
	return Result{Profile: Profile, Canonical: canonical, Digest: Digest(ArgumentDomain, canonical)}, nil
}

// Canonicalize strictly parses and canonicalizes JSON without asserting schema
// validity. Call NormalizeArguments for security-sensitive tool arguments.
func Canonicalize(input []byte, limits Limits) ([]byte, error) {
	value, err := Parse(input, limits)
	if err != nil {
		return nil, err
	}
	return format(value)
}

// Digest computes the profile's domain-separated SHA-256 digest.
func Digest(domainSeparator string, canonical []byte) domain.Digest {
	buffer := make([]byte, 0, len(domainSeparator)+len(canonical))
	buffer = append(buffer, domainSeparator...)
	buffer = append(buffer, canonical...)
	return domain.DigestBytes(buffer)
}

// Parse accepts exactly one UTF-8 JSON value, detects duplicate decoded member
// names, enforces I-JSON string/number constraints, and preserves no raw number
// spelling after its safe IEEE-754 value has been established.
func Parse(input []byte, limits Limits) (any, error) {
	limits = withDefaults(limits)
	if len(input) == 0 || len(input) > limits.MaxBytes {
		return nil, fmt.Errorf("%w: input bytes", ErrLimit)
	}
	if !utf8.Valid(input) {
		return nil, fmt.Errorf("%w: input is not UTF-8", ErrInvalidJSON)
	}
	if err := validateSurrogates(input); err != nil {
		return nil, err
	}

	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	state := parseState{decoder: decoder, limits: limits}
	value, err := state.value(1)
	if err != nil {
		return nil, err
	}
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("%w: trailing token %v", ErrInvalidJSON, token)
		}
		return nil, fmt.Errorf("%w: trailing data: %v", ErrInvalidJSON, err)
	}
	return value, nil
}

type parseState struct {
	decoder *json.Decoder
	limits  Limits
	members int
}

func (s *parseState) value(depth int) (any, error) {
	if depth > s.limits.MaxDepth {
		return nil, fmt.Errorf("%w: nesting depth", ErrLimit)
	}
	token, err := s.decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}
	switch token := token.(type) {
	case nil, bool:
		return token, nil
	case string:
		if len(token) > s.limits.MaxStringBytes {
			return nil, fmt.Errorf("%w: string bytes", ErrLimit)
		}
		return token, nil
	case json.Number:
		return s.number(string(token))
	case json.Delim:
		switch token {
		case '[':
			values := make([]any, 0)
			for s.decoder.More() {
				value, err := s.value(depth + 1)
				if err != nil {
					return nil, err
				}
				values = append(values, value)
			}
			if end, err := s.decoder.Token(); err != nil || end != json.Delim(']') {
				return nil, fmt.Errorf("%w: unterminated array", ErrInvalidJSON)
			}
			return values, nil
		case '{':
			object := make(map[string]any)
			for s.decoder.More() {
				keyToken, err := s.decoder.Token()
				if err != nil {
					return nil, fmt.Errorf("%w: object key: %v", ErrInvalidJSON, err)
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, fmt.Errorf("%w: object key is not a string", ErrInvalidJSON)
				}
				if len(key) > s.limits.MaxStringBytes {
					return nil, fmt.Errorf("%w: string bytes", ErrLimit)
				}
				if _, exists := object[key]; exists {
					return nil, fmt.Errorf("%w: duplicate object member %q", ErrInvalidJSON, key)
				}
				s.members++
				if s.members > s.limits.MaxMembers {
					return nil, fmt.Errorf("%w: object members", ErrLimit)
				}
				value, err := s.value(depth + 1)
				if err != nil {
					return nil, err
				}
				object[key] = value
			}
			if end, err := s.decoder.Token(); err != nil || end != json.Delim('}') {
				return nil, fmt.Errorf("%w: unterminated object", ErrInvalidJSON)
			}
			return object, nil
		}
	}
	return nil, fmt.Errorf("%w: unexpected token", ErrInvalidJSON)
}

func (s *parseState) number(token string) (float64, error) {
	if len(token) > s.limits.MaxNumberBytes {
		return 0, fmt.Errorf("%w: number bytes", ErrLimit)
	}
	rational, ok := new(big.Rat).SetString(token)
	if !ok {
		return 0, fmt.Errorf("%w: malformed number", ErrInvalidJSON)
	}
	if rational.IsInt() {
		absolute := new(big.Int).Abs(rational.Num())
		if absolute.Cmp(big.NewInt(maxSafeInteger)) > 0 {
			return 0, fmt.Errorf("%w: integer exceeds IEEE-754 safe range", ErrUnsafeNumber)
		}
	}
	value, err := strconv.ParseFloat(token, 64)
	if err != nil || math.IsInf(value, 0) || math.IsNaN(value) || (value == 0 && rational.Sign() != 0) {
		return 0, fmt.Errorf("%w: number cannot be represented as finite binary64", ErrUnsafeNumber)
	}
	return value, nil
}

func format(value any) ([]byte, error) {
	canonical, err := jcs.Format(value)
	if err != nil {
		return nil, fmt.Errorf("canonicalize JSON: %w", err)
	}
	return []byte(canonical), nil
}

func withDefaults(limits Limits) Limits {
	defaults := DefaultLimits()
	if limits.MaxBytes <= 0 {
		limits.MaxBytes = defaults.MaxBytes
	}
	if limits.MaxDepth <= 0 {
		limits.MaxDepth = defaults.MaxDepth
	}
	if limits.MaxMembers <= 0 {
		limits.MaxMembers = defaults.MaxMembers
	}
	if limits.MaxStringBytes <= 0 {
		limits.MaxStringBytes = defaults.MaxStringBytes
	}
	if limits.MaxNumberBytes <= 0 {
		limits.MaxNumberBytes = defaults.MaxNumberBytes
	}
	return limits
}

func validateSurrogates(input []byte) error {
	for index := 0; index < len(input); index++ {
		if input[index] != '"' {
			continue
		}
		index++
		for index < len(input) && input[index] != '"' {
			if input[index] != '\\' {
				index++
				continue
			}
			index++
			if index >= len(input) {
				break
			}
			if input[index] != 'u' {
				index++
				continue
			}
			first, ok := hexQuad(input, index+1)
			if !ok {
				break
			}
			index += 5
			if first >= 0xd800 && first <= 0xdbff {
				if index+5 >= len(input) || input[index] != '\\' || input[index+1] != 'u' {
					return fmt.Errorf("%w: unpaired high surrogate", ErrInvalidJSON)
				}
				second, valid := hexQuad(input, index+2)
				if !valid || second < 0xdc00 || second > 0xdfff {
					return fmt.Errorf("%w: unpaired high surrogate", ErrInvalidJSON)
				}
				index += 6
			} else if first >= 0xdc00 && first <= 0xdfff {
				return fmt.Errorf("%w: unpaired low surrogate", ErrInvalidJSON)
			}
		}
	}
	return nil
}

func hexQuad(input []byte, start int) (uint16, bool) {
	if start+4 > len(input) {
		return 0, false
	}
	value, err := strconv.ParseUint(string(input[start:start+4]), 16, 16)
	return uint16(value), err == nil
}
