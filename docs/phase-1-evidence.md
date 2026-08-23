# Phase 1 engineering-foundation evidence

Status: complete on 2026-08-23; clean-checkout gate verified at `66b783d`

Phase 1 established the buildable, observable, testable service foundation. This
record maps every checklist item to its isolated implementation commit and records
the exit-gate evidence used to close the phase.

| Item | Commit | Evidence |
|---|---|---|
| ENG-001 | `9c8c46e` | Go 1.27.0 module, command/core/adapter/test boundaries, import-boundary test |
| ENG-002 | `3770355` | dependency/source/license/vulnerability policy, isolated exact tool pins, exception/generated-file process |
| ENG-003 | `22dc3ef` | strict default/file/environment/flag configuration, production safety, secret-canary tests |
| ENG-004 | `2b21e3f` | correlated structured logging, stable events, bounded values, recursive redaction tests |
| ENG-005 | `9678cb6` | private Prometheus registry, no-op/local/OTLP tracing, W3C propagation, safe shutdown |
| ENG-006 | `76d19ec` | UUIDv7, UTC clock, checked quantities/integers, SHA-256, signed cursors, typed errors, fuzz tests |
| ENG-007 | `40a14dc` | bounded HTTP server/middleware, auth hook, RFC 7807, health/metrics, panic recovery and drain |
| ENG-008 | `4301db1` | pinned Make/tool contract and aggregate verification gate |
| ENG-009 | `6cac026` | digest-pinned PostgreSQL/Valkey Compose profiles and contract-shaped AG/GR fakes |
| ENG-010 | `28b8978` | SHA-pinned CI jobs for drift, analysis, race/fuzz, integration, security, contracts, build and container smoke |
| ENG-011 | `c452aef` | pinned multi-stage scratch image, static service, numeric non-root runtime and hardened smoke |
| ENG-012 | `66b783d` | pinned OpenAPI generation, committed deterministic adapter types and core import guard |
| ENG-013 | final evidence commit | this closure record, clean-clone verification, hardened image/SIGTERM proof |

## Clean-checkout verification

An isolated `git clone --no-hardlinks` of `66b783d` was created under the ignored
workspace temporary area. The clone began clean and the following completed with
exit code zero:

```sh
make verify
make container-smoke IMAGE=thinkpixeltg:phase1-final
```

`make verify` proved:

- Redocly OpenAPI lint and Phase 0 contract validation;
- no generated OpenAPI drift after `go generate ./...`;
- `gofmt`, pinned `golangci-lint`, `go vet`, unit and race tests;
- integration, end-to-end, security and MCP-conformance tagged gates;
- module checksum/source/license checks and pinned `govulncheck` with no findings;
- static builds for all command packages.

The three primitive fuzz targets also received focused local smoke campaigns with
more than 200,000 aggregate executions and no failures. The CI workflow parsed
cleanly with `actionlint` v1.7.7 and contains no floating `uses:` references.

## Container exit gate

The clean clone rebuilt the image from the digest-pinned Go 1.27.0 builder into a
`scratch` runtime. The smoke test verified numeric user/group `65532:65532`, a
read-only root filesystem, all Linux capabilities dropped, `no-new-privileges`, a
bounded `noexec,nosuid` tmpfs, no `/bin/sh`, successful `/livez`, and exit code
zero after Docker delivered SIGTERM with a ten-second drain bound.

The application content manifest was
`sha256:09713ff8d15d52a237b9d116b3f8329ec3b0d5cd5d1da0d9da372611e5566049`.
PostgreSQL, Valkey, the Go builder, and Dockerfile frontend inputs are all
digest-addressed in their respective Compose/Dockerfile contracts.

## Gate result

Phase 1 exits successfully. Phase 2 may build authoritative persistence and
invocation primitives on these package, configuration, telemetry, transport,
tooling, CI, local dependency, and container foundations.
