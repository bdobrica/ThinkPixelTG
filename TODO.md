# ThinkPixelTG Release-Candidate TODO

This is the chronological implementation checklist for ThinkPixelTG.

Execute the first unchecked item whose dependencies are complete. An item is checked only after its acceptance evidence passes. Follow the coding-agent and commit protocol in `PLAN.md` after every completed implementation item.

Status notation:

- `[ ]` pending;
- `[x]` implemented and verified.

When checking an item, append completion metadata in this form:

```text
— completed YYYY-MM-DD, commit <sha>, evidence: <commands/artifacts>
```

A checked item means the implementation, tests, migrations/contracts, security handling, telemetry, and documentation required by that item are all complete. Partial implementation stays unchecked.

Unless an item states otherwise, dependencies are the preceding items in the same phase plus the prior phase gate. Do not reorder work merely to make a visible feature land sooner if doing so bypasses an identity, authorization, credential, idempotency, approval, or evidence prerequisite.

---

## Phase 0 — Decisions, threat model, and contracts

- [x] GOV-001 Create `docs/`, `docs/adr/`, `docs/contracts/`, `docs/security/`, `docs/operations/`, and `docs/architecture/`; add an ADR template covering status, context, decision, alternatives, consequences, security, operations, compatibility, and references. — completed 2026-08-22, commit 21eba26, evidence: `git diff --check`; `docs/adr/0000-template.md`; documentation directory indexes
- [x] GOV-002 Write the system-context and trust-boundary document showing harnesses, ThinkPixelTG, ThinkPixelAG, ThinkPixelGR, ThinkPixelLLMGW, PostgreSQL, optional Valkey, credential providers, evidence sinks, connector destinations, operators, and downstream enterprise systems. — completed 2026-08-22, commit e7f2b2b, evidence: `docs/architecture/system-context.md`; `git diff --check`
- [x] GOV-003 Produce the initial threat model covering identity spoofing, tenant/run confusion, confused deputy behavior, credential exfiltration, token passthrough, approval TOCTOU, replay, duplicate side effects, SSRF, stale authorization/revocation, MCP origin/routing/continuation attacks, result prompt injection, evidence leakage/tampering, and privileged-admin compromise. — completed 2026-08-22, commit b5fad60, evidence: `docs/security/threat-model.md`; threat register TM-01 through TM-20; MCP revision update commit 58a5978
- [x] GOV-004 Define data-classification and redaction rules for inbound arguments, downstream credentials, provider responses, tool results, GR inputs/outputs, authorization decisions, approvals, evidence, logs, traces, metrics, database rows, and dead-letter/outbox payloads. — completed 2026-08-22, commit 7d6e8af, evidence: `docs/security/data-classification.md`; surface classification matrix
- [x] GOV-005 Define the glossary and authoritative ownership matrix for subject principal, agent identity/version, run identity, workload identity, tenant, tool, tool version, connector, credential binding, invocation, attempt, approval, decision, evidence event, and trusted usage event. — completed 2026-08-22, commit e964f2a, evidence: `docs/architecture/glossary-and-ownership.md`; authority matrix
- [x] GOV-006 Formalize the tool identity/version model, immutable publication rules, trusted risk metadata, connector-operation binding, resource projection, exposure rules, and retirement/disable semantics. — completed 2026-08-22, commit 36749c4, evidence: `docs/contracts/tool-catalog.md`
- [x] GOV-007 Formalize the invocation state machine, terminal/non-terminal states, allowed actors, legal transitions, fencing/claim semantics, approval wait, GR block, retry, reconciliation, ambiguity, manual review, success, and failure semantics. — completed 2026-08-22, commit 31a4b77, evidence: `docs/contracts/invocation-state-machine.md`; transition outline and actor invariants
- [x] GOV-008 Define canonical JSON normalization, canonical argument digesting, schema-validation ordering, resource projection, security-relevant transformation rules, and cross-language deterministic test vectors. — completed 2026-08-22, commit 6afffea, evidence: `docs/contracts/canonical-json.md`; `python3 -m json.tool docs/contracts/testdata/canonical-json-v1.json`; SHA-256 fixture verification
- [x] GOV-009 Define the retry/idempotency classification contract for `safe`, `downstream_idempotency`, `gateway_deduplicated`, `reconcile_before_retry`, `at_least_once_accepted`, and `non_retryable`, including publication requirements and ambiguity handling. — completed 2026-08-22, commit da1d7ec, evidence: `docs/contracts/retry-idempotency.md`
- [x] GOV-010 Define the typed ThinkPixelAG authorization request/response contract, reason codes, constraint-narrowing semantics, freshness/TTL behavior, revocation interaction, malformed-decision handling, and fail-closed posture. — completed 2026-08-22, commit ebdba97, evidence: `docs/contracts/thinkpixelag-authorization.md`
- [x] GOV-011 Define the action-scoped ThinkPixelAG approval contract, including exact run/tool/version binding, final canonical argument digest, resource projection, expiry, single-use logical-action semantics, revocation, reauthorization, and evidence references. — completed 2026-08-22, commit 8795808, evidence: `docs/contracts/thinkpixelag-approval.md`
- [x] GOV-012 Define the ThinkPixelGR `pre_tool`/`post_tool` contract, allowed decisions/transforms, mandatory versus observational profiles, failure behavior, evidence correlation, result trust labeling, and prohibition on credential exposure to GR. — completed 2026-08-22, commit ab989d6, evidence: `docs/contracts/thinkpixelgr.md`
- [x] GOV-013 Define the credential-provider and credential-binding contracts, including trusted selection, capability types, lease/expiry, refresh, revocation, cache limits, structural redaction, and explicit prohibition of caller-selected secret names or raw credential passthrough. — completed 2026-08-22, commit eef547c, evidence: `docs/contracts/credential-provider.md`
- [x] GOV-014 Define the connector contract, connector-result/error taxonomy, deadline/cancellation behavior, egress restrictions, idempotency evidence, reconciliation interface, and required conformance tests. — completed 2026-08-22, commit 401b983, evidence: `docs/contracts/connector.md`
- [x] GOV-015 Draft the canonical OpenAPI 3.1 contract for discovery, invocation, invocation lookup, typed errors/RFC 7807, pagination, request limits, authentication, administrative publication operations, and operational endpoints. — completed 2026-08-22, commit f0a34db, evidence: `api/openapi.yaml`; `docs/contracts/rest-api.md`; required-path structural check
- [x] GOV-016 Select and document the supported MCP revision and transport baseline; define `tools/list`/`tools/call` mapping, annotation derivation, authentication/origin/per-request posture, and the rule that MCP request IDs do not automatically equal logical `tool_call_id`. — completed 2026-08-22, commit e842e69, updated commit 58a5978, evidence: `docs/contracts/mcp.md`; official MCP revision 2026-07-28 specification and migration changes
- [x] GOV-017 Draft the PostgreSQL schema and transaction boundaries for tool catalog, connector instances, credential bindings, invocations, attempts, decisions, approvals, results, reconciliation, trusted usage, audit, idempotency, and transactional outbox. — completed 2026-08-22, commit 994207d, evidence: `docs/contracts/postgresql-schema.sql`; `docs/contracts/postgresql-transactions.md`
- [x] GOV-018 Define the evidence model and correlation identifiers required to reconstruct a consequential tool call across AG/TG/GR/downstream execution without storing unnecessary secret or enterprise-content payloads. — completed 2026-08-22, commit a01c3d6, evidence: `docs/contracts/evidence.md`; reconstruction acceptance criteria
- [x] GOV-019 Record supported Go, PostgreSQL, optional Valkey, Kubernetes, OpenAPI, MCP, OIDC/JWT, SPIFFE, OAuth/token-exchange, OpenTelemetry, and OCI baseline versions plus the dependency/upgrade policy in `docs/supported-versions.md`. — completed 2026-08-22, commit 2747e37, evidence: `docs/supported-versions.md`; official upstream release/version sources verified 2026-08-22
- [x] GOV-020 Define initial SLOs and capacity targets for API availability/latency, AG authorization overhead, GR overhead, credential resolution, invocation queue/claim latency, outbox lag, ambiguity/reconciliation processing, and maximum supported response sizes/concurrency. — completed 2026-08-22, commit 287f8ed, evidence: `docs/operations/slos-and-capacity.md`; service/capacity tables
- [x] GOV-021 Review Phase 0 artifacts against `PLAN.md`; close every ambiguous source of authority for identity, tool semantics, authorization, approval, credential selection, connector destination, replay, and evidence. — completed 2026-08-22, commit 99a0576, evidence: `docs/security/phase-0-authority-review.md`; authority closure matrix and resolved findings AR-001 through AR-005
- [x] GOV-022 Run documentation/schema validation, record `docs/phase-0-evidence.md`, and commit Phase 0 only when no security-relevant contract is still implicit. — completed 2026-08-22, commit 596f127, evidence: `make verify`; `docs/phase-0-evidence.md`; Redocly CLI 2.46.2 OpenAPI validation with zero warnings; Phase 0 artifact/link/digest/invariant validation

---

## Phase 1 — Engineering foundation

- [x] ENG-001 Initialize the Go module and pin the Go/toolchain version; create the planned `cmd`, `internal`, `api`, `migrations`, `deployments`, `docs`, and `test` package boundaries without importing adapter/provider types into domain/application packages.
- [x] ENG-002 Establish dependency policy, license/source checks, vulnerability scanning, reproducible tool pinning, generated-file policy, and a documented exception process for consequential dependencies.
- [x] ENG-003 Implement strict typed configuration with environment/flag/file loading as selected, safe defaults, startup validation, unknown/unsafe option rejection, production/development mode separation, and secret-safe rendering tests.
- [x] ENG-004 Implement structured logging with request/trace correlation, stable event names, centralized recursive redaction, cardinality discipline, and secret-canary leakage tests.
- [x] ENG-005 Bootstrap Prometheus-compatible metrics and OpenTelemetry tracing with no-op/local/OTLP modes, W3C Trace Context propagation, bounded attribute values, safe shutdown, and tests.
- [x] ENG-006 Add UUIDv7/ID helpers, UTC clock abstraction, checked integer/exact-quantity primitives, digest helpers, pagination cursor primitives, typed domain errors, and deterministic/fuzz tests.
- [x] ENG-007 Implement the HTTP server skeleton with middleware ordering, authentication hook points, request IDs, body/header/time limits, request cancellation, RFC 7807 responses, panic recovery, graceful shutdown, `/livez`, `/readyz`, and `/metrics`.
- [x] ENG-008 Create the repository Makefile contract with pinned `tools`, `generate`, `fmt`, `lint`, `vet`, `test`, `test-race`, `test-integration`, `test-e2e`, `test-security`, `test-mcp-conformance`, `openapi-check`, `migration-test`, `build`, `image`, and aggregate `verify` targets.
- [x] ENG-009 Add local Compose orchestration for pinned PostgreSQL and optional Valkey plus contract-faithful AG/GR fakes; use isolated credentials, health checks, deterministic ports/profile selection, and explicit cleanup.
- [x] ENG-010 Add CI jobs for formatting/generation drift, lint/vet, unit/race/fuzz smoke tests, integration tests, security tests, OpenAPI/MCP conformance, dependency/license/vulnerability gates, build, and container smoke.
- [x] ENG-011 Add baseline multi-stage static Go Dockerfile, `.dockerignore`, immutable/pinned build inputs, OCI labels/build metadata, non-root runtime, no shell requirement, and a hardened container smoke test.
- [x] ENG-012 Add initial OpenAPI generation/lint/drift checking and ensure generated transport types do not leak into the domain/application model.
- [x] ENG-013 Verify a clean checkout passes `make verify`, the image runs non-root with read-only rootfs/capabilities dropped, SIGTERM drains cleanly, and record `docs/phase-1-evidence.md`.

---

## Phase 2 — Authoritative persistence and invocation primitives

- [x] DATA-001 Select and wrap the PostgreSQL driver, migration approach, query/repository strategy, and transaction abstraction; record the consequential decision in an ADR.
- [x] DATA-002 Create migrations for tenants or tenant references, tools, immutable tool versions, tenant exposure/publication state, connector instances, and credential-binding references with integrity constraints and indexes.
- [x] DATA-003 Create migrations for logical invocations, canonical argument digests, trusted resource projection, current invocation state/version, timestamps, terminal classifications, and unique `(tenant_id, run_id, tool_call_id)` identity.
- [x] DATA-004 Create migrations for invocation attempts, monotonic attempt sequence, owner/claim/fencing data, downstream request/result identifiers, retry classification, ambiguity classification, and reconciliation metadata.
- [x] DATA-005 Create migrations for authorization decisions, GR evaluations, action-approval references/bindings, execution-result metadata, and security-relevant digests without persisting raw credentials.
- [x] DATA-006 Create migrations for trusted usage events, audit/evidence records, idempotency/replay records, and transactional outbox claim/retry/dead-letter fields.
- [x] DATA-007 Implement PostgreSQL pool configuration, dependency readiness, statement/transaction timeouts, transaction helpers, retriable-error classification, telemetry, and shutdown behavior.
- [x] DATA-008 Implement tenant-scoped repositories for tool catalog, connector instances, credential bindings, invocations, attempts, decisions, approvals, results, usage, audit, and outbox; test rollback and cross-tenant isolation.
- [x] DATA-009 Implement canonical JSON normalization and digest library with deterministic fixtures, malformed-number rejection, Unicode/object-order cases, schema-aware boundary tests, property tests, and fuzz campaigns.
- [x] DATA-010 Implement trusted resource projection from validated normalized arguments; reject missing/ambiguous projections required by the tool contract and test injection/edge cases.
- [x] DATA-011 Implement the pure invocation state machine with actor permissions, legal/illegal transitions, optimistic versioning, terminal immutability, approval wait, ambiguity/manual-review semantics, and table/fuzz tests.
- [x] DATA-012 Implement logical invocation acquisition/replay: one owner for a new `(tenant, run, tool_call_id)`, exact replay for matching tool/version/digest, deterministic conflict for mismatched replay, and bounded abandoned-owner recovery.
- [x] DATA-013 Implement invocation-attempt claiming with monotonic attempt numbers and fencing so concurrent/stale workers cannot both execute or finalize the same attempt.
- [x] DATA-014 Implement transactional mutation + audit + outbox helpers so no protected state change can commit without its required evidence/publication record.
- [x] DATA-015 Implement a replay-safe outbox publisher with bounded retry, backoff/jitter, leases/claims, poison-message handling, dead-letter visibility, metrics, and crash-after-send tests.
- [ ] DATA-016 Test migrations from empty DB and prior fixtures, checksum/immutability rules, constraints/index access paths, forward recovery after failed migration, backup-friendly schema behavior, and compatibility rules.
- [ ] DATA-017 Evaluate PostgreSQL RLS as defense in depth; implement it or record why repository enforcement is the RC posture, with adversarial tenant-isolation evidence either way.
- [ ] DATA-018 Run real-PostgreSQL concurrency/property tests proving tenant isolation, replay safety, conflict semantics, rollback, single attempt ownership, fencing, outbox atomicity, and record `docs/phase-2-evidence.md`.

---

## Phase 3 — Identity and authorization enforcement

- [ ] AUTH-001 Implement OIDC issuer discovery/JWKS retrieval with explicitly configured issuers, audiences/resources, algorithms, key-cache bounds, rotation, time validation, and deterministic outage behavior.
- [ ] AUTH-002 Implement bearer-token authentication middleware and typed authenticated-principal context; reject forged forwarding/governance headers and conflicting body/header identity hints.
- [ ] AUTH-003 Define and implement governed context derivation for tenant, subject principal, agent/version, and run identity from authenticated/signed sources rather than ordinary invocation arguments.
- [ ] AUTH-004 Implement workload identity abstraction for the calling process/pod; support a local development identity and a production-oriented SPIFFE/mTLS or equivalent trusted workload source without making workload identity equal end-user/run authority.
- [ ] AUTH-005 Add authentication adversarial tests for bad signature, issuer, audience/resource, algorithm, expiry/not-before, key rotation, missing governed claims, cross-tenant/run substitution, and forged proxy headers.
- [ ] AUTH-006 Implement the `Authorizer` application port and strict typed authorization decision model independent of the ThinkPixelAG HTTP client types.
- [ ] AUTH-007 Implement the ThinkPixelAG authorization adapter with deadlines, typed request/response validation, stable reason codes, correlation IDs, and no policy duplication inside TG.
- [ ] AUTH-008 Enforce authorization constraints as narrowing only; prove TG cannot expand allowed repositories/resources/actions/argument bounds returned by AG.
- [ ] AUTH-009 Implement decision freshness rules, bounded cache where justified, decision-key normalization/digesting, revocation epoch/checkpoint handling required by the AG contract, and safe cache bypass/failure behavior.
- [ ] AUTH-010 Require live/current authorization for configured high-risk writes and fail closed when the declared freshness contract cannot be met.
- [ ] AUTH-011 Integrate authorization into readiness/degradation semantics so the process does not advertise protected-write readiness when mandatory AG freshness cannot be established.
- [ ] AUTH-012 Prove authorization executes before downstream credential resolution or connector execution; add spies/canaries that fail tests if an unauthorized call reaches either boundary.
- [ ] AUTH-013 Run cross-tenant/run forgery, stale decision, malformed AG response, AG timeout/outage, revocation-freshness, cache-poisoning, and constraint-expansion tests; record `docs/phase-3-evidence.md`.

---

## Phase 4 — Tool catalog and canonical discovery/invocation API

- [ ] API-001 Implement domain validation for tool IDs, semantic/monotonic versioning policy as selected, immutable published versions, enabled/disabled state, trusted risk metadata, connector-operation binding, retry class, approval class, open-world classification, resource projection, and limits.
- [ ] API-002 Implement JSON Schema input/output validation with bounded compilation/cache behavior, deterministic errors, recursion/size safeguards, and tests for malformed/hostile schemas.
- [ ] API-003 Implement tool publication validation so a version cannot be published without a connector operation, credential-binding selector, retry semantics, required resource projection, metering semantics, request/result limits, and reviewed descriptions.
- [ ] API-004 Implement immutable tool-version persistence and reject direct mutation of security-relevant fields after publication; changes require a new version.
- [ ] API-005 Implement tenant exposure/visibility rules and authorization-filtered discovery without cross-tenant enumeration leaks.
- [ ] API-006 Implement `GET /v1/tools` with deterministic ordering, cursor pagination, bounded page sizes, authorization filtering, and stable OpenAPI examples.
- [ ] API-007 Implement `GET /v1/tools/{tool_id}` or equivalent version-aware describe contract with enumeration-safe errors and trusted metadata projection.
- [ ] API-008 Implement `POST /v1/tool-calls` through the application service: authenticate, derive context, resolve immutable tool version, validate/normalize arguments, project resource, acquire logical invocation, authorize, persist required decisions, and execute only through a connector port.
- [ ] API-009 Implement `GET /v1/tool-calls/{tool_call_id}` with tenant/run authorization, stable state/result representation, enumeration-safe behavior, and no credential/internal-provider leakage.
- [ ] API-010 Implement a hermetic mock connector supporting deterministic reads, writes, delays, retry classes, injected transport errors, ambiguous outcomes, and reconciliation for application/integration testing.
- [ ] API-011 Define and implement stable REST error codes for authentication, context, tool/version, schema, replay conflict, authorization, approval, GR, credential, connector, downstream rejection, ambiguity, result blocking, rate limiting, and budget exhaustion.
- [ ] API-012 Add strict request/result size, timeout, content-type, pagination, concurrency, and schema-complexity limits to public handlers.
- [ ] API-013 Implement initial administrative publication/exposure endpoints only behind a separate privileged authorization path; do not allow ordinary harness credentials to mutate tool definitions or connector bindings.
- [ ] API-014 Run OpenAPI conformance, tenant isolation, replay/conflict, malformed argument, timeout, cancellation, and deterministic discovery tests.
- [ ] API-015 Prove a governed mock invocation completes end to end through authentication, authorization, persistence, execution, evidence/outbox, and idempotent replay; record `docs/phase-4-evidence.md`.

---

## Phase 5 — Credential broker and first real connector

- [ ] CRED-001 Implement the credential-binding domain model tying trusted connector instances/tool selectors to credential providers, scopes/audiences/resources, tenancy, optional subject delegation, and policy-relevant metadata.
- [ ] CRED-002 Implement credential-selection rules from trusted tool/connector configuration only; reject caller-provided secret names, credential provider names, raw auth headers, arbitrary scopes, or connector destinations.
- [ ] CRED-003 Implement typed credential capability objects with expiration, audience/resource, optional refresh/lease metadata, zeroizable/short-lived in-memory representation where practical, and secret-safe `String`/error behavior.
- [ ] CRED-004 Implement a development-only environment/file provider with explicit production prohibition, strong redaction, and isolated tests.
- [ ] CRED-005 Implement at least one production-oriented provider such as Kubernetes projected/workload identity, Vault, AWS STS, Google workload federation, Azure managed identity, or standards-based OAuth token exchange; document the selected RC provider.
- [ ] CRED-006 Implement bounded credential/token caching keyed only by trusted binding/context with expiry skew, revocation/rotation behavior, singleflight/concurrency safety, and no persistence of plaintext credentials.
- [ ] CRED-007 Implement a connector registry whose connector instances/destinations are administrator-controlled and immutable or versioned; ordinary tool arguments cannot select arbitrary hosts/connectors.
- [ ] CRED-008 Implement shared downstream HTTP security primitives: allowlisted schemes/hosts, TLS verification, bounded redirects, metadata/private-address protections as applicable, deadlines, body limits, safe headers, and telemetry.
- [ ] CRED-009 Implement the first GitHub connector instance and read-only operation using a trusted credential binding; validate repository/resource projection before downstream execution.
- [ ] CRED-010 Implement `github.pull.comment` or equivalent reference write operation with documented side-effect/retry semantics and native downstream idempotency propagation where the API supports it.
- [ ] CRED-011 Record provider request/result identifiers and bounded safe metadata needed for reconciliation/evidence without storing bearer tokens or sensitive full payloads.
- [ ] CRED-012 Add connector conformance tests for deadline/cancellation, HTTP status/error mapping, response limits, redirect rejection, DNS/host policy, auth-header non-leakage, credential expiry, and server-side rate limiting.
- [ ] CRED-013 Add secret-canary tests across HTTP responses, application errors, logs, traces, metrics, audit/outbox payloads, GR requests, database rows, and panic paths.
- [ ] CRED-014 Run real or isolated-canary GitHub read/write qualification proving unauthorized calls never resolve credentials, credentials never reach the harness, and the reference write is correlated to a governed invocation.
- [ ] CRED-015 Record connector/credential architecture and `docs/phase-5-evidence.md`; do not leave live provider tokens or captured payloads in the repository.

---

## Phase 6 — ThinkPixelGR guardrails integration

- [ ] GR-001 Implement a strict typed ThinkPixelGR client/port with deadlines, version/profile selection, response validation, reason codes, evidence IDs, and mandatory versus observational failure modes.
- [ ] GR-002 Define the exact `pre_tool` projection sent to GR, excluding downstream credentials and unnecessary sensitive metadata while preserving the tool/run/resource context needed for evaluation.
- [ ] GR-003 Insert mandatory `pre_tool` evaluation after schema normalization and initial authorization but before credential resolution/connector execution.
- [ ] GR-004 Implement `pre_tool` allow/block/transform handling with deterministic audit/evidence and stable public errors.
- [ ] GR-005 When `pre_tool` transforms security-relevant arguments, re-run schema validation, canonicalization, resource projection, authorization, and any approval matching before execution; prove stale authorization cannot survive a transform.
- [ ] GR-006 Define the bounded downstream result projection sent to `post_tool`, including open-world/result trust labels and size/content handling.
- [ ] GR-007 Implement `post_tool` allow/block/redact/transform handling before returning any result to the harness, with deterministic output schema validation after transformations.
- [ ] GR-008 Implement configured GR outage/timeout/malformed-response behavior for mandatory and observational profiles; mandatory protected paths fail according to the documented posture.
- [ ] GR-009 Correlate GR evaluation IDs/digests with invocation evidence without persisting unnecessary full sensitive content.
- [ ] GR-010 Run block/redact/transform/outage/malformed-response/secret-canary tests and record `docs/phase-6-evidence.md`.

---

## Phase 7 — Action-scoped approvals

- [ ] APR-001 Add `waiting_for_approval` and related approval-reference semantics to the invocation state machine and persistence model if not already present.
- [ ] APR-002 Implement the typed ThinkPixelAG approval-request/reference adapter with exact run, tool/version, final canonical argument digest, trusted resource projection, policy/version, expiry, and correlation metadata.
- [ ] APR-003 Ensure approval is requested only after all security-relevant pre-execution transformations that affect the executable action have been applied and reauthorized.
- [ ] APR-004 Persist the approval binding transactionally with the invocation state/evidence; the harness receives only the approval state/reference needed for UX, never reusable broad authority.
- [ ] APR-005 Implement approval polling/signal/retry flow so a waiting invocation can resume without changing its logical `tool_call_id`, immutable tool version, approved digest, or resource projection.
- [ ] APR-006 Re-check current authorization, approval validity/expiry, tool/version enabled state, and required revocation/freshness immediately before credential resolution/execution.
- [ ] APR-007 Enforce one approval for one governed logical action; reject approval reuse across run, tenant, tool version, digest, resource projection, or a second logical `tool_call_id`.
- [ ] APR-008 Handle approval denial, expiry, revocation, cancellation, and argument changes with explicit terminal/restart semantics rather than silently mutating the approved operation.
- [ ] APR-009 Add adversarial TOCTOU/substitution/replay/concurrency tests proving the exact executable digest/resource is the one AG approved.
- [ ] APR-010 Record approval lifecycle/evidence examples and `docs/phase-7-evidence.md`.

---

## Phase 8 — Side-effect reliability and reconciliation

- [ ] REL-001 Implement the retry-policy engine driven exclusively by the trusted immutable tool-version retry class, not connector guesses or MCP annotations.
- [ ] REL-002 Implement attempt scheduling/claiming with maximum-attempt/deadline policy, backoff/jitter, fencing, and cancellation so only one active executor can own a protected attempt.
- [ ] REL-003 Implement propagation of stable downstream idempotency keys derived from trusted logical operation identity for connectors that support native idempotency.
- [ ] REL-004 Implement gateway-deduplicated completion checks only where TG can prove completion from authoritative persisted/downstream evidence; document the proof mechanism.
- [ ] REL-005 Implement `reconcile_before_retry` connector API and orchestration so ambiguous operations are inspected before any replay.
- [ ] REL-006 Implement `non_retryable` behavior preventing automatic replay after a possibly sent side effect.
- [ ] REL-007 Implement `at_least_once_accepted` behavior with explicit operator/user-visible contract and evidence that duplicates are possible by design.
- [ ] REL-008 Implement error classification distinguishing pre-send failure, definitely rejected downstream operation, definitely committed success, transient safe retry, and unknown/ambiguous outcome.
- [ ] REL-009 Implement persistent `ambiguous` invocation state and ensure timeout/connection-loss paths cannot be mislabeled as a clean failure when commit status is unknown.
- [ ] REL-010 Implement connector reconciliation result types such as confirmed-success, confirmed-not-applied, still-unknown, or unsafe-to-retry, with deterministic state transitions.
- [ ] REL-011 Implement privileged/manual-review operations for invocations that cannot be reconciled automatically, with strong authorization and append-only evidence.
- [ ] REL-012 Make process crash, worker crash, DB retry, outbox retry, and connector timeout paths replay-safe; test crash points before send, after send/before response, after response/before commit, and after commit/before publication.
- [ ] REL-013 Implement trusted usage event identity/deduplication at the logical accounting boundary so invocation/attempt replay cannot double-charge.
- [ ] REL-014 Create connector-specific idempotency/reconciliation qualification documents and tests for every shipped write operation; no write tool ships with undocumented ambiguous semantics.
- [ ] REL-015 Run repeated failure-injection/concurrency campaigns proving native-idempotent tools produce one logical side effect and `non_retryable`/`reconcile_before_retry` tools never blind-replay; record `docs/phase-8-evidence.md`.

---

## Phase 9 — MCP compatibility layer

- [ ] MCP-001 Pin the selected MCP protocol revision and transport requirements in code/docs/CI; protocol upgrades require an explicit compatibility change rather than an SDK-only bump.
- [ ] MCP-002 Implement MCP protocol types/validation behind an adapter boundary so MCP-specific types do not leak into the canonical invocation domain.
- [ ] MCP-003 Implement deterministic `tools/list` mapping from the trusted TG catalog, including names, descriptions, input/output schemas, compatibility annotations, and stable ordering.
- [ ] MCP-004 Derive MCP annotations from TG trusted risk/side-effect metadata; never consume caller/server-supplied annotations as authorization, approval, idempotency, or credential-selection facts.
- [ ] MCP-005 Implement `tools/call` by translating into the canonical invocation application service; there must be no alternate MCP-only execution path.
- [ ] MCP-006 Define how a target harness supplies or derives a stable logical `tool_call_id` across retries; explicitly reject the assumption that a transient JSON-RPC request ID alone provides platform idempotency.
- [ ] MCP-007 Implement revision `2026-07-28` stateless Streamable HTTP with strict authentication, configured Origin/Host validation, required routing-header/body agreement, per-request identity/capability metadata, request/body/time/concurrency bounds, cancellation, and safe error mapping.
- [ ] MCP-008 Implement any required MCP authorization/protected-resource metadata behavior without OAuth token passthrough to downstream connectors.
- [ ] MCP-009 Implement deterministic mapping of TG typed errors/states—including approval-required and ambiguous outcomes—into MCP-compatible results/errors without leaking internal credentials/providers.
- [ ] MCP-010 Implement an optional stdio adapter only if required by the target harness; authenticate it through trusted local/workload/run context rather than trusting arbitrary environment/body IDs.
- [ ] MCP-011 Add MCP conformance/compatibility tests for optional discovery, schema fidelity/cache hints, tool calls, errors, per-request version/client/capability metadata, routing-header mismatch, continuation replay, cancellation, and auth/origin behavior.
- [ ] MCP-012 Run a Codex App Server or selected harness smoke test proving it can discover/call TG tools, receive approval/result states, and never receives a downstream credential; record `docs/phase-9-evidence.md`.

---

## Phase 10 — Resource metering, evidence, and self-protection

- [ ] EVID-001 Implement the typed ThinkPixelAG trusted-usage event contract with stable event IDs, run identity, tool/version, logical invocation identity, dimension/units, timestamps, and exact quantities.
- [ ] EVID-002 Define when tool usage is charged—attempt, confirmed side effect, result, provider unit, or tool-specific rule—and encode that choice in immutable tool metering metadata.
- [ ] EVID-003 Implement transactional creation and outbox publication of trusted usage events so replay cannot double-apply charges in AG.
- [ ] EVID-004 Implement reconciliation/visibility for unpublished or rejected usage events and safe operator repair without inventing duplicate accounting identity.
- [ ] EVID-005 Implement the complete audit event taxonomy for authentication/context, tool resolution, validation, authorization, GR, approval, credential binding, attempt lifecycle, reconciliation, result handling, metering, admin mutation, and outbox outcomes.
- [ ] EVID-006 Implement an external evidence-sink publisher through the transactional outbox with bounded retry/dead-letter handling and replay-safe event IDs.
- [ ] EVID-007 Implement evidence retention/content-class controls that default to metadata/digests and require explicit policy for exceptional raw enterprise content storage.
- [ ] EVID-008 Harden logging/tracing/metrics cardinality and redaction; prohibit raw arguments/results as ordinary metric labels or unbounded trace attributes.
- [ ] EVID-009 Add request, per-tenant, per-run, per-tool, per-connector, and global concurrency controls with deterministic rejection/backpressure behavior.
- [ ] EVID-010 Add rate limiting/self-protection using PostgreSQL or optional Valkey only as allowed by the correctness model; Valkey failure must not authorize or corrupt protected writes.
- [ ] EVID-011 Implement bounded downstream/result buffering and streaming strategy so large-but-valid responses cannot exhaust gateway memory.
- [ ] EVID-012 Separate privileged administration authentication/authorization from ordinary harness invocation authority; add explicit audit for tool publication, connector/binding changes, manual reconciliation, and emergency operations.
- [ ] EVID-013 Create security/operations metrics and alerts for authorization denies/outages, stale decisions, GR failures, credential failures, ambiguous invocations, retries, dead letters, evidence lag, usage lag, connector errors, secret-redaction canaries, and saturation.
- [ ] EVID-014 Add an end-to-end evidence reconstruction test that follows one consequential invocation across AG authorization/approval, TG invocation/attempt, GR evaluation, connector/downstream ID, usage event, and outbox/evidence records.
- [ ] EVID-015 Prove metering is not double-applied by request replay, attempt retry, process crash, or outbox replay; record `docs/phase-10-evidence.md`.

---

## Phase 11 — Kubernetes and production operations

- [ ] OPS-001 Finalize the production multi-stage image with static Go binary, pinned digest-addressed bases, non-root numeric UID/GID, read-only rootfs compatibility, no shell requirement, build metadata, reproducible inputs, and container smoke tests.
- [ ] OPS-002 Add Kubernetes Deployment, Service, ServiceAccount, ConfigMap, secret references, migration Job, PodDisruptionBudget, and optional HPA/monitoring resources with secure defaults.
- [ ] OPS-003 Integrate the selected production workload identity/credential provider and prove pods receive only the authority required for configured connectors.
- [ ] OPS-004 Add NetworkPolicy/egress controls for ThinkPixelTG allowing only required AG/GR/PostgreSQL/Valkey/credential/evidence/downstream destinations.
- [ ] OPS-005 Add the reference harness-worker NetworkPolicy proving the harness can reach TG/LLMGW/runtime dependencies but cannot directly reach governed GitHub/Slack/Jira/production Kubernetes endpoints.
- [ ] OPS-006 Harden pod/container security context with dropped capabilities, `RuntimeDefault` seccomp, non-root enforcement, bounded ephemeral storage, read-only filesystem, and least-privilege RBAC.
- [ ] OPS-007 Define production readiness/liveness semantics, startup/migration ordering, graceful connection draining, termination budgets, and rolling-upgrade behavior.
- [ ] OPS-008 Add TLS/mTLS/ingress/service-mesh configuration guidance as selected and prove certificate/audience/hostname validation on security-critical internal/downstream connections.
- [ ] OPS-009 Implement backup/restore procedures and tests for PostgreSQL authoritative state, including invocations waiting for approval, ambiguous/manual-review records, outbox state, and immutable catalog versions.
- [ ] OPS-010 Implement upgrade/rollback rehearsal, migration compatibility checks, and forward-fix rules; released migrations are immutable.
- [ ] OPS-011 Run load/capacity tests against documented SLOs for reads, governed writes, authorization/GR latency, credential resolution, invocation concurrency, large valid results, and outbox pressure.
- [ ] OPS-012 Run disruption/resilience tests for pod termination, replica loss, PostgreSQL failover/reconnect, optional Valkey outage, AG/GR outage, credential-provider outage, downstream timeout, and outbox backlog.
- [ ] OPS-013 Add live reference-connector qualification/canary procedures using isolated enterprise resources; destructive or production mutations are not part of ordinary CI.
- [ ] OPS-014 Add SBOM generation, checksums/provenance/signature hooks, vulnerability scan, dependency/source/license reports, and release artifact inventory.
- [ ] OPS-015 Write operations/security runbooks for install, migration, readiness degradation, AG freshness recovery, GR outage, credential rotation/revocation, ambiguous invocation review, outbox recovery, usage reconciliation, backup/restore, upgrade/rollback, and suspected credential leakage.
- [ ] OPS-016 Create dashboards/alerts/SLO documentation and exercise alert paths for saturation, authz/GR failures, ambiguous states, dead-letter growth, usage/evidence lag, and connector/provider errors.
- [ ] OPS-017 Install into a disposable cluster and prove migration, canary invocation, direct-harness-egress denial, disruption recovery, restore, rolling upgrade, and smoke tests; record `docs/phase-11-evidence.md`.

---

## Phase 12 — Release-candidate closure

- [ ] RC-001 Freeze and document the RC OpenAPI, MCP revision/transport behavior, authorization/approval/GR contracts, credential-provider interface, connector interface, retry classifications, and database migration baseline.
- [ ] RC-002 Run the full clean-checkout `make verify` gate with no generated-contract drift, race failures, skipped mandatory security tests, or local uncommitted dependencies.
- [ ] RC-003 Run full real-PostgreSQL integration/concurrency suites and verify migration install/upgrade/forward-recovery/backup-restore behavior against the supported version baseline.
- [ ] RC-004 Run adversarial tenant/run/workload identity tests proving caller-supplied governance fields cannot create authority and cross-tenant enumeration/execution is blocked.
- [ ] RC-005 Run authorization security gates proving deny-by-default behavior, declared freshness/revocation behavior, AG outage posture, constraint narrowing, and no credential resolution before authorization.
- [ ] RC-006 Run credential-exfiltration gates proving downstream credentials are absent from harness responses, GR payloads, logs, traces, metrics, audit/evidence, outbox/dead-letter payloads, and normal database rows.
- [ ] RC-007 Run mandatory ThinkPixelGR failure/block/transform/redaction gates and prove transformed security-relevant arguments cannot execute under stale authorization or approval.
- [ ] RC-008 Run approval gates proving exact final digest/resource binding, expiry/revocation, single logical-action use, reauthorization before execution, and rejection of substitution/replay.
- [ ] RC-009 Run crash/retry/ambiguity qualification for every shipped side-effecting tool; no shipped tool may have undocumented idempotency or ambiguous-write semantics.
- [ ] RC-010 Run MCP conformance and Codex/target-harness end-to-end tests proving all calls traverse the canonical application service and no downstream credential crosses the harness boundary.
- [ ] RC-011 Run reference Kubernetes bypass tests proving the harness cannot directly reach governed downstream APIs while TG retains only explicitly required egress.
- [ ] RC-012 Run capacity/resource gates and verify documented API/connector/outbox latency, availability assumptions, concurrency, memory bounds, and large-result behavior.
- [ ] RC-013 Resolve or formally block release on all critical/high exploitable vulnerabilities, threat-model findings, policy violations, unsafe dependency findings, and secret-leakage failures; no silent waivers.
- [ ] RC-014 Exercise operational procedures for restore, rolling upgrade, rollback/forward fix, credential rotation, revocation freshness recovery, AG/GR outage, outbox recovery, trusted-usage reconciliation, and ambiguous-invocation review.
- [ ] RC-015 Complete isolated live connector canaries for each connector claimed as RC-supported and capture provider-side evidence without committing sensitive payloads/tokens.
- [ ] RC-016 Extract stable architecture/security decisions from `PLAN.md` into ADRs/contracts, reconcile README/API/operations docs with actual implementation, and explicitly document any accepted deviation from the original plan.
- [ ] RC-017 Generate reproducible release artifacts: OCI image, SBOM, checksums, provenance/signature hooks, Kubernetes artifacts, API/MCP contract snapshots, dependency reports, and release notes.
- [ ] RC-018 Perform final threat-model review against the shipped implementation and close every unresolved release blocker around authorization drift, harness bypass, credential exfiltration, token passthrough, approval TOCTOU, duplicate side effects, stale revocation, MCP churn, SSRF, prompt-injected results, and sensitive evidence.
- [ ] RC-019 Verify every checked TODO has completion metadata/evidence, every remaining unchecked item is explicitly non-RC or a release blocker, and `docs/phase-12-evidence.md` links all final gate artifacts.
- [ ] RC-020 From a clean checkout, reproducibly build a traceable taggable/signable release candidate and record the final commit, image digest, contract versions, migration version, supported connector/tool set, and release-gate summary.

---

## First vertical-slice milestone

The first meaningful end-to-end milestone is reached when the following contiguous subset is complete:

```text
GOV-* required contracts
ENG-* foundation
DATA-* invocation primitives
AUTH-* identity/authorization
API-* canonical invocation + catalog
CRED-* GitHub connector
GR-* pre/post-tool
APR-* if policy requires approval
REL-* retry/ambiguity path for github.pull.comment
MCP-* target harness adapter
EVID-* trusted usage/evidence
OPS-005 harness-bypass NetworkPolicy proof
```

The vertical slice is accepted only when all of these statements are demonstrated:

- the Codex/minimal MCP harness can discover and invoke `github.pull.comment`;
- the harness/model never receives the GitHub credential;
- an unauthorized repository is rejected before credential resolution;
- malformed arguments never reach GitHub;
- GR can block the write before it reaches GitHub;
- if approval is required, the executed arguments/resource exactly match the approved digest;
- replay of the same logical call follows the published idempotency contract and cannot create an unintended duplicate;
- an unknowable downstream write outcome becomes `ambiguous` rather than a misleading clean failure;
- the final operation can be correlated to run, tool/version, authorization, GR, approval when applicable, downstream provider identifier, usage event, and evidence;
- the reference Kubernetes policy prevents the harness from bypassing ThinkPixelTG and calling GitHub directly.

---

## Per-item completion protocol

Before checking any item:

1. Re-read the relevant `PLAN.md` section, current ADRs/contracts, and repository status.
2. Confirm all dependencies are complete.
3. Identify the security invariant and acceptance evidence for the item.
4. Implement the smallest complete vertical change, including migrations/contracts/telemetry/docs required by that change.
5. Run focused tests during development.
6. Run the item's acceptance commands and relevant adversarial tests.
7. Run `make verify` before checking any phase-gate item.
8. Review the diff for secrets, provider payload dumps, generated drift, binaries, local DB state, and unrelated changes.
9. Update `README.md`, `PLAN.md`, OpenAPI/MCP contracts, or ADRs if implementation changed an external behavior or architectural assumption.
10. Mark the item checked only after evidence passes and append completion date, commit SHA, and evidence references.

If a required check cannot run, the item remains unchecked and the evidence gap/blocker is recorded.

---

## Coding-agent safety reminders

- Never make a failing authorization, approval, GR, or credential check optional just to make an integration test pass.
- Never add caller-controlled generic escape hatches such as arbitrary URL, arbitrary secret name, arbitrary connector type, raw downstream `Authorization` header, or unbounded provider options without a reviewed plan/ADR change.
- Never trust MCP annotations, model text, tool descriptions, or ordinary request fields as authorization facts.
- Never resolve a downstream credential before the protected invocation has passed the controls that precede credential resolution.
- Never claim a write is idempotent unless the exact mechanism and failure windows are documented and tested.
- Never blindly retry an ambiguous side effect.
- Never allow a post-approval transformation to execute without recomputing the canonical digest/resource and revalidating authorization/approval.
- Never persist or log plaintext downstream credentials.
- Prefer hermetic connector fakes for ordinary tests; live side effects require isolated qualification resources.
- Preserve unrelated work and shared history; do not amend/rewrite shared commits to make checklist history appear cleaner.
- A checked checkbox is a claim backed by reproducible evidence, not an estimate of implementation progress.
