package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bdobrica/ThinkPixelTG/internal/domain"
	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

type adminAuthorizerFake struct {
	deny   bool
	calls  []ports.AdministrativeAction
	tenant string
}

func (fake *adminAuthorizerFake) AuthorizeAdministration(_ context.Context, action ports.AdministrativeAction, tenant string) error {
	fake.calls = append(fake.calls, action)
	fake.tenant = tenant
	if fake.deny {
		return errors.New("denied")
	}
	return nil
}

type administratorFake struct {
	published []ports.AdministrativeMutation
	exposures []ports.AdministrativeMutation
}

func (fake *administratorFake) PublishToolVersion(_ context.Context, mutation ports.AdministrativeMutation) error {
	fake.published = append(fake.published, mutation)
	return nil
}
func (fake *administratorFake) SetTenantToolExposure(_ context.Context, mutation ports.AdministrativeMutation) error {
	fake.exposures = append(fake.exposures, mutation)
	return nil
}

type schemaCompilerFake struct{ calls int }

func (fake *schemaCompilerFake) CompileSchema([]byte) error { fake.calls++; return nil }

func TestAdministrationPublishesValidatedVersionAfterPrivilegedAuthorization(t *testing.T) {
	authorizer, service, compiler := &adminAuthorizerFake{}, &administratorFake{}, &schemaCompilerFake{}
	handler, err := NewAdministrationHandler(AdministrationOptions{Authorizer: authorizer, Service: service, Compiler: compiler})
	if err != nil {
		t.Fatal(err)
	}
	body := `{
      "tool_id":"github.pull.list","version":"1.0.0","title":"List pull requests","description":"Lists reviewed pull requests.","review_reference":"review:1",
      "input_schema":{"type":"object"},"output_schema":{"type":"object"},"risk":"read","side_effect":false,"retry_class":"safe","retry_qualification":"test:1","approval":"never","open_world_result":true,
      "canonicalization_profile":"jcs-v1","connector":{"type":"github","operation":"pull.list","instance_selector":"github.read"},"credential_binding_selector":"github.read",
      "resource_projection":{"fields":[{"name":"repository","pointer":"/repository","required":true,"type":"string"}]},
      "limits":{"request_bytes":1024,"result_bytes":4096,"deadline_ms":1000,"concurrency":2,"max_attempts":1},
      "metering":{"dimension":"tool_call","units":"1","charge_point":"result","deduplication_scope":"logical_invocation"}
    }`
	request := httptest.NewRequest(http.MethodPost, "/v1/admin/tool-versions", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "publication-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if len(authorizer.calls) != 1 || authorizer.calls[0] != ports.AdminPublishToolVersion {
		t.Fatalf("authorization calls = %#v", authorizer.calls)
	}
	if compiler.calls != 2 || len(service.published) != 1 {
		t.Fatalf("compiler calls=%d publications=%d", compiler.calls, len(service.published))
	}
	mutation := service.published[0]
	if mutation.IdempotencyKey != "publication-0001" || mutation.Definition.Connector.Operation != "pull.list" || mutation.Definition.Limits.Deadline.String() != "1s" {
		t.Fatalf("mutation = %#v", mutation)
	}
}

func TestAdministrationRejectsDeniedPublicationBeforeParsingOrCompilation(t *testing.T) {
	authorizer, service, compiler := &adminAuthorizerFake{deny: true}, &administratorFake{}, &schemaCompilerFake{}
	handler, _ := NewAdministrationHandler(AdministrationOptions{Authorizer: authorizer, Service: service, Compiler: compiler})
	request := httptest.NewRequest(http.MethodPost, "/v1/admin/tool-versions", strings.NewReader(`not-json`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "publication-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || compiler.calls != 0 || len(service.published) != 0 {
		t.Fatalf("response=%d compiler=%d publications=%d", response.Code, compiler.calls, len(service.published))
	}
}

func TestAdministrationSetsTenantScopedExposureAfterAuthorization(t *testing.T) {
	authorizer, service := &adminAuthorizerFake{}, &administratorFake{}
	handler, _ := NewAdministrationHandler(AdministrationOptions{Authorizer: authorizer, Service: service, Compiler: &schemaCompilerFake{}})
	request := httptest.NewRequest(http.MethodPut, "/v1/admin/tenant-tool-exposures", strings.NewReader(`{"tenant_id":"01890f3e-7b6d-7cc0-98c4-dc0c0c073990","tool_id":"github.pull.list","version":"1.0.0","enabled":true}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "exposure-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || len(service.exposures) != 1 {
		t.Fatalf("response=%d %s mutations=%d", response.Code, response.Body.String(), len(service.exposures))
	}
	if authorizer.tenant != "01890f3e-7b6d-7cc0-98c4-dc0c0c073990" || service.exposures[0].IdempotencyKey != "exposure-0001" {
		t.Fatalf("authorization=%#v mutation=%#v", authorizer, service.exposures[0])
	}
}

func TestAdministrationRequiresStrictJSONAndIdempotencyKey(t *testing.T) {
	authorizer, service := &adminAuthorizerFake{}, &administratorFake{}
	handler, _ := NewAdministrationHandler(AdministrationOptions{Authorizer: authorizer, Service: service, Compiler: &schemaCompilerFake{}})
	for name, testCase := range map[string][2]string{
		"missing key":   {`{}`, ""},
		"unknown field": {`{"tenant_id":"01890f3e-7b6d-7cc0-98c4-dc0c0c073990","tool_id":"github.pull.list","version":"1.0.0","enabled":true,"connector":"hostile"}`, "exposure-1"},
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPut, "/v1/admin/tenant-tool-exposures", strings.NewReader(testCase[0]))
			request.Header.Set("Content-Type", "application/json")
			if testCase[1] != "" {
				request.Header.Set("Idempotency-Key", testCase[1])
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("response=%d %s", response.Code, response.Body.String())
			}
		})
	}
	if len(service.exposures) != 0 {
		t.Fatal("invalid request reached mutation service")
	}
}

var _ domain.SchemaCompiler = (*schemaCompilerFake)(nil)
