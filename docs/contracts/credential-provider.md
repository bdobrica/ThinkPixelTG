# Credential provider and binding contract

Status: Phase 0 normative baseline; contract version `tg.credential/v1alpha1`

## Binding

A credential binding is an administrator-authored, tenant-scoped reference joining
an immutable tool selector/connector instance to provider, capability type,
audience/resource, scopes, optional subject-delegation policy, and cache/revocation
policy. Selection uses trusted tool/resource/context only and occurs after current
authorization, mandatory GR, approval, enabled-state, and fencing checks.

Ordinary callers cannot provide provider names, secret paths/names, raw tokens,
authorization headers, scopes, audiences, destinations, or binding overrides.
Conflicting fields are rejected rather than ignored. PostgreSQL stores references
and safe metadata only; the provider owns secret plaintext.

## Provider interface

`Resolve(context, binding, governed_subject, deadline) -> Capability | Error`

A typed capability contains kind (`oauth_access_token`, `api_token`, `mtls`,
`signed_request`, or connector-specific reviewed type), opaque secret material,
audience/resource/scopes, issuer/provider reference, issued/expiry times, lease ID,
refresh/revocation metadata, and safe evidence metadata. Its string/debug/JSON/error
representations are always `[REDACTED]`. Connectors receive capabilities through a
non-serializable in-process interface and validate kind/audience before use.

Provider errors are typed as unavailable, denied, binding-invalid, expired,
revoked, rate-limited, or internal, with safe codes only. Inbound TG bearer tokens
are never passed through; token exchange obtains a distinct audience-bound token.

## Lifetime, refresh, and cache

Capabilities are least privilege, short lived, held in memory only, and released
after the attempt. Cache keys use trusted binding/context/subject/audience/scopes;
never caller strings or secret values. Entries expire before provider expiry with
bounded skew, have size/TTL bounds and singleflight, and are evicted on binding,
revocation, run, or provider-epoch change. Refresh repeats current authorization
preconditions. Cache/provider failure cannot fall back to stale or broader secrets.

## Redaction and audit

No plaintext capability enters logs, traces, metrics, panic dumps, AG/GR messages,
database, audit, outbox/dead letter, tests, or caller results. Evidence records
binding/provider/capability-type/lease-reference digests and timings only. Secret
canaries verify every serialization and failure path. Production rejects
development environment/file providers.
