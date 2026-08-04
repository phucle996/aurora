package iamRepoImpl

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	iamproto "controlplane/internal/iam/transport/rpc/proto"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
)

type RbacTenantRepository struct {
	cfg    *config.Config
	db     *pgxpool.Pool
	schema string
}

func NewRbacTenantRepository(cfg *config.Config, db *pgxpool.Pool) iamRepoInterface.RbacTenantRepository {
	return &RbacTenantRepository{cfg: cfg, db: db, schema: cfg.SchemaSQL.IAM}
}

func (r *RbacTenantRepository) ListTenantRoles(ctx context.Context, in *iamEntity.ListTenantRoles) ([]iamEntity.ListTenantRoles, error) {
	query := fmt.Sprintf(`
		WITH actor AS MATERIALIZED (
			SELECT mr.role_level
			FROM %s.tenant_memberships tm
			JOIN %s.membership_role mr
			  ON mr.membership_id=tm.id
			 AND mr.workspace_id='00000000-0000-0000-0000-000000000000'
			JOIN %s.tenant_role_permissions trp ON trp.tenant_role_id=mr.tenant_role_id
			JOIN %s.permissions p
			  ON p.id=trp.permission_id AND p.module='iam' AND p.object='role' AND p.behavior='read'
			WHERE tm.tenant_id=$1 AND tm.user_id=$2 AND tm.status='active'
			LIMIT 1
		), visible_roles AS (
			SELECT tr.id, tr.code, tr.name, COALESCE(tr.description, '') AS description,
			       tr.role_level, tr.version,
			       (SELECT COUNT(*) FROM %s.membership_role mr WHERE mr.tenant_role_id=tr.id) AS assignments_count,
			       (SELECT COUNT(*) FROM %s.tenant_role_permissions rp WHERE rp.tenant_role_id=tr.id) AS permissions_count,
			       tr.created_at
			FROM %s.tenant_roles tr CROSS JOIN actor
			WHERE tr.tenant_id=$1 AND tr.role_level > actor.role_level
		)
		SELECT EXISTS(SELECT 1 FROM actor),
		       COALESCE(id, '00000000-0000-0000-0000-000000000000'::uuid),
		       COALESCE(code, ''), COALESCE(name, ''), COALESCE(description, ''),
		       COALESCE(role_level, 0), COALESCE(version, 0),
		       COALESCE(assignments_count, 0), COALESCE(permissions_count, 0),
		       COALESCE(created_at, '0001-01-01 00:00:00Z'::timestamptz)
		FROM (SELECT 1) sentinel LEFT JOIN visible_roles ON true
		ORDER BY role_level, id
	`, r.cfg.SchemaSQL.Hierarchy, r.schema, r.schema, r.schema, r.schema, r.schema, r.schema)

	rows, err := r.db.Query(ctx, query, in.TenantID, in.ActorUserID)
	if err != nil {
		return nil, fmt.Errorf("tenant rbac repo: list roles: %w", err)
	}
	defer rows.Close()

	items := make([]iamEntity.ListTenantRoles, 0)
	authorized := false
	for rows.Next() {
		item := iamEntity.ListTenantRoles{ActorUserID: in.ActorUserID, TenantID: in.TenantID}
		if err := rows.Scan(
			&authorized, &item.ID, &item.Code, &item.Name, &item.Description,
			&item.RoleLevel, &item.Version, &item.AssignmentsCount, &item.PermissionsCount, &item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("tenant rbac repo: scan role: %w", err)
		}
		if item.ID != uuid.Nil {
			items = append(items, item)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tenant rbac repo: iterate roles: %w", err)
	}
	if !authorized {
		return nil, iamTaxonomy.ErrActionNotAllowed
	}
	return items, nil
}

func (r *RbacTenantRepository) CreateTenantRole(ctx context.Context, in *iamEntity.CreateTenantRole) (*iamEntity.CreateTenantRole, error) {
	query := fmt.Sprintf(`
		WITH requested AS MATERIALIZED (
			SELECT DISTINCT permission_id FROM unnest($9::uuid[]) AS requested(permission_id)
		), valid_permissions AS MATERIALIZED (
			SELECT requested.permission_id
			FROM requested JOIN %s.permissions p ON p.id=requested.permission_id
		), actor AS MATERIALIZED (
			SELECT mr.role_level, mr.tenant_role_id
			FROM %s.tenant_memberships tm
			JOIN %s.tenants t ON t.id=tm.tenant_id AND t.status='active'
			JOIN %s.membership_role mr
			  ON mr.membership_id=tm.id
			 AND mr.workspace_id='00000000-0000-0000-0000-000000000000'
			JOIN %s.tenant_role_permissions trp ON trp.tenant_role_id=mr.tenant_role_id
			JOIN %s.permissions p
			  ON p.id=trp.permission_id AND p.module='iam' AND p.object='role' AND p.behavior='write'
			WHERE tm.tenant_id=$2 AND tm.user_id=$3 AND tm.status='active'
			LIMIT 1
		), inserted_role AS (
			INSERT INTO %s.tenant_roles
				(id, tenant_id, code, name, description, role_level, version, created_by, created_at, updated_at)
			SELECT $1, $2, $4, $5, $6, $7, $8, $3, $10, $10
			FROM actor
			WHERE actor.role_level < $7
			  AND (SELECT COUNT(*) FROM requested)=(SELECT COUNT(*) FROM valid_permissions)
			RETURNING id, tenant_id, code, name, COALESCE(description, '') AS description,
			          role_level, version, created_at
		), inserted_permissions AS (
			INSERT INTO %s.tenant_role_permissions (tenant_role_id, permission_id, created_at)
			SELECT inserted_role.id, valid_permissions.permission_id, $10
			FROM inserted_role CROSS JOIN valid_permissions
			RETURNING permission_id
		)
		SELECT EXISTS(SELECT 1 FROM %s.tenants WHERE id=$2),
		       EXISTS(SELECT 1 FROM actor),
		       COALESCE((SELECT role_level FROM actor), 999) < $7,
		       (SELECT COUNT(*) FROM requested)=(SELECT COUNT(*) FROM valid_permissions),
		       EXISTS(SELECT 1 FROM inserted_role),
		       COALESCE((SELECT id FROM inserted_role), '00000000-0000-0000-0000-000000000000'::uuid),
		       COALESCE((SELECT code FROM inserted_role), ''),
		       COALESCE((SELECT name FROM inserted_role), ''),
		       COALESCE((SELECT description FROM inserted_role), ''),
		       COALESCE((SELECT role_level FROM inserted_role), 0),
		       COALESCE((SELECT version FROM inserted_role), 0),
		       COALESCE((SELECT created_at FROM inserted_role), $10)
	`, r.schema, r.cfg.SchemaSQL.Hierarchy, r.cfg.SchemaSQL.Hierarchy, r.schema, r.schema, r.schema, r.schema, r.schema, r.cfg.SchemaSQL.Hierarchy)

	out := &iamEntity.CreateTenantRole{ActorUserID: in.ActorUserID, TenantID: in.TenantID, PermissionIDs: in.PermissionIDs}
	var tenantExists, actorAuthorized, hierarchyOK, permissionsValid, inserted bool
	err := r.db.QueryRow(ctx, query,
		in.ID, in.TenantID, in.ActorUserID, in.Code, in.Name, in.Description,
		in.RoleLevel, in.Version, in.PermissionIDs, in.CreatedAt,
	).Scan(
		&tenantExists, &actorAuthorized, &hierarchyOK, &permissionsValid, &inserted,
		&out.ID, &out.Code, &out.Name, &out.Description, &out.RoleLevel, &out.Version, &out.CreatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, iamTaxonomy.ErrAlreadyExists
		}
		return nil, fmt.Errorf("tenant rbac repo: create role: %w", err)
	}
	if !tenantExists {
		return nil, iamTaxonomy.ErrNotFound
	}
	if !actorAuthorized || !hierarchyOK {
		return nil, iamTaxonomy.ErrActionNotAllowed
	}
	if !permissionsValid {
		return nil, iamTaxonomy.ErrPreconditionFailed
	}
	if !inserted {
		return nil, iamTaxonomy.ErrConflict
	}
	return out, nil
}

func (r *RbacTenantRepository) ResolveTenantAccess(ctx context.Context, in *iamEntity.ResolveTenantAccess) (*iamEntity.ResolveTenantAccess, error) {
	query := fmt.Sprintf(`
		SELECT mr.tenant_role_id, mr.role_level
		FROM %s.tenant_memberships tm
		JOIN %s.tenants t ON t.id=tm.tenant_id AND t.status='active'
		JOIN %s.tenant_domains td ON td.tenant_id=t.id AND lower(td.domain)=lower($3)
		JOIN %s.users u ON u.id=tm.user_id AND u.status='active'
		JOIN %s.membership_role mr
		  ON mr.membership_id=tm.id
		 AND mr.workspace_id='00000000-0000-0000-0000-000000000000'
		WHERE tm.user_id=$1 AND tm.tenant_id=$2 AND tm.status='active'
		ORDER BY mr.role_level, mr.tenant_role_id
		LIMIT 1
	`, r.cfg.SchemaSQL.Hierarchy, r.cfg.SchemaSQL.Hierarchy, r.cfg.SchemaSQL.Hierarchy, r.schema, r.schema)
	out := &iamEntity.ResolveTenantAccess{UserID: in.UserID, TenantID: in.TenantID, TenantDomain: in.TenantDomain}
	if err := r.db.QueryRow(ctx, query, in.UserID, in.TenantID, in.TenantDomain).Scan(&out.RoleID, &out.RoleLevel); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, iamTaxonomy.ErrActionNotAllowed
		}
		return nil, fmt.Errorf("tenant rbac repo: resolve access: %w", err)
	}
	return out, nil
}

func (r *RbacTenantRepository) GetRoleIDByUserAndTenantID(ctx context.Context, userID uuid.UUID, tenantID uuid.UUID) (string, int32, error) {
	query := fmt.Sprintf(`
		SELECT mr.tenant_role_id::text, mr.role_level
		FROM %s.tenant_memberships tm
		JOIN %s.tenants t ON t.id=tm.tenant_id AND t.status='active'
		JOIN %s.membership_role mr
		  ON mr.membership_id=tm.id
		 AND mr.workspace_id='00000000-0000-0000-0000-000000000000'
		WHERE tm.user_id=$1 AND tm.tenant_id=$2 AND tm.status='active'
		ORDER BY mr.role_level, mr.tenant_role_id
		LIMIT 1
	`, r.cfg.SchemaSQL.Hierarchy, r.cfg.SchemaSQL.Hierarchy, r.schema)
	var roleID string
	var level int32
	if err := r.db.QueryRow(ctx, query, userID, tenantID).Scan(&roleID, &level); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, iamTaxonomy.ErrNotFound
		}
		return "", 0, fmt.Errorf("tenant rbac repo: resolve role: %w", err)
	}
	return roleID, level, nil
}

func (r *RbacTenantRepository) GetUserTenantBillingPermissions(ctx context.Context, userID uuid.UUID, tenantID uuid.UUID) ([]byte, error) {
	binaryData, err := r.GetUserTenantRolePermissions(ctx, userID, tenantID)
	if err != nil {
		return nil, err
	}
	var source iamproto.RoleEntry
	if err := proto.Unmarshal(binaryData, &source); err != nil {
		return nil, fmt.Errorf("tenant rbac repo: decode Billing permissions: %w", err)
	}
	prefix := tenantID.String() + ":"
	filtered := make([]string, 0, len(source.Permissions))
	for _, permission := range source.Permissions {
		parts := strings.Split(permission, ":")
		if len(parts) == 5 && parts[0] == tenantID.String() && parts[2] == "billing" && strings.HasPrefix(permission, prefix) {
			filtered = append(filtered, permission)
		}
	}
	if len(filtered) == 0 {
		return nil, iamTaxonomy.ErrActionNotAllowed
	}
	return proto.MarshalOptions{Deterministic: true}.Marshal(&iamproto.RoleEntry{Permissions: filtered})
}

func (r *RbacTenantRepository) GetUserTenantRolePermissions(ctx context.Context, userID uuid.UUID, tenantID uuid.UUID) ([]byte, error) {
	query := fmt.Sprintf(`
		SELECT mr.list_perm
		FROM %s.tenant_memberships tm
		JOIN %s.tenants t ON t.id=tm.tenant_id AND t.status='active'
		JOIN %s.users u ON u.id=tm.user_id AND u.status='active'
		JOIN %s.membership_role mr ON mr.membership_id=tm.id
		WHERE tm.tenant_id=$1 AND tm.user_id=$2 AND tm.status='active'
		ORDER BY mr.workspace_id, mr.role_level, mr.id
	`, r.cfg.SchemaSQL.Hierarchy, r.cfg.SchemaSQL.Hierarchy, r.schema, r.schema)
	rows, err := r.db.Query(ctx, query, tenantID, userID)
	if err != nil {
		return nil, fmt.Errorf("tenant rbac repo: query compiled assignments: %w", err)
	}
	defer rows.Close()
	merged := make([]string, 0, 32)
	for rows.Next() {
		var binaryData []byte
		if err := rows.Scan(&binaryData); err != nil {
			return nil, fmt.Errorf("tenant rbac repo: scan compiled assignment: %w", err)
		}
		var entry iamproto.RoleEntry
		if err := proto.Unmarshal(binaryData, &entry); err != nil {
			return nil, fmt.Errorf("tenant rbac repo: decode compiled assignment: %w", err)
		}
		for _, permission := range entry.Permissions {
			parts := strings.Split(permission, ":")
			if len(parts) != 5 || parts[0] != tenantID.String() {
				return nil, fmt.Errorf("tenant rbac repo: invalid compiled tenant permission")
			}
			merged = append(merged, permission)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tenant rbac repo: iterate compiled assignments: %w", err)
	}
	if len(merged) == 0 {
		return nil, iamTaxonomy.ErrActionNotAllowed
	}
	sort.Strings(merged)
	unique := merged[:0]
	for _, permission := range merged {
		if len(unique) == 0 || unique[len(unique)-1] != permission {
			unique = append(unique, permission)
		}
	}
	return proto.MarshalOptions{Deterministic: true}.Marshal(&iamproto.RoleEntry{Permissions: unique})
}
