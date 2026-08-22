# Canonical JSON, digest, and transformation contract

Status: Phase 0 normative baseline; profile `tg-cjson-v1`

## Validation and ordering

1. Enforce content type and compressed/uncompressed byte, depth, member, string,
   and numeric-token limits; reject duplicate object member names.
2. Parse UTF-8 JSON with arbitrary-precision number handling. Reject invalid UTF-8,
   non-finite numbers, leading zeros, overflow, and schema-disallowed values.
3. Validate the raw value against the immutable input JSON Schema; defaults are
   not silently injected unless the tool version explicitly defines them.
4. Canonicalize using RFC 8785 JSON Canonicalization Scheme (JCS), with I-JSON
   constraints. Object keys sort by UTF-16 code units; strings use JSON escaping;
   numbers follow JCS/ECMAScript serialization. Integers outside the exactly
   interoperable IEEE-754 range require schema representation as strings.
5. Compute `SHA-256("ThinkPixelTG:arguments:tg-cjson-v1\x00" || canonical_bytes)`;
   encode lowercase hexadecimal and persist profile plus digest.
6. Project the typed resource from the validated canonical value, canonicalize it
   under the same profile with domain `ThinkPixelTG:resource:tg-cjson-v1`, and digest.

Schema validation precedes canonical authorization data so rejected or ambiguous
values never receive a policy decision. The canonical bytes, not input spelling,
bind replay and approval. Raw arguments are not persisted by default.

## Transformations

GR transformations are structured JSON values, never textual patches. TG applies
the transform to a bounded copy, then repeats schema validation, canonicalization,
argument/resource digests, risk/resource projection, AG authorization, and approval
matching. Any security-relevant difference invalidates previous decisions. Output
transforms repeat output-schema validation and result limits before disclosure.

## Deterministic vectors

The machine-readable fixture is `docs/contracts/testdata/canonical-json-v1.json`.
Implementations in every language MUST produce its exact canonical UTF-8 bytes and
digests, and MUST reject every reject vector. Adding vectors is backward compatible;
changing an existing expected value requires a new profile/version.
