# Canonical REST API rules

The normative Phase 0 draft is `api/openapi.yaml` using OpenAPI 3.1.2 and JSON
Schema 2020-12 semantics. The canonical API is the only application execution
path; MCP translates into it.

All protected endpoints require TLS and an audience-bound TG credential. Tenant,
subject, agent/version, run, and workload context is derived from verified
authentication/trusted lookup, never request bodies. Administration uses a
separate audience, authorization path, and evidence trail.

Requests are JSON-only with strict unknown-field rejection, body/header/deadline
limits, and cancellation. `Idempotency-Key` is the logical `tool_call_id` scoped by
trusted tenant/run. Pagination cursors are opaque, integrity protected, bounded,
query-bound, and expiring. List ordering is stable and errors are enumeration safe.

`GET /v1/tools` derives its complete discovery context from authenticated caller
and workload identity, authorization-filters the tenant exposure set before
pagination, and orders results by ascending `(tool_id, version)` UTF-8 byte value.
The default page size is 50 and the maximum is 100. Continuation cursors bind the
governed identity and effective page size, and a malformed, expired, tampered, or
rebound cursor returns `invalid_arguments` without exposing catalog contents.

Errors use `application/problem+json` per RFC 9457 with stable `code` and opaque
correlation ID. Details never include arguments, provider payloads, tokens, secret
references, policy internals, or existence across tenant boundaries. `202` is an
observable nonterminal state, not proof that a downstream action occurred.

Default initial limits are documented in `docs/operations/slos-and-capacity.md`;
tool metadata may only narrow them. Contract-breaking changes require a new API
major path/profile and migration period. OpenAPI changes require lint, schema,
example, generated-type drift, and implementation conformance checks.
