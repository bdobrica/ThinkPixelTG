# ThinkPixelGR pre-tool and post-tool contract

Status: Phase 0 normative baseline; profile `tg.gr.tool/v1alpha1`

## Common envelope

Mutually authenticated TG requests contain evaluation ID, phase, tenant/run/agent
opaque context required by policy, exact tool/version and risk/open-world labels,
policy profile/version, bounded content projection, content digest/classification,
resource projection/digest where applicable, deadline, and trace/evidence
correlation. They never contain downstream credentials, inbound authorization,
credential binding/provider secret references, cookies, or private keys.

GR responses strictly contain matching evaluation/correlation and content digest,
decision (`allow`, `block`, `redact`, `transform`, or `observe` as phase/profile
permits), stable reasons, policy/profile/version, timestamps, evidence reference,
and a bounded structured replacement when required. Unknown decisions, textual
patches, mismatched digests, invalid transforms, or oversize responses are malformed.

## `pre_tool`

TG sends validated canonical arguments or an explicitly defined minimized
projection plus trusted action/resource context, after initial AG authorization
and before credentials. `allow` continues; `block` terminally blocks; `transform`
supplies a complete structured argument value. TG then repeats schema validation,
canonicalization, resource/risk projection, AG authorization, and approval matching.
GR cannot authorize, broaden AG constraints, select credentials, or change the
tool/version/connector. Repeated transforms are bounded to prevent loops.

## `post_tool`

TG sends a size/content-bounded result projection labeled `open_world` or
`trusted_closed_world`, provider metadata classification, and output schema ID.
This occurs before any result reaches the harness. `block` suppresses content;
`redact`/`transform` supplies a replacement that TG size-checks and validates
against output schema. Provider/model instructions in results are untrusted data.

## Profiles and failure

`mandatory` means timeout, outage, authentication failure, malformed response, or
invalid transform blocks/fails the protected path without credential/result
disclosure. `observational` records unavailable/observed status and may continue
only where the immutable tool version and policy allow; it never converts a block
or authorization denial to allow. Writes default mandatory pre-tool. Result
handling posture is explicit per tool/profile.

TG persists IDs, decisions, reason/policy codes, timing, input/output digests, and
transform classification—not unnecessary full content—and applies C2 retention.
