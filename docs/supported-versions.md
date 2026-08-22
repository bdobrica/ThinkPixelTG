# Supported versions and upgrade policy

Status: Phase 0 baseline, verified 2026-08-22

| Technology/standard | RC baseline | Policy |
|---|---|---|
| Go | 1.27.0 toolchain/language | pin exact patch in builds; support current and previous Go major only during migration; security patches expedited |
| PostgreSQL | 18.6; protocol/features compatible with PostgreSQL 17.11 where tests pass | pin image digest/current minor; PostgreSQL 18 is production baseline; test upgrades and backup/restore |
| Valkey (optional) | 9.1.1 | pin digest; acceleration only and never authoritative; service remains correct when absent |
| Kubernetes | 1.35-1.36 API baseline; conformance target 1.36.2 | support currently maintained minors after conformance; avoid alpha APIs |
| OpenAPI | 3.1.2 with JSON Schema 2020-12 | API document remains 3.1 until explicit compatibility review; lint against current 3.1 schema |
| MCP | revision 2025-11-25, Streamable HTTP | exact protocol revision negotiation; upgrades require contract/conformance change |
| OIDC/JWT | OpenID Connect Core 1.0 (errata 2); RFC 7519/8725/9068 | explicit issuer/audience/resource/algorithm profiles; track security BCP updates |
| OAuth/token exchange | OAuth 2.0 RFC 6749 + Security BCP RFC 9700; RFC 8693; protected-resource metadata RFC 9728 | no inbound token passthrough; sender/audience-bound profiles selected per integration |
| SPIFFE | specification 1.15.2; X.509-SVID/Workload API profile | upgrade only after identity interoperability and trust-bundle rotation tests |
| OpenTelemetry | specification 1.57.0; W3C Trace Context | pin compatible Go modules/collector image independently; no unstable semantic fields as durable contract |
| OCI | Image Spec 1.1.1, Distribution Spec 1.1.1, Runtime Spec 1.2.x | digest-pinned bases/artifacts, OCI labels, SBOM/provenance/signature hooks |

## Dependency policy

Direct dependencies require a documented owner/purpose, compatible license,
maintained upstream, checksum lock, vulnerability/source review, and preference for
standard library or narrow interfaces. Build tools and images are exact-version and
digest pinned. Lockfiles/checksums and generated artifacts are reviewed in CI.

Patch/security updates may be fast-tracked after focused tests. Minor/major updates
require release notes, compatibility/security review, full verify, contract and
migration drift checks, and rollback/forward-fix plan. MCP, OpenAPI major/minor,
identity profiles, database major, and canonicalization changes require explicit
ADR/contract revision. Released migrations and immutable tool semantics are never
rewritten to accommodate an upgrade.

CI produces dependency/source/license/vulnerability reports and SBOM. Critical or
high exploitable findings block release unless a time-bounded, owner-approved,
documented exception states reachability, compensating controls, and removal date.
End-of-life runtimes/platforms are unsupported. Baselines are reviewed monthly and
at every release gate; current patch adoption is validated rather than floating.

Official baselines: [Go releases](https://go.dev/doc/devel/release),
[PostgreSQL policy](https://www.postgresql.org/support/versioning/),
[Valkey downloads](https://valkey.io/download/),
[Kubernetes releases](https://kubernetes.io/releases/),
[OpenAPI specifications](https://spec.openapis.org/oas/),
[MCP transport revision](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports),
[SPIFFE specifications](https://spiffe.io/docs/latest/spiffe-specs/), and
[OpenTelemetry releases](https://github.com/open-telemetry/opentelemetry-specification/releases).
