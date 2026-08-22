# Initial threat model

Status: Phase 0 normative baseline

## Scope and assets

The scope is the complete governed tool-call path from authenticated ingress to
downstream execution, result return, persistence, and evidence publication.
Critical assets are governance identities, policy/approval integrity, downstream
credentials, immutable tool semantics, enterprise data, side-effect uniqueness,
authoritative invocation history, usage accounting, and administrator authority.

Assume the model, harness, MCP client, prompts, files, retrieved data, connector
responses, and ordinary request fields are hostile. AG, GR, credential providers,
PostgreSQL, and configured connectors are trusted only for their documented roles
and may be unavailable, stale, malformed, or compromised.

## Security objectives

- No caller can manufacture tenant, subject, agent/version, run, or workload authority.
- No side effect occurs without the exact current authorization and approval required.
- Downstream credentials never cross the harness, policy, telemetry, or durable-data boundary.
- Caller input cannot select a connector destination or secret.
- A logical call is replayed only under its published retry contract; uncertainty is visible.
- Evidence is sufficient to reconstruct decisions without retaining avoidable secrets/content.
- Privileged actions are separate, least-privileged, and append-only audited.

## Threat register

| ID | Threat / attack | Required controls | Verification |
|---|---|---|---|
| TM-01 | Forged subject, workload, agent, run, or tenant identity | Verify issuer/audience/signature and workload channel; derive governed context from trusted material; reject conflicting hints | bad-token and cross-tenant substitution tests |
| TM-02 | Tenant/run confusion through identifier swapping or enumeration | tenant-scoped keys/repositories, `(tenant, run, tool_call_id)` uniqueness, enumeration-safe responses, AG correlation validation | adversarial isolation and replay tests |
| TM-03 | Confused deputy / token audience confusion | TG inbound tokens authenticate only to TG; validate audience/resource; obtain a separately scoped downstream capability | audience tests and outbound-header canary |
| TM-04 | OAuth token passthrough | prohibit forwarding inbound authorization; connector accepts typed credential capability only | connector conformance and secret-canary tests |
| TM-05 | Credential exfiltration in results/errors/logs/traces/GR/evidence | structural sensitive types, centralized recursive redaction, bounded metadata-only persistence, no raw request dumps | repository-wide canary and panic-path tests |
| TM-06 | Approval TOCTOU or argument substitution | approve final canonical digest/resource after transformations; bind run/tool/version/call; single use; reauthorize immediately before execution | mutation, expiry, replay, and concurrency tests |
| TM-07 | Replayed request creates duplicate side effects | stable logical ID, digest conflict detection, immutable retry class, fencing, downstream idempotency/reconciliation | crash-point and concurrent replay tests |
| TM-08 | Blind retry after unknown commit | distinguish pre-send/rejected/committed/unknown; persist `ambiguous`; reconcile before retry or require manual review | timeout-after-send fault injection |
| TM-09 | SSRF, redirect escape, or caller-selected destination | admin-controlled destinations, scheme/host/port allowlists, bounded redirects, DNS/IP checks, TLS verification, no arbitrary URL arguments | redirect, private-IP, rebinding, DNS tests |
| TM-10 | Stale authorization or revocation | bounded TTL, decision-key digest, revocation epoch/checkpoint, live checks for high-risk writes, fail closed | stale-cache and AG-outage tests |
| TM-11 | MCP Origin/session/request-ID attacks | pinned revision, strict Origin allowlist, authenticated bounded sessions, expiry/cancellation, body limits; logical ID separate from JSON-RPC ID | conformance, replay, cross-origin tests |
| TM-12 | Prompt injection in downstream results | label open-world content; bounded `post_tool` GR; schema validate transform; never interpret result as policy or credentials | malicious-result and transform tests |
| TM-13 | Poisoned tool description or annotation | descriptions reviewed/versioned; annotations derived from trusted metadata and never authorize | catalog publication tests |
| TM-14 | Evidence leakage or tampering | metadata/digests by default, transactional append/outbox, stable event IDs, integrity/authenticated sink, restricted access | canary, atomicity, and replay tests |
| TM-15 | Privileged administrator compromise | separate admin identity/policy, least privilege, two-person/approval where configured, immutable versions, auditable emergency/manual actions | role-separation and admin audit tests |
| TM-16 | Resource exhaustion via payload, schema, concurrency, latency, or output | strict byte/depth/schema/time/concurrency limits, backpressure, bounded streaming/buffers, rate limits | limit/load/fuzz tests |
| TM-17 | Connector/dependency compromise | compiled reviewed registry, pinned dependencies, narrow egress and credentials, SBOM/scans, signed build inputs | supply-chain gates and egress tests |
| TM-18 | Database/cache race or stale worker finalizes execution | transactional state/evidence, optimistic versions, leases and monotonic fencing; Valkey never authoritative | real-PostgreSQL concurrency tests |
| TM-19 | Malformed AG/GR/provider response broadens authority | strict typed decoding, reject unknown/invalid security fields, narrowing-only constraints, fail closed | mutation/fuzz tests |
| TM-20 | Error or metric cardinality leaks enterprise data | stable reason codes, bounded labels, digests/opaque IDs, sanitized RFC 7807 details | telemetry snapshot tests |

## Abuse cases and invariants

- If a caller supplies `tenant_id`, `run_id`, `agent_id`, an auth header for the
  destination, a secret name, or a URL, TG treats it as non-authoritative and
  rejects it when it conflicts with or attempts to influence trusted selection.
- If GR transforms arguments affecting resource, action, or risk, prior digest,
  authorization, and approval become unusable.
- If the connector may have sent a write but cannot prove the result, TG must not
  automatically retry unless reconciliation or native idempotency proves safety.
- If AG/GR/provider responses are malformed or required dependencies time out,
  no protected side effect occurs.
- If evidence publication fails, authoritative committed evidence remains queued;
  operators cannot silently edit history to hide an event.

## Residual risks

Enterprise providers can misreport completion, administrators can misuse valid
authority, and qualified `at_least_once_accepted` tools may duplicate by design.
Each shipped write operation must document these residual risks, require explicit
publication review, expose ambiguity, and provide operational reconciliation.

This model must be reviewed at every phase gate and whenever identity, protocol,
credential, connector, storage, or deployment boundaries change.
