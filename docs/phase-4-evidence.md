# Phase 4 tool API evidence

Status: complete on 2026-09-03

Phase 4 establishes immutable tool publication, tenant-scoped deterministic
discovery, canonical REST invocation and status APIs, strict public limits and
errors, privileged administration separation, and a hermetic mock connector.

## Governed invocation exit gate

The dedicated real-PostgreSQL gate is:

```sh
TPTG_TEST_DATABASE_URL='postgres://…' make test-phase4-e2e
```

`TestPhase4GovernedMockInvocationE2E` sends the same authenticated HTTP tool call
twice through the canonical application service. It proves:

| Required property | Executable evidence |
|---|---|
| Authentication | A verified bearer principal and independently resolved workload identity supply tenant, subject, agent/version, run, and workload context; the body supplies none of them. |
| Authorization | The complete governed request and trusted resource projection reach the authorization port before credential or connector access. |
| Persistence | The PostgreSQL ledger durably binds one tenant/run/tool-call tuple to its exact tool version and canonical argument/resource digests and records one authorization decision. |
| Execution | The hermetic mock write connector receives canonical arguments once, and its credential capability is released after execution. |
| Evidence and outbox | Authorization persistence atomically creates one minimized `tg.evidence/v1alpha1` audit event and its matching outbox message. |
| Idempotent replay | The second identical request returns the existing invocation with HTTP `200`; it creates no second decision, audit event, outbox message, credential lease, or connector call. |

## Supporting verification

The Phase 4 conformance gate remains `make test-phase4`. It covers OpenAPI
responses, tenant isolation, replay conflict, malformed arguments, timeouts,
cancellation, and deterministic discovery. The repository formatting, generated
artifact, lint, vet, unit, race, OpenAPI, migration, dependency, and build gates
provide the broader release evidence.

## Gate result

The end-to-end test passed against the digest-pinned PostgreSQL 18.6 Compose
service. Phase 4 exits with one governed logical invocation and one evidence
publication identity across transport replay; Phase 5 may add production
credential providers and the first real connector without changing the canonical
application path.
