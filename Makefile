NPX ?= npx
REDOCLY_VERSION ?= 2.46.2

.PHONY: verify verify-phase0 openapi-check

verify: verify-phase0

verify-phase0: openapi-check
	git diff HEAD --check
	node scripts/validate-phase0.mjs

openapi-check:
	$(NPX) --yes @redocly/cli@$(REDOCLY_VERSION) lint api/openapi.yaml
