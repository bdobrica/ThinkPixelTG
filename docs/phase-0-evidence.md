# Phase 0 completion evidence

Date: 2026-08-22

Phase 0 defines the ThinkPixelTG authority, security, domain, integration,
protocol, persistence, evidence, version, and operating contracts. It does not
claim runtime enforcement; implementation conformance belongs to later phases.

## Item evidence

| Items | Implementation commits | Primary artifacts |
|---|---|---|
| GOV-001 | `21eba26` | documentation layout and ADR template |
| GOV-002–005 | `e7f2b2b`, `b5fad60`, `7d6e8af`, `e964f2a` | context/trust boundaries, threat model, data classification, glossary/ownership |
| GOV-006–009 | `36749c4`, `31a4b77`, `6afffea`, `da1d7ec` | catalog, state machine, canonical JSON vectors, retry/idempotency |
| GOV-010–014 | `ebdba97`, `8795808`, `ab989d6`, `eef547c`, `401b983` | AG authorization/approval, GR, credential, and connector contracts |
| GOV-015–020 | `f0a34db`, `e842e69` (MCP baseline updated by `58a5978`), `994207d`, `a01c3d6`, `2747e37`, `287f8ed` | OpenAPI, MCP 2026-07-28, PostgreSQL, evidence, versions, SLO/capacity |
| GOV-021 | `99a0576` | authority closure review and disposition |

The corresponding checklist ledger commits record the completion date, artifact,
and exact implementation SHA without rewriting implementation history.

## Reproducible validation

Run from the repository root:

```sh
make verify
```

The Phase 0 gate pins Redocly CLI `2.46.2` and performs:

- OpenAPI 3.1 parsing, reference resolution, and recommended lint rules;
- `git diff HEAD --check` for whitespace errors;
- required-artifact checks;
- local Markdown-link validation;
- GOV-001 through GOV-022 checklist-state validation;
- required security-invariant coverage checks;
- canonical JSON fixture encoding and domain-separated SHA-256 verification;
- OpenAPI required-surface structural checks.

Observed result on 2026-08-22:

```text
api/openapi.yaml: validated
Woohoo! Your API description is valid.
Phase 0 validation passed: 23 artifacts, Markdown links, checklist,
security invariants, canonical JSON vectors, and OpenAPI structure.
```

The first linter run identified missing health-operation summaries and recommended
metadata/explicit client errors; these were corrected. The link validator identified
the initial README's nonexistent `LICENSE` link; the README and API now explicitly
state that license selection is pending rather than implying a license.

## Gate review

- The authority closure matrix in `docs/security/phase-0-authority-review.md`
  assigns one source and enforcement/failure posture to identity, tool semantics,
  authorization, approval, resource projection, destination, credential selection,
  replay/completion, persistence, evidence/usage, and administration.
- Protected paths are fail closed and credential resolution follows all mandatory
  pre-execution controls.
- Security-relevant transformations invalidate digest/resource/authorization/approval.
- Unknown post-send completion is consistently `ambiguous`, never a clean failure.
- Caller fields, MCP annotations/request IDs, inbound tokens, arbitrary URLs, and
  secret references cannot establish authority.
- C3 credential material is prohibited from durable records, policy/GR payloads,
  telemetry, evidence, and caller results.
- The PostgreSQL artifact is deliberately a logical schema draft rather than an
  executable migration; empty/prior-database migration testing begins in Phase 2.

No security-relevant Phase 0 contract remains implicit. Current version sources
were verified against official upstream publications on the review date. Future
changes that alter an authority owner, control order, failure posture, protocol, or
canonicalization profile require an explicit contract/ADR and compatibility review.
