# ThinkPixelAG authorization contract

Status: Phase 0 normative baseline; media type/profile `tg.ag.authorization/v1alpha1`

## Request

TG sends a mutually authenticated request containing opaque `request_id`, current
UTC time, trusted tenant/subject/agent/version/run/workload identities, exact
tool/version and risk/side-effect/approval/retry metadata, argument digest profile
and digest, typed resource projection and digest, operation/action, connector type
(not credential), and requested deadline. No inbound/downstream token or raw
credential is included. Raw arguments are excluded unless a separately reviewed
policy field explicitly requires a bounded C2 projection.

## Response

AG returns strict fields: `decision_id`, matching `request_id` and context digest,
`outcome` (`allow` or `deny`), stable `reason_codes[]`, `policy_id/version`,
`issued_at`, `not_before`, `expires_at`, `revocation_epoch/checkpoint`, narrowing
`constraints`, `approval_requirement`, and evidence reference. Unknown outcome,
missing required field, invalid time/order/signature, correlation mismatch,
unrecognized constraint type, or contradictory response is malformed and denied.

Stable initial reason codes include `allowed`, `policy_denied`, `run_inactive`,
`agent_version_denied`, `tool_denied`, `resource_denied`, `budget_exhausted`,
`approval_required`, `revoked`, `decision_stale`, `dependency_unavailable`, and
`malformed_decision`. Caller errors expose only safe mapped codes and correlation.

## Constraint narrowing

TG intersects AG constraints with the immutable tool-version/schema/resource and
local deployment limits. It may reduce allowed repositories, resources, actions,
argument bounds, result size, deadline, or rate; it MUST NOT union, default-open,
ignore an unknown mandatory constraint, or substitute a broader resource. Empty
intersection is denial. Constraint validation occurs before GR/credential use and
again after any security-relevant transform.

## Freshness and revocation

Cache keys cover all request security fields/digests and policy/profile version.
Reuse ends at the earliest of `expires_at`, configured TTL, run/policy/tool change,
or observed revocation checkpoint. High-risk writes require the configured live or
max-age check immediately before credential resolution. Clock skew is bounded and
cannot extend validity. Denials may be cached only for a bounded safe interval.

AG timeout, authentication failure, stale state, invalid/malformed data, cache
poisoning suspicion, or unavailable required revocation freshness fails closed.
Any explicitly degraded read-only posture requires a separate threat-reviewed
profile; there is no implicit stale-allow fallback.
