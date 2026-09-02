# Tool identity and publication contract

Status: Phase 0 normative baseline; contract version `tg.tool-catalog/v1alpha1`

## Identity

A tool is identified by a stable lower-case dotted `tool_id` such as
`github.pull.comment`. A published version is addressed by `(tool_id, version)`;
tenant is exposure scope, not part of tool identity. Versions use SemVer 2.0.0.
For a given `tool_id`, each new version MUST have greater SemVer precedence than
every existing version. Build metadata does not affect precedence and therefore
cannot create a distinct revision at the same precedence. Any change to
executable or security-relevant semantics requires a new version.
Discovery may select an administrator-configured default, but invocation persists
the exact version before authorization and never floats during execution/replay.

## Immutable version record

A publishable version MUST contain:

- input and output JSON Schemas and canonicalization profile;
- reviewed name/description and result open-world trust label;
- trusted side-effect/risk and approval classes;
- retry/idempotency class and its proof/qualification reference;
- connector type, compiled operation, and connector-instance selector;
- trusted credential-binding selector (never secret plaintext/name from caller);
- deterministic resource-projection definition and required fields;
- request/result/deadline/concurrency limits;
- GR pre/post profiles and whether each is mandatory;
- metering rule and evidence classification/retention policy;
- exposure compatibility annotations derived from the preceding fields.

Publication validates the complete record and atomically changes `draft` to
`published`. Published fields are immutable in application code and database
constraints. Corrections require a new version; released migrations/records are
not edited in place.

Publication is fail closed. The transition MUST compile both bounded schemas and
MUST reject a record unless the connector type, operation, instance selector,
credential-binding selector, retry qualification reference, canonicalization
profile, request/result/deadline/concurrency limits, and at least one required
resource-projection field are valid. A reviewed description consists of bounded
title and description text plus a non-empty administrative review reference;
unreviewed downstream text is not publishable.

The metering rule MUST declare an exact non-negative decimal quantity, dimension,
charge point (`attempt`, `confirmed_side_effect`, `result`, `provider_unit`, or
`tool_specific`), and deduplication scope (`logical_invocation`, `attempt`, or
`provider_unit`). These fields describe immutable accounting semantics; they do
not themselves emit or accept a usage event.

## Trusted metadata

Risk (`read`, `bounded_write`, `consequential_write`, `privileged`), side-effect,
approval, retry, open-world, resource, destination, and credential metadata are
administrator-controlled security facts. Connector guesses, model text, caller
fields, MCP annotations, or provider responses cannot override them. Derived MCP
annotations are compatibility hints only.

## Exposure and lifecycle

Tenant exposure independently maps an enabled published version to a tenant and
optional discovery policy. `disabled` prevents new invocation acquisition and
must be rechecked immediately before credential resolution. An already committed
side effect remains historically valid. `retired` prevents new exposure and
default selection but preserves lookup/replay/evidence. Records are never deleted
while referenced. Emergency disable is separately authorized and audited.

Discovery is tenant scoped, authorization filtered, deterministically ordered,
and enumeration safe. A caller cannot request an unexposed version by knowing its
ID. Replay of an existing logical invocation uses its persisted version even if
the default changed, but cannot execute if current disable/revocation rules block.
Discovery candidates are derived only from the authenticated tenant's enabled,
published exposures. Authorization may only remove those candidates; an unavailable
or malformed discovery decision fails closed and never returns the unfiltered set.

## Connector and resource binding

The connector operation is a compiled registry key, not executable code or URL.
The connector instance resolves to an administrator-controlled destination.
Resource projection runs only after schema validation/canonicalization and yields
a typed, bounded structure consumed by AG, approval, evidence, and binding logic.
Missing or ambiguous required projection makes the version unpublishable or the
call invalid; it never falls back to an unconstrained resource.

## Schema processing

Input and output schemas use JSON Schema 2020-12. TG compiles schemas under the
capacity limits in `docs/operations/slos-and-capacity.md` and accepts only local
`$ref`/`$dynamicRef` targets; catalog compilation never performs network or file
resolution. Compiled schemas are immutable and may be held in a bounded process
cache keyed by the exact schema digest. Instance validation applies byte, depth,
node, object-member, string, and error-count bounds before returning deterministic,
payload-free violation codes and JSON Pointer locations.
