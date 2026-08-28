# Phase 3 identity and authorization evidence

Status: complete on 2026-08-28

Phase 3 establishes authenticated governed identity, workload identity, strict
ThinkPixelAG authorization, narrowing-only constraints, freshness/revocation
handling, protected-write readiness, and authorization-first execution ordering.

## Adversarial exit gate

The dedicated reproducible gate is:

```sh
make test-phase3
```

It runs the named authentication and authorization adversarial tests without the
Go test cache across `internal/authn`, `internal/adapters/thinkpixelag`, and
`internal/app`.

| Required property | Executable evidence |
|---|---|
| Cross-tenant/run forgery | Governed claim/body substitution and forged governance headers are rejected; tenant or run changes cannot reuse a cached allow and force a distinct live decision. |
| Stale decisions | Expired, future-issued, and not-yet-valid decisions fail closed against the injected UTC clock. |
| Malformed AG response | Truncated/trailing/oversized JSON, unknown fields/outcomes/reasons, missing correlation, and context/request mismatches produce no decision. |
| AG timeout/outage | A transport blocked until its bounded request deadline and an immediate connection failure both return errors and never synthesize an allow. |
| Revocation freshness | Unavailable state, newer epochs, and same-epoch checkpoint changes reject reuse; high-risk writes also reject old or revocation-behind live decisions. |
| Cache poisoning | Returned decisions are deep-copied; caller mutation of constraint slices and maps cannot alter the cached allow. |
| Constraint expansion | Set intersections discard out-of-ceiling repositories/resources/actions, empty intersections fail, unknown argument constraints fail, and numeric sweeps never exceed either the immutable ceiling or AG bound. |
| Enforcement order | Panic canaries prove denial, malformed decisions, and AG errors cannot reach credential or connector boundaries. |

## Gate result

On 2026-08-28, `make test-phase3` passed all three packages. The full formatting,
lint, vet, unit, and race-test gates also completed successfully. Phase 4 can
therefore build catalog and invocation APIs on the fail-closed identity and
authorization boundary established here.
