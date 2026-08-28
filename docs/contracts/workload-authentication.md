# Workload authentication contract

Status: AUTH-004 implemented 2026-08-28

Workload identity records which ThinkPixelTG client process or pod established
the connection. It is authenticated independently from bearer-token authority
and is stored in a separate typed context value. It cannot supply or replace
tenant, subject, actor, agent/version, or run identity.

Local development may use an explicitly configured, non-empty static identity.
This source is intended only for isolated development and tests; it does not
infer identity from headers, request bodies, process environment, or token
claims.

The production-oriented source accepts a SPIFFE ID from the URI SAN of the leaf
client certificate only when the HTTP TLS stack reports a non-empty verified
chain. Exactly one URI SAN is required, its scheme must be `spiffe`, and its
trust domain must match an explicit allowlist. Unverified peer certificates,
multiple identities, malformed IDs, empty chains, disagreeing verified leaves,
and unconfigured trust domains fail closed.

Deployments terminate client mTLS at the TG process when using this source. A
proxy-provided certificate or identity header is not equivalent to a verified
TLS connection and remains rejected by the HTTP authentication boundary.
