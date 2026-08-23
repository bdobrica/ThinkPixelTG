# ADR-0001: PostgreSQL persistence tooling

- Status: accepted
- Date: 2026-08-23
- Decision owners: ThinkPixelTG maintainers
- Supersedes: none
- Superseded by: none

## Context

PostgreSQL is ThinkPixelTG's authoritative store. Phase 2 needs one driver,
migration mechanism, query strategy, and transaction abstraction before schemas
and repositories can be implemented. The choices must preserve tenant scoping,
make transaction boundaries reviewable, support PostgreSQL concurrency features,
and keep persistence types out of the application and domain packages.

## Decision

Use `github.com/jackc/pgx/v5` and `pgxpool` through the native pgx API. PostgreSQL
is the only supported relational database, so a database-neutral runtime layer is
not a goal.

Use numbered SQL migrations embedded in the migration binary and applied through
the wrapped `github.com/pressly/goose/v3` Provider API. Migration execution uses
the pgx `database/sql` adapter only because Goose requires that interface. It is
not available to repositories. Migration runners take a PostgreSQL advisory lock;
production migrations are forward-only and released files are immutable. CI and
Phase 2 migration tests will enforce ordering, drift, upgrade, and recovery rules.

Write named repository methods with handwritten, parameterized PostgreSQL SQL,
explicit column lists, and explicit scans. Repository constructors accept the
small adapter-owned `DBTX` surface shared by a pool and a transaction. Do not add
an ORM, reflection mapper, or generated query layer without a superseding ADR.

Use callback-scoped `Transactor.WithinTransaction`. The callback receives `DBTX`;
the wrapper exclusively owns begin, commit, rollback, and panic cleanup. It does
not retry automatically. Isolation/access options must be selected at each atomic
application boundary, and external network calls must not occur in a database
transaction.

## Alternatives considered

- `database/sql` with `lib/pq`: less access to PostgreSQL-specific types, pool
  controls, batching, `COPY`, and cancellation; `lib/pq` is in maintenance mode.
- An ORM: hides SQL and tenant predicates that must remain directly reviewable
  for this security boundary, and adds mapping/runtime complexity.
- `sqlc`: viable, but generated query interfaces would add a second abstraction
  before repository shapes stabilize. It may be reconsidered by a superseding ADR.
- A custom migration engine: checksum flexibility does not justify owning locking,
  parsing, version-table, and failure-recovery behavior.
- Runtime auto-migration: couples service availability and schema privilege to
  every replica. The dedicated migration command/job remains the deployment gate.

## Consequences

Persistence adapters intentionally depend on pgx types, while `internal/app` and
`internal/domain` remain driver-independent. PostgreSQL-specific behavior can be
used deliberately. Handwritten SQL requires disciplined review and integration
tests. Goose and pgx become reviewed runtime dependencies. Migration SQL and the
schema it creates begin in DATA-002; pool policy, telemetry, readiness, and error
classification begin in DATA-007.

## Security

Every tenant-owned repository query must carry the authoritative `tenant_id` and
tests must attempt cross-tenant access. Parameterized queries are mandatory.
Explicit transactions make protected mutation, evidence, and outbox atomicity
auditable. There is no automatic retry that could duplicate a side effect.
Migration credentials are deployment-only and must not be available to the
runtime service role. Migration advisory locking prevents concurrent runners.

## Operations

Build one migration artifact with embedded SQL and run it as a pre-deployment job.
Only one runner applies migrations at a time. Failed or incompatible migrations
stop rollout; recovery is a new forward migration. Pool sizing, timeouts,
readiness, shutdown, and database telemetry are deferred to DATA-007.

## Compatibility

Migrations are monotonically numbered and forward-only after release. Schema
changes must support the documented compatibility window during rolling rollout.
Downgrading application code is allowed only while the current schema remains
compatible; production schema rollback is not supported.

## References

- [Phase 2 delivery plan](../../PLAN.md#phase-2--authoritative-persistence-and-invocation-primitives)
- [PostgreSQL transaction contract](../contracts/postgresql-transactions.md)
- [PostgreSQL logical schema](../contracts/postgresql-schema.sql)
- [Dependency policy](../development/dependencies.md)
