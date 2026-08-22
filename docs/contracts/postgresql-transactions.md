# PostgreSQL schema and transaction boundaries

`postgresql-schema.sql` is a logical draft, not a migration. PostgreSQL is the
authoritative state store; every tenant-owned primary/foreign/unique key includes
`tenant_id` to prevent accidental cross-tenant joins. Published tool versions are
immutable through application permissions/triggers in real migrations. C3
credential plaintext is structurally absent.

## Atomic boundaries

- Acquire invocation: insert exact logical identity/digests or lock/read the
  existing row and return match/conflict in one transaction.
- State transition: compare state version, validate pure transition, append audit,
  and enqueue required outbox/usage event in one transaction.
- Claim attempt/reconciliation/outbox: lock eligible row with `FOR UPDATE SKIP
  LOCKED`, increment monotonic fence/attempt, set bounded lease, and commit before work.
- Finalize attempt: require current fence, insert result/classification, transition
  state, audit, usage, and outbox atomically. Stale workers affect zero rows.
- Approval binding/consumption: verify exact digest/status/expiry and claim single
  use with invocation transition/evidence in one transaction.
- Tool publication/exposure/binding change: separate admin transaction plus audit
  and outbox; no partial visible security metadata.

Network calls never occur inside a database transaction. Persist intent/claim,
call externally, then finalize with the fence. A crash between send and finalize
therefore becomes recoverable ambiguity under the retry contract.

Use UTC `timestamptz`, application-generated UUIDv7, exact numeric usage quantities,
statement/lock/idle-transaction timeouts, least-privilege roles, explicit column
lists, and transaction retry only for operations proven replay safe. Migrations are
forward-only and immutable after release. RLS remains a Phase 2 defense-in-depth
decision; repository tenant scoping is mandatory regardless.
