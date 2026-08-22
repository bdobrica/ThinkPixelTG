# MCP compatibility baseline

Status: Phase 0 normative baseline

ThinkPixelTG pins MCP protocol revision `2025-11-25`. The initial remote transport
is Streamable HTTP; legacy HTTP+SSE is not supported. stdio is deferred until a
target harness requires it and can provide authenticated local workload/run context.
Protocol upgrades are explicit contract changes with conformance evidence, not an
automatic SDK dependency update.

## Mapping

`tools/list` maps the authenticated tenant's authorization-filtered TG catalog in
stable `(tool_id, version)` order. MCP name maps deterministically to exact TG
identity/version; descriptions and input/output schemas come from immutable
reviewed metadata. Compatibility annotations are derived from TG risk,
side-effect, approval, retry, and open-world fields. They are never consumed as
authorization, credential, approval, resource, destination, or idempotency facts.

`tools/call` validates the MCP envelope then invokes the same canonical application
service as `POST /v1/tool-calls`. It supplies/derives an explicit stable logical
`tool_call_id` under the negotiated TG extension. A JSON-RPC request ID identifies
one transport exchange and MUST NOT be treated as the logical idempotency key.
Absent stable call identity on a side-effecting tool is rejected.

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
- Session IDs are cryptographically random, authenticated-context bound, expiring,
  rotation-capable, replay limited, and never authorization by themselves.
- Enforce protocol-version negotiation, content types, request/response byte and
  method limits, per-session/global concurrency, deadlines, cancellation, and
  bounded event streams. Reject stale sessions and cross-context reuse.
- DNS rebinding/Host and proxy-forwarded-header trust are controlled by deployment
  allowlists; do not infer public origin from arbitrary forwarded headers.

Conformance covers initialization/version negotiation, list/schema fidelity,
calls/replay, cancellation, sessions, Origin/Host/auth failures, malformed JSON-RPC,
size/concurrency limits, approval/ambiguity mapping, and credential canaries.
