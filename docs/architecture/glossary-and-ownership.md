# Glossary and authoritative ownership

Status: Phase 0 normative baseline

## Terms

| Term | Definition | Authoritative owner/source |
|---|---|---|
| Subject principal | employee or initiating service whose intent starts a governed run | identity provider plus ThinkPixelAG governed context |
| Agent identity | stable governed agent definition | ThinkPixelAG |
| Agent version | immutable approved agent revision acting in a run | ThinkPixelAG |
| Run identity | governed logical execution with policy/resource envelope | ThinkPixelAG |
| Workload identity | authenticated process/pod submitting the request | workload identity system, verified by TG |
| Tenant | isolation and policy namespace containing all governed identities/resources | ThinkPixelAG/identity mapping; enforced by TG storage keys |
| Tool | stable semantic capability identifier, not a destination URL | TG catalog administrator |
| Tool version | immutable published schema and operational/security semantics | TG catalog administrator and PostgreSQL |
| Connector | compiled implementation for a fixed family of downstream operations | TG release artifact/registry |
| Connector instance | administrator-configured destination and TLS/egress policy | TG administration and PostgreSQL |
| Credential binding | reference mapping trusted tenant/tool/connector context to a provider capability | TG administration; plaintext owned by credential provider |
| Invocation | one logical requested operation identified within tenant/run/tool-call scope | TG PostgreSQL ledger |
| Attempt | one fenced execution/reconciliation effort belonging to an invocation | TG PostgreSQL ledger |
| Authorization decision | AG answer whether exact context/action/resource is allowed, with narrowing constraints and freshness | ThinkPixelAG; validated/enforced by TG |
| Approval | action-scoped decision bound to final run/tool/version/digest/resource/logical call | ThinkPixelAG; binding/use enforced by TG |
| GR evaluation | safety/data-policy result over a bounded pre-tool or post-tool projection | ThinkPixelGR; ordering/enforcement owned by TG |
| Evidence event | append-only fact correlating a control or state transition without avoidable secret/content payload | TG at source; external sink receives a copy |
| Trusted usage event | idempotent accounting fact derived from TG execution truth | TG produces; ThinkPixelAG consumes/accountably deduplicates |
| Resource projection | deterministic policy-relevant resource/action extracted from validated canonical arguments under tool-version rules | TG immutable tool version plus TG implementation |
| Logical tool call ID | caller-stable idempotency key within tenant/run, distinct from transport request IDs | caller supplies identity; TG validates scope and owns persisted meaning |

## Authority matrix

`D` decides/defines, `E` enforces, `V` verifies, `R` records. Blank means no authority.

| Fact | IdP/workload | AG | GR | TG | PostgreSQL | Provider | Connector | Caller |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Subject authentication | D | V | | V | | | | |
| Workload authentication | D | | | V | | | | |
| Tenant/agent/version/run governance | | D | | V/E | R | | | |
| Tool/version semantics | | | | D/E | R | | V | discover only |
| Authorization and constraints | | D | | V/E | R | | | request only |
| Safety/data-policy evaluation | | | D | V/E | R | | | request only |
| Approval grant/revocation | | D | | V/E single use | R | | | UX reference only |
| Resource projection/digest | | consumes | consumes bounded | D/E | R | | consumes | supplies raw args only |
| Connector instance/destination | | | | D/E | R | | consumes fixed value | |
| Credential binding | | may constrain | | D/E selection | reference only | D plaintext/lease | consumes capability | |
| Invocation/attempt state | | consumes evidence | consumes projection | D/E | authoritative R | | reports outcome | observes safe view |
| Provider completion fact | | | | classifies | R | | D/report | |
| Evidence event | consumes | | consumes refs | D | authoritative R | | supplies safe refs | |
| Usage event | consumes/D accounting | | | D source fact | authoritative R/outbox | | supplies units/IDs | |

## Precedence and conflict rules

1. Authentication establishes a principal but does not grant a tool action.
2. AG authorization may narrow capability; TG cannot broaden it. GR may block or
   transform within contract but cannot turn a denial into an allow.
3. Caller identifiers are routing/idempotency inputs only after trusted context
   scopes them; conflicting governance hints are rejected.
4. Published TG tool-version metadata, not MCP annotations or connector guesses,
   defines schemas, risk, approval, resource projection, retry, limits, and binding.
5. Credential providers prove capability material, not policy permission.
6. Connector/provider observations inform outcome classification; PostgreSQL holds
   the authoritative TG state transition and its evidence.
7. Valkey and external evidence sinks are never authoritative for protected state.

No shared service account or composite identifier may collapse subject, agent,
run, workload, or tenant identities in authorization, persistence, or evidence.
