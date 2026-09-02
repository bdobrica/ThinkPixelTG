package schema

import (
	"bytes"
	"container/list"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	DefaultMaxSchemaBytes   = 512 << 10
	DefaultMaxInstanceBytes = 4 << 20
	DefaultMaxDepth         = 64
	DefaultMaxNodes         = 10_000
	DefaultMaxStringBytes   = 256 << 10
	DefaultMaxObjectMembers = 10_000
	DefaultMaxCacheEntries  = 128
	DefaultMaxErrors        = 32
	maximumCacheEntries     = 1_024
	maximumErrors           = 128
)

var (
	ErrSchemaTooLarge     = errors.New("schema exceeds size limit")
	ErrInstanceTooLarge   = errors.New("JSON instance exceeds size limit")
	ErrMalformedSchema    = errors.New("schema is not valid JSON")
	ErrMalformedInstance  = errors.New("instance is not valid JSON")
	ErrSchemaComplexity   = errors.New("schema exceeds complexity limit")
	ErrInstanceComplexity = errors.New("instance exceeds complexity limit")
	ErrExternalReference  = errors.New("schema contains a non-local reference")
	ErrInvalidSchema      = errors.New("schema is invalid")
)

type Limits struct {
	MaxSchemaBytes   int
	MaxInstanceBytes int
	MaxDepth         int
	MaxNodes         int
	MaxStringBytes   int
	MaxObjectMembers int
	MaxCacheEntries  int
	MaxErrors        int
}

func (limits Limits) normalized() Limits {
	if limits.MaxSchemaBytes <= 0 || limits.MaxSchemaBytes > DefaultMaxSchemaBytes {
		limits.MaxSchemaBytes = DefaultMaxSchemaBytes
	}
	if limits.MaxInstanceBytes <= 0 || limits.MaxInstanceBytes > DefaultMaxInstanceBytes {
		limits.MaxInstanceBytes = DefaultMaxInstanceBytes
	}
	if limits.MaxDepth <= 0 || limits.MaxDepth > DefaultMaxDepth {
		limits.MaxDepth = DefaultMaxDepth
	}
	if limits.MaxNodes <= 0 || limits.MaxNodes > DefaultMaxNodes {
		limits.MaxNodes = DefaultMaxNodes
	}
	if limits.MaxStringBytes <= 0 || limits.MaxStringBytes > DefaultMaxStringBytes {
		limits.MaxStringBytes = DefaultMaxStringBytes
	}
	if limits.MaxObjectMembers <= 0 || limits.MaxObjectMembers > DefaultMaxObjectMembers {
		limits.MaxObjectMembers = DefaultMaxObjectMembers
	}
	if limits.MaxCacheEntries <= 0 || limits.MaxCacheEntries > maximumCacheEntries {
		limits.MaxCacheEntries = DefaultMaxCacheEntries
	}
	if limits.MaxErrors <= 0 || limits.MaxErrors > maximumErrors {
		limits.MaxErrors = DefaultMaxErrors
	}
	return limits
}

type Validator struct {
	limits Limits
	mu     sync.Mutex
	cache  map[[sha256.Size]byte]*list.Element
	lru    *list.List
}

type cacheEntry struct {
	key    [sha256.Size]byte
	schema *Compiled
}

type Compiled struct {
	schema *jsonschema.Schema
	limits Limits
}

type Violation struct {
	InstanceLocation string
	KeywordLocation  string
	Code             string
}

type ValidationError struct{ Violations []Violation }

func (err *ValidationError) Error() string {
	return fmt.Sprintf("JSON instance failed schema validation (%d violation(s))", len(err.Violations))
}

func NewValidator(limits Limits) *Validator {
	limits = limits.normalized()
	return &Validator{limits: limits, cache: make(map[[sha256.Size]byte]*list.Element), lru: list.New()}
}

func (validator *Validator) Compile(raw []byte) (*Compiled, error) {
	if len(raw) > validator.limits.MaxSchemaBytes {
		return nil, ErrSchemaTooLarge
	}
	key := sha256.Sum256(raw)
	validator.mu.Lock()
	defer validator.mu.Unlock()
	if element := validator.cache[key]; element != nil {
		validator.lru.MoveToFront(element)
		return element.Value.(*cacheEntry).schema, nil
	}
	document, err := decodeJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedSchema, err)
	}
	if err := inspect(document, validator.limits, true); err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertVocabs()
	if err := compiler.AddResource("urn:thinkpixel:tool-schema", document); err != nil {
		return nil, ErrInvalidSchema
	}
	compiledSchema, err := compiler.Compile("urn:thinkpixel:tool-schema")
	if err != nil {
		return nil, ErrInvalidSchema
	}
	compiled := &Compiled{schema: compiledSchema, limits: validator.limits}
	element := validator.lru.PushFront(&cacheEntry{key: key, schema: compiled})
	validator.cache[key] = element
	for validator.lru.Len() > validator.limits.MaxCacheEntries {
		oldest := validator.lru.Back()
		delete(validator.cache, oldest.Value.(*cacheEntry).key)
		validator.lru.Remove(oldest)
	}
	return compiled, nil
}

func (compiled *Compiled) ValidateJSON(raw []byte) error {
	if len(raw) > compiled.limits.MaxInstanceBytes {
		return ErrInstanceTooLarge
	}
	value, err := decodeJSON(raw)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrMalformedInstance, err)
	}
	if err := inspect(value, compiled.limits, false); err != nil {
		return err
	}
	if err := compiled.schema.Validate(value); err != nil {
		return deterministicValidationError(err, compiled.limits.MaxErrors)
	}
	return nil
}

func decodeJSON(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("multiple JSON values")
	}
	return value, nil
}

func inspect(root any, limits Limits, schemaDocument bool) error {
	type item struct {
		value any
		depth int
	}
	stack := []item{{root, 1}}
	nodes := 0
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		nodes++
		if nodes > limits.MaxNodes || current.depth > limits.MaxDepth {
			if schemaDocument {
				return ErrSchemaComplexity
			}
			return ErrInstanceComplexity
		}
		switch value := current.value.(type) {
		case map[string]any:
			if len(value) > limits.MaxObjectMembers {
				if schemaDocument {
					return ErrSchemaComplexity
				}
				return ErrInstanceComplexity
			}
			for key, child := range value {
				if schemaDocument && (key == "$ref" || key == "$dynamicRef") {
					reference, ok := child.(string)
					if !ok || (reference != "#" && !strings.HasPrefix(reference, "#/")) {
						return ErrExternalReference
					}
				}
				stack = append(stack, item{child, current.depth + 1})
			}
		case []any:
			for _, child := range value {
				stack = append(stack, item{child, current.depth + 1})
			}
		case string:
			if len(value) > limits.MaxStringBytes {
				if schemaDocument {
					return ErrSchemaComplexity
				}
				return ErrInstanceComplexity
			}
		}
	}
	return nil
}

func deterministicValidationError(err error, maximum int) error {
	validationErr := new(jsonschema.ValidationError)
	if !errors.As(err, &validationErr) {
		return errors.New("schema validation failed")
	}
	violations := make([]Violation, 0)
	var collect func(*jsonschema.ValidationError)
	collect = func(current *jsonschema.ValidationError) {
		if len(current.Causes) > 0 {
			for _, cause := range current.Causes {
				collect(cause)
			}
			return
		}
		keyword := current.ErrorKind.KeywordPath()
		code := "schema"
		if len(keyword) > 0 {
			code = keyword[len(keyword)-1]
		}
		violations = append(violations, Violation{InstanceLocation: pointer(current.InstanceLocation), KeywordLocation: pointer(keyword), Code: code})
	}
	collect(validationErr)
	sort.Slice(violations, func(i, j int) bool {
		left, right := violations[i], violations[j]
		if left.InstanceLocation != right.InstanceLocation {
			return left.InstanceLocation < right.InstanceLocation
		}
		if left.KeywordLocation != right.KeywordLocation {
			return left.KeywordLocation < right.KeywordLocation
		}
		return left.Code < right.Code
	})
	if len(violations) > maximum {
		violations = violations[:maximum]
	}
	return &ValidationError{Violations: violations}
}

func pointer(tokens []string) string {
	if len(tokens) == 0 {
		return ""
	}
	var result strings.Builder
	for _, token := range tokens {
		result.WriteByte('/')
		result.WriteString(strings.ReplaceAll(strings.ReplaceAll(token, "~", "~0"), "/", "~1"))
	}
	return result.String()
}

func (validator *Validator) CacheLen() int {
	validator.mu.Lock()
	defer validator.mu.Unlock()
	return validator.lru.Len()
}
