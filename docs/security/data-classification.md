# Data classification and redaction

Status: Phase 0 normative baseline

## Classes

| Class | Meaning | Examples | Default handling |
|---|---|---|---|
| C0 Public | intentionally public, reviewed | published tool name/schema, public health status | may be logged and returned |
| C1 Internal | operational metadata with low disclosure impact | opaque IDs, stable reason codes, versions, timing buckets | authenticated access; bounded telemetry |
| C2 Confidential | tenant or enterprise content/metadata | arguments, resource names, provider response content, policy constraints | least privilege; encrypt in transit/at rest; no ordinary logs |
| C3 Restricted | credentials or data that grants authority/high-impact secrets | bearer/refresh tokens, private keys, cookies, auth headers, secret plaintext | memory-only minimum lifetime; never caller-visible or durably persisted |

Classification is inherited by derived values unless an approved transformation
reduces sensitivity. A cryptographic digest of C2 data is at least C1 and may be
C2 when it enables dictionary inference. Opaque identifiers are not automatically
public. Unknown fields/content default to C2; credential-shaped values default C3.

## Surface rules

| Data category | Minimum class | Persistence and disclosure rule |
|---|---:|---|
| Inbound arguments | C2 | validate/normalize in bounded memory; persist canonical digest and required projection by default, not raw payload; never log wholesale |
| Downstream credentials and provider leases | C3 | typed secret container; resolve after controls; never send to AG/GR/caller; never DB/outbox/dead-letter/log/trace/metric; erase references promptly |
| Provider request/response bodies | C2, credential fields C3 | connector processes with byte limits; persist only approved identifiers/digests/safe metadata; redact before errors |
| Tool results | C2/open-world | post-tool evaluation before caller; bounded storage only if tool policy explicitly permits; label untrusted content |
| GR inputs | C2 minimized | only bounded context/arguments/results necessary for profile; exclude C3 and unnecessary identity/content |
| GR outputs | C2 | store decision code, evaluation ID, policy/profile/version, digests, and transform metadata; raw transformed content follows argument/result rule |
| Authorization decisions | C2 | store decision ID, outcome/reasons, policy/version, validity/revocation data, constraint digest and necessary structured constraints; never tokens |
| Approvals | C2 | store reference, exact bindings/digests, outcome, expiry/revocation/use state; no broad reusable authority or raw credential |
| Audit/evidence | C1/C2 | append-only metadata and digests by default; raw enterprise content requires explicit field-level policy and retention |
| Logs | C1 | stable event/reason codes and opaque correlation IDs; structural recursive redaction; no raw bodies/headers/query strings |
| Traces | C1 | bounded allowlisted attributes; no arguments/results/tokens; sampling must not weaken redaction |
| Metrics | C0/C1 | fixed low-cardinality labels; tenant/run/resource/tool-call IDs and content prohibited as labels |
| Database rows | C1/C2 | column-level allowlist, encryption and tenant isolation; C3 plaintext prohibited; TTL/retention per record type |
| Outbox/dead letter | C1/C2 | event schema references/digests; same classification and retention as source; dead-letter is not a raw-payload escape hatch |

## Structural redaction

Redaction happens before serialization and at every trust-boundary adapter. It is
not based only on string replacement. Types representing credentials must render
as `[REDACTED]` for string, formatting, JSON, error, and debug paths. Recursive
maps/headers redact case-insensitive names including `authorization`, `cookie`,
`set-cookie`, `token`, `access_token`, `refresh_token`, `client_secret`,
`private_key`, `password`, `api_key`, and connector-defined sensitive fields.

Values matching registered secret canaries are replaced even under unknown keys.
Error wrapping must preserve stable classifications, not provider bodies. URL
userinfo and sensitive query parameters are prohibited; URLs are recorded as
normalized scheme/host/operation identifiers, not full caller data.

## Data minimization and retention

- Collect a field only when a named control, reconciliation mechanism, accounting
  rule, or evidence query requires it.
- Prefer keyed or domain-separated digests for equality/correlation where raw
  content is unnecessary. Digest algorithms and domains are versioned.
- Raw C2 argument/result retention is disabled by default. Exceptions specify
  fields, purpose, access role, encryption, TTL, deletion, and incident impact.
- C3 is never placed in persistent queues, crash dumps, standard telemetry, test
  fixtures, snapshots, or support bundles.
- Test data uses synthetic values and secret canaries; live captured provider
  payloads are not committed.

## Output and failure behavior

Public errors use RFC 7807 with a stable code, safe title, opaque correlation ID,
and no provider payload. Redaction failure on a C3-capable path is a security
failure: suppress the questionable field/event and emit a separate safe alert.
Telemetry/evidence sink failure must not cause raw fallback logging.

## Access and deletion

Access is tenant-scoped and role-based; privileged evidence access is separate
from harness authority and itself audited. Retention/deletion jobs operate on
declared record classes and preserve legally/operationally required integrity
links. Deleting optional content must not delete the immutable fact, digest, or
classification of a consequential invocation.
