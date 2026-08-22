# ThinkPixelAG action-scoped approval contract

Status: Phase 0 normative baseline; profile `tg.ag.approval/v1alpha1`

Approval is required only as dictated by the final current authorization/tool
policy and is requested after all security-relevant pre-tool transforms.

## Request and binding

TG sends a unique approval request/reference bound to tenant, subject, agent and
version, run, workload class where policy requires it, exact tool/version,
logical `tool_call_id`, final argument digest/profile, typed resource projection
and digest, action/risk/side-effect/retry classes, authorization decision/policy
references, requested expiry, and TG evidence correlation. Content is minimized;
no credentials or inbound tokens are sent.

AG returns strict `approval_id`, matching binding digest, status (`pending`,
`approved`, `denied`, `expired`, `revoked`), approver/policy evidence reference,
`issued_at`, `expires_at`, revocation checkpoint, and optional narrowing conditions.
TG persists the binding transactionally with `waiting_for_approval` and exposes
only a safe status/reference for harness UX.

## Enforcement

- Approval grants one governed logical action only. It cannot be reused for a
  different tenant/run/tool/version/call/digest/resource, even if content matches.
- Approval conditions only narrow the current authorization/tool contract.
- TG verifies authenticity, exact binding, status, expiry, revocation, unused
  state, current tool enablement, and fresh authorization immediately before
  credential resolution; use is claimed transactionally and fenced.
- Concurrent consumers cannot both consume one approval. Retry attempts belonging
  to the same approved logical action do not consume a second approval, subject
  to validity and immutable action binding.
- Any post-approval argument/resource transformation invalidates approval and
  returns the invocation to validation/authorization/new approval.
- Denial, expiry, or revocation blocks that action. Changed arguments start a new
  logical call or explicit restart; TG never mutates the approved action silently.
- Approval-service outage or malformed state fails closed when approval is mandatory.

Evidence records lifecycle transitions, binding digests, policy/decision IDs,
expiry/revocation/use, and correlation without storing broad reusable authority or
unnecessary action content.
