package ports

import (
	"context"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
)

type AdministrativeAction string

const (
	AdminPublishToolVersion AdministrativeAction = "tool_version.publish"
	AdminSetTenantExposure  AdministrativeAction = "tenant_tool_exposure.set"
)

// AdministrativeAuthorizer is a privileged policy boundary independent from
// ordinary harness invocation authorization.
type AdministrativeAuthorizer interface {
	AuthorizeAdministration(context.Context, AdministrativeAction, string) error
}

type AdministrativeMutation struct {
	IdempotencyKey string
	Definition     domain.ToolVersionDefinition
	TenantID       string
	ToolID         string
	Version        string
	Enabled        bool
}

// ToolAdministrator persists already-authorized mutations with their required
// audit/outbox records and deduplicates retries by IdempotencyKey.
type ToolAdministrator interface {
	PublishToolVersion(context.Context, AdministrativeMutation) error
	SetTenantToolExposure(context.Context, AdministrativeMutation) error
}
