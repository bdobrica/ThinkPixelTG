# Dependency and generated-file policy

ThinkPixelTG prefers the Go standard library. A direct runtime dependency needs a
named owner and purpose, a maintained upstream, a source allowed by
`dependency-policy.yaml`, a compatible license, checksums in `go.sum`, and a
security/reachability review. Provider SDKs remain behind ports and adapters.

Build tools are isolated in `tools/go.mod` and pinned to exact module versions.
Production code must never import that module. Updates are reviewed like runtime
dependencies and must pass the complete verification gate.

## Direct dependency register

| Modules | Owner | Purpose and boundary |
|---|---|---|
| `github.com/prometheus/client_golang` | observability maintainers | Prometheus registry and exposition, confined to `internal/telemetry` and HTTP adapter wiring |
| `go.opentelemetry.io/otel`, `otel/trace`, `otel/sdk` | observability maintainers | tracing API/SDK, confined to telemetry bootstrap and application-safe trace interfaces |
| `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp` | observability maintainers | OTLP/HTTP export adapter, confined to `internal/telemetry` |
| `go.opentelemetry.io/otel/exporters/stdout/stdouttrace` | observability maintainers | local-development trace sink, confined to `internal/telemetry` |

## Required checks

- `go mod verify` authenticates downloaded production modules.
- `go list -m -json all` is checked against the approved source hosts.
- `go-licenses check` (excluding the unpublished main module) rejects
  unapproved or forbidden third-party dependency licenses.
- `cd tools && go tool govulncheck -C .. ./...` performs call-graph-aware
  vulnerability scanning with the repository-pinned scanner.
- committed `go.mod`, `go.sum`, `tools/go.mod`, and `tools/go.sum` changes are
  reviewed together with the code that needs them.

CI and `make dependency-check` run these checks. A critical or high reachable
finding blocks release unless an active exception explicitly covers it.

## Generated files

Committed generated files begin with the standard `Code generated ... DO NOT
EDIT.` marker, identify their generator in adjacent documentation, and are
reproducible through `make generate`. Generated output must not contain secrets,
environment-specific paths, timestamps, or unordered content. `make generate`
followed by a clean-tree check is the drift gate. Reviewers inspect source and
generated changes together; hand edits to generated files are rejected.

## Exceptions

Consequential exceptions are recorded in
`docs/development/dependency-exceptions.yaml`. Each entry requires the module or
finding, owner, justification and reachability, compensating controls, approval,
creation and expiry dates, and removal issue. Expired or incomplete exceptions
fail the gate. Renewals require a fresh review; an exception never silently
becomes policy.
