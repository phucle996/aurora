CREATE TABLE IF NOT EXISTS tenants (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name varchar(255) NOT NULL,
    status varchar(32) NOT NULL DEFAULT 'active',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS tenant_domains (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    domain varchar(255) NOT NULL,
    is_primary boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS tenant_memberships (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id uuid NOT NULL,
    status varchar(32) NOT NULL DEFAULT 'active',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS tenant_roles (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    code varchar(128) NOT NULL,
    name varchar(255) NOT NULL,
    description text NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS tenant_membership_roles (
    membership_id uuid NOT NULL REFERENCES tenant_memberships(id) ON DELETE CASCADE,
    role_id uuid NOT NULL REFERENCES tenant_roles(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (membership_id, role_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS tenant_domains_domain_uidx ON tenant_domains(domain);
CREATE UNIQUE INDEX IF NOT EXISTS tenant_memberships_tenant_user_uidx ON tenant_memberships(tenant_id, user_id);
CREATE UNIQUE INDEX IF NOT EXISTS tenant_roles_tenant_code_uidx ON tenant_roles(tenant_id, code);
