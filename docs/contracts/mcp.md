# MCP compatibility baseline

Status: Phase 0 normative baseline

ThinkPixelTG pins MCP protocol revision `2026-07-28`. The initial remote transport
is stateless Streamable HTTP; legacy HTTP+SSE is not supported. stdio is deferred
until a target harness requires it and can provide authenticated local workload/run context.
Protocol upgrades are explicit contract changes with conformance evidence, not an
automatic SDK dependency update.

The pinned revision removes the `initialize`/`initialized` handshake and
`Mcp-Session-Id`. Each request is self-contained and carries protocol version,
client identity, and capabilities in the revision-defined headers and `_meta`.
`server/discover` is optional. TG MUST NOT recreate hidden protocol session state.

## Mapping

`tools/list` maps the authenticated tenant's authorization-filtered TG catalog in
stable `(tool_id, version)` order. MCP name maps deterministically to exact TG
identity/version; descriptions and input/output schemas come from immutable
reviewed metadata. Compatibility annotations are derived from TG risk,
side-effect, approval, retry, and open-world fields. They are never consumed as
authorization, credential, approval, resource, destination, or idempotency facts.
List responses supply revision-defined `ttlMs` and `cacheScope`; cache scope is
derived from authenticated tenant/principal visibility and never broadens access.

`tools/call` validates the MCP envelope then invokes the same canonical application
service as `POST /v1/tool-calls`. It supplies/derives an explicit stable logical
`tool_call_id` under the negotiated TG extension. A JSON-RPC request ID identifies
one transport exchange and MUST NOT be treated as the logical idempotency key.
Absent stable call identity on a side-effecting tool is rejected.

Streamable HTTP requests require `MCP-Protocol-Version`, `Mcp-Method`, and, where
defined by the revision, `Mcp-Name`. TG validates that routing headers match the
JSON-RPC body before authentication/authorization routing; headers are not security
authority and cannot override the body or immutable catalog.

TG states map deterministically: waiting approval and in-progress return safe
structured status; terminal results use MCP content/structured content after GR;
typed failures map to bounded MCP errors; ambiguity is explicit and never mapped
to a clean retryable failure. Credentials and internal provider details are absent.

## Authentication and transport posture

- HTTPS only; bearer/resource authorization is audience-bound to TG and never
  forwarded downstream. Protected-resource metadata follows the pinned revision.
- Validate `Origin` against an exact configured allowlist for browser-capable
  clients; absent/invalid Origin follows explicit deployment policy and defaults
  deny for remote browser contexts. Bind locally only to loopback when local.
- Validate RFC 9207 authorization issuer responses where applicable, bind client
  credentials to their issuer, and prefer Client ID Metadata Documents over the
  revision-deprecated Dynamic Client Registration.
- Enforce per-request protocol version/client identity/capability metadata, content
  types, request/response byte and method limits, per-principal/global concurrency,
  deadlines, cancellation, and bounded optional subscription streams. Reject
  conflicting header/body routing and cross-context replay.
- Multi Round-Trip Request `requestState` is opaque continuation data, not
  authentication, authorization, approval, or logical idempotency identity. TG
  platform approval remains action-scoped through ThinkPixelAG.
- DNS rebinding/Host and proxy-forwarded-header trust are controlled by deployment
  allowlists; do not infer public origin from arbitrary forwarded headers.

Conformance covers stateless per-request version/capability metadata, optional
`server/discover`, routing-header/body agreement, list schema/order/cache hints,
calls/replay, cancellation, Origin/Host/auth failures, malformed JSON-RPC,
cross-context continuation replay, size/concurrency limits, approval/ambiguity
mapping, and credential canaries.
