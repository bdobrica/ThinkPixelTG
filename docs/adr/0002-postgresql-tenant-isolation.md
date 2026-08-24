# ADR-0002: Repository-enforced tenant isolation for the release candidate

- Status: accepted
- Date: 2026-08-24
- Decision owners: ThinkPixelTG maintainers
- Supersedes: none
- Superseded by: none

## Context

ThinkPixelTG stores many tenants in one PostgreSQL schema. Every tenant-owned
table has `tenant_id` in its primary, foreign, and unique keys, and tenant
repositories bind an authoritative tenant to every operation. DATA-017 evaluates
PostgreSQL row-level security (RLS) as an additional containment boundary.

RLS needs a trustworthy database-visible tenant identity. The current runtime
uses a shared pgx connection pool and repository methods may execute outside an
explicit transaction. A session setting can survive connection reuse if cleanup
is missed; `SET LOCAL` is safe only when every tenant operation is transaction
bound. Phase 2 also has deliberately cross-tenant infrastructure work, including
outbox claiming, which would need a separate role and carefully reviewed policy
bypass. Phase 3 has not yet established the authenticated principal from which a
database tenant setting could safely be derived.

## Decision

Do not enable RLS for the release candidate. Repository enforcement remains the
primary runtime isolation boundary:

- `NewTenantRepositories` validates and captures one tenant; individual methods
  never accept a tenant argument;
- `tenantScopedDB` rejects tenant-repository SQL unless it references
  `tenant_id`, uses `$1`, and binds the captured tenant as the first argument;
- tenant-owned primary, foreign, and unique keys include `tenant_id`, preventing
  cross-tenant child references and same-identifier collisions;
- handwritten, parameterized SQL and tenant predicates remain mandatory; and
- cross-tenant workers do not receive tenant repositories and must constrain
  mutations using complete composite identifiers and fencing.

The query guard is intentionally a tripwire, not a SQL parser. Code review and
real-PostgreSQL adversarial tests remain required because merely mentioning
`tenant_id` does not prove that every join and predicate is correct.

## Alternatives considered

- A custom session setting such as `app.tenant_id` with an RLS policy. Rejected
  for the RC because pooled session state can leak between requests and current
  repository calls are not universally transaction scoped.
- `SET LOCAL app.tenant_id` in every transaction. This avoids session leakage,
  but would require making every tenant operation transactional and still needs
  distinct operational roles for cross-tenant workers.
- One database role or schema per tenant. This creates provisioning, migration,
  pooling, and observability costs disproportionate to the current deployment
  model.

## Consequences

The RC has no database policy capable of containing arbitrary SQL executed by a
compromised runtime role. Its isolation depends on the repository boundary,
composite schema constraints, least privilege, review, and tests. In return, the
system avoids mutable pooled tenant context and keeps cross-tenant worker access
explicit.

RLS must be reevaluated after Phase 3 identity is available and before a
multi-tenant production release. Adoption requires transaction-local tenant
context, forced RLS for the service role, separate migration and cross-tenant
worker roles, connection-reuse tests, policy coverage checks for every
tenant-owned table, and operational recovery procedures. Repository scoping
remains mandatory even if RLS is later enabled.

## Security evidence

Unit tests prove the query guard rejects missing tenant references, missing
bindings, malformed bindings, and caller tenant substitution. Real-PostgreSQL
repository tests prove guessed cross-tenant reads fail, composite foreign keys
reject cross-tenant parent references, identical object IDs remain isolated, and
tenant-scoped upserts do not overwrite another tenant. DATA-018 records the full
Phase 2 evidence run.

## References

- [Phase 2 delivery plan](../../PLAN.md#phase-2--authoritative-persistence-and-invocation-primitives)
- [PostgreSQL transaction contract](../contracts/postgresql-transactions.md)
- [PostgreSQL persistence tooling](0001-postgresql-persistence-tooling.md)
- [Threat model](../security/threat-model.md)
