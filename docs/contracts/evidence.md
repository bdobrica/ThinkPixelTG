# Evidence and correlation contract

Status: Phase 0 normative baseline; event profile `tg.evidence/v1alpha1`

## Correlation identifiers

Every consequential path can be joined by opaque `tenant_id`, `run_id`,
`invocation_id`, logical `tool_call_id`, exact `tool_id/version`, and `trace_id`.
Stage-specific IDs include AG `decision_id`, GR `evaluation_id`, `approval_id`,
attempt number/fence, connector instance/operation, safe provider request/result
reference, reconciliation sequence, trusted `usage_event_id`, `audit_event_id`, and
`outbox_id`. Identifiers do not replace authorization and are never secrets.

## Event envelope

Each append-only event contains version, globally stable event ID, event type,
source/service build, trusted tenant/run/invocation identity, stage correlations,
UTC occurred/recorded times, actor class/workload identity reference, outcome and
stable reason codes, policy/tool/config versions, relevant domain-separated
digests, data classifications, and safe typed details. Event IDs survive outbox
retry so sinks and AG can deduplicate.

Required taxonomy covers authentication/context, catalog resolution/administration,
validation/canonicalization/resource projection, invocation acquisition/replay,
authorization/freshness, pre/post GR, approval lifecycle/use, credential binding
resolution (never secret), attempt claim/send/outcome, ambiguity/reconciliation/
manual review, result handling, usage creation/acceptance, state transition, and
outbox publication/dead letter.

## Integrity, atomicity, and minimization

Every protected state mutation commits its audit event and required outbox rows in
the same PostgreSQL transaction. Events are immutable; corrections append a new
event referencing the original. The sink is authenticated and may add receipt/
integrity mechanisms, but PostgreSQL remains authoritative until publication.

Store metadata, classifications, reason codes, and digests by default. Raw
arguments/results, authorization/GR bodies, provider payloads, and all C3 material
are excluded. Exceptional C2 content requires named policy, field allowlist,
encryption/access/TTL, and cannot appear in normal logs or dead letters. Secret
canary detection suppresses the unsafe field/event and emits a safe incident signal.

## Reconstruction acceptance

Given tenant/run/tool-call identity, an authorized reviewer can prove the exact
tool/version and action/resource digests, identity source, AG policy decision and
freshness, GR decisions/transforms, approval binding/use if required, connector and
credential-binding reference, each fenced attempt and outcome, provider reference,
post-tool disposition, final state/result digest, usage rule/event, and external
publication receipt—without retrieving a downstream credential or unnecessary
enterprise content.
