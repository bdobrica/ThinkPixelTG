# Retry, idempotency, and ambiguity contract

Status: Phase 0 normative baseline; contract version `tg.retry/v1alpha1`

The immutable tool version declares exactly one class. Neither callers,
annotations, connectors, nor runtime errors may upgrade it to a safer class.

| Class | Publication proof | Automatic replay |
|---|---|---|
| `safe` | operation has no externally observable mutation and repeated reads are acceptable | allowed for classified transient pre-result failures within limits |
| `downstream_idempotency` | provider contract and connector propagate a stable key derived from logical invocation; response/retry window documented | allowed with the identical key/payload while provider guarantee remains valid |
| `gateway_deduplicated` | TG can prove completion before send from authoritative ledger/downstream unique record; crash windows documented | allowed only when proof says not applied |
| `reconcile_before_retry` | connector has authoritative reconciliation keyed by stable operation evidence | never before reconciliation confirms not applied |
| `at_least_once_accepted` | product/security owner accepts possible duplicates and caller-visible contract states it | allowed only within explicit bound; duplicates remain possible |
| `non_retryable` | no safe proof exists | only failures proven pre-send may retry; possibly sent operations become ambiguous/manual review |

## Outcome taxonomy

An attempt records `not_sent`, `definitely_rejected`, `confirmed_success`,
`transient_safe`, or `unknown`. Connection loss/timeout after send is `unknown`
unless provider evidence proves otherwise. `unknown` transitions to `ambiguous`;
it must never be represented as ordinary failure or silently retried.

Reconciliation returns `confirmed_success`, `confirmed_not_applied`,
`still_unknown`, or `unsafe_to_retry`, with provider evidence reference/digest.
Only `confirmed_not_applied` may schedule a replay; `confirmed_success` finalizes
the original logical action; remaining outcomes wait or require manual review.

## Execution rules

- Attempt sequence and fence are monotonic; one active sender per invocation.
- Stable downstream keys derive from tenant/run/logical call/tool-version identity,
  are domain separated, contain no raw secret/content, and never change on retry.
- Retry deadlines, maximum attempts, exponential backoff/jitter, provider windows,
  cancellation, and charge point are declared by tool metadata.
- Identical transport retries do not create a new logical invocation or approval.
- Usage events deduplicate at the documented logical accounting boundary.
- Manual resolution is privileged, append-only, and cannot claim proof it lacks.

Every shipped write operation needs connector-specific fault-injection evidence at
the before-send, after-send/before-response, after-response/before-commit, and
after-commit/before-publication crash points.
