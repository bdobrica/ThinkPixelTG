# ThinkPixelTG Implementation Plan

## 1. Purpose

This document is the implementation contract for taking ThinkPixelTG from an empty repository to a release candidate.

ThinkPixelTG is the enterprise tool gateway and tool-execution enforcement plane for the ThinkPixel platform. It sits between untrusted or semi-trusted agent harnesses and enterprise systems, mediating every governed tool invocation so that credentials, authorization, guardrails, idempotency, approvals, resource accounting, and evidence remain outside the harness.

The central invariant is:

> The harness may decide what it wants to do. ThinkPixelTG decides whether and how that operation is allowed to reach the downstream system.

ThinkPixelTG is intended to work with ThinkPixelAG, ThinkPixelGR, and ThinkPixelLLMGW, but its internal contracts must remain modular enough that individual integrations can be replaced without changing the tool-execution domain model.

`TODO.md` should be the chronological execution ledger. This plan explains the architecture, invariants, interfaces, delivery phases, and quality gates; the checklist should record what remains, what was verified, and the evidence for each completed unit of work.

---

## 2. Product boundary

ThinkPixelTG owns the trusted execution boundary for enterprise tools.

It is responsible for:

- canonical, versioned tool definitions and operational metadata;
- tenant-scoped tool discovery;
- normalization and validation of tool arguments;
- authenticated run, agent, user, and workload context;
- authorization enforcement using an external decision point, primarily ThinkPixelAG;
- action-scoped approval enforcement;
- ThinkPixelGR `pre_tool` and `post_tool` evaluation;
- downstream credential brokerage without exposing credentials to the model or harness;
- connector selection and downstream API execution;
- side-effect classification and retry semantics;
- persistent logical `tool_call_id` handling and deduplication;
- propagation of downstream idempotency keys when supported;
- ambiguous-outcome handling when downstream completion cannot be proven;
- trusted usage and resource-metering events for ThinkPixelAG;
- structured audit/evidence records correlating request, decision, execution, and result;
- MCP compatibility as an adapter over the canonical gateway semantics;
- bounded, observable, horizontally scalable execution suitable for Kubernetes.

ThinkPixelTG does **not** own:

- agent registration, agent-version approval, run admission, or run lifecycle; those belong to ThinkPixelAG;
- the authoritative definition of which agent/run is allowed to use which capability; ThinkPixelTG enforces decisions supplied by the configured authorization decision point;
- general prompt, response, retrieval, or ingestion safety policy; ThinkPixelGR evaluates those concerns;
- LLM provider routing, token accounting, model budgets, or model credentials; those belong to ThinkPixelLLMGW;
- agent planning, reasoning, memory, or harness state;
- durable workflow orchestration for an entire agent run;
- long-lived storage of user refresh tokens in the first release;
- a generic arbitrary-URL HTTP proxy;
- a general-purpose secrets manager;
- an MCP marketplace or arbitrary third-party MCP proxy;
- direct execution of shell commands inside agent workspaces;
- replacing downstream systems' own authorization, audit, idempotency, or safety controls.

A release candidate is successful when a harness such as Codex can discover and invoke enterprise tools through ThinkPixelTG while being unable to obtain the downstream credentials or bypass the gateway's authorization and evidence path.

---

## 3. Core security model

### 3.1 The harness is not a security boundary

Assume the harness, model context, tool descriptions, tool outputs, retrieved content, and user-provided content may all contain malicious or misleading instructions.

Therefore:

- a valid model-generated tool call is only a request to attempt an operation;
- MCP client-side confirmation is useful UX but is not authoritative authorization;
- tool annotations are compatibility hints, never the source of security policy;
- a tool name presented by the harness does not grant access to that tool;
- a skill or prompt cannot expand the capability ceiling of the run;
- downstream credentials must not be placed in model context or tool output;
- direct network access from the harness to governed enterprise systems must be prevented by deployment policy, not merely discouraged in prompts.

### 3.2 Preserve distinct identities

Every protected invocation must preserve at least these identities independently:

```text
subject principal   employee or initiating service
agent identity       governed agent and immutable version
run identity         governed logical run
workload identity    process/pod actually calling ThinkPixelTG
```

These values must never collapse into a shared service account in the authorization or audit model.

The exact authenticated representation may vary by deployment, but ThinkPixelTG must be able to reconstruct:

```text
who requested the run?
which governed agent version is acting?
which run is this operation part of?
which workload submitted the call?
which tool definition/version was executed?
which downstream identity/credential binding was used?
```

### 3.3 Never trust caller-supplied governance identity

Fields such as `tenant_id`, `principal_id`, `agent_id`, `agent_version`, and `run_id` in an invocation body are informational at most.

Authoritative identity must derive from verified authentication material and trusted lookups. A header such as:

```http
X-Run-ID: run-123
```

must never by itself establish run authority.

### 3.4 Credentials do not cross the harness boundary

The model and harness must never receive:

- GitHub PATs;
- Slack cookies/tokens;
- cloud provider access keys;
- Kubernetes bearer tokens;
- OAuth refresh tokens;
- client secrets;
- private keys used for downstream authentication;
- secret-manager plaintext values.

The gateway resolves a credential binding only after the invocation has passed authentication, authorization, approval, argument validation, and mandatory pre-execution guardrails.

### 3.5 Deny by default

For protected operations, failure to establish any mandatory prerequisite results in no downstream side effect.

Examples include:

- invalid or expired authentication;
- unknown tenant/run/agent context;
- stale or unavailable authorization when freshness is required;
- malformed policy decision;
- missing mandatory approval;
- mismatched approval argument digest;
- missing credential binding;
- unavailable required GR evaluation;
- invalid argument schema;
- unknown tool version;
- unclassifiable side-effect semantics;
- an ambiguous prior invocation when safe replay cannot be proven.

Read-only operations may have explicitly configured degraded behavior only after a documented threat-model decision. Writes default to fail closed.

---

## 4. Trust boundaries and threat model

Phase 0 must produce a durable threat model, but implementation begins with the following required attack classes.

### 4.1 Threat actors and failure sources

Assume possible compromise or malicious behavior from:

- the model;
- the agent harness;
- user-supplied prompts and files;
- retrieved documents;
- tool responses from open-world systems;
- compromised MCP clients;
- forged tenant/run identifiers;
- compromised downstream credentials;
- compromised connector dependencies;
- malicious tenant administrators attempting cross-tenant access;
- stale authorization caches;
- repeated delivery caused by worker crashes or network ambiguity;
- compromised or misconfigured downstream APIs;
- SSRF through connector parameters;
- oversized or adversarial JSON inputs and outputs;
- poisoned tool descriptions intended to manipulate the model;
- log/event injection and secret leakage into telemetry.

### 4.2 Required threat mitigations

The initial design must explicitly mitigate:

- confused-deputy attacks;
- OAuth token passthrough;
- token audience confusion;
- credential exfiltration;
- cross-tenant identifier substitution;
- TOCTOU between authorization/approval and execution;
- approval replay;
- argument substitution after approval;
- duplicate external side effects;
- stale revocation state;
- connector SSRF;
- DNS rebinding on MCP Streamable HTTP;
- arbitrary redirect following to untrusted hosts;
- secret leakage through errors/logs/traces;
- tool-name collisions and version ambiguity;
- malformed or misleading MCP tool annotations;
- resource exhaustion through large payloads or high concurrency;
- unbounded downstream latency;
- retry storms;
- MCP routing-header/body confusion and cross-context continuation replay;
- downstream result injection returned to the model.

### 4.3 No OAuth token passthrough

Inbound bearer tokens authenticate the caller to ThinkPixelTG only. They must not be forwarded unchanged to downstream APIs.

If a downstream API requires OAuth, ThinkPixelTG obtains or exchanges a separate token whose audience is the downstream resource and whose authority is derived from the governed credential binding.

This follows the MCP authorization model and avoids turning ThinkPixelTG into a confused deputy.

---

## 5. High-level architecture

```text
                           ┌─────────────────────────────┐
                           │ Agent harness / MCP client  │
                           │ Codex, SDK, internal loop   │
                           └──────────────┬──────────────┘
                                          │
                         MCP or canonical │ tool API
                                          ▼
┌───────────────────────────────────────────────────────────────────────┐
│                         ThinkPixelTG — Go                             │
│                                                                       │
│  ┌──────────────┐  ┌──────────────┐  ┌───────────────────────────┐   │
│  │ HTTP / MCP   │  │ Identity     │  │ Tool catalog + schemas    │   │
│  │ adapters     │  │ resolution   │  │ trusted metadata          │   │
│  └──────┬───────┘  └──────┬───────┘  └────────────┬──────────────┘   │
│         │                  │                       │                  │
│         └──────────────┬───┴───────────────────────┘                  │
│                        ▼                                               │
│                 Invocation orchestrator                                │
│                        │                                               │
│          ┌─────────────┼──────────────┐                                │
│          ▼             ▼              ▼                                │
│      Authorizer      ThinkPixelGR   Approval verifier                  │
│      (AG adapter)    pre/post tool  (AG adapter)                       │
│          │             │              │                                │
│          └─────────────┴──────┬───────┘                                │
│                               ▼                                        │
│                       Credential broker                                │
│                               │                                        │
│                               ▼                                        │
│                       Connector registry                               │
│                               │                                        │
│                               ▼                                        │
│                        Downstream execution                            │
│                               │                                        │
│         ┌─────────────────────┼──────────────────────┐                 │
│         ▼                     ▼                      ▼                 │
│  invocation ledger       usage/evidence         outbox/telemetry       │
└─────────┬─────────────────────┬──────────────────────┬─────────────────┘
          │                     │                      │
          ▼                     ▼                      ▼
      PostgreSQL          ThinkPixelAG           Evidence sink
                         trusted metering

             connector traffic
                    │
        ┌───────────┼────────────┬──────────────┐
        ▼           ▼            ▼              ▼
      GitHub      Slack       Kubernetes       Jira / others
```

The gateway process is deliberately on the critical path for governed side effects.

---

## 6. Architecture decisions

These are provisional until captured as ADRs.

### 6.1 Deployment shape

Start as a modular monolith written in Go and built as a static single binary.

Use explicit domain/application/port/adapter boundaries so that these components can later be extracted without changing domain semantics:

- MCP protocol adapter;
- connector execution workers;
- credential providers;
- evidence/outbox publisher;
- authorization integration;
- ThinkPixelGR integration.

The release-candidate deployment is a stateless horizontally scalable API/execution service backed by PostgreSQL. The process may execute short-lived tool calls synchronously, but durable invocation state must survive process loss.

### 6.2 Canonical protocol

REST/JSON with OpenAPI 3.1 is the canonical external and internal contract for the first release.

MCP is an adapter over that domain contract, not the source of truth for execution semantics.

A future gRPC interface may be added after profiling without changing:

- tool identity;
- authorization semantics;
- invocation IDs;
- idempotency rules;
- approval binding;
- result/evidence contracts.

### 6.3 Persistence

PostgreSQL is mandatory and authoritative for:

- tool definitions and immutable versions;
- connector instances and metadata;
- credential binding metadata/references;
- tool invocation state;
- invocation attempts;
- idempotency/deduplication records;
- approval references and validated digests;
- downstream result metadata;
- usage/metering ledger;
- audit records;
- transactional outbox;
- authorization/revocation checkpoints when required.

Plaintext downstream secrets must not be stored in PostgreSQL in the release candidate.

### 6.4 Optional cache

Valkey is optional and may be used for:

- bounded authorization decision cache;
- short-lived downstream OAuth token cache;
- rate-limit counters;
- connector concurrency semaphores;
- discovery cache;
- revocation fanout hints.

Every security-sensitive cached value must be keyed by relevant policy/revocation/version material and have a bounded TTL.

Cache failure must not invalidate the authoritative invocation ledger or permit a protected write that would otherwise be denied.

### 6.5 Authorization architecture

ThinkPixelTG is a Policy Enforcement Point (PEP).

The `Authorizer` port returns a typed decision. The primary production adapter calls ThinkPixelAG, which acts as the Policy Decision Point (PDP) for governed runs.

ThinkPixelTG must not silently grow an independent copy of agent/run authorization policy.

Development-only/static authorizers may exist for local testing, but production startup must reject unsafe authorizer modes unless explicitly compiled/configured for a documented non-governed deployment.

### 6.6 Guardrails architecture

ThinkPixelGR remains an evaluator. ThinkPixelTG enforces its `pre_tool` and `post_tool` decisions.

Authorization and guardrails are separate:

```text
ThinkPixelAG / Authorizer:
    "may this run perform this operation?"

ThinkPixelGR:
    "does this proposed call/result violate configured safety/data policy?"

ThinkPixelTG:
    "enforce both decisions before/after the downstream call."
```

A GR `allow` never overrides an authorization denial.

### 6.7 Credential architecture

ThinkPixelTG stores credential **references and bindings**, not plaintext enterprise credentials.

Credential material should be obtained through pluggable providers such as:

- Kubernetes-mounted secret or projected token;
- HashiCorp Vault;
- AWS Secrets Manager / STS;
- Google Secret Manager / workload federation;
- Azure Key Vault / managed identity;
- OAuth authorization server/token exchange;
- a development-only environment provider.

Credential providers are selected by trusted connector/binding configuration, never by arbitrary invocation arguments.

### 6.8 Connector architecture

Initial connectors are compiled Go implementations behind a common interface.

External connector processes are a future extension and, if introduced, must use mutually authenticated, strongly versioned contracts. The RC should not support arbitrary executable plugins loaded from writable disk.

### 6.9 IDs, timestamps, exact quantities, and errors

Use the same primitive discipline as ThinkPixelAG:

- canonical RFC 9562 UUIDv7 identifiers generated with cryptographic randomness;
- UTC timestamps from an injectable clock;
- typed errors with closed public machine codes;
- RFC 7807 Problem Details at REST boundaries;
- versioned HMAC-authenticated pagination cursors;
- no floating-point representation for authoritative cost/usage values;
- W3C Trace Context propagation;
- canonical request IDs generated/validated at ingress.

---

## 7. Domain model

Principal domain entities:

- `Tool`;
- `ToolVersion`;
- `ToolSchema`;
- `ToolRiskProfile`;
- `ConnectorType`;
- `ConnectorInstance`;
- `CredentialBinding`;
- `Invocation`;
- `InvocationAttempt`;
- `InvocationResult`;
- `InvocationDecision`;
- `ApprovalBinding`;
- `UsageEntry`;
- `IdempotencyRecord`;
- `AuditEvent`;
- `OutboxMessage`;
- `AuthorizationCheckpoint` or equivalent freshness state.

### 7.1 Tool identity

A tool has a stable logical identity and immutable versions.

Example:

```text
tool_id: github.pull.comment
version: 3
canonical: github.pull.comment@3
```

The stable tool name is suitable for MCP and SHOULD follow the interoperable MCP naming subset:

```text
[A-Za-z0-9_.-]
```

with a maximum of 128 characters.

Semantic changes that can affect authorization, side effects, credential selection, input validation, or output interpretation require a new tool version.

### 7.2 Tool definition

A trusted tool version contains at least:

```yaml
id: github.pull.comment
version: 3
title: Comment on pull request
description: Add a comment to an existing GitHub pull request.
connector:
  type: github
  operation: pull.comment
input_schema: {...}
output_schema: {...}
side_effect: write
risk_class: medium
retry_semantics: downstream_idempotency
idempotency_scope: logical_tool_call
open_world: true
credential_binding_selector: github-user-delegated
resource_projection:
  repository: $.repository
  pull_request: $.pull_request
metering:
  dimension: tool_calls
  units: "1"
limits:
  request_bytes: 65536
  response_bytes: 1048576
  timeout: 20s
```

The exact YAML/JSON administrative representation is an adapter concern. The domain representation is typed and validated.

### 7.3 Authoritative risk metadata

ThinkPixelTG defines trusted operational semantics independently of MCP annotations.

Required fields include:

```text
side_effect:
    read | write | destructive

retry_semantics:
    safe
    downstream_idempotency
    gateway_deduplicated
    reconcile_before_retry
    at_least_once_accepted
    non_retryable

approval_class:
    never
    policy
    always

open_world:
    true | false
```

MCP annotations are derived from these fields for clients. They never flow in the opposite direction as authoritative policy.

### 7.4 Tool version immutability

After publication, the following are immutable:

- tool name;
- connector type/operation;
- input schema;
- output schema where authoritative;
- resource projection;
- side-effect class;
- retry semantics;
- credential-binding selector;
- risk metadata;
- timeout and result-size security bounds where changing them alters safety behavior.

Corrections create a new version. A tool version can be disabled/revoked without mutating its historical definition.

### 7.5 Invocation

An invocation represents one logical requested operation.

```text
Invocation
    invocation_id
    tool_call_id
    tenant_id
    run_id
    agent_id
    agent_version
    subject_principal
    workload_principal
    tool_id
    tool_version
    normalized_arguments
    arguments_digest
    resource_projection
    requested_at
    state
    state_version
```

`tool_call_id` belongs to the logical operation and survives worker/harness retries.

For a governed run, the tuple:

```text
(tenant_id, run_id, tool_call_id)
```

must uniquely identify one normalized logical operation.

Reusing the same `tool_call_id` with different tool/version/arguments is a conflict, never a new invocation.

### 7.6 Invocation state machine

Initial state machine:

```text
RECEIVED
    -> VALIDATING
    -> DENIED
    -> WAITING_FOR_APPROVAL
    -> AUTHORIZED
    -> EXECUTING
    -> SUCCEEDED
    -> FAILED
    -> AMBIGUOUS

WAITING_FOR_APPROVAL
    -> AUTHORIZED
    -> DENIED
    -> EXPIRED

AMBIGUOUS
    -> RECONCILING
    -> SUCCEEDED
    -> FAILED
    -> MANUAL_REVIEW
```

Terminal states are explicit. State transitions are concurrency-safe and recorded with an invocation event/audit record.

A process crash must not revert a persisted state to an earlier semantic state.

### 7.7 Invocation attempt

An `InvocationAttempt` records one actual attempt to communicate with the downstream system.

Multiple attempts may belong to one logical invocation only when its retry semantics permit them.

An attempt records:

- attempt number;
- connector implementation/version;
- downstream endpoint identity, not secret URL query data;
- credential binding ID and credential mechanism, not credential material;
- authorization decision ID;
- GR evaluation IDs;
- approval ID if used;
- downstream request ID/idempotency key;
- start/end timestamps;
- bounded response metadata;
- outcome classification;
- network/protocol error classification.

---

## 8. Canonical invocation lifecycle

Every protected tool invocation follows this order unless a documented read-only optimization preserves the same security semantics.

```text
1. authenticate caller/workload
2. derive tenant/subject/agent/run identity
3. resolve exact tool version
4. validate request envelope and size
5. normalize + validate arguments
6. derive canonical argument digest and resource projection
7. claim/create logical invocation by tool_call_id
8. resolve authorization freshness
9. obtain typed authorization decision from Authorizer/ThinkPixelAG
10. if required, validate action-scoped approval
11. invoke ThinkPixelGR pre_tool
12. apply permitted deterministic transformations, if any
13. re-normalize transformed arguments and recompute digest
14. if transformed security-relevant arguments changed, re-authorize/re-approve
15. resolve credential binding
16. obtain short-lived downstream credential
17. execute connector with timeout and retry policy
18. classify downstream outcome
19. persist result/attempt atomically with evidence/outbox state
20. invoke ThinkPixelGR post_tool on bounded result content
21. redact/transform/block result release according to GR policy
22. emit trusted usage/metering event
23. return canonical result/replay result to caller
```

### 8.1 No decision/result gaps

For a consequential side effect, evidence must make it possible to correlate:

```text
logical tool call
    -> normalized arguments digest
    -> authorization decision
    -> approval, if any
    -> credential binding
    -> connector attempt
    -> downstream request identifier
    -> downstream outcome
    -> post-tool guardrail decision
    -> usage event
```

### 8.2 Authorization after transformations

If ThinkPixelGR returns a transformed tool request, ThinkPixelTG must not execute it under an authorization decision for different security-relevant arguments.

After transformation:

- revalidate schema;
- recompute canonical arguments;
- recompute resource projection and digest;
- reuse the prior authorization only if the decision contract explicitly states that the transformed values remain within the authorized constraints;
- otherwise perform authorization again;
- action-scoped approval must match the final executable argument digest.

This prevents a content transformation layer from accidentally invalidating the authorization boundary.

---

## 9. Canonical argument representation and digests

Action approvals, idempotency, evidence, and replay depend on deterministic argument identity.

### 9.1 Canonicalization

Define one documented canonical JSON representation for security-sensitive hashing.

Requirements:

- parse JSON into a typed/schema-validated structure;
- reject duplicate object keys before normalization;
- normalize strings only where the tool schema explicitly defines semantic normalization;
- preserve numeric meaning without floating-point ambiguity;
- order object keys deterministically;
- preserve array order;
- distinguish omitted fields from explicit `null` where the schema semantics differ;
- apply defaults before hashing only when defaults are part of the published tool contract;
- reject non-finite numbers;
- bound nesting depth, key count, string length, and total bytes.

Use SHA-256 or a stronger standardized digest over a versioned canonical envelope such as:

```text
thinkpixeltg-invocation-v1\n
<tenant_id>\n
<run_id>\n
<tool_id>@<version>\n
<canonical-json-arguments>
```

The digest algorithm/version is stored with the record.

### 9.2 Resource projection

Authorization should not require the PDP to understand arbitrary connector payloads.

Each tool version defines a bounded `resource_projection` that extracts security-relevant resource identity from normalized arguments.

Example:

```json
{
  "tool": "kubernetes.deployment.restart@2",
  "resource": {
    "cluster": "production",
    "namespace": "payments",
    "deployment": "api"
  }
}
```

Projection failure is a denial, not an empty resource.

The projection implementation must be deterministic and versioned with the tool definition.

---

## 10. Authentication, delegation, and workload identity

### 10.1 HTTP authentication

REST and MCP Streamable HTTP use bearer authentication or mutually authenticated workload identity according to deployment configuration.

JWT validation includes:

- trusted issuer allowlist;
- exact audience/resource validation for ThinkPixelTG;
- allowed algorithms;
- signature/JWKS verification;
- `exp`, `nbf`, issuer, and audience validation;
- bounded clock skew;
- token size limits;
- rejection of ambiguous tenant/subject mappings.

### 10.2 Delegated identity

The preferred governed token model preserves:

```text
sub     = initiating user/service
act     = current delegated actor where supported
run     = governed run identifier
agent   = governed agent identifier/version
azp     = authorized client/workload where applicable
aud     = ThinkPixelTG resource identifier
```

RFC 8693 token exchange is an appropriate integration mechanism where the organization's authorization server supports it, but ThinkPixelTG does not become a general security-token service in the first release.

### 10.3 SPIFFE

Support a `WorkloadAuthenticator` port so Kubernetes deployments may use SPIFFE/SPIRE or an equivalent workload identity system.

SPIFFE identity can authenticate the process/pod making the request. It does not, by itself, prove the user/run delegation. The final invocation context may combine:

- workload identity from mTLS/X.509-SVID;
- run-scoped bearer authority from the platform STS/AG integration.

### 10.4 MCP authentication

For Streamable HTTP, ThinkPixelTG behaves as an OAuth-protected resource when configured for MCP authorization.

Requirements include:

- validate bearer tokens on every HTTP request;
- reject tokens intended for a different audience;
- never place access tokens in URI query strings;
- never forward the incoming MCP token to downstream APIs;
- validate `Origin` on Streamable HTTP requests;
- use HTTPS outside trusted local development;
- validate the pinned revision, client identity/capabilities, and required routing
  headers on every stateless request;
- reject header/body disagreement and treat continuation state as non-authoritative.

### 10.5 stdio adapter authentication

A stdio MCP adapter may run beside a harness such as Codex.

The stdio process is **not** a credential broker. It receives only enough run-scoped authority to call the canonical ThinkPixelTG API and must not receive downstream provider credentials.

Suggested shape:

```text
Codex
  |
  | stdio MCP
  v
thinkpixeltg-mcp
  |
  | HTTPS + run-scoped token / workload identity
  v
ThinkPixelTG
```

---

## 11. Authorization decision contract

The `Authorizer` port consumes a normalized, bounded request and returns a typed decision.

Suggested internal input:

```json
{
  "request_id": "...",
  "tenant_id": "...",
  "subject": {"id": "..."},
  "actor": {
    "agent_id": "incident-investigator",
    "agent_version": "sha256:...",
    "workload_id": "spiffe://..."
  },
  "run": {"id": "..."},
  "action": "tool.invoke",
  "tool": {
    "id": "kubernetes.deployment.restart",
    "version": 2,
    "risk_class": "high",
    "side_effect": "write"
  },
  "resource": {
    "cluster": "production",
    "namespace": "payments",
    "deployment": "api"
  },
  "arguments_digest": "sha256:...",
  "security_state": {
    "authorization_age_ms": 0,
    "revocation_checkpoint": "..."
  }
}
```

Suggested decision:

```json
{
  "decision_id": "...",
  "effect": "allow",
  "reason_codes": ["tool.invoke.allowed"],
  "constraints": {},
  "obligations": [],
  "approval": null,
  "decision_ttl_seconds": 5,
  "policy_version": "...",
  "revocation_epoch": "..."
}
```

Supported effects:

```text
allow
deny
require_approval
```

Malformed/unknown effects fail closed.

### 11.1 Constraint narrowing

An `allow` decision may narrow requested behavior.

Examples:

- repository must equal `payments`;
- maximum messages sent in this run is 3;
- Kubernetes namespace restricted to `staging`;
- result fields must exclude sensitive attributes;
- specific credential binding must be used;
- maximum response size reduced;
- operation allowed only before a timestamp.

ThinkPixelTG enforces returned constraints locally before downstream execution.

The authorizer cannot expand the immutable safety bounds of the tool definition.

### 11.2 Freshness

Protected writes require authorization/revocation state within the configured security freshness contract.

Default posture should align with ThinkPixelAG:

- high-risk writes: live or explicitly strongly fresh authoritative decision;
- normal writes: bounded freshness, initially no more than 30 seconds unless policy chooses stricter;
- sensitive reads: bounded freshness, initially no more than 60 seconds;
- low-risk reads may use a short decision cache only when revocation epochs/checkpoints are valid.

Exact defaults are finalized jointly with the ThinkPixelAG integration contract.

---

## 12. Action-scoped approvals

Approvals authorize an operation, not an agent session.

### 12.1 Approval binding

A valid approval must be bound to at least:

```text
tenant_id
run_id
tool_id
tool_version
arguments_digest
resource_projection
approval_policy/action class
approver identity
issued_at
expires_at
approval_id
```

Optional policy may bind additional context such as the exact authorization decision/policy version.

### 12.2 Approval lifecycle

When authorization returns `require_approval`:

1. ThinkPixelTG persists the invocation in `WAITING_FOR_APPROVAL`.
2. ThinkPixelTG requests or references an approval object in ThinkPixelAG.
3. The caller receives a non-executed result indicating approval is required.
4. The run/harness may pause or release its worker.
5. After approval, the caller retries the **same** logical `tool_call_id` and final arguments.
6. ThinkPixelTG loads the existing invocation.
7. ThinkPixelTG validates the approval is current and matches the exact digest/resource/tool/run.
8. ThinkPixelTG re-checks current authorization/revocation state.
9. Only then may execution proceed.

Changing the arguments, tool version, target resource, or run invalidates the approval.

### 12.3 One approval, one governed action

Default semantics are single-use for a logical `tool_call_id`.

A policy may define a bounded multi-use approval only if the approved object explicitly represents that capability and its limits. Do not infer reusable approval from a user clicking “approve” once.

### 12.4 Approval expiry and revocation

An unexpired approval does not override:

- run cancellation;
- agent/tool revocation;
- principal revocation;
- policy change when policy requires re-evaluation;
- credential revocation;
- a changed arguments digest.

---

## 13. Credential broker

The credential broker is one of the most security-sensitive modules.

### 13.1 Credential binding

A `CredentialBinding` maps governed context to a provider strategy without exposing secret material.

Example:

```yaml
id: github-prod-delegated
connector_type: github
mode: oauth_token_exchange
provider: corp-idp
resource: https://api.github.com/
secret_ref: null
allowed_tenants: [acme]
rotation_policy: provider-managed
```

Or:

```yaml
id: slack-bot-incident
connector_type: slack
mode: secret_manager
provider: vault
secret_ref: kv/thinkpixel/slack/incident-bot
```

### 13.2 Credential selection

Selection uses only trusted inputs:

- tool version;
- authorization constraints/obligations;
- tenant;
- governed subject/agent/run context;
- connector instance configuration.

Invocation arguments must not be able to select arbitrary secret references, IAM roles, token audiences, or credential providers.

### 13.3 Credential provider interface

Suggested Go port:

```go
type CredentialProvider interface {
    Resolve(ctx context.Context, req CredentialRequest) (Credential, error)
}
```

A returned `Credential` should expose the smallest possible application interface and support zeroization/release hooks where practical.

Avoid a generic `map[string]string` secret bag when typed credentials can reduce accidental leakage.

### 13.4 Short-lived authority

Prefer:

- STS credentials;
- workload federation;
- OAuth token exchange;
- client credentials with narrow resource scopes;
- short-lived Kubernetes tokens;
- signed requests generated inside the broker/connector.

Static long-lived secrets are compatibility fallbacks, not the desired default.

### 13.5 Token caching

Short-lived downstream tokens may be cached only when the cache key includes the authority-defining dimensions, such as:

```text
tenant
subject/delegation identity
credential binding
resource/audience
scope set
policy/revocation epoch when relevant
```

Never share a user-delegated token across principals because the connector/tool is the same.

Refresh before expiry with bounded jitter. Never use an expired credential because the credential service is unavailable.

### 13.6 Secret redaction

Secrets must be structurally excluded or redacted from:

- logs;
- traces;
- metrics labels;
- error bodies;
- audit records;
- PostgreSQL invocation records;
- MCP responses;
- GR evaluation payloads unless a policy explicitly requires secret detection over a derived value;
- panic dumps.

Unit and integration tests must use canary secrets and assert that they never appear in captured telemetry/output.

---

## 14. Connector framework

### 14.1 Connector interface

Suggested domain-facing interface:

```go
type Connector interface {
    Type() string
    Capabilities() ConnectorCapabilities
    Execute(ctx context.Context, req ConnectorRequest) (ConnectorResult, error)
    Reconcile(ctx context.Context, req ReconcileRequest) (ReconcileResult, error)
}
```

Not every connector supports reconciliation. Capabilities must state this explicitly.

### 14.2 Connector request

Connector code receives:

- already validated normalized arguments;
- tool definition/version;
- derived credential object;
- logical `tool_call_id`;
- attempt metadata;
- downstream idempotency key when applicable;
- deadline;
- trace context;
- trusted constraints.

Connector code must not perform independent authorization based on untrusted harness fields.

### 14.3 Connector result

Return a typed envelope:

```text
status:
    succeeded
    failed
    ambiguous

structured_output
content_type
provider_request_id
downstream_resource_version
retry_after
safe_error
usage
```

The connector should preserve enough evidence to reconcile ambiguous writes without persisting secrets or excessive raw payloads.

### 14.4 Reference connectors

The RC should include:

1. `mock` connector for deterministic tests;
2. one real read/write SaaS connector, preferably GitHub;
3. one infrastructure connector, preferably Kubernetes, after the framework is proven.

Slack/Jira/etc. can follow once the connector contract is stable.

The first real connector must include both a read-only operation and a side-effecting operation so authorization, credential brokerage, idempotency, and approval paths are exercised end to end.

### 14.5 No arbitrary URL connector in the RC

A generic connector accepting a caller-controlled `url` is an SSRF primitive.

If an HTTP-template connector is introduced later:

- base hosts are administrator-controlled and immutable per tool version;
- scheme is HTTPS unless explicitly trusted/internal;
- DNS/IP resolution is policy constrained;
- redirects are disabled by default or revalidated hop by hop;
- private/link-local/metadata ranges are blocked unless explicitly configured for that connector;
- request headers are allowlisted;
- credential headers cannot be overridden by invocation arguments;
- body/query templates are schema constrained;
- response bytes and decompression ratio are bounded.

### 14.6 Kubernetes connector

A Kubernetes connector requires special care:

- use a dedicated workload identity/service account or impersonation mechanism;
- do not accept arbitrary kubeconfig from the harness;
- cluster identity comes from trusted connector configuration;
- namespace/name/resource are projected for authorization;
- server-side dry-run may be used for compatible operations where useful;
- watch/exec/attach/port-forward are out of initial scope unless separately designed;
- arbitrary `kubectl` command execution is not a tool implementation.

---

## 15. Idempotency and distributed side-effect semantics

This is a core product feature, not an implementation detail.

### 15.1 Logical operation identity

Every invocation uses a persistent `tool_call_id` supplied by the runtime/harness adapter or generated before the logical operation is first attempted.

Retries of the same logical operation reuse the same ID.

### 15.2 Request replay rules

When ThinkPixelTG receives an existing `(tenant, run, tool_call_id)`:

- same tool version + same canonical arguments digest:
  - return the established terminal result when available;
  - return current waiting/processing state when not terminal;
  - continue reconciliation only through the defined state machine;
- different tool version or digest:
  - return `409 Conflict` / typed `tool_call_id_conflict`;
  - never reinterpret it as a second operation.

### 15.3 Retry classes

#### `safe`

Operation is read-only or contractually safe to repeat.

Gateway may retry transient failures within strict attempt/deadline limits.

#### `downstream_idempotency`

Downstream API accepts an idempotency key.

ThinkPixelTG passes a deterministic key derived from or equal to the logical `tool_call_id` according to connector rules.

#### `gateway_deduplicated`

ThinkPixelTG can prove completion from its own durable records before retrying, but downstream has no native key.

This does **not** solve the crash-after-downstream-success-before-commit case and therefore must not be claimed when ambiguity remains possible.

#### `reconcile_before_retry`

After an ambiguous failure, the connector queries downstream state using a deterministic external identifier or operation marker before deciding whether another attempt is safe.

#### `at_least_once_accepted`

Duplicates are possible and explicitly accepted by the published tool contract/policy.

This classification should be rare and clearly surfaced to authorization/approval UX.

#### `non_retryable`

An ambiguous result enters `MANUAL_REVIEW`/terminal ambiguous state. Automatic replay is prohibited.

### 15.4 Ambiguous outcomes

A timeout does not mean a write failed.

Connectors classify errors into:

- definitely not sent;
- sent with confirmed failure;
- confirmed success;
- ambiguous after possible side effect.

Only the first two categories are automatically retryable according to tool policy.

### 15.5 Connector-specific idempotency evidence

Each side-effecting tool version documents:

- idempotency mechanism;
- downstream idempotency-key field/header;
- retention window if downstream dedupe expires;
- reconciliation query;
- duplicate-risk behavior;
- compensating action if one exists.

A tool cannot be published as `safe` merely because the connector implementation hopes retries are unlikely.

---

## 16. ThinkPixelGR integration

### 16.1 `pre_tool`

Before credential resolution/downstream execution, ThinkPixelTG sends a bounded evaluation containing:

- tenant/run correlation identifiers;
- tool ID/version;
- normalized tool arguments or policy-approved representation;
- side-effect/risk metadata;
- resource projection;
- content trust labels where available;
- configured GR profile/policies derived from platform/tenant/tool policy.

GR may return:

```text
allow
block
redact
rewrite
monitor
require_review
```

TG maps `require_review` to a non-executing path; it does not interpret GR review as authorization approval unless an explicit integration contract maps it to ThinkPixelAG approval creation.

### 16.2 `post_tool`

Before returning tool output to the harness/model, TG may evaluate:

- structured output;
- textual content;
- external/open-world trust label;
- content size/classification metadata.

Use this path for:

- prompt-injection indicators in retrieved/open-world content;
- secret/PII redaction;
- policy-based output blocking;
- structured-output validation.

### 16.3 Fail behavior

Mandatory GR policies have explicit fail-closed behavior.

Observational policies must not block execution because a detector is unavailable.

The effective GR policy/version/evaluation IDs are captured in invocation evidence.

### 16.4 No credential exposure to GR

GR receives normalized business arguments/results, not downstream bearer tokens or secret-manager material.

If a tool legitimately transports secret data, its policy must define the minimum representation necessary for GR and the evidence store must still avoid plaintext retention by default.

---

## 17. MCP adapter

### 17.1 Scope

The first MCP implementation supports the minimum server features necessary for enterprise tool use:

- stateless per-request protocol/client/capability metadata;
- optional `server/discover` when required by target clients;
- ping as required by SDK/conformance needs;
- `tools/list`;
- `tools/call`;
- pagination;
- revision-defined deterministic list ordering and cache hints;
- Streamable HTTP transport;
- stdio adapter as a separate binary if needed for harness compatibility.

Do not initially expose:

- prompts;
- resources;
- sampling;
- elicitation;
- roots;
- arbitrary proxying of upstream MCP servers;
- experimental task behavior unless a concrete harness requirement justifies it.

### 17.2 Protocol revision support

Pin one current stable MCP protocol revision in tests and document supported compatibility revisions.

Do not silently track an unversioned “latest” schema in production builds.

The selected baseline is `2026-07-28`, with explicit conformance tests for the
clients targeted by the project. Its core is stateless: it removes the
`initialize`/`initialized` handshake and protocol session identifier, carries
version/client/capability metadata per request, and optionally exposes
`server/discover`.

### 17.3 Tool discovery

`tools/list` returns only tools that are both:

- published/enabled for the tenant/environment; and
- discoverable under the current governed context.

Discovery filtering is not a substitute for call-time authorization. Every `tools/call` is authorized again against current state.

Ordering must be deterministic for a stable underlying tool set to improve
client/model prompt caching. Revision-defined `ttlMs` and `cacheScope` must reflect
the authenticated catalog visibility; cache metadata cannot broaden discovery.

### 17.4 MCP tool mapping

MCP fields derive from trusted tool definitions:

```text
name                <- stable ThinkPixelTG tool ID
title               <- tool title
description         <- reviewed tool description
inputSchema         <- published JSON Schema
outputSchema        <- published output schema when available
annotations         <- derived trusted hints
execution           <- derived task-support hint if implemented
```

Derived annotations:

- `readOnlyHint = true` only for `side_effect=read`;
- `destructiveHint = true` for destructive operations;
- `idempotentHint = true` only when repeat semantics genuinely support it;
- `openWorldHint` from the trusted tool definition.

### 17.5 MCP `tools/call`

The MCP adapter translates a call into the canonical invocation application service.

MCP JSON-RPC request IDs are **not** the logical `tool_call_id` unless a documented mapping guarantees stability across harness retries.

The adapter should accept/extract a stable logical call ID through the documented
TG `_meta`/runtime integration where interoperable, or generate and return one as
explicit continuation data. It must not rely on hidden protocol session mapping.
For Codex integration, define and test the exact logical-ID propagation mechanism
rather than assuming the JSON-RPC ID or MRTR `requestState` is sufficient.

### 17.6 Error mapping

Use MCP protocol-level errors for protocol problems such as:

- unknown method;
- malformed JSON-RPC;
- unsupported protocol version;
- unknown tool.

Represent normal tool execution failures as tool results with `isError=true` and bounded safe content so the model can recover when appropriate.

Authorization denials and approval requirements must be represented consistently and without leaking policy internals.

### 17.7 Streamable HTTP security

Implement:

- stateless request/response semantics required by revision `2026-07-28`;
- required `MCP-Protocol-Version`, `Mcp-Method`, and applicable `Mcp-Name` headers,
  with rejection when routing headers and JSON-RPC body disagree;
- per-request client identity and capabilities in the revision-defined `_meta`;
- content-type/Accept validation;
- origin validation;
- authentication on every request;
- request/body limits;
- per-principal/global concurrency limits;
- optional subscription-stream limits/timeouts when implemented;
- bounded, integrity-protected MRTR continuation state when implemented, never
  treated as authentication, authorization, approval, or logical-call identity;
- graceful disconnect handling.

Bind local development endpoints to loopback by default.

---

## 18. Canonical REST API

OpenAPI 3.1 is authoritative and must define schemas, limits, authentication, idempotency, errors, and concurrency behavior before handlers are considered complete.

### 18.1 Discovery API

```text
GET /v1/tools
GET /v1/tools/{tool_id}
```

Responses are tenant/governance filtered and cursor paginated.

A caller may request a particular published version when permitted; otherwise the gateway returns/resolves the version defined by the governed run/tool contract.

### 18.2 Invocation API

Preferred shape:

```text
POST /v1/tool-calls
GET  /v1/tool-calls/{tool_call_id}
```

Example request:

```json
{
  "tool_call_id": "019c...",
  "tool": "github.pull.comment",
  "version": 3,
  "arguments": {
    "repository": "payments",
    "pull_request": 842,
    "text": "Looks good after the retry fix."
  }
}
```

The body does not establish tenant/run/user/agent identity. Those come from authenticated context.

Example successful response:

```json
{
  "tool_call_id": "019c...",
  "invocation_id": "019c...",
  "state": "succeeded",
  "tool": "github.pull.comment",
  "version": 3,
  "result": {
    "structured": {
      "comment_id": 123456
    }
  },
  "evidence": {
    "decision_id": "...",
    "provider_request_id": "..."
  }
}
```

Example approval-required response:

```json
{
  "tool_call_id": "019c...",
  "invocation_id": "019c...",
  "state": "waiting_for_approval",
  "approval": {
    "approval_request_id": "019c...",
    "expires_at": "2026-08-22T20:00:00Z"
  }
}
```

Use an HTTP status contract that clearly distinguishes:

- completed logical result;
- accepted/waiting state;
- authorization denial;
- logical ID conflict;
- unsafe ambiguous replay;
- transport/internal failure.

### 18.3 Administrative API

A trusted/admin route group manages:

- tool creation/version publication;
- enable/disable/revoke;
- connector instances;
- credential binding metadata/references;
- tenant exposure assignments;
- test/validation of connectors without exposing secrets;
- reconciliation/manual review actions for ambiguous invocations;
- evidence inspection subject to policy.

Administrative mutation uses strong authorization, idempotency keys, audit records, and transactional outbox writes.

### 18.4 Operational endpoints

```text
GET /livez
GET /readyz
GET /metrics
```

They expose no tenant data.

Readiness includes only dependencies required to satisfy the configured safety contract. A connector outage should not necessarily make the whole gateway unready, but an unavailable authoritative database, mandatory authorization integration, or required security state may.

---

## 19. Tool publication and administration

### 19.1 Publication workflow

A tool version is not executable merely because a connector registers it at runtime.

Publication should require:

1. syntactically valid stable tool name/version;
2. valid bounded input/output JSON Schemas;
3. known connector type and operation;
4. valid resource projection;
5. explicit side-effect/risk classification;
6. explicit retry/idempotency semantics;
7. explicit credential-binding strategy;
8. request/response/deadline limits;
9. GR policy/profile mapping where required;
10. administrative authorization;
11. evidence/audit record;
12. immutable persisted version.

### 19.2 Tool descriptions are security-relevant reviewed content

Tool descriptions are inserted into model context by MCP/harnesses. Therefore they are reviewed artifacts, not arbitrary downstream strings.

They must not be dynamically copied from untrusted APIs without validation/review.

Description changes create a new tool version when they can materially alter model behavior.

### 19.3 Connector instance separation

A logical tool version may reference a connector *type/operation* while environment/tenant policy selects an approved `ConnectorInstance`.

Example:

```text
github.pull.comment@3
    -> connector type: github
    -> instance: github-enterprise-eu
```

The harness cannot choose `github-enterprise-prod-admin` by passing a connector name in arguments.

---

## 20. Database design and migrations

### 20.1 Core tables

The schema should include tables equivalent to:

```text
tools
tool_versions
tool_tenant_exposure
connector_instances
credential_bindings
invocations
invocation_attempts
invocation_events
invocation_results
approval_bindings
usage_entries
idempotency_records
audit_events
outbox_messages
authorization_checkpoints
```

Names may change, but responsibilities must remain explicit.

### 20.2 Tenant scoping

Every tenant-owned row contains `tenant_id` and all repositories enforce tenant scoping.

Use tenant-prefixed indexes for expected access paths.

Evaluate PostgreSQL Row Level Security as defense in depth after repository-level isolation is proven.

### 20.3 Invocation uniqueness

Enforce with database constraints where possible:

```text
UNIQUE (tenant_id, run_id, tool_call_id)
```

Store canonical argument digest and reject conflicting reuses transactionally.

### 20.4 Attempt sequencing

Attempts are append-only and unique within an invocation:

```text
UNIQUE (invocation_id, attempt_number)
```

A database transaction claims the right to create the next attempt so two gateway replicas cannot concurrently execute the same non-parallelizable logical operation.

### 20.5 Result storage

Do not assume full raw tool results belong in PostgreSQL.

Persist:

- bounded structured results when safe/useful;
- hashes/digests;
- content classification;
- provider IDs;
- replay-safe response subset;
- external artifact references for large results.

Sensitive or large raw outputs may be stored in an encrypted object/evidence store with tenant-specific retention policy.

### 20.6 Outbox

State changes and audit/outbox messages that must correspond are committed in one PostgreSQL transaction.

Publishing is at-least-once with stable event IDs.

### 20.7 Migration rules

- migrations run as an explicit job, not from every API replica;
- released migrations are immutable;
- use expand/migrate/contract for rolling compatibility;
- test migration from empty database and from the previous release;
- rehearse restore and forward recovery before RC.

---

## 21. Trusted metering and ThinkPixelAG resource integration

ThinkPixelTG emits trusted usage events after it has authoritative knowledge of execution.

### 21.1 Usage event

Suggested event:

```json
{
  "source": "thinkpixeltg",
  "event_id": "019c...",
  "tenant_id": "...",
  "run_id": "...",
  "tool_call_id": "...",
  "tool": "github.pull.comment",
  "tool_version": 3,
  "dimensions": [
    {"name": "tool_calls", "quantity": "1", "unit": "call"}
  ],
  "outcome": "succeeded",
  "occurred_at": "..."
}
```

If a connector has billable units, those use exact fixed-scale quantities.

### 21.2 When to meter

Define per dimension whether consumption occurs on:

- accepted invocation;
- downstream attempt;
- confirmed successful side effect;
- downstream provider-reported usage.

Do not silently report zero usage when provider accounting is unknown.

### 21.3 Budget rejection

ThinkPixelAG remains authoritative for resource envelopes.

If AG indicates the run has exhausted the relevant tool-call budget, TG does not execute the operation.

TG may maintain fast local counters only as an enforcement optimization, not as an independent source of budget truth.

### 21.4 Deduplication

Usage events carry unique producer event IDs and ThinkPixelAG must be able to apply them idempotently.

TG likewise records whether trusted usage publication has been acknowledged/reconciled.

---

## 22. Evidence, audit, and observability

### 22.1 Evidence goals

The evidence system should answer:

```text
Who requested this operation?
Which agent/version/run acted?
Which exact tool version and arguments digest were used?
Which policy decision allowed/denied it?
Was a human approval required and which exact action was approved?
Which credential binding was used?
Was the downstream operation actually attempted?
What downstream request ID/result was observed?
What guardrails evaluated the request/result?
Was the call retried or reconciled?
Which resource usage was charged?
```

It does not need to store private model chain-of-thought.

### 22.2 Audit event classes

At minimum:

- tool version published/disabled/revoked;
- connector instance changed;
- credential binding changed;
- tool invocation received;
- invocation denied;
- approval required/validated/rejected;
- pre-tool GR decision;
- downstream attempt started;
- downstream attempt confirmed success/failure/ambiguous;
- reconciliation performed;
- post-tool GR decision;
- invocation result released;
- usage event emitted/reconciled;
- administrative manual resolution.

### 22.3 Logging

Structured logs include stable IDs and safe metadata.

Do not log:

- authorization headers;
- cookies;
- credentials;
- raw secret-manager responses;
- full arbitrary arguments/results by default;
- raw approval descriptions containing sensitive data;
- unbounded downstream error bodies.

### 22.4 Metrics

Required metrics include:

- request rate/error/latency by API class;
- tool invocation rate by tool/side-effect class using bounded-cardinality labels;
- authorization allow/deny/approval counts;
- authorization latency/error/freshness;
- GR pre/post latency/decision/failure;
- connector latency/outcome/retry/ambiguous counts;
- credential resolution latency/errors;
- idempotent replay/conflict counts;
- approval wait/expiry counts;
- database pool saturation;
- outbox lag;
- usage publication lag;
- MCP active requests/subscription connections;
- response-size limit hits;
- rate-limit/concurrency-limit denials;
- readiness degradation reasons.

Never use `run_id`, `tool_call_id`, user IDs, repositories, or arbitrary resource names as Prometheus labels.

### 22.5 Tracing

Use OpenTelemetry.

Suggested spans:

```text
http.request
mcp.request
invocation.resolve
invocation.authorize
gr.pre_tool
credential.resolve
connector.execute
connector.reconcile
gr.post_tool
usage.publish
```

Trace attributes are bounded and redacted.

---

## 23. Limits, rate control, and resource protection

### 23.1 Request limits

Global and per-tool bounds include:

- maximum HTTP header bytes;
- maximum request body bytes;
- maximum JSON nesting depth;
- maximum argument properties/array entries;
- maximum string length;
- maximum MCP request, continuation, and batch behavior according to the selected revision;
- maximum result bytes;
- maximum decompressed response bytes;
- maximum connector duration.

### 23.2 Concurrency

Support bounded concurrency at:

- process-wide execution;
- tenant;
- connector instance;
- tool;
- credential binding where downstream quotas require it.

Never create an unbounded goroutine per incoming request.

### 23.3 Rate limiting

Rate limiting is an operational protection distinct from AG resource governance.

Use local/Valkey token buckets or equivalent for abuse and downstream quota protection.

Rate-limit failure behavior is explicit per endpoint class; privileged write paths must not become unlimited because Valkey is down.

### 23.4 Backpressure

When execution capacity is exhausted:

- reject early with a typed retriable error, or
- use a bounded queue with explicit maximum wait.

Do not allow unbounded in-memory queues.

---

## 24. Downstream network security

### 24.1 Egress allowlisting

Connector network destinations come from trusted configuration.

Kubernetes NetworkPolicy/firewall/service-mesh controls should restrict ThinkPixelTG egress to:

- required identity/secret systems;
- ThinkPixelAG;
- ThinkPixelGR;
- database/cache/evidence dependencies;
- declared downstream enterprise APIs.

The agent harness should separately be denied direct access to those governed downstream APIs where TG is intended to be the enforcement boundary.

### 24.2 TLS

- verify server certificates;
- support custom enterprise CA bundles through explicit configuration;
- no global insecure-skip-verify mode in production;
- connector-specific mTLS is supported through credential providers;
- certificate/key material is never logged.

### 24.3 Redirects

HTTP connectors disable redirects by default unless the connector contract requires them.

Allowed redirects are revalidated for scheme/host and must not carry credentials to a different origin unless explicitly safe and specified.

### 24.4 DNS and metadata protection

For connectors that resolve caller-influenced hostnames in future versions, protect against:

- loopback;
- link-local;
- cloud metadata ranges;
- private ranges where not explicitly approved;
- DNS rebinding between validation and connection.

Prefer no caller-influenced hostnames in the RC.

---

## 25. Go implementation approach

Target a supported Go release pinned in `go.mod` and CI. Prefer the standard library where practical and isolate consequential dependencies behind small interfaces.

### 25.1 Suggested repository layout

```text
/
├── cmd/
│   ├── thinkpixeltg/
│   │   └── main.go
│   ├── thinkpixeltg-mcp/
│   │   └── main.go
│   └── migrate/
│       └── main.go
├── internal/
│   ├── domain/
│   │   ├── tool.go
│   │   ├── invocation.go
│   │   ├── approval.go
│   │   ├── credential.go
│   │   └── errors.go
│   ├── app/
│   │   ├── discovery.go
│   │   ├── invoke.go
│   │   ├── reconcile.go
│   │   └── administration.go
│   ├── ports/
│   │   ├── authorizer.go
│   │   ├── guardrails.go
│   │   ├── credentials.go
│   │   ├── connector.go
│   │   ├── repositories.go
│   │   ├── evidence.go
│   │   ├── usage.go
│   │   ├── clock.go
│   │   └── ids.go
│   ├── adapters/
│   │   ├── http/
│   │   ├── mcp/
│   │   ├── postgres/
│   │   ├── valkey/
│   │   ├── auth/
│   │   ├── thinkpixelag/
│   │   ├── thinkpixelgr/
│   │   ├── credentials/
│   │   │   ├── env/
│   │   │   ├── kubernetes/
│   │   │   ├── vault/
│   │   │   └── oauth/
│   │   ├── connectors/
│   │   │   ├── mock/
│   │   │   ├── github/
│   │   │   └── kubernetes/
│   │   └── evidence/
│   ├── canonicaljson/
│   ├── schema/
│   ├── telemetry/
│   └── config/
├── api/
│   └── openapi.yaml
├── deployments/
│   ├── kubernetes/
│   └── compose/
├── test/
│   ├── integration/
│   ├── conformance/
│   ├── security/
│   └── fixtures/
├── docs/
│   ├── contracts/
│   ├── adr/
│   └── threat-model.md
├── Makefile
├── go.mod
├── PLAN.md
└── TODO.md
```

The exact folder tree may evolve, but domain/application code must not depend directly on MCP, HTTP, PostgreSQL, ThinkPixelAG, or provider SDK types.

### 25.2 Core interfaces

Keep ports narrow and explicit.

Example:

```go
type Authorizer interface {
    AuthorizeToolInvocation(ctx context.Context, req AuthorizationRequest) (AuthorizationDecision, error)
}

type Guardrails interface {
    Evaluate(ctx context.Context, req GuardrailRequest) (GuardrailDecision, error)
}

type CredentialBroker interface {
    Resolve(ctx context.Context, req CredentialRequest) (Credential, error)
}

type ConnectorRegistry interface {
    Resolve(connectorType string) (Connector, error)
}
```

Do not create a generic service-locator interface that hides dependencies and makes tests ambiguous.

### 25.3 Context and deadlines

- propagate `context.Context` to every I/O boundary;
- derive connector deadline from the minimum of caller deadline, tool limit, and policy constraint;
- do not retry after context cancellation;
- use fresh bounded contexts for shutdown/evidence flush where appropriate.

### 25.4 Error discipline

Domain errors use closed machine codes such as:

```text
authentication_required
invalid_run_context
tool_not_found
tool_version_disabled
arguments_invalid
tool_call_id_conflict
authorization_denied
approval_required
approval_expired
approval_mismatch
guardrail_blocked
credential_unavailable
connector_unavailable
downstream_rejected
downstream_ambiguous
result_blocked
rate_limited
budget_exhausted
```

Do not expose raw provider/SQL/secret-manager errors directly to clients.

### 25.5 Dependency policy

Prefer mature libraries with narrow scope.

Consequential dependencies require ADR/evidence review, especially for:

- MCP SDK/protocol implementation;
- JSON Schema;
- OIDC/JWT;
- PostgreSQL driver/query layer;
- secret-manager SDKs;
- Kubernetes client;
- OAuth;
- OpenTelemetry.

Avoid dependency-heavy frameworks that materially undermine the static lightweight service goal without clear benefit.

### 25.6 Makefile contract

The repository-root Makefile is the stable developer/CI surface.

Expected targets include:

```text
make generate
make fmt
make lint
make vet
make test
make test-race
make test-integration
make test-e2e
make test-security
make test-mcp-conformance
make openapi-check
make migration-test
make build
make image
make verify
```

`make verify` must be runnable from a clean checkout with documented local dependencies and must fail on generated-contract drift.

---

## 26. Configuration

Use explicit typed configuration with startup validation.

Configuration categories:

- HTTP listener/timeouts/limits;
- MCP protocol revisions/transports/per-request metadata and concurrency limits;
- database;
- optional Valkey;
- authentication issuers/audiences/JWKS;
- ThinkPixelAG endpoint/security/freshness;
- ThinkPixelGR endpoint/profile mappings/timeouts;
- credential provider definitions;
- connector instances;
- evidence sink;
- telemetry;
- rate/concurrency limits;
- feature gates.

### 26.1 Configuration safety

- unknown configuration keys should fail in strict production mode;
- secrets use secret references/env injection, not committed YAML;
- log effective non-secret configuration at startup with redaction;
- validate duplicate tool/connector/binding IDs;
- validate all configured durations/limits are positive and sane;
- reject production startup with development auth/credential modes unless an explicit break-glass flag is present and clearly logged.

### 26.2 Dynamic configuration

Do not require hot reload in the first vertical slice.

Tool versions, connector instances, and credential bindings that are authoritative runtime state should live in PostgreSQL and be changed through audited APIs.

Process-level security configuration may require restart initially.

---

## 27. Kubernetes and container design

### 27.1 Image

Use a pinned Go builder and minimal non-root runtime image.

Requirements:

- reproducible build flags;
- embedded version/commit/build metadata;
- CA certificates;
- no shell requirement;
- non-root numeric UID/GID;
- read-only root filesystem;
- dropped Linux capabilities;
- seccomp RuntimeDefault;
- bounded writable temp volume only if required;
- clean SIGTERM handling.

### 27.2 Kubernetes objects

Provide:

- Deployment;
- Service;
- ServiceAccount;
- ConfigMap;
- secret references, not plaintext Secret manifests in source;
- migration Job;
- PodDisruptionBudget;
- NetworkPolicy;
- optional HPA;
- optional ServiceMonitor/PodMonitor.

### 27.3 Egress policy

The deployment documentation must demonstrate the security architecture, not merely the TG pod.

A reference namespace should include NetworkPolicy showing:

```text
Harness worker:
    allow -> ThinkPixelTG, ThinkPixelLLMGW, AG/runtime dependencies
    deny  -> governed GitHub/Slack/Kubernetes/etc. APIs directly

ThinkPixelTG:
    allow -> AG, GR, DB/cache/secret services, approved downstream APIs
```

Without this or an equivalent network control, TG is not the exclusive enforcement boundary.

### 27.4 Probes

`/livez` checks process health only.

`/readyz` considers:

- PostgreSQL connectivity;
- loaded valid security configuration;
- required Authorizer/AG connectivity or acceptable cached freshness;
- mandatory GR dependency when configured fail closed;
- ability to satisfy the configured credential/security freshness contract.

Do not mark the process dead because GitHub has a transient outage.

### 27.5 Supply chain

CI/release should produce:

- OCI image by digest;
- SBOM;
- vulnerability scan;
- checksums;
- provenance/signature hooks;
- license report;
- immutable dependency/action pins where practical.

---

## 28. Testing strategy

### 28.1 Unit tests

Cover:

- tool/version validation;
- state-machine transitions;
- canonical JSON/digests;
- duplicate-key rejection;
- resource projection;
- typed authorization decisions;
- approval matching/expiry;
- GR decision composition;
- credential selection;
- retry classification;
- MCP mapping;
- error mapping;
- cursor signing;
- limits.

### 28.2 Property and fuzz tests

Fuzz:

- JSON parsing/canonicalization;
- schema validation boundaries;
- resource projection;
- MCP message decoding;
- HTTP header/body parser boundaries;
- tool-name validation;
- provider response parsers.

Property tests must prove:

```text
same normalized invocation => same digest
semantically security-relevant change => different digest
same tool_call_id + different digest => conflict
terminal invocation cannot execute again
approval for digest A cannot authorize digest B
```

### 28.3 Repository integration tests

Run against pinned real PostgreSQL.

Cover:

- migration empty/upgrades;
- transaction rollback;
- tenant isolation;
- concurrent logical invocation creation;
- attempt claiming;
- idempotent replay;
- outbox claiming/retry/dead-letter;
- optimistic/concurrency state updates.

### 28.4 Connector tests

Each connector has:

- hermetic fake-server tests;
- request construction tests;
- auth header/signing tests without secret logging;
- timeout tests;
- response-size tests;
- redirect behavior;
- error classification;
- idempotency propagation;
- reconciliation tests where supported.

Live-provider tests are separate qualification gates and never required for ordinary pull requests without secrets.

### 28.5 Authorization tests

Cover:

- allow/deny/approval;
- malformed decision;
- stale decision;
- AG outage;
- revoked run/tool/principal;
- constraint narrowing;
- cross-tenant substitution;
- attempts to override resource projection;
- approval after policy/revocation change.

### 28.6 GR tests

Cover:

- pre-tool block;
- pre-tool transform and reauthorization;
- post-tool redact/block;
- mandatory GR outage;
- observational detector outage;
- secret non-exposure.

### 28.7 MCP conformance tests

Test against the pinned MCP revision and representative clients.

Cover:

- stateless per-request protocol version, client identity, and capability metadata;
- optional `server/discover`;
- required routing headers and header/body mismatch rejection;
- authentication;
- origin validation;
- `tools/list` pagination/determinism/cache hints;
- `tools/call` success/tool error;
- approval-required mapping;
- protocol errors;
- cross-context MRTR continuation replay rejection when MRTR is supported;
- Streamable HTTP disconnect behavior;
- stdio framing if adapter is shipped.

Add a Codex-specific smoke/conformance test when practical.

### 28.8 Security tests

Adversarial cases include:

- bearer token with wrong audience;
- token passthrough assertions;
- tenant/run ID body/header forgery;
- approval digest substitution;
- approval replay;
- MCP DNS rebinding/origin attack;
- SSRF attempts;
- redirects to metadata service;
- CRLF/header injection;
- malicious downstream error bodies;
- oversized/compression-bomb responses;
- secret canary leakage into logs/traces/audit/MCP;
- path traversal in artifact handling;
- request smuggling edge cases supported by Go HTTP stack/proxy deployment;
- cache poisoning;
- privilege escalation through connector selection;
- unsafe dev-mode startup in production config.

### 28.9 Resilience tests

Exercise:

- gateway crash before downstream send;
- crash after send before durable success record;
- database outage;
- AG latency/outage;
- GR latency/outage;
- secret provider outage;
- Valkey loss;
- downstream rate limiting;
- ambiguous timeout;
- rolling restart;
- stale revocation state;
- outbox backlog/replay;
- duplicate client retries across replicas.

### 28.10 End-to-end scenarios

Minimum E2E flows:

1. discover allowed read tool -> authorize -> execute -> GR -> result -> meter;
2. unauthorized tool -> no credential resolution -> no downstream traffic;
3. side-effect tool -> action approval required -> approve -> retry same `tool_call_id` -> execute once;
4. approval args changed -> denied;
5. downstream native idempotency -> crash/retry -> one side effect;
6. ambiguous non-idempotent write -> reconcile/manual state, no blind replay;
7. post-tool GR redacts open-world injection/secret before model receives it;
8. run/tool revocation -> cached allow invalidated/fails freshness;
9. harness network policy cannot reach downstream API directly.

---

## 29. Performance and SLO targets

Exact targets are finalized after Phase 1 baseline profiling, but the architecture should be designed around these principles.

### 29.1 Latency budget

For a warm, cacheable, read-only invocation, TG overhead excluding downstream API latency should target low tens of milliseconds in-cluster.

Measure separately:

- ingress/auth;
- DB invocation claim;
- AG authorization;
- GR evaluation;
- credential resolution;
- connector overhead;
- evidence persistence.

Do not optimize by bypassing security checks.

### 29.2 Availability

The gateway is critical infrastructure, but availability policy differs by operation class.

Writes should prefer safety over availability: failure of mandatory authorization, approval validation, credential brokerage, or mandatory guardrails blocks the write.

Read-only paths may define bounded degraded modes through explicit policy and evidence.

### 29.3 Capacity tests

Before RC, qualify:

- sustained discovery QPS;
- mixed read/write invocation QPS;
- high-cardinality tenant/run traffic without metric explosion;
- database connection pool sizing;
- MCP active request/subscription count;
- connector concurrency limits;
- large-but-valid result handling;
- outbox/evidence backpressure.

---

## 30. Delivery phases and exit gates

### Phase 0 — Decisions, threat model, and contracts

Define:

- product boundary;
- trust boundaries/threat model;
- glossary;
- tool/version model;
- invocation state machine;
- canonical argument/digest format;
- retry/idempotency classes;
- authorization decision contract with ThinkPixelAG;
- approval contract with ThinkPixelAG;
- GR pre/post-tool contract;
- credential provider interface;
- connector interface;
- canonical REST/OpenAPI draft;
- MCP revision/transport baseline;
- database schema draft;
- evidence model;
- supported versions and dependency policy.

Exit when there is no ambiguous source of authority for identity, tool semantics, authorization, approval, credentials, or side-effect replay.

### Phase 1 — Engineering foundation

Implement:

- Go module and repository layout;
- config loading/validation;
- structured logging;
- metrics/tracing bootstrap;
- request IDs/trace propagation;
- HTTP server limits/timeouts/shutdown;
- `/livez`, `/readyz`, `/metrics`;
- Makefile/CI;
- minimal container image;
- initial OpenAPI generation/checking;
- local Compose dependencies.

Exit when `make verify` passes from a clean checkout and the minimal image runs as non-root/read-only with graceful shutdown.

### Phase 2 — Authoritative persistence and invocation primitives

Implement:

- PostgreSQL migrations;
- transaction manager;
- tool/tool-version repositories;
- invocation/attempt/event/result repositories;
- idempotency records;
- outbox;
- canonical JSON/digest library;
- resource projection;
- invocation state machine;
- concurrent invocation claim/replay behavior.

Exit when real PostgreSQL tests prove tenant isolation, replay safety, conflict semantics, transaction rollback, and no duplicate attempt ownership under concurrency.

### Phase 3 — Identity and authorization enforcement

Implement:

- OIDC/JWT authentication;
- audience/resource validation;
- workload identity abstraction;
- trusted context derivation;
- `Authorizer` port;
- ThinkPixelAG adapter;
- decision validation/cache/freshness;
- constraint enforcement;
- revocation/freshness integration required by the AG contract.

Exit when cross-tenant/run forgery, stale authorization, malformed decisions, and AG outage behavior all fail according to the declared security posture.

### Phase 4 — Tool catalog and canonical discovery/invocation API

Implement:

- tool publication validation;
- immutable versions;
- tenant exposure;
- discovery endpoints;
- `POST /v1/tool-calls`;
- `GET /v1/tool-calls/{id}`;
- mock connector;
- full request/result/error contracts.

Exit when a governed mock invocation completes end to end through authentication, authorization, persistence, execution, evidence, and idempotent replay.

### Phase 5 — Credential broker and first real connector

Implement:

- credential binding model;
- development secret provider;
- at least one production-oriented secret/workload provider;
- typed credential lifecycle/redaction;
- connector registry;
- GitHub reference connector or equivalent;
- read and write tool operations;
- native downstream idempotency propagation where supported.

Exit when credentials never reach the harness/logs/database and a real connector read/write flow passes integration and canary qualification.

### Phase 6 — Guardrails integration

Implement:

- ThinkPixelGR client;
- `pre_tool` enforcement;
- `post_tool` enforcement;
- transforms with revalidation/reauthorization;
- mandatory/observational failure modes;
- evaluation evidence correlation.

Exit when block/redact/transform/outage paths behave deterministically and transformed security-relevant arguments cannot execute under stale authorization/approval.

### Phase 7 — Action-scoped approvals

Implement:

- approval-required state;
- AG approval request/reference integration;
- argument/resource digest binding;
- expiry;
- retry-after-approval flow;
- current authorization recheck;
- single-use logical action enforcement;
- audit/evidence.

Exit when an approved operation executes exactly the approved logical action and all argument/resource substitution/replay tests fail safely.

### Phase 8 — Side-effect reliability and reconciliation

Implement:

- retry policy engine;
- attempt claiming;
- downstream idempotency mapping;
- error/ambiguity classification;
- connector reconciliation API;
- manual-review state/admin operation;
- crash/replay tests;
- usage event deduplication.

Exit when failure-injection tests demonstrate no blind duplicate for `non_retryable`/`reconcile_before_retry` tools and native-idempotent tools survive crash/retry with one logical side effect.

### Phase 9 — MCP compatibility layer

Implement:

- pinned MCP protocol types/conformance;
- Streamable HTTP;
- auth/origin/per-request routing and continuation controls;
- `tools/list`;
- `tools/call`;
- deterministic discovery;
- trusted annotation mapping;
- stdio adapter if required;
- Codex integration smoke test.

Exit when Codex or another target harness can use TG without receiving downstream credentials and every MCP tool call traverses the canonical invocation application service.

### Phase 10 — Resource metering, evidence, and self-protection

Implement:

- ThinkPixelAG trusted usage events;
- reconciliation of usage publication;
- full audit classes;
- external evidence sink/outbox publisher;
- rate/concurrency limits;
- redaction hardening;
- privileged administration separation;
- security dashboards/alerts.

Exit when a consequential invocation can be reconstructed across AG/TG/GR evidence and metering cannot be double-applied through replay.

### Phase 11 — Kubernetes and production operations

Finalize:

- production image;
- manifests/Helm if desired;
- NetworkPolicy proving harness cannot bypass TG;
- secret/workload identity integration;
- dashboards/alerts/SLOs;
- backup/restore;
- upgrade/rollback;
- load tests;
- live connector qualification;
- disruption tests;
- SBOM/provenance/signature hooks;
- operations/security runbooks.

Exit when a disposable cluster passes installation, migration, canary invocation, disruption, restore, rolling upgrade, network-bypass, and smoke tests.

### Phase 12 — Release-candidate closure

Run all gates, resolve critical/high findings, freeze RC contracts, extract stable decisions into ADRs, update README/operations docs, prepare release notes, and remove temporary planning files when the project follows the same planning lifecycle as ThinkPixelAG.

Exit when a clean checkout can reproducibly produce a traceable, signed/taggable RC artifact and the threat model/quality gate has no unresolved release blockers.

---

## 31. Coding-agent operating instructions

These instructions apply to every implementation session and intentionally mirror the discipline used in ThinkPixelAG.

1. Read `README.md`, `PLAN.md`, `TODO.md`, relevant contracts/ADRs, and inspect repository status before editing. Preserve unrelated changes.
2. Select the first unchecked TODO whose dependencies are complete. Work on one atomic item or a tightly coupled contiguous group.
3. Restate acceptance criteria internally before implementation and identify the tests/evidence that will prove completion.
4. Re-check the trust boundary before changing authentication, authorization, approval, credential, connector, idempotency, MCP, or evidence behavior.
5. Implement the smallest complete vertical change, including tests, migrations, OpenAPI/schema updates, telemetry, security handling, and docs required by the item.
6. Never bypass a failing authorization/guardrail/credential check to make an integration test pass. Fix the contract or dependency.
7. Never add caller-controlled generic escape hatches such as arbitrary downstream URL, arbitrary secret name, arbitrary connector type, raw authorization header, or unbounded provider options without a reviewed plan/ADR change.
8. Run narrow tests during development, then item acceptance commands. Run `make verify` before declaring a milestone complete.
9. If a required check cannot run, leave the TODO unchecked and record the blocker/evidence gap.
10. Update `TODO.md` in the same change: mark only verified items, include date/commit placeholder/reference, and record material evidence/deviations.
11. Update `PLAN.md` whenever implementation invalidates an assumption, boundary, sequencing decision, or security invariant. Do not rewrite completed history to hide deviations.
12. Update `README.md` whenever user-facing setup, API behavior, configuration, connector support, MCP compatibility, or operational expectations change.
13. Review generated OpenAPI/MCP artifacts and repository diffs. Do not commit secrets, test tokens, binaries, local DB state, provider payload dumps, or unrelated modifications.
14. For side-effecting connector tests, prefer hermetic fakes. Live-provider mutations require isolated test resources and explicit qualification instructions.
15. Before committing a connector operation, document its retry/idempotency/ambiguity semantics and prove them with tests.
16. Before committing an approval path, prove the exact executable digest/resource is what was approved.
17. Before committing credential code, run secret-canary leakage tests over logs, errors, traces, audit/event payloads, and persisted rows.
18. Commit completed verified units directly to `main` only when repository policy permits it. Use imperative descriptive messages such as `feat(invocation): enforce logical tool-call idempotency`.
19. Never amend/rewrite shared history or bypass failing hooks.
20. At every phase exit, run the full gate, update phase status in planning files, and record dedicated evidence under `docs/`.

### Commit scope and safety

- Default branch is confirmed before implementation commits.
- Stage explicit files when unrelated work exists.
- Released/shared database migrations are immutable; use corrective migrations.
- A checked TODO means implemented **and verified**.
- Any skipped/flaky security test, known secret leak, unexplained ambiguous-write behavior, temporary fail-open path, or unreviewed authorization bypass blocks RC.
- Security-sensitive changes should receive a second review when repository workflow supports it.
- Never claim a downstream operation is idempotent without specifying which component guarantees it.

---

## 32. Plan maintenance and ADR transition

During implementation, `PLAN.md` and `TODO.md` are living documents.

Durable decisions should move to `docs/adr/` as they stabilize.

At minimum, final ADRs should cover:

1. service shape and canonical protocol;
2. PostgreSQL/cache responsibilities;
3. tool identity/versioning and trusted metadata;
4. canonical JSON/argument digest/resource projection;
5. authentication/delegation/workload identity;
6. ThinkPixelAG authorization contract and freshness;
7. action-scoped approvals;
8. credential brokerage and secret-provider model;
9. connector framework;
10. idempotency/retry/ambiguous-outcome semantics;
11. ThinkPixelGR integration;
12. MCP protocol revision/transports/authentication;
13. evidence/outbox/usage integration;
14. Kubernetes/network security;
15. administrative privilege model.

When every RC item is verified:

1. reconcile plan, TODO, commits, contracts, threat model, and release evidence;
2. ensure ADRs capture stable rationale and alternatives;
3. move enduring setup/API/operations/contribution information into `README.md` and `docs/`;
4. ensure no unresolved blocker or security rationale would be lost;
5. if following the ThinkPixelAG lifecycle, remove `PLAN.md` and `TODO.md` in the final documentation transition;
6. run documentation/link checks and `make verify`;
7. commit the release-preparation documentation change.

---

## 33. Release-candidate quality gate

An RC requires all of the following:

- all RC TODO items checked with evidence and no unresolved blockers;
- clean build plus formatting, vet/static analysis, unit, race, fuzz/property, integration, contract, MCP conformance, end-to-end, security, resilience, and Kubernetes smoke gates;
- OpenAPI and supported MCP contract revisions frozen/documented;
- migration compatibility reviewed and tested;
- tenant/run isolation demonstrated adversarially;
- no path for caller input to select arbitrary connector destinations or credentials;
- downstream credentials proven absent from harness responses, logs, traces, audit records, and normal database rows;
- authorization deny-by-default and declared freshness behavior demonstrated under AG outage/staleness;
- mandatory GR fail behavior demonstrated;
- action-scoped approval digest/resource binding demonstrated;
- crash/retry behavior demonstrated for every shipped side-effect retry class;
- no shipped tool has undocumented ambiguous-write semantics;
- harness network-bypass prevention demonstrated in the reference Kubernetes deployment;
- load/resource targets met;
- zero unresolved critical/high exploitable vulnerabilities or policy violations;
- threat model, data classification, SLOs, alerts, and operational/security runbooks reviewed;
- restore, upgrade, rollback, key/credential rotation, revocation freshness recovery, outbox recovery, and ambiguous-invocation procedures exercised;
- live reference connector canaries completed in an isolated environment;
- OCI image, SBOM, checksums/provenance/signature hooks, Kubernetes artifacts, and release notes generated reproducibly;
- stable architecture decisions extracted to ADRs.

---

## 34. Initial risks

### 34.1 Authorization drift

**Risk:** TG duplicates AG policy logic and the two systems disagree.

**Mitigation:** TG is a PEP with a typed Authorizer port. AG is the primary governed PDP. Local logic enforces tool invariants/constraints, not competing agent authorization rules.

### 34.2 Harness bypass

**Risk:** agent calls GitHub/Slack/Kubernetes directly and ignores TG.

**Mitigation:** reference Kubernetes NetworkPolicy/egress controls deny direct governed endpoints and keep downstream credentials out of the harness.

### 34.3 Credential exfiltration

**Risk:** credentials leak through model context, result payloads, errors, or telemetry.

**Mitigation:** secret references, typed credential interfaces, structural redaction, canary leakage tests, no plaintext DB storage.

### 34.4 Confused deputy / token passthrough

**Risk:** inbound harness token is reused against a downstream service or accepted for the wrong audience.

**Mitigation:** strict audience validation, no token passthrough, separate downstream token acquisition, RFC 8707-style resource binding where supported.

### 34.5 Approval TOCTOU

**Risk:** user approves one operation and agent executes another.

**Mitigation:** canonical final argument digest, resource projection, exact tool/version/run binding, expiry, reauthorization immediately before execution.

### 34.6 Duplicate side effects

**Risk:** crash/retry sends duplicate messages, comments, deployments, or destructive operations.

**Mitigation:** persistent logical `tool_call_id`, database claim, explicit retry classes, native idempotency keys, reconciliation, no blind replay after ambiguous writes.

### 34.7 False idempotency claims

**Risk:** tool metadata says an operation is idempotent when downstream behavior is not.

**Mitigation:** publication review plus connector tests must prove the mechanism; MCP annotation is derived from trusted semantics.

### 34.8 Stale revocation

**Risk:** cached authorization survives run/tool/principal revocation.

**Mitigation:** bounded TTL, AG revocation epochs/checkpoints, live checks for high-risk writes, readiness degradation when freshness contract cannot be met.

### 34.9 MCP protocol churn

**Risk:** fast-moving MCP revisions break clients or introduce semantics TG accidentally trusts.

**Mitigation:** pin supported revisions, maintain conformance tests, keep MCP as adapter, derive security semantics from TG domain metadata.

### 34.10 SSRF through connectors

**Risk:** agent arguments turn the gateway into an arbitrary network client.

**Mitigation:** administrator-controlled destinations, no generic URL tool in RC, strict redirect/DNS policy, network egress allowlists.

### 34.11 Tool-result prompt injection

**Risk:** open-world tool result attempts to manipulate the model/harness.

**Mitigation:** trusted `open_world` classification, GR `post_tool`, result trust labels, schema/size limits, explicit harness guidance. Authorization remains independent even if detection misses the attack.

### 34.12 Gateway availability becoming platform availability

**Risk:** every tool call depends on TG and its security dependencies.

**Mitigation:** horizontal stateless replicas, PostgreSQL HA, bounded caches, narrow timeouts, degraded read policy where explicitly allowed. Accept fail-closed availability cost for governed writes.

### 34.13 Connector sprawl

**Risk:** each connector implements authentication, retries, evidence, and errors differently.

**Mitigation:** small connector interface, shared HTTP/security primitives, conformance test suite every connector must pass, reference implementations before expansion.

### 34.14 Sensitive evidence accumulation

**Risk:** the enforcement point becomes a high-value store of raw enterprise content.

**Mitigation:** metadata/digests by default, bounded replay subset, explicit content retention classes, encrypted external evidence store for exceptional payloads, tenant retention policy.

### 34.15 Plan becoming stale

**Risk:** implementation diverges while coding agents continue to rely on obsolete invariants.

**Mitigation:** plan/TODO update in implementation changes, phase evidence, ADR extraction, explicit deviations rather than silent drift.

---

## 35. Initial standards and compatibility baseline

The implementation should track standards deliberately rather than by accidental SDK behavior.

Initial references to record in `docs/supported-versions.md` include:

- MCP specification revision `2026-07-28`, selected for implementation and conformance;
- MCP Streamable HTTP transport and authorization requirements;
- MCP tool schema/annotations, treating annotations as hints only;
- OAuth 2.0 / OAuth 2.1 security best practices as applicable;
- RFC 8693 OAuth 2.0 Token Exchange for delegated subject/actor models where supported;
- RFC 8707 Resource Indicators for audience/resource-bound authorization where supported;
- RFC 9728 OAuth 2.0 Protected Resource Metadata if MCP authorization deployment uses it;
- OIDC/JWT validation requirements;
- SPIFFE Workload API/SVID specifications where workload identity is enabled;
- OpenAPI 3.1;
- RFC 7807 Problem Details;
- W3C Trace Context;
- OpenTelemetry;
- RFC 9562 UUIDs.

Supported versions are pinned in documentation and CI. A dependency upgrade does not implicitly change a wire protocol or security contract.

---

## 36. Reference integration with the ThinkPixel platform

The intended complete path is:

```text
Client / IDE
    |
    v
ThinkPixelAG
    | creates governed run + resource envelope
    v
Runtime worker / Codex harness
    |
    | model calls
    +---------------------> ThinkPixelLLMGW
    |                           |
    |                           +--> ThinkPixelGR pre/post model
    |
    | tool calls
    v
ThinkPixelTG
    |
    +--> derive run/user/agent/workload identity
    +--> ThinkPixelAG authorize tool action
    +--> ThinkPixelGR pre_tool
    +--> ThinkPixelAG action approval when required
    +--> credential broker
    +--> downstream connector
    +--> ThinkPixelGR post_tool
    +--> trusted usage -> ThinkPixelAG
    +--> evidence/outbox
```

The design is successful if Codex can later be replaced by another harness without changing the authorization, credential, approval, idempotency, or evidence model.

Likewise, MCP can be replaced or supplemented by another harness-facing protocol without changing the connector/security domain.

---

## 37. Definition of the first useful vertical slice

Before attempting a broad connector catalog, prove one complete governed operation.

Recommended slice:

```text
Tool:
    github.pull.comment

Harness:
    Codex or a minimal MCP client

Identity:
    run-scoped bearer token + trusted tenant/agent/run mapping

Authorization:
    ThinkPixelAG adapter or a contract-faithful fake initially

Guardrails:
    ThinkPixelGR pre_tool/post_tool

Credential:
    GitHub token obtained through a non-harness credential provider

Idempotency:
    stable tool_call_id + downstream/gateway behavior documented

Evidence:
    PostgreSQL invocation/attempt + audit/outbox

Metering:
    one trusted tool_calls usage event
```

Acceptance criteria:

1. the model/harness never sees the GitHub credential;
2. an unauthorized repository is blocked before credential resolution;
3. malformed arguments never reach GitHub;
4. the same logical call replay cannot create an unintended second comment according to the published retry contract;
5. a GR block prevents the downstream call;
6. the final result can be correlated to run, tool version, authorization decision, provider request/result, and usage event;
7. direct harness egress to GitHub is denied in the reference Kubernetes deployment.

Once this vertical slice is solid, adding more tools should mostly be connector/domain-data work rather than reinvention of the security path.

---

## 38. Final architectural invariant

ThinkPixelTG should remain boring in the most useful possible sense.

It does not need to understand how an agent reasons.

It needs to make these statements reliable:

```text
This is the governed run and actor that requested the operation.
This is the exact immutable tool definition they requested.
These are the final normalized arguments and target resource.
This policy decision allowed, denied, or required approval for them.
This exact action was approved, if approval was needed.
The harness never received the downstream credential.
This connector made this downstream attempt under these retry semantics.
This is what we can prove about the external side effect.
This result passed the required post-tool controls before returning to the model.
This usage was charged once.
This evidence can be reconstructed later.
```

If those statements remain true when the harness, model, connector set, and MCP revision change, ThinkPixelTG is doing the right job.
