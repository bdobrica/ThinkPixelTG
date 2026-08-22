-- ThinkPixelTG Phase 0 logical PostgreSQL schema draft. Not an executable migration.
-- Production migrations add deployment-specific roles, grants, partitions, and triggers.
BEGIN;

CREATE TYPE tool_version_state AS ENUM ('draft','published','retired');
CREATE TYPE invocation_state AS ENUM ('received','validated','authorized','pre_tool_passed','waiting_for_approval','ready','executing','retry_wait','reconciling','ambiguous','manual_review','post_tool','succeeded','failed','denied','blocked','cancelled');

CREATE TABLE tenants (tenant_id uuid PRIMARY KEY, created_at timestamptz NOT NULL);
CREATE TABLE tools (tool_id text PRIMARY KEY, created_at timestamptz NOT NULL);
CREATE TABLE tool_versions (
  tool_id text NOT NULL REFERENCES tools, version text NOT NULL,
  state tool_version_state NOT NULL, definition jsonb NOT NULL,
  definition_digest bytea NOT NULL CHECK (octet_length(definition_digest)=32),
  published_at timestamptz, PRIMARY KEY(tool_id,version),
  CHECK ((state='draft') OR published_at IS NOT NULL)
);
CREATE TABLE tenant_tool_exposures (
  tenant_id uuid NOT NULL REFERENCES tenants, tool_id text NOT NULL, version text NOT NULL,
  enabled boolean NOT NULL, updated_at timestamptz NOT NULL,
  PRIMARY KEY(tenant_id,tool_id,version),
  FOREIGN KEY(tool_id,version) REFERENCES tool_versions
);
CREATE TABLE connector_instances (
  tenant_id uuid NOT NULL REFERENCES tenants, connector_instance_id uuid NOT NULL,
  connector_type text NOT NULL, destination_config jsonb NOT NULL,
  config_digest bytea NOT NULL, enabled boolean NOT NULL,
  PRIMARY KEY(tenant_id,connector_instance_id)
);
CREATE TABLE credential_bindings (
  tenant_id uuid NOT NULL REFERENCES tenants, credential_binding_id uuid NOT NULL,
  connector_instance_id uuid NOT NULL, provider_ref text NOT NULL,
  capability_metadata jsonb NOT NULL, enabled boolean NOT NULL,
  PRIMARY KEY(tenant_id,credential_binding_id),
  FOREIGN KEY(tenant_id,connector_instance_id) REFERENCES connector_instances
);
CREATE TABLE invocations (
  tenant_id uuid NOT NULL REFERENCES tenants, invocation_id uuid NOT NULL,
  run_id text NOT NULL, tool_call_id text NOT NULL, tool_id text NOT NULL, tool_version text NOT NULL,
  argument_profile text NOT NULL, argument_digest bytea NOT NULL,
  resource_projection jsonb NOT NULL, resource_digest bytea NOT NULL,
  retry_class text NOT NULL, state invocation_state NOT NULL, state_version bigint NOT NULL DEFAULT 0,
  terminal_code text, created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
  PRIMARY KEY(tenant_id,invocation_id), UNIQUE(tenant_id,run_id,tool_call_id),
  FOREIGN KEY(tool_id,tool_version) REFERENCES tool_versions,
  CHECK(octet_length(argument_digest)=32 AND octet_length(resource_digest)=32)
);
CREATE TABLE attempts (
  tenant_id uuid NOT NULL, invocation_id uuid NOT NULL, attempt_no integer NOT NULL,
  fence bigint NOT NULL, owner_id text, lease_expires_at timestamptz, classification text,
  downstream_request_ref text, evidence jsonb NOT NULL DEFAULT '{}',
  started_at timestamptz NOT NULL, finished_at timestamptz,
  PRIMARY KEY(tenant_id,invocation_id,attempt_no), UNIQUE(tenant_id,invocation_id,fence),
  FOREIGN KEY(tenant_id,invocation_id) REFERENCES invocations
);
CREATE TABLE authorization_decisions (
  tenant_id uuid NOT NULL, invocation_id uuid NOT NULL, decision_id text NOT NULL,
  context_digest bytea NOT NULL, outcome text NOT NULL, policy_ref text NOT NULL,
  constraints jsonb NOT NULL, issued_at timestamptz NOT NULL, expires_at timestamptz NOT NULL,
  revocation_checkpoint text, PRIMARY KEY(tenant_id,invocation_id,decision_id),
  FOREIGN KEY(tenant_id,invocation_id) REFERENCES invocations
);
CREATE TABLE gr_evaluations (
  tenant_id uuid NOT NULL, invocation_id uuid NOT NULL, evaluation_id text NOT NULL,
  phase text NOT NULL, content_digest bytea NOT NULL, decision text NOT NULL,
  policy_ref text NOT NULL, created_at timestamptz NOT NULL,
  PRIMARY KEY(tenant_id,invocation_id,evaluation_id),
  FOREIGN KEY(tenant_id,invocation_id) REFERENCES invocations
);
CREATE TABLE approvals (
  tenant_id uuid NOT NULL, invocation_id uuid NOT NULL, approval_id text NOT NULL,
  binding_digest bytea NOT NULL, status text NOT NULL, expires_at timestamptz NOT NULL,
  consumed_at timestamptz, revocation_checkpoint text,
  PRIMARY KEY(tenant_id,invocation_id,approval_id), UNIQUE(tenant_id,approval_id),
  FOREIGN KEY(tenant_id,invocation_id) REFERENCES invocations
);
CREATE TABLE execution_results (
  tenant_id uuid NOT NULL, invocation_id uuid NOT NULL, result_digest bytea,
  safe_result jsonb, classification text NOT NULL, downstream_ref text,
  created_at timestamptz NOT NULL, PRIMARY KEY(tenant_id,invocation_id),
  FOREIGN KEY(tenant_id,invocation_id) REFERENCES invocations
);
CREATE TABLE reconciliations (
  tenant_id uuid NOT NULL, invocation_id uuid NOT NULL, sequence integer NOT NULL,
  outcome text NOT NULL, evidence jsonb NOT NULL, created_at timestamptz NOT NULL,
  PRIMARY KEY(tenant_id,invocation_id,sequence),
  FOREIGN KEY(tenant_id,invocation_id) REFERENCES invocations
);
CREATE TABLE trusted_usage_events (
  tenant_id uuid NOT NULL, event_id uuid NOT NULL, invocation_id uuid NOT NULL,
  accounting_key text NOT NULL, dimension text NOT NULL, quantity numeric NOT NULL,
  occurred_at timestamptz NOT NULL, PRIMARY KEY(tenant_id,event_id),
  UNIQUE(tenant_id,accounting_key,dimension),
  FOREIGN KEY(tenant_id,invocation_id) REFERENCES invocations
);
CREATE TABLE audit_events (
  tenant_id uuid NOT NULL, event_id uuid NOT NULL, invocation_id uuid,
  event_type text NOT NULL, correlation jsonb NOT NULL, safe_payload jsonb NOT NULL,
  occurred_at timestamptz NOT NULL, PRIMARY KEY(tenant_id,event_id)
);
CREATE TABLE outbox (
  tenant_id uuid NOT NULL, outbox_id uuid NOT NULL, event_id uuid NOT NULL,
  topic text NOT NULL, safe_payload jsonb NOT NULL, created_at timestamptz NOT NULL,
  available_at timestamptz NOT NULL, claim_owner text, claim_until timestamptz,
  attempts integer NOT NULL DEFAULT 0, published_at timestamptz, dead_lettered_at timestamptz,
  PRIMARY KEY(tenant_id,outbox_id), UNIQUE(tenant_id,topic,event_id)
);
CREATE INDEX invocations_claim_idx ON invocations(state,updated_at) WHERE state IN ('ready','retry_wait','reconciling');
CREATE INDEX outbox_claim_idx ON outbox(available_at) WHERE published_at IS NULL AND dead_lettered_at IS NULL;
COMMIT;
