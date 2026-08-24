package resourceprojection

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/bdobrica/ThinkPixelTG/internal/canonicaljson"
	"github.com/bdobrica/ThinkPixelTG/internal/domain"
)

var (
	ErrInvalidDefinition = errors.New("invalid resource projection definition")
	ErrInvalidArguments  = errors.New("invalid normalized arguments")
	ErrMissing           = errors.New("required resource projection value is missing")
	ErrType              = errors.New("resource projection value has the wrong type")
	ErrLimit             = errors.New("resource projection limit exceeded")
)

const (
	defaultMaxFields     = 32
	defaultMaxOutputSize = 16 << 10
)

// Type is the JSON type a selected argument must have. Any accepts every JSON
// type, including null, and should be used only where the tool contract truly
// treats the value as opaque.
type Type string

const (
	Any     Type = "any"
	String  Type = "string"
	Number  Type = "number"
	Boolean Type = "boolean"
	Object  Type = "object"
	Array   Type = "array"
	Null    Type = "null"
)

// Field defines one top-level member of the resource object. Exactly one of
// Pointer and Literal must be set. Pointer is an exact RFC 6901 JSON Pointer;
// wildcards and alternative/fallback selectors are intentionally unsupported.
type Field struct {
	Name     string
	Pointer  string
	Literal  any
	Required bool
	Type     Type
}

// Definition is immutable tool-version metadata, not invocation input.
type Definition struct {
	Fields         []Field
	MaxFields      int
	MaxOutputBytes int
}

// Projection contains the canonical resource object and its domain-separated
// digest. Value is a fresh object and does not alias the parsed arguments.
type Projection struct {
	Value     map[string]any
	Canonical []byte
	Digest    domain.Digest
}

type compiledField struct {
	name      string
	tokens    []string
	literal   any
	isLiteral bool
	required  bool
	typeName  Type
}

// Project validates and compiles trusted rules, then applies them to a result
// returned by canonicaljson.NormalizeArguments.
func Project(arguments canonicaljson.Result, definition Definition) (Projection, error) {
	fields, maxOutput, err := compile(definition)
	if err != nil {
		return Projection{}, err
	}
	if arguments.Profile != canonicaljson.Profile || len(arguments.Canonical) == 0 {
		return Projection{}, fmt.Errorf("%w: canonical profile", ErrInvalidArguments)
	}
	recanonical, err := canonicaljson.Canonicalize(arguments.Canonical, canonicaljson.Limits{MaxBytes: len(arguments.Canonical)})
	if err != nil || !bytes.Equal(recanonical, arguments.Canonical) || canonicaljson.Digest(canonicaljson.ArgumentDomain, arguments.Canonical) != arguments.Digest {
		return Projection{}, fmt.Errorf("%w: canonical bytes or digest", ErrInvalidArguments)
	}
	root, err := canonicaljson.Parse(arguments.Canonical, canonicaljson.Limits{MaxBytes: len(arguments.Canonical)})
	if err != nil {
		return Projection{}, fmt.Errorf("%w: %v", ErrInvalidArguments, err)
	}

	value := make(map[string]any, len(fields))
	for _, field := range fields {
		selected, found := field.literal, field.isLiteral
		if !field.isLiteral {
			selected, found = resolve(root, field.tokens)
		}
		if !found {
			if field.required {
				return Projection{}, fmt.Errorf("%w: %q", ErrMissing, field.name)
			}
			continue
		}
		if !matches(field.typeName, selected) {
			return Projection{}, fmt.Errorf("%w: %q requires %s", ErrType, field.name, field.typeName)
		}
		value[field.name] = selected
	}

	raw, err := json.Marshal(value)
	if err != nil {
		return Projection{}, fmt.Errorf("marshal resource projection: %w", err)
	}
	canonical, err := canonicaljson.Canonicalize(raw, canonicaljson.Limits{MaxBytes: maxOutput})
	if err != nil {
		if errors.Is(err, canonicaljson.ErrLimit) {
			return Projection{}, fmt.Errorf("%w: output bytes", ErrLimit)
		}
		return Projection{}, fmt.Errorf("canonicalize resource projection: %w", err)
	}
	if len(canonical) > maxOutput {
		return Projection{}, fmt.Errorf("%w: output bytes", ErrLimit)
	}
	detached, err := canonicaljson.Parse(canonical, canonicaljson.Limits{MaxBytes: maxOutput})
	if err != nil {
		return Projection{}, fmt.Errorf("parse canonical resource projection: %w", err)
	}
	detachedValue, ok := detached.(map[string]any)
	if !ok {
		return Projection{}, errors.New("canonical resource projection is not an object")
	}
	return Projection{Value: detachedValue, Canonical: canonical, Digest: canonicaljson.Digest(canonicaljson.ResourceDomain, canonical)}, nil
}

func compile(definition Definition) ([]compiledField, int, error) {
	maxFields := definition.MaxFields
	if maxFields == 0 {
		maxFields = defaultMaxFields
	}
	maxOutput := definition.MaxOutputBytes
	if maxOutput == 0 {
		maxOutput = defaultMaxOutputSize
	}
	if maxFields < 1 || maxOutput < 2 || len(definition.Fields) == 0 || len(definition.Fields) > maxFields {
		return nil, 0, fmt.Errorf("%w: invalid limits or field count", ErrInvalidDefinition)
	}
	seen := make(map[string]struct{}, len(definition.Fields))
	compiled := make([]compiledField, 0, len(definition.Fields))
	for _, field := range definition.Fields {
		if field.Name == "" || len(field.Name) > 128 || strings.ContainsAny(field.Name, "\x00\r\n") {
			return nil, 0, fmt.Errorf("%w: invalid output field name", ErrInvalidDefinition)
		}
		if _, exists := seen[field.Name]; exists {
			return nil, 0, fmt.Errorf("%w: ambiguous duplicate output field %q", ErrInvalidDefinition, field.Name)
		}
		seen[field.Name] = struct{}{}
		hasPointer := field.Pointer != ""
		hasLiteral := field.Literal != nil
		if hasPointer == hasLiteral {
			return nil, 0, fmt.Errorf("%w: field %q must have exactly one source", ErrInvalidDefinition, field.Name)
		}
		typeName := field.Type
		if typeName == "" {
			typeName = Any
		}
		if !validType(typeName) {
			return nil, 0, fmt.Errorf("%w: field %q has unknown type", ErrInvalidDefinition, field.Name)
		}
		entry := compiledField{name: field.Name, literal: field.Literal, isLiteral: hasLiteral, required: field.Required, typeName: typeName}
		if hasPointer {
			var err error
			entry.tokens, err = parsePointer(field.Pointer)
			if err != nil {
				return nil, 0, fmt.Errorf("%w: field %q: %v", ErrInvalidDefinition, field.Name, err)
			}
		} else if !jsonSafe(field.Literal) {
			return nil, 0, fmt.Errorf("%w: field %q literal is not safe JSON", ErrInvalidDefinition, field.Name)
		}
		compiled = append(compiled, entry)
	}
	return compiled, maxOutput, nil
}

func parsePointer(pointer string) ([]string, error) {
	if pointer == "" || pointer[0] != '/' {
		return nil, errors.New("pointer must be non-empty and start with slash")
	}
	parts := strings.Split(pointer[1:], "/")
	for index, part := range parts {
		var output strings.Builder
		for position := 0; position < len(part); position++ {
			if part[position] != '~' {
				output.WriteByte(part[position])
				continue
			}
			if position+1 >= len(part) || (part[position+1] != '0' && part[position+1] != '1') {
				return nil, errors.New("invalid pointer escape")
			}
			position++
			if part[position] == '0' {
				output.WriteByte('~')
			} else {
				output.WriteByte('/')
			}
		}
		parts[index] = output.String()
	}
	return parts, nil
}

func resolve(value any, tokens []string) (any, bool) {
	current := value
	for _, token := range tokens {
		switch node := current.(type) {
		case map[string]any:
			var found bool
			current, found = node[token]
			if !found {
				return nil, false
			}
		case []any:
			if token == "-" || (len(token) > 1 && token[0] == '0') {
				return nil, false
			}
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 || index >= len(node) {
				return nil, false
			}
			current = node[index]
		default:
			return nil, false
		}
	}
	return current, true
}

func validType(value Type) bool {
	switch value {
	case Any, String, Number, Boolean, Object, Array, Null:
		return true
	default:
		return false
	}
}

func matches(expected Type, value any) bool {
	if expected == Any {
		return true
	}
	switch expected {
	case String:
		_, ok := value.(string)
		return ok
	case Number:
		_, ok := value.(float64)
		return ok
	case Boolean:
		_, ok := value.(bool)
		return ok
	case Object:
		_, ok := value.(map[string]any)
		return ok
	case Array:
		_, ok := value.([]any)
		return ok
	case Null:
		return value == nil
	default:
		return false
	}
}

func jsonSafe(value any) bool {
	raw, err := json.Marshal(value)
	if err != nil {
		return false
	}
	_, err = canonicaljson.Canonicalize(raw, canonicaljson.Limits{MaxBytes: defaultMaxOutputSize})
	return err == nil
}
