# OIDC token verification contract

Status: AUTH-001 implemented 2026-08-28

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

Bearer extraction, protected-route middleware, principal context construction,
and rejection of forged identity headers belong to AUTH-002.
