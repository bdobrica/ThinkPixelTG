GO ?= go
GOFLAGS ?= -mod=readonly
NPX ?= npx
DOCKER ?= docker
REDOCLY_VERSION ?= 2.46.2
GO_PACKAGES ?= ./...
BUILD_DIR ?= build
IMAGE ?= thinkpixeltg:dev
COMPOSE ?= docker compose
COMPOSE_FILE ?= deployments/compose/compose.yaml
TOOLS_RUN := ./scripts/run-go-tool.sh
PROJECT_TMP ?= $(CURDIR)/.tmp
export TMPDIR := $(PROJECT_TMP)

.DEFAULT_GOAL := build

.PHONY: prepare tools generate generate-check fmt fmt-check lint vet test test-race \
	test-integration test-e2e test-security test-mcp-conformance openapi-check \
	test-phase2 test-phase3 migration-test dependency-check license-check vuln-check build image container-smoke \
	verify verify-phase0 compose-up compose-down compose-clean clean

prepare:
	mkdir -p $(PROJECT_TMP)

tools generate fmt lint vet test test-race test-integration test-e2e \
	test-security test-mcp-conformance openapi-check migration-test \
	test-phase2 test-phase3 dependency-check license-check vuln-check build image: | prepare

tools:
	$(GO) -C tools/golangci-lint mod download
	$(GO) -C tools/govulncheck mod download
	$(GO) -C tools/go-licenses mod download
	$(GO) -C tools/oapi-codegen mod download
	$(GO) -C tools/golangci-lint tool golangci-lint version
	$(GO) -C tools/govulncheck tool govulncheck -version
	$(GO) -C tools/go-licenses tool go-licenses --help >/dev/null 2>&1
	$(GO) -C tools/oapi-codegen tool oapi-codegen -version

generate:
	$(GO) generate $(GO_PACKAGES)

generate-check: generate
	git diff --exit-code

fmt:
	$(GO) fmt $(GO_PACKAGES)

fmt-check:
	@files="$$(gofmt -l $$(find . -name '*.go' -not -path './.git/*'))"; \
	if test -n "$$files"; then echo "Go files require formatting:"; echo "$$files"; exit 1; fi

lint:
	$(TOOLS_RUN) golangci-lint run ./...

vet:
	$(GO) vet $(GO_PACKAGES)

test:
	$(GO) test $(GO_PACKAGES)

test-race:
	$(GO) test -race $(GO_PACKAGES)

test-integration:
	$(GO) test -tags=integration $(GO_PACKAGES) -run 'Integration'

test-phase2:
	@test -n "$(TPTG_TEST_DATABASE_URL)" || \
		(echo "TPTG_TEST_DATABASE_URL is required for the Phase 2 evidence suite"; exit 1)
	$(GO) test -tags=integration ./internal/adapters/postgres -count=1 \
		-run 'Test(RepositoriesIntegrationTenantIsolationAndRollback|LogicalInvocationAcquisition(ReplayConflictAndRecovery|ConcurrencyProperties)Integration|AttemptClaimingConcurrencyAndFencingIntegration|ProtectedMutationIntegrationAtomicity|OutboxPublisherIntegration)'

test-phase3:
	$(GO) test ./internal/authn ./internal/adapters/thinkpixelag ./internal/app -count=1 \
		-run 'Test(Authentication|Authorization)Adversarial'

test-e2e:
	$(GO) test -tags=e2e $(GO_PACKAGES) -run 'E2E'

test-security:
	$(GO) test -tags=security $(GO_PACKAGES) -run 'Security|Secret|Redact|Tamper|Production'

test-mcp-conformance:
	$(GO) test -tags=mcp_conformance $(GO_PACKAGES) -run 'MCP|Conformance'

openapi-check:
	$(NPX) --yes @redocly/cli@$(REDOCLY_VERSION) lint api/openapi.yaml

migration-test:
	node scripts/validate-phase0.mjs
	$(GO) test -tags=integration ./internal/adapters/postgres/migrations -run 'Migration'

dependency-check:
	$(GO) mod verify
	node scripts/check-dependencies.mjs
	$(MAKE) license-check vuln-check

license-check:
	$(TOOLS_RUN) go-licenses check ./... --ignore=github.com/bdobrica/ThinkPixelTG \
		--allowed_licenses=Apache-2.0,BSD-2-Clause,BSD-3-Clause,ISC,MIT

vuln-check:
	$(GO) -C tools/govulncheck tool govulncheck -C ../.. ./...

build:
	mkdir -p $(BUILD_DIR)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags='-s -w' -o $(BUILD_DIR)/ ./cmd/...

image:
	$(DOCKER) build --tag $(IMAGE) .

container-smoke: image
	./scripts/container-smoke.sh $(IMAGE)

compose-up:
	$(COMPOSE) --file $(COMPOSE_FILE) up --detach --wait

compose-down:
	$(COMPOSE) --file $(COMPOSE_FILE) down --remove-orphans

compose-clean:
	$(COMPOSE) --file $(COMPOSE_FILE) down --volumes --remove-orphans

verify-phase0: openapi-check
	git diff HEAD --check
	node scripts/validate-phase0.mjs

verify: verify-phase0 fmt-check generate-check lint vet test test-race test-phase3 \
	test-integration test-e2e test-security test-mcp-conformance migration-test \
	dependency-check build

clean:
	rm -rf $(BUILD_DIR)
