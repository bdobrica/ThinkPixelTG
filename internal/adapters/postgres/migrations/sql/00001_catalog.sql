-- +goose Up

CREATE TABLE tenants (
    tenant_id uuid PRIMARY KEY,
    created_at timestamptz NOT NULL
);

CREATE TABLE tools (
    tool_id text PRIMARY KEY
        CHECK (tool_id ~ '^[a-z][a-z0-9]*(\.[a-z][a-z0-9_]*)+$'),
    created_at timestamptz NOT NULL
);

CREATE TABLE tool_versions (
    tool_id text NOT NULL REFERENCES tools (tool_id) ON DELETE RESTRICT,
    version text NOT NULL CHECK (version <> ''),
    state text NOT NULL CHECK (state IN ('draft', 'published', 'retired')),
    definition jsonb NOT NULL CHECK (jsonb_typeof(definition) = 'object'),
    definition_digest bytea NOT NULL CHECK (octet_length(definition_digest) = 32),
    published_at timestamptz,
    PRIMARY KEY (tool_id, version),
    CHECK (
        (state = 'draft' AND published_at IS NULL)
        OR (state IN ('published', 'retired') AND published_at IS NOT NULL)
    )
);

-- +goose StatementBegin
CREATE FUNCTION protect_released_tool_version() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.state IN ('published', 'retired') THEN
        IF TG_OP = 'DELETE' THEN
            RAISE EXCEPTION 'released tool versions are immutable'
                USING ERRCODE = '23514';
        END IF;

        IF OLD.state = 'published'
           AND NEW.state = 'retired'
           AND NEW.tool_id = OLD.tool_id
           AND NEW.version = OLD.version
           AND NEW.definition = OLD.definition
           AND NEW.definition_digest = OLD.definition_digest
           AND NEW.published_at = OLD.published_at THEN
            RETURN NEW;
        END IF;

        IF NEW IS DISTINCT FROM OLD THEN
            RAISE EXCEPTION 'released tool versions are immutable'
                USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER tool_versions_released_immutable
BEFORE UPDATE OR DELETE ON tool_versions
FOR EACH ROW EXECUTE FUNCTION protect_released_tool_version();

CREATE TABLE tenant_tool_exposures (
    tenant_id uuid NOT NULL REFERENCES tenants (tenant_id) ON DELETE RESTRICT,
    tool_id text NOT NULL,
    version text NOT NULL,
    enabled boolean NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, tool_id, version),
    FOREIGN KEY (tool_id, version)
        REFERENCES tool_versions (tool_id, version) ON DELETE RESTRICT
);

-- +goose StatementBegin
CREATE FUNCTION require_published_tool_version_for_exposure() RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    version_state text;
BEGIN
    IF NOT NEW.enabled THEN
        RETURN NEW;
    END IF;

    SELECT state INTO version_state
    FROM tool_versions
    WHERE tool_id = NEW.tool_id AND version = NEW.version
    FOR KEY SHARE;

    IF version_state IS DISTINCT FROM 'published' THEN
        RAISE EXCEPTION 'enabled tenant exposure requires a published tool version'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER tenant_tool_exposures_require_published
BEFORE INSERT OR UPDATE OF tool_id, version, enabled ON tenant_tool_exposures
FOR EACH ROW EXECUTE FUNCTION require_published_tool_version_for_exposure();

CREATE INDEX tenant_tool_exposures_enabled_idx
    ON tenant_tool_exposures (tenant_id, tool_id, version)
    WHERE enabled;

CREATE TABLE connector_instances (
    tenant_id uuid NOT NULL REFERENCES tenants (tenant_id) ON DELETE RESTRICT,
    connector_instance_id uuid NOT NULL,
    connector_type text NOT NULL CHECK (connector_type <> ''),
    destination_config jsonb NOT NULL
        CHECK (jsonb_typeof(destination_config) = 'object'),
    config_digest bytea NOT NULL CHECK (octet_length(config_digest) = 32),
    enabled boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, connector_instance_id),
    CHECK (updated_at >= created_at)
);

CREATE INDEX connector_instances_enabled_type_idx
    ON connector_instances (tenant_id, connector_type, connector_instance_id)
    WHERE enabled;

CREATE TABLE credential_bindings (
    tenant_id uuid NOT NULL REFERENCES tenants (tenant_id) ON DELETE RESTRICT,
    credential_binding_id uuid NOT NULL,
    connector_instance_id uuid NOT NULL,
    provider_ref text NOT NULL CHECK (provider_ref <> ''),
    capability_metadata jsonb NOT NULL
        CHECK (jsonb_typeof(capability_metadata) = 'object'),
    enabled boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (tenant_id, credential_binding_id),
    FOREIGN KEY (tenant_id, connector_instance_id)
        REFERENCES connector_instances (tenant_id, connector_instance_id)
        ON DELETE RESTRICT,
    CHECK (updated_at >= created_at)
);

CREATE INDEX credential_bindings_enabled_connector_idx
    ON credential_bindings (
        tenant_id,
        connector_instance_id,
        credential_binding_id
    )
    WHERE enabled;
