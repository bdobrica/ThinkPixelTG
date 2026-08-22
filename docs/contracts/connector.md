# Connector contract

Status: Phase 0 normative baseline; contract version `tg.connector/v1alpha1`

## Interface and authority

A connector is compiled, reviewed code registered by type and operation. It
receives immutable operation metadata, validated canonical arguments, trusted
resource projection, configured connector instance/destination, typed credential
capability, logical idempotency material where qualified, attempt fence, and a
bounded deadline/cancellation context. It cannot authorize, choose credentials,
change retry class, accept arbitrary URLs, or expose credential material.

`Execute` returns a bounded typed result plus provider request/resource/version
identifiers and safe reconciliation evidence. `Reconcile` exists only for
qualified operations and takes stable stored evidence—not raw caller-selected
destinations or credentials outside a fresh trusted binding resolution.

## Result and error taxonomy

| Classification | Meaning / orchestration consequence |
|---|---|
| `confirmed_success` | provider proves accepted/completed; may proceed to post-tool |
| `definitely_rejected` | provider proves no side effect; terminal or policy-safe retry |
| `not_sent` | local validation/connection failure proves no request bytes committed; safe policy may retry |
| `transient_safe` | retry safety is proven by immutable class/idempotency contract |
| `unknown` | request may have applied; transition to ambiguous, never blind retry |
| `cancelled_pre_send` | cancellation occurred with proof no send |

HTTP status alone does not prove non-application. Timeout/reset/cancellation after
send defaults `unknown`. Provider bodies are parsed under size/schema limits and
never embedded in public errors.

## Network and execution rules

Connector instances fix scheme/host/port/base path/TLS roots and allowed redirects.
Shared transport enforces DNS/private/metadata-address policy, revalidation,
hostname/certificate checks, safe proxy rules, header allowlists, response limits,
connection/overall deadlines, cancellation, and bounded concurrency. Redirects
cannot change trust zone or forward credentials unless explicitly qualified.

Cancellation stops local work but cannot assert a sent write was cancelled.
Connector output is open-world unless immutable metadata says otherwise and must
pass post-tool handling before caller disclosure.

## Required conformance

Every operation tests schema/resource enforcement, binding/audience, auth-header
non-leakage, destination/redirect/DNS policy, deadline/cancellation before and after
send, response limits/malformed bodies, status mapping, secret canaries, stable
idempotency propagation, all crash windows, reconciliation outcomes, telemetry
cardinality/redaction, and concurrent fencing. Each write has a qualification
document explaining its provider proof and residual duplicate/ambiguity risk.
