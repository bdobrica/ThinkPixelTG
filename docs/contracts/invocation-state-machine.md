# Invocation state machine

Status: Phase 0 normative baseline; contract version `tg.invocation-state/v1alpha1`

## Identity and concurrency

One logical invocation is uniquely identified by `(tenant_id, run_id,
tool_call_id)` and permanently binds exact tool/version, canonical argument
digest, resource projection digest, and retry class. A matching replay observes or
resumes that invocation; a mismatch is `replay_conflict`. State has a monotonic
version. Attempts have monotonic sequence numbers, lease owner/expiry, and fencing
token. Only the current fence may report or finalize an attempt.

## States

| State | Terminal | Meaning |
|---|---:|---|
| `received` | no | identity/tool resolved; logical call acquired |
| `validated` | no | schema, canonical digest, resource projection persisted |
| `authorized` | no | current AG allow and narrowed constraints persisted |
| `pre_tool_passed` | no | mandatory pre-tool GR completed for current digest |
| `waiting_for_approval` | no | exact final action awaits AG approval |
| `ready` | no | all controls passed; execution may be claimed |
| `executing` | no | fenced attempt may send downstream |
| `retry_wait` | no | definitely safe retry scheduled under immutable policy |
| `reconciling` | no | prior outcome checked before replay |
| `ambiguous` | no | downstream commit status is unknown; blind retry forbidden |
| `manual_review` | no | privileged resolution is required |
| `post_tool` | no | confirmed result undergoing output guardrail/schema handling |
| `succeeded` | yes | confirmed result safely finalized |
| `failed` | yes | no success; failure classification is final and non-ambiguous |
| `denied` | yes | authorization denied |
| `blocked` | yes | GR or approval policy blocked action/result |
| `cancelled` | yes | cancelled before an unsafe send or after safe reconciliation |

`ambiguous` is intentionally non-terminal for reconciliation, but cannot execute
again without an allowed reconciliation transition. Terminal state is immutable.

## Legal transition outline

```text
received -> validated -> authorized -> pre_tool_passed
pre_tool_passed -> waiting_for_approval -> ready
pre_tool_passed --------------------------> ready
ready -> executing -> post_tool -> succeeded
executing -> retry_wait -> executing
executing -> ambiguous -> reconciling
reconciling -> post_tool | retry_wait | manual_review
ambiguous -> manual_review
any pre-send nonterminal -> failed | denied | blocked | cancelled (as applicable)
post_tool -> blocked | failed
```

A security-relevant GR transformation returns to `validated` with a new digest
and invalidates prior authorization/approval. Authorization denial only reaches
`denied`; GR block reaches `blocked`. Unknown post-send completion only reaches
`ambiguous`. `failed` asserts no unresolved side-effect uncertainty.

## Actors and invariants

- Ingress/application may acquire, validate, request controls, and cancel pre-send.
- AG adapter records authorization/approval facts but cannot move execution state alone.
- GR adapter records evaluations; orchestrator applies the transition.
- A worker with current fence alone may enter/send from `executing` or report outcome.
- Reconciler with current fence alone may classify ambiguity.
- Separately authorized operator may move `manual_review` using an append-only
  resolution; never rewrite attempts or fabricate a provider response.
- Transactional mutation includes state version, audit event, and required outbox.
- Credential resolution occurs only from `ready` after current-control rechecks.
- Lease expiry permits a new claim, not acceptance of a stale worker's completion.
