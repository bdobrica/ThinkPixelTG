# Phase 5 connector and credential evidence

Status: complete on 2026-09-03

Phase 5 establishes trusted credential selection, short-lived redacted
capabilities, development and Kubernetes projected-token providers, bounded
in-memory caching, administrator-controlled connector routing, secured downstream
HTTP, and qualified GitHub read/write operations.

## Exit-gate evidence

| Required property | Executable or durable evidence |
|---|---|
| Authorization precedes credential access | Application and GitHub qualification denial tests use broker and connector spies; a denied call records no credential resolution and reaches no connector. |
| Credentials do not cross the harness boundary | Capability formatting/serialization tests and secret-canary coverage inspect public HTTP/application errors and results, logs, traces, metrics, audit/outbox data, GR requests, database rows, and panic handling. |
| Credentials remain short lived | Capability tests cover expiry, callback-scoped access, release, and redaction; cache tests cover skewed expiry, trusted keys, singleflight, eviction, rotation/revocation, and plaintext-free state boundaries. |
| Production provider is constrained | Kubernetes provider tests cover exact path allowlists, issuer/audience and JWT lifetime validation, bounded reads, cancellation, rotation, safe errors, and production configuration. [ADR-0003](adr/0003-kubernetes-projected-credential-provider.md) records the selected profile. |
| Connector routing is trusted | Registry tests prove tenant and immutable tool metadata select enabled administrator-owned instances and compiled operations; arbitrary argument-selected connector destinations are rejected. |
| Downstream HTTP is bounded | Conformance tests cover scheme/host/address rules, TLS-preserving redirect rejection, safe headers, deadlines/cancellation, response limits, rate limiting, telemetry, and authorization-header cleanup. |
| Reference read/write flow works | The real `github.pull.get` and `github.pull.comment` adapters run against an isolated provider double using a synthetic in-memory canary. Read and write succeed without exposing it. |
| Consequential writes remain governed | Qualification captures the application-assigned invocation ID at the authorization ledger and connector boundaries and requires equality. The non-idempotent write's ambiguity and no-blind-retry posture are documented in the [qualification record](connectors/github-pull-comment-qualification.md). |
| Evidence is minimized | Connector tests accept only bounded provider identifiers/resource versions and safe metadata; provider bodies, arbitrary headers, credentials, and full payloads are not retained. |

## Verification

The focused Phase 5 packages were exercised with the race detector throughout
the phase. The completion gate reran:

```sh
go test -race -count=1 ./internal/adapters/connectors/github ./internal/connectors/downstreamhttp ./internal/credentials ./internal/adapters/credentials/development ./internal/adapters/credentials/kubernetes
go test -count=1 ./...
go vet ./...
make lint
make fmt-check
```

The GitHub qualification is isolated: it makes no live GitHub request and uses
no live provider token. Repository evidence contains descriptions, bounded safe
identifiers, and synthetic test data only; it does not contain captured request
or response payloads.

## Gate result

Phase 5 exits with replaceable credential-provider and connector adapters behind
stable application ports. A governed GitHub read/write flow proves that denial
prevents credential resolution, allowed execution correlates to its logical
invocation, and credential material remains inside the provider/connector call.
Phase 6 may add ThinkPixelGR enforcement before this credential boundary without
changing connector or provider ownership.
