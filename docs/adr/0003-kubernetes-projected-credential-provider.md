# ADR-0003: Kubernetes projected token as the RC production credential provider

- Status: accepted
- Date: 2026-09-03
- Decision owners: ThinkPixelTG maintainers
- Supersedes: none
- Superseded by: none

## Context

The release candidate needs one production-oriented credential provider behind
the provider port. Adding a cloud secrets SDK would couple the core release to a
vendor. OAuth token exchange needs the shared downstream HTTP controls scheduled
for CRED-008. The Kubernetes deployment baseline can project short-lived,
audience-bound service-account tokens into the TG pod without exposing them to
the harness or storing them in PostgreSQL.

## Decision

The first RC production provider is `kubernetes_projected`. Kubelet projects a
service-account token into the TG container and is authoritative for the token's
signature and file rotation. TG reads tokens only from deployment-configured,
exact absolute path allowlists. A credential binding uses a `projected:`
reference, declares the `api_token` capability and at least one audience, and
cannot request subject delegation.

On every resolution TG rereads the file, bounds it to 64 KiB, parses the compact
JWT claims without treating caller input as a token, and requires an allowlisted
issuer, all binding audiences, current `iat`/`exp`, and a configured maximum
lifetime. The capability expires no later than the token or binding TTL. File
bytes are copied into the opaque in-memory capability and the read buffer is
erased. Provider errors contain stable descriptions only.

This provider is for downstream systems that accept the projected Kubernetes
identity. It is not end-user delegation, a general file-secret provider, or a
substitute for AG authorization. OAuth exchange and cloud-provider adapters may
be added independently behind the same port.

## Alternatives considered

- Standards-based OAuth token exchange remains appropriate for delegated user
  access, but is deferred until the common outbound HTTP security layer exists.
- Vault and cloud-specific STS/workload federation are viable later adapters;
  selecting one now would add a vendor dependency without an RC deployment need.
- Kubernetes Secret mounts provide static secret bytes but lack the projected
  token's audience and expiry claims and make rotation semantics weaker.
- Independently verifying the projected JWT in TG duplicates API-server trust
  discovery and rotation. The protected kubelet mount is the credential-provider
  boundary; TG still validates the claims needed for correct use.

## Consequences

The RC gains a dependency-free production credential path aligned with its
Kubernetes baseline. Deployments must arrange a suitable downstream audience;
not every enterprise API accepts Kubernetes service-account tokens. Additional
providers remain replaceable and require no change to domain or connector ports.

## Security

The token volume MUST be mounted only into the TG container, never a harness
sidecar. The pod service account, projection audience, expiration, file path,
container access, and network egress are privileged deployment configuration.
Bindings cannot escape the configured path allowlist. A compromised TG pod or
kubelet remains able to access or replace the token, which is inherent in this
provider's trust boundary. Missing, malformed, expired, overlong, wrong-issuer,
or wrong-audience tokens fail closed and no source error or token bytes are
returned.

## Operations

Use a Kubernetes `serviceAccountToken` projected volume with an explicit
audience and short `expirationSeconds`, mounted read-only at an allowlisted path.
Kubelet rotates the file; TG rereads it for each uncached resolution. CRED-006 may
add a bounded in-memory cache that expires before the capability. Rotation and
read failures reduce credential availability and do not fall back to another
path, audience, provider, or stale token.

## Compatibility

This adds the internal provider key `kubernetes_projected` and `projected:`
reference profile. It does not alter a public HTTP contract or database schema.
Other providers may coexist behind `CredentialProvider`; removal of this profile
would require deployment migration of all referencing bindings.

## References

- [Credential provider and binding contract](../contracts/credential-provider.md)
- [System context and trust boundaries](../architecture/system-context.md)
- [Data classification and redaction](../security/data-classification.md)
- [Supported versions](../supported-versions.md)
- [Phase 5 delivery plan](../../PLAN.md#phase-5--credential-broker-and-first-real-connector)
