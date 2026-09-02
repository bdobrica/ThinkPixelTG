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

`GET /v1/tools/{tool_id}` requires one exact `version` query parameter and never
floats to a newer version. It applies the same tenant exposure and authorization
filter as list discovery before returning the trusted metadata projection.
Unknown, malformed, unexposed, and unauthorized tool-version keys all return the
same `404 tool_not_found` response without revealing which condition occurred.

`POST /v1/tool-calls` accepts only `tool_id`, optional exact `version`, and
`arguments`; governance identity, destinations, credentials, and retry overrides
are rejected. `Idempotency-Key` is required and is bound to the authenticated
tenant and run. The application resolves an exposed immutable version (or the
tenant's configured default once), schema-validates and canonically normalizes
arguments, derives the trusted resource projection, and atomically acquires the
logical invocation before requesting authorization. It durably records every
well-formed allow or deny decision before acting on it. Credentials are resolved
only after a recorded allow, and execution is possible only through the connector
port with canonical arguments and the trusted projection. A matching replay
returns the existing invocation; any tool-version or argument-digest mismatch is
`409 replay_conflict` and cannot reach authorization or execution.
Connector content remains undisclosed in `post_tool` until output-schema and
mandatory post-tool controls safely finalize it.

`GET /v1/tool-calls/{tool_call_id}` resolves only within the authenticated tenant
and run. Unknown, malformed, cross-run, and cross-tenant identifiers all return
the same `404 tool_call_not_found` response. Its stable projection contains only
the logical call identifier, exact tool/version, public state, safe terminal
code, timestamps, and—only for `succeeded`—the finalized `safe_result`. Internal
invocation IDs, resource projections and digests, authorization constraints,
connector or credential identifiers, downstream references, and raw provider
content are never returned.

Errors use `application/problem+json` per RFC 9457 with stable `code` and opaque
correlation ID. Details never include arguments, provider payloads, tokens, secret
references, policy internals, or existence across tenant boundaries. `202` is an
observable nonterminal state, not proof that a downstream action occurred.

The stable v1 codes and HTTP statuses are: `unauthenticated` (401),
`identity_provider_unavailable` (503),
`invalid_context` (401), `tool_not_found` (404), `tool_call_not_found` (404),
`invalid_arguments` (400), `replay_conflict` (409), `authorization_denied` (403),
`approval_required` (409), `approval_invalid` (403), `guardrail_blocked` (403),
`credential_unavailable` (503), `connector_error` (502),
`downstream_rejected` (502), `ambiguous_outcome` (409), `result_blocked` (403),
`rate_limited` (429), `budget_exhausted` (403), `service_unavailable` (503),
`not_ready` (503), and `internal` (500). An
unrecognized internal classification is always collapsed to `internal`; its
message and cause are never copied into the public problem document.

Default initial limits are documented in `docs/operations/slos-and-capacity.md`;
tool metadata may only narrow them. Contract-breaking changes require a new API
major path/profile and migration period. OpenAPI changes require lint, schema,
example, generated-type drift, and implementation conformance checks.
