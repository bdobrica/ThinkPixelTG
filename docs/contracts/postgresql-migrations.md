# PostgreSQL migration and compatibility contract

ThinkPixelTG ships one embedded, forward-only migration artifact. The migration
job validates its ordered SHA-256 manifest before it connects Goose to the
schema. Any missing, inserted, renumbered, or edited released migration stops the
rollout. A correction is always a new migration; the manifest is updated in the
same reviewed change.

## Rolling compatibility

Schema changes use expand, migrate, contract:

1. **Expand** adds nullable columns, columns with safe defaults, new tables,
   indexes, or constraints that both the old and new application tolerate.
2. **Migrate** deploys code that can read the old shape while writing the new
   shape, then backfills in bounded, restartable batches outside the migration
   transaction when necessary.
3. **Contract** removes the old shape only in a later release after every old
   binary and rollback target has left service.

A release migration must not rename or drop a table/column, narrow a type, make
an existing column required without a safe default/backfill, or introduce an
immediately validated constraint that existing rows may violate. Such changes
need an explicit compatibility review and staged migrations. Application
rollback is supported only across schemas inside the declared rolling window;
database downgrade is not supported.

## Failure and recovery

The deployment job takes the PostgreSQL session advisory lock and runs before
application rollout. Transactional DDL failure leaves the version pending and
is safe to retry after the external cause is corrected. Never edit an already
released migration to recover production. Non-transactional operations require
an operator runbook, idempotent guards, and a subsequent forward corrective
migration. A failed migration blocks rollout rather than starting partially
compatible application replicas.

## Backup behavior

Authoritative tables are permanent PostgreSQL relations: no `UNLOGGED` or
temporary application tables, database-generated identity sequences, large
objects, or filesystem references are part of the schema. IDs are supplied by
the application and all durable state is visible to ordinary logical and
physical PostgreSQL backup tooling. Schema and Goose version metadata must be
backed up together. Backup/restore rehearsal and point-in-time recovery remain
the operational acceptance work tracked by OPS-009.

## Required evidence

The migration test target proves:

- installation from an empty schema and idempotent re-execution;
- upgrade from the immediately prior schema fixture with row preservation and
  safe defaults;
- manifest checksum, contiguity, and unreviewed-file rejection;
- transactional forward recovery after a failed application;
- permanent, sequence-free application relations;
- database constraints and the indexes selected for claim/recovery paths.

Every release that adds a migration advances the prior-version fixture and adds
tests for its data transformation, compatibility window, constraints, and
expected query plans.
