# Core Secret Rotation Migration V1 Temp Spec

## 0. Context

Project: `aurora/controlplane`
Module: `internal/core`
Purpose: define a minimal source of truth for shared secret rotation.

This spec is only for **database migration design** of the `core` module.

This spec does **not** implement:
- secret provider runtime
- IAM auth flow
- handler
- repository
- service
- KMS integration
- secret distribution worker

Goal of v1:
- create a clean DB foundation for secret families and secret versions
- support safe rotation with one active primary signer and optional overlap window
- let IAM and other modules consume rotated secrets later through runtime code

---

## 1. Scope

V1 only includes these database concerns:

1. secret family registry
2. secret version registry
3. activation / retirement / revoke lifecycle fields
4. uniqueness rules for versioning and primary key selection
5. migration-safe indexes and comments

V1 intentionally excludes:
- operational audit event table for secret rotation
- secret access logs
- secret lease / worker ownership table
- secret distribution progress table
- project-specific secret scoping
- tenant-scoped secret storage

---

## 2. Folder Convention

Follow module-local migration structure.

Target folder:

`internal/core/migrations/`

Expected shape:

```txt
internal/core/migrations/
├── 000001_core_enums.up.sql
├── 000001_core_enums.down.sql
├── 000002_core_tables.up.sql
├── 000002_core_tables.down.sql
├── 000003_core_indexes.up.sql
├── 000003_core_indexes.down.sql
├── embed.go
```

For v1, functions / triggers / seeds are optional.
If we only need enums, tables, indexes, then stop there.

---

## 3. Design Rules

### 3.1 Primary keys

All primary keys use `UUID` and must be generated as UUIDv7 in application/service layer.

Examples:
- family id: UUIDv7
- version id: UUIDv7

Migration does not generate IDs.
Application/service layer generates UUIDv7 IDs.

### 3.2 Timestamps

Main tables must include:

- `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`

Mutable tables must include:

- `updated_at TIMESTAMPTZ NOT NULL DEFAULT now()`

### 3.3 Secret storage rule

Never store raw secret as plain text.

V1 stores:
- `secret_ciphertext`
- `secret_fingerprint`

Meaning:
- `secret_ciphertext` contains encrypted secret material
- `secret_fingerprint` is a deterministic non-sensitive fingerprint used for duplicate detection and operational reference

### 3.4 Rotation model

A secret family can have multiple versions.

Rules:
- only one version should be primary for new signing at a time
- old versions may remain active during a grace period for verification
- revoked version must never be primary
- retired version should not be used for new issue/sign operations

### 3.5 Search path

Migration must work under configured PostgreSQL search path.

Current expected app config default:

```txt
PSQL_SCHEMA=iam,public
```

When `core` migration is introduced, expected search path will become:

```txt
PSQL_SCHEMA=core,iam,public
```

`RunMigrations()` should create and migrate `core` using config-defined search path, not hardcoded search path.

---

## 4. Enum Design

### 4.1 `core_secret_status`

Allowed values:
- `pending`
- `active`
- `retired`
- `revoked`

Semantics:
- `pending`: created but not yet serving traffic
- `active`: usable for sign and/or verify depending on `is_primary`
- `retired`: no longer used for new signing, may already be outside grace period
- `revoked`: explicitly invalidated and must not be used

---

## 5. Table: `core_secret_families`

### 5.1 Purpose

Registry of all secret families used by the controlplane.

Each row defines one logical family of secrets that rotates independently.

Examples:
- `access_token`
- `refresh_token`
- `admin_api_key`
- `one_time_token`

If needed later, `cookie_signing` can be added as another family, but it is not required for v1.

### 5.2 Columns

- `id UUID PRIMARY KEY`
- `code TEXT NOT NULL`
- `name TEXT NOT NULL`
- `description TEXT NULL`
- `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`

### 5.3 Constraints

- `UNIQUE(code)`

### 5.4 Required comments

Need `COMMENT ON TABLE` and `COMMENT ON COLUMN` for all fields.

### 5.5 Notes

`code` is the stable lookup key used by runtime secret provider.

Recommended initial family codes:
- `access_token`
- `refresh_token`
- `admin_api_key`
- `one_time_token`

---

## 6. Table: `core_secret_versions`

### 6.1 Purpose

Stores actual rotated versions of a secret family.

One family has many versions.

This is the main source of truth for:
- current primary secret
- previous active secrets during overlap window
- revoked / retired history

### 6.2 Columns

- `id UUID PRIMARY KEY`
- `family_id UUID NOT NULL REFERENCES core_secret_families(id) ON DELETE CASCADE`
- `version INT NOT NULL`
- `secret_ciphertext TEXT NOT NULL`
- `secret_fingerprint TEXT NOT NULL`
- `status core_secret_status NOT NULL DEFAULT 'pending'`
- `is_primary BOOLEAN NOT NULL DEFAULT false`
- `not_before TIMESTAMPTZ NOT NULL DEFAULT now()`
- `not_after TIMESTAMPTZ NULL`
- `activated_at TIMESTAMPTZ NULL`
- `retired_at TIMESTAMPTZ NULL`
- `revoked_at TIMESTAMPTZ NULL`
- `rotation_reason TEXT NULL`
- `created_at TIMESTAMPTZ NOT NULL DEFAULT now()`
- `updated_at TIMESTAMPTZ NOT NULL DEFAULT now()`

### 6.3 Column meanings

`version`
- monotonic version number inside one family

`secret_ciphertext`
- encrypted secret material
- plaintext must never be stored

`secret_fingerprint`
- operational fingerprint for duplicate detection and reference
- must not reveal the secret itself

`status`
- lifecycle state of the version

`is_primary`
- whether this version is the current primary version for new sign/issue operations

`not_before`
- earliest timestamp this version is considered usable

`not_after`
- latest timestamp this version may still be considered valid for verify flow if runtime chooses to respect this boundary

`activated_at`
- timestamp when version becomes operationally active

`retired_at`
- timestamp when version is retired from active serving

`revoked_at`
- timestamp when version is explicitly revoked

`rotation_reason`
- optional short human-readable reason for rotation

### 6.4 Constraints

- `UNIQUE(family_id, version)`
- `UNIQUE(secret_fingerprint)`
- `CHECK ((status = 'revoked' AND revoked_at IS NOT NULL) OR status <> 'revoked')`
- `CHECK (NOT (is_primary = true AND status IN ('retired', 'revoked')))`

### 6.5 Partial unique index rule

Must enforce at most one non-revoked primary version per family:

```sql
CREATE UNIQUE INDEX ux_core_secret_versions_one_primary
ON core_secret_versions(family_id)
WHERE is_primary = true AND revoked_at IS NULL;
```

This still allows:
- one primary active version
- multiple active non-primary versions for verify overlap

### 6.6 Required comments

Need `COMMENT ON TABLE` and `COMMENT ON COLUMN` for all fields.

---

## 7. Seed Recommendation

V1 may seed `core_secret_families`.

Recommended seed records:
- `access_token`
- `refresh_token`
- `admin_api_key`
- `one_time_token`

Family IDs are UUIDv7 values generated by application/service layer.
No string prefix naming convention is used for this module.

No version rows should be seeded in migration.
Secret versions must be created by service/runtime/bootstrap logic after deployment.

---

## 8. Index Design

### 8.1 `core_secret_families`

Indexes:
- unique index on `code`

### 8.2 `core_secret_versions`

Indexes:
- unique index on `(family_id, version)`
- unique index on `secret_fingerprint`
- index on `(family_id, status)`
- index on `(family_id, created_at DESC)`
- partial unique index for one primary version per family

---

## 9. Migration Order

### Up

1. create enum `core_secret_status`
2. create `core_secret_families`
3. create `core_secret_versions`
4. create indexes
5. add comments
6. optional seed for secret families

### Down

1. delete seeded secret families if seeding is used
2. drop indexes
3. drop `core_secret_versions`
4. drop `core_secret_families`
5. drop enum `core_secret_status`

---

## 10. Operational Rules For Runtime

These are not migration tasks, but schema must support them.

### 10.1 Rotation flow

1. create new secret version in `pending`
2. distribute / warm runtime if needed
3. mark version `active`
4. set `is_primary=true` on new version
5. set old primary `is_primary=false`
6. keep old version `active` during overlap verify window if needed
7. later set old version `retired`
8. revoke only when emergency or explicit invalidation is required

### 10.2 Verify behavior

Runtime secret provider may load:
- primary active secret for sign
- all active non-revoked secrets for verify

### 10.3 Rollback behavior

If a newly promoted primary secret fails operationally:
- demote new primary
- promote previous active version back to primary
- do not require DB data rewrite outside version metadata updates

---

## 11. Out Of Scope For V1

Do not add these yet:
- `core_secret_rotation_events`
- per-tenant secrets
- per-workspace secrets
- KMS provider metadata table
- envelope-key hierarchy table
- background rotation lease table
- secret read audit table

Those can be added in v2 if needed.

---

## 12. Acceptance Criteria

Migration is acceptable when:

1. `core` module has its own migration folder
2. `core_secret_families` exists
3. `core_secret_versions` exists
4. enum `core_secret_status` exists
5. only one non-revoked primary version per family is allowed
6. multiple active non-primary versions per family are allowed
7. raw secret plaintext is not stored
8. all tables have `COMMENT ON TABLE`
9. all columns have `COMMENT ON COLUMN`
10. migration works with config-defined PostgreSQL search path
11. migration does not introduce project scope, tenant scope, or workspace scope into secret storage

---

## 13. Proposed Next Step After Approval

If this spec is approved, next implementation task should be:

1. create `internal/core/migrations/`
2. write enums/tables/indexes migration files
3. add `embed.go`
4. update bootstrap migration runner to include `core` before `iam`
5. update default `PSQL_SCHEMA` from `iam,public` to `core,iam,public`
6. then design `internal/core` entity/model/repository/provider` layers
