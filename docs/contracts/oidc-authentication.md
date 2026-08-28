# OIDC token verification contract

Status: AUTH-001 through AUTH-003 implemented 2026-08-28

ThinkPixelTG accepts JWTs only from an explicit issuer profile. Each profile
defines one or more TG audiences, optional OAuth resource indicators, an
algorithm allowlist, clock skew, JWKS refresh/stale periods, and a maximum key
count. Production issuers and discovered JWKS endpoints require HTTPS.

Discovery uses the issuer's OpenID Provider Configuration endpoint. The returned
`issuer` must exactly equal the configured issuer. Redirects, oversized or
malformed responses, duplicate key IDs, unsupported key types, RSA keys below
2048 bits, and sets outside the configured key bound are rejected. Supported
profiles are `RS256`/RSA-2048+, `ES256`/P-256, and `EdDSA`/Ed25519; symmetric and
`none` algorithms are never accepted.

Signature verification precedes trusted use of claims. Verification requires an
exact configured issuer, an intersecting configured audience, an intersecting
resource when the profile requires resources, a current `exp`, and current `nbf`
and `iat` values when present. Numeric dates must be integral JSON numbers and
use only the configured clock skew.

## Rotation and outage behavior

JWKS entries are cached per issuer with an explicit maximum count. A missing key
ID or an expired refresh interval triggers synchronous discovery and JWKS
refresh, allowing normal issuer key rotation. A refresh replaces the complete
cached set atomically.

During issuer outage, a previously known key may verify tokens only until
`refresh_after + max_stale` after its successful retrieval. An unknown key never
uses stale fallback. Once the bound expires, verification returns the stable
`identity_provider_unavailable` classification. Invalid credentials return
`invalid_token`, and unconfigured issuers return `unsupported_issuer`; public
errors intentionally do not expose provider or cryptographic details.

## HTTP authentication and trusted principal

AUTH-002 adds a strict HTTP adapter around verification. Protected requests must
carry exactly one `Authorization: Bearer` field. Multiple/comma-joined values,
other schemes, malformed credentials, invalid claims, and ambiguous claim aliases
fail with a sanitized `401`. An IdP outage beyond the AUTH-001 cached-key bound
fails closed with a sanitized `503`. Explicit exact-path exemptions are available
only for intentionally public operational endpoints.

The adapter derives a typed context principal from verified claims. `iss`, `sub`,
and an unambiguous `tenant_id`/`tenant` are required. Optional `act`, `run`,
`agent`, `agent_version`, `workload_id`, and `azp` claims remain distinct; an
alias disagreement rejects the credential. Consumers use only the typed context
accessor and never raw headers or request-body identity fields as authority.

Caller-supplied `Forwarded`, `X-Forwarded-*`, and governance identity headers are
rejected at this trust boundary. Top-level JSON identity hints may be retained for
compatibility only when they exactly equal the authenticated principal; a conflict,
malformed value, or hint for an absent trusted dimension is rejected. The body is
restored byte-for-byte for downstream schema handling, and inbound bearer tokens
are neither placed in the principal nor forwarded downstream.

## Governed invocation context

AUTH-003 introduces a second, stricter boundary for protected application
operations. The governed context is derived exclusively from the private typed
authenticated-principal context. It requires tenant, subject, agent, agent
version, and run identity; actor identity remains optional delegation context.
If any required dimension is absent or malformed, derivation fails closed.

Request bodies, tool arguments, query parameters, and caller-controlled headers
are not inputs to governed-context derivation. They may contain application data
or matching compatibility hints, but cannot create or replace authority. The
governed type intentionally omits bearer credentials, raw claims, and workload
identity; workload provenance is established independently by AUTH-004.
