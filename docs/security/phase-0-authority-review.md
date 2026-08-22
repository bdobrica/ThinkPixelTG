# Phase 0 authority review

Review date: 2026-08-22

Scope: GOV-001 through GOV-020 artifacts reviewed against `PLAN.md`, especially
product boundary, core security model, trust boundaries, domain model, integration
contracts, persistence, evidence, and Phase 0 exit criteria.

## Review method

For each security-relevant decision, the review identified exactly one decision
source, TG enforcement point, persisted evidence, forbidden substitutes, and
failure posture. Cross-document terms were checked against the glossary; execution
ordering was checked against the context flow and state machine; persistence was
checked for a tenant-scoped record and atomic evidence boundary.

## Authority closure matrix

| Concern | Decision source | TG enforcement/evidence | Explicitly non-authoritative | Failure posture |
|---|---|---|---|---|
| Tenant, subject, agent/version, run | verified IdP context plus AG governed context | ingress/context resolver; context and auth events | ordinary headers/body, tool args, MCP session | reject/fail closed |
| Workload identity | configured workload identity verifier | authenticated request context/evidence | end-user identity, forwarded header, body | reject/fail closed |
| Tool semantics/version | immutable published TG version | catalog resolution and persisted exact version | descriptions, MCP annotations, connector guesses, caller version defaults | unknown/unexposed/disabled rejects |
| Authorization | authenticated current AG decision | strict adapter, narrowing intersection, persisted decision | GR, caller, connector/provider, local broadening | deny/fail closed |
| Approval | AG action-scoped approval | exact final digest/resource/call binding, fenced single use, lifecycle evidence | UI/client confirmation, stale or reusable grant | block/fail closed |
| GR safety/data policy | authenticated GR response under immutable profile | ordered pre/post enforcement and evaluation evidence | authorization grant, credential/destination choice | mandatory fails closed; observational explicit |
| Argument identity | TG `tg-cjson-v1` after schema validation | domain-separated digest and fixtures | raw spelling/order, transport ID | reject malformed/ambiguous values |
| Resource projection | immutable tool-version projection over validated canonical args | digest passed to AG/approval/evidence | caller resource claim or connector inference | missing/ambiguous rejects |
| Connector/destination | compiled connector plus admin connector instance | registry/egress enforcement and config digest | URL/host/redirect/proxy from caller | reject/fail closed |
| Credential selection/material | admin binding; provider resolves scoped capability | post-control broker, typed secret, binding-only evidence | caller secret/provider/scope/header; inbound token | fail closed/no fallback |
| Retry/replay safety | immutable tool-version class plus qualified provider proof | logical invocation, attempt fence, ambiguity/reconciliation ledger | MCP/connector annotations, error guess, new request ID | unknown becomes ambiguous |
| Execution completion | connector/provider evidence classified by TG contract | fenced attempt/result/reconciliation records | HTTP status assumption or stale worker | ambiguous/manual review if unproven |
| Authoritative state | PostgreSQL transaction | tenant keys, optimistic state, audit/outbox atomicity | Valkey, evidence sink, worker memory | no authority from degraded cache/sink |
| Evidence/usage | TG at state/accounting source | stable IDs, transactional audit/usage/outbox | caller/provider claim alone, mutable log | retain queue; safe alert, no raw fallback |
| Administration/manual review | separate privileged identity and policy | separate API/transaction and append-only evidence | harness invocation token, direct unaudited mutation | deny/fail closed |

## Cross-contract invariants verified

1. The context flow, state machine, AG, GR, approval, credential, connector, retry,
   SQL, and evidence contracts all place credential resolution after mandatory
   validation/authorization/GR/approval and immediately-current rechecks.
2. A security-relevant pre-tool transformation invalidates digest, resource,
   authorization, and approval consistently across all affected contracts.
3. Unknown post-send outcome maps to `ambiguous` in state, connector, retry,
   persistence, REST/MCP representation, evidence, SLO/backlog, and threat model.
4. Exact tool version and trusted retry/resource/destination/binding metadata are
   immutable; disable/revocation is rechecked without rewriting history.
5. Tenant is included in logical invocation identity, storage keys, discovery,
   credential binding, evidence, and administrative scope.
6. Caller/MCP annotations, transport IDs, body identities, inbound tokens, URLs,
   and secret references are explicitly denied authority in every relevant layer.
7. PostgreSQL transactions atomically couple protected changes to audit/outbox;
   Valkey and evidence sinks cannot authorize or replace authoritative state.
8. C3 credentials are prohibited from AG/GR, API/MCP, storage, evidence/outbox,
   telemetry, provider errors, and test fixtures; C2 content is minimized/digested.

## Findings and disposition

- AR-001, resolved in GOV-008: canonical numeric interoperability is restricted to
  JCS/I-JSON-safe values; larger exact integers require schema strings.
- AR-002, resolved in GOV-007/GOV-009: cancellation after send cannot create a
  clean `cancelled`/`failed` state without proof; it becomes ambiguous.
- AR-003, resolved in GOV-006/GOV-011: approval does not override an emergency
  disabled tool or fresh authorization/revocation check.
- AR-004, resolved in GOV-015/GOV-016: transport request IDs are distinct from the
  logical call ID in both REST and MCP.
- AR-005, resolved in GOV-017/GOV-018: network calls are outside DB transactions;
  attempt fences and ambiguity bridge crash windows while evidence remains atomic.

No open security-relevant authority ambiguity remains at the Phase 0 design level.
Implementation choices that could change an owner or failure posture require a new
ADR/contract update and phase-gate review. Phase 0 completion does not claim that
these controls are implemented; subsequent phases must prove conformance.
