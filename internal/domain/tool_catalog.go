package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	toolIDPattern      = regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z][a-z0-9_]*)+$`)
	semverCorePattern  = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	semverIDPattern    = regexp.MustCompile(`^[0-9A-Za-z-]+$`)
	registryKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)
)

type ToolID string

func ParseToolID(value string) (ToolID, error) {
	if len(value) > 255 || !toolIDPattern.MatchString(value) {
		return "", errors.New("tool ID must be a lower-case dotted identifier")
	}
	return ToolID(value), nil
}

type SemanticVersion struct {
	raw        string
	major      uint64
	minor      uint64
	patch      uint64
	prerelease []string
}

func ParseSemanticVersion(value string) (SemanticVersion, error) {
	if value == "" || len(value) > 255 {
		return SemanticVersion{}, errors.New("version must be valid SemVer 2.0.0")
	}
	precedence, build, hasBuild := strings.Cut(value, "+")
	if hasBuild && !validSemverIdentifiers(build, false) || strings.Contains(build, "+") {
		return SemanticVersion{}, errors.New("version must be valid SemVer 2.0.0")
	}
	core, prereleaseText, hasPrerelease := strings.Cut(precedence, "-")
	if hasPrerelease && !validSemverIdentifiers(prereleaseText, true) {
		return SemanticVersion{}, errors.New("version must be valid SemVer 2.0.0")
	}
	match := semverCorePattern.FindStringSubmatch(core)
	if match == nil {
		return SemanticVersion{}, errors.New("version must be valid SemVer 2.0.0")
	}
	parts := [3]uint64{}
	for index := range parts {
		parsed, err := strconv.ParseUint(match[index+1], 10, 64)
		if err != nil {
			return SemanticVersion{}, errors.New("version component exceeds uint64")
		}
		parts[index] = parsed
	}
	var prerelease []string
	if hasPrerelease {
		prerelease = strings.Split(prereleaseText, ".")
	}
	return SemanticVersion{raw: value, major: parts[0], minor: parts[1], patch: parts[2], prerelease: prerelease}, nil
}

func validSemverIdentifiers(value string, rejectNumericLeadingZero bool) bool {
	if value == "" {
		return false
	}
	for _, identifier := range strings.Split(value, ".") {
		if !semverIDPattern.MatchString(identifier) {
			return false
		}
		if rejectNumericLeadingZero && len(identifier) > 1 && identifier[0] == '0' && numericIdentifier(identifier) {
			return false
		}
	}
	return true
}

func numericIdentifier(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return value != ""
}

func (version SemanticVersion) String() string { return version.raw }

// Compare implements SemVer precedence. Build metadata does not affect precedence.
func (version SemanticVersion) Compare(other SemanticVersion) int {
	for _, pair := range [][2]uint64{{version.major, other.major}, {version.minor, other.minor}, {version.patch, other.patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if len(version.prerelease) == 0 && len(other.prerelease) != 0 {
		return 1
	}
	if len(version.prerelease) != 0 && len(other.prerelease) == 0 {
		return -1
	}
	for index := 0; index < len(version.prerelease) && index < len(other.prerelease); index++ {
		left, right := version.prerelease[index], other.prerelease[index]
		if left == right {
			continue
		}
		leftNumeric, rightNumeric := numericIdentifier(left), numericIdentifier(right)
		switch {
		case leftNumeric && rightNumeric:
			if len(left) < len(right) {
				return -1
			}
			if len(left) > len(right) {
				return 1
			}
			if left < right {
				return -1
			}
			return 1
		case leftNumeric:
			return -1
		case rightNumeric:
			return 1
		case left < right:
			return -1
		default:
			return 1
		}
	}
	if len(version.prerelease) < len(other.prerelease) {
		return -1
	}
	if len(version.prerelease) > len(other.prerelease) {
		return 1
	}
	return 0
}

func RequireMonotonicVersion(previous, next SemanticVersion) error {
	if next.Compare(previous) <= 0 {
		return fmt.Errorf("version %s must have greater SemVer precedence than %s", next, previous)
	}
	return nil
}

type RiskClass string

const (
	RiskRead               RiskClass = "read"
	RiskBoundedWrite       RiskClass = "bounded_write"
	RiskConsequentialWrite RiskClass = "consequential_write"
	RiskPrivileged         RiskClass = "privileged"
)

type RetryClass string

const (
	RetrySafe                  RetryClass = "safe"
	RetryDownstreamIdempotency RetryClass = "downstream_idempotency"
	RetryGatewayDeduplicated   RetryClass = "gateway_deduplicated"
	RetryReconcileBeforeRetry  RetryClass = "reconcile_before_retry"
	RetryAtLeastOnceAccepted   RetryClass = "at_least_once_accepted"
	RetryNonRetryable          RetryClass = "non_retryable"
)

type ApprovalClass string

const (
	ApprovalNever  ApprovalClass = "never"
	ApprovalPolicy ApprovalClass = "policy"
	ApprovalAlways ApprovalClass = "always"
)

type ProjectionValueType string

const (
	ProjectionAny     ProjectionValueType = "any"
	ProjectionString  ProjectionValueType = "string"
	ProjectionNumber  ProjectionValueType = "number"
	ProjectionBoolean ProjectionValueType = "boolean"
	ProjectionObject  ProjectionValueType = "object"
	ProjectionArray   ProjectionValueType = "array"
	ProjectionNull    ProjectionValueType = "null"
)

type ResourceProjectionField struct {
	Name       string
	Pointer    string
	Literal    any
	LiteralSet bool
	Required   bool
	Type       ProjectionValueType
}

type ResourceProjectionDefinition struct {
	Fields         []ResourceProjectionField
	MaxFields      int
	MaxOutputBytes int
}

type ToolLimits struct {
	RequestBytes int64
	ResultBytes  int64
	Deadline     time.Duration
	Concurrency  int
	MaxAttempts  int
}

type ConnectorBinding struct {
	ConnectorType    string
	Operation        string
	InstanceSelector string
}

type ToolVersionDefinition struct {
	ToolID             ToolID
	Version            SemanticVersion
	Risk               RiskClass
	SideEffect         bool
	Retry              RetryClass
	Approval           ApprovalClass
	OpenWorldResult    bool
	Connector          ConnectorBinding
	ResourceProjection ResourceProjectionDefinition
	Limits             ToolLimits
}

func ValidateToolVersionDefinition(definition ToolVersionDefinition) error {
	if _, err := ParseToolID(string(definition.ToolID)); err != nil {
		return err
	}
	if _, err := ParseSemanticVersion(definition.Version.String()); err != nil {
		return err
	}
	if !validRisk(definition.Risk) {
		return errors.New("invalid risk class")
	}
	if !validRetry(definition.Retry) {
		return errors.New("invalid retry class")
	}
	if !validApproval(definition.Approval) {
		return errors.New("invalid approval class")
	}
	if definition.Risk == RiskRead && definition.SideEffect {
		return errors.New("read risk cannot declare a side effect")
	}
	if definition.Retry == RetrySafe && definition.SideEffect {
		return errors.New("safe retry class requires a side-effect-free operation")
	}
	if !validRegistryKey(definition.Connector.ConnectorType) || !validRegistryKey(definition.Connector.Operation) || !validRegistryKey(definition.Connector.InstanceSelector) {
		return errors.New("invalid connector-operation binding")
	}
	if err := validateProjection(definition.ResourceProjection); err != nil {
		return err
	}
	if definition.Limits.RequestBytes < 1 || definition.Limits.RequestBytes > 1<<20 || definition.Limits.ResultBytes < 1 || definition.Limits.ResultBytes > 4<<20 || definition.Limits.Deadline <= 0 || definition.Limits.Deadline > 30*time.Second || definition.Limits.Concurrency < 1 || definition.Limits.Concurrency > 250 || definition.Limits.MaxAttempts < 1 || definition.Limits.MaxAttempts > 3 {
		return errors.New("tool limits must be positive and within the platform capacity envelope")
	}
	return nil
}

func validRisk(value RiskClass) bool {
	return value == RiskRead || value == RiskBoundedWrite || value == RiskConsequentialWrite || value == RiskPrivileged
}
func validRetry(value RetryClass) bool {
	return value == RetrySafe || value == RetryDownstreamIdempotency || value == RetryGatewayDeduplicated || value == RetryReconcileBeforeRetry || value == RetryAtLeastOnceAccepted || value == RetryNonRetryable
}
func validApproval(value ApprovalClass) bool {
	return value == ApprovalNever || value == ApprovalPolicy || value == ApprovalAlways
}
func validRegistryKey(value string) bool {
	return len(value) <= 128 && registryKeyPattern.MatchString(value)
}

func validateProjection(definition ResourceProjectionDefinition) error {
	maxFields := definition.MaxFields
	if maxFields == 0 {
		maxFields = 32
	}
	maxBytes := definition.MaxOutputBytes
	if maxBytes == 0 {
		maxBytes = 16 << 10
	}
	if len(definition.Fields) == 0 || maxFields < 1 || len(definition.Fields) > maxFields || maxFields > 32 || maxBytes < 2 || maxBytes > 16<<10 {
		return errors.New("invalid resource projection limits or field count")
	}
	seen := map[string]struct{}{}
	for _, field := range definition.Fields {
		if field.Name == "" || len(field.Name) > 128 || strings.ContainsAny(field.Name, "\x00\r\n") {
			return errors.New("invalid resource projection field name")
		}
		if _, exists := seen[field.Name]; exists {
			return errors.New("duplicate resource projection field")
		}
		seen[field.Name] = struct{}{}
		hasPointer, hasLiteral := field.Pointer != "", field.LiteralSet || field.Literal != nil
		if hasPointer == hasLiteral || (hasPointer && !validJSONPointer(field.Pointer)) {
			return errors.New("resource projection field must have exactly one valid source")
		}
		if field.Type != ProjectionAny && field.Type != ProjectionString && field.Type != ProjectionNumber && field.Type != ProjectionBoolean && field.Type != ProjectionObject && field.Type != ProjectionArray && field.Type != ProjectionNull {
			return errors.New("invalid resource projection value type")
		}
	}
	return nil
}

func validJSONPointer(pointer string) bool {
	if pointer == "" || pointer[0] != '/' {
		return false
	}
	for position := 0; position < len(pointer); position++ {
		if pointer[position] != '~' {
			continue
		}
		if position+1 >= len(pointer) || pointer[position+1] != '0' && pointer[position+1] != '1' {
			return false
		}
		position++
	}
	return true
}

type ToolVersionState string

const (
	ToolVersionDraft     ToolVersionState = "draft"
	ToolVersionPublished ToolVersionState = "published"
	ToolVersionRetired   ToolVersionState = "retired"
)

type ToolVersion struct {
	definition ToolVersionDefinition
	state      ToolVersionState
}

func NewToolVersion(definition ToolVersionDefinition) (ToolVersion, error) {
	if err := ValidateToolVersionDefinition(definition); err != nil {
		return ToolVersion{}, err
	}
	return ToolVersion{definition: cloneToolDefinition(definition), state: ToolVersionDraft}, nil
}
func (version ToolVersion) Definition() ToolVersionDefinition {
	return cloneToolDefinition(version.definition)
}
func (version ToolVersion) State() ToolVersionState { return version.state }
func (version ToolVersion) Publish() (ToolVersion, error) {
	if version.state != ToolVersionDraft {
		return ToolVersion{}, errors.New("only a draft tool version can be published")
	}
	version.state = ToolVersionPublished
	return version, nil
}
func (version ToolVersion) Retire() (ToolVersion, error) {
	if version.state != ToolVersionPublished {
		return ToolVersion{}, errors.New("only a published tool version can be retired")
	}
	version.state = ToolVersionRetired
	return version, nil
}

type ToolExposure struct{ enabled bool }

func NewToolExposure(version ToolVersion, enabled bool) (ToolExposure, error) {
	if enabled && version.state != ToolVersionPublished {
		return ToolExposure{}, errors.New("only a published tool version can be enabled")
	}
	return ToolExposure{enabled: enabled}, nil
}
func (exposure ToolExposure) Enabled() bool { return exposure.enabled }
func (exposure ToolExposure) SetEnabled(version ToolVersion, enabled bool) (ToolExposure, error) {
	return NewToolExposure(version, enabled)
}

func cloneToolDefinition(definition ToolVersionDefinition) ToolVersionDefinition {
	definition.ResourceProjection.Fields = append([]ResourceProjectionField(nil), definition.ResourceProjection.Fields...)
	for index := range definition.ResourceProjection.Fields {
		definition.ResourceProjection.Fields[index].Literal = cloneJSONValue(definition.ResourceProjection.Fields[index].Literal)
	}
	return definition
}

func cloneJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		copyValue := make(map[string]any, len(typed))
		for key, child := range typed {
			copyValue[key] = cloneJSONValue(child)
		}
		return copyValue
	case []any:
		copyValue := make([]any, len(typed))
		for index, child := range typed {
			copyValue[index] = cloneJSONValue(child)
		}
		return copyValue
	default:
		return value
	}
}
