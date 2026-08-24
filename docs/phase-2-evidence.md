# Phase 2 authoritative-persistence evidence

Status: complete on 2026-08-24

Phase 2 establishes PostgreSQL as the authoritative store and supplies the
canonicalization, resource-projection, invocation-state, acquisition, attempt,
evidence, and publication primitives required by later application phases.

## Real-PostgreSQL exit gate

The dedicated gate is:

```sh
TPTG_TEST_DATABASE_URL='postgres://…' make test-phase2
```

The suite applies the embedded migrations to a fresh, isolated schema for each
test and runs against PostgreSQL 18.6. The final local evidence run used the
digest-pinned Compose database declared in `deployments/compose/compose.yaml`.

| Required property | Real-database evidence |
|---|---|
| Tenant isolation | Guessed cross-tenant reads return not-found, composite foreign keys reject cross-tenant child attachment, and same identifiers/upserts remain independent across tenants. |
| Replay safety | Completed logical invocations return the stored status, digest, and safe payload; concurrent exact replays converge on that same durable envelope. |
| Conflict semantics | A reused `(tenant, run, tool_call_id)` with a different tool version or argument digest deterministically returns conflict without changing the original invocation. |
| Rollback | Explicit transaction failure removes its mutation; protected mutation or outbox failure rolls back mutation, audit, and publication records together. |
| Single invocation ownership | Property scenarios with 2, 3, 8, and 16 simultaneous claimants produce exactly one owner and `N-1` pending results. |
| Single attempt ownership and fencing | Eight simultaneous attempt claimants produce one owner; lease recovery increments attempt/fence values, and stale or duplicate finalization returns conflict. |
| Outbox atomicity and replay | Protected changes commit with audit/outbox records or not at all; crash-after-send recovery republishes the stable outbox identity, while bounded failures become visible dead letters. |

The concurrency scenarios use synchronized goroutine starts and repeat across
different cardinalities. Database constraints, row locking, conditional
upserts, ownership predicates, and fencing values—not process-local locks—decide
the winners.

## Supporting verification

The Phase 2 gate is supplemented by the full project formatting, generation,
lint, vet, unit, and race-test gates. Migration integration tests separately
prove empty and prior-schema upgrades, checksums, failure recovery, constraints,
indexes, and backup-friendly behavior. Canonical JSON, trusted projection, and
the pure invocation state machine have table, property, and fuzz coverage in
their respective packages.

## Gate result

Phase 2 exits successfully. Phase 3 may build identity and authorization on the
tenant-scoped persistence boundary and concurrency semantics proven here.
