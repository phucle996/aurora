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
	iamproto "controlplane/internal/iam/transport/proto"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
)

type TenantRbacRepository struct {
	cfg    *config.Config
	db     *pgxpool.Pool
	schema string
}

func NewTenantRbacRepository(cfg *config.Config, db *pgxpool.Pool) iamRepoInterface.TenantRbacRepository {
	return &TenantRbacRepository{cfg: cfg, db: db, schema: cfg.SchemaSQL.IAM}
}

func (r *TenantRbacRepository) ListTenantRoles(ctx context.Context, in *iamEntity.ListTenantRoles) ([]iamEntity.ListTenantRoles, error) {
	query := fmt.Sprintf(`
		WITH actor AS MATERIALIZED (
			SELECT actor_revision.role_level
			FROM %s.tenant_memberships tm
			JOIN %s.membership_role mr
			  ON mr.membership_id=tm.id
			 AND mr.workspace_id='00000000-0000-0000-0000-000000000000'
			JOIN %s.tenant_role_revisions actor_revision ON actor_revision.id=mr.tenant_role_revision_id
			JOIN %s.tenant_role_revision_permissions trp ON trp.tenant_role_revision_id=mr.tenant_role_revision_id
			JOIN %s.permissions p
			  ON p.id=trp.permission_id AND p.module='iam' AND p.object='role' AND p.behavior='read'
			WHERE tm.tenant_id=$1 AND tm.user_id=$2 AND tm.status='active'
			LIMIT 1
		), visible_roles AS (
			SELECT tr.id, tr.code, revision.name, COALESCE(revision.description, '') AS description,
			       revision.role_level, revision.version,
			       (SELECT COUNT(*) FROM %s.membership_role mr WHERE mr.tenant_role_id=tr.id) AS assignments_count,
			       (SELECT COUNT(*) FROM %s.membership_role mr
			        JOIN %s.tenant_role_revisions pinned_revision ON pinned_revision.id=mr.tenant_role_revision_id
			        WHERE mr.tenant_role_id=tr.id AND pinned_revision.version<>tr.current_version) AS outdated_assignments_count,
			       (SELECT COUNT(*) FROM %s.tenant_role_revision_permissions rp WHERE rp.tenant_role_revision_id=revision.id) AS permissions_count,
			       tr.created_at
			FROM %s.tenant_roles tr CROSS JOIN actor
			JOIN %s.tenant_role_revisions revision
			  ON revision.tenant_role_id=tr.id AND revision.version=tr.current_version
			WHERE tr.tenant_id=$1 AND revision.role_level > actor.role_level
		)
		SELECT EXISTS(SELECT 1 FROM actor),
		       COALESCE(id, '00000000-0000-0000-0000-000000000000'::uuid),
		       COALESCE(code, ''), COALESCE(name, ''), COALESCE(description, ''),
		       COALESCE(role_level, 0), COALESCE(version, 0),
		       COALESCE(assignments_count, 0), COALESCE(outdated_assignments_count, 0), COALESCE(permissions_count, 0),
		       COALESCE(created_at, '0001-01-01 00:00:00Z'::timestamptz)
		FROM (SELECT 1) sentinel LEFT JOIN visible_roles ON true
		ORDER BY role_level, id
		`, r.cfg.SchemaSQL.Hierarchy, r.schema, r.schema, r.schema, r.schema, r.schema, r.schema, r.schema, r.schema, r.schema, r.schema)

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
			&item.RoleLevel, &item.Version, &item.AssignmentsCount, &item.OutdatedAssignmentsCount, &item.PermissionsCount, &item.CreatedAt,
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

func (r *TenantRbacRepository) CreateTenantRole(ctx context.Context, in *iamEntity.CreateTenantRole) (*iamEntity.CreateTenantRole, error) {
	query := fmt.Sprintf(`
		WITH requested AS MATERIALIZED (
			SELECT DISTINCT permission_id FROM unnest($9::uuid[]) AS requested(permission_id)
		), valid_permissions AS MATERIALIZED (
			SELECT requested.permission_id
			FROM requested JOIN %s.permissions p ON p.id=requested.permission_id
		), actor AS MATERIALIZED (
			SELECT actor_revision.role_level
			FROM %s.tenant_memberships tm
			JOIN %s.tenants t ON t.id=tm.tenant_id AND t.status='active'
			JOIN %s.membership_role mr
			  ON mr.membership_id=tm.id
			 AND mr.workspace_id='00000000-0000-0000-0000-000000000000'
			JOIN %s.tenant_role_revisions actor_revision ON actor_revision.id=mr.tenant_role_revision_id
			JOIN %s.tenant_role_revision_permissions trp ON trp.tenant_role_revision_id=mr.tenant_role_revision_id
			JOIN %s.permissions p
			  ON p.id=trp.permission_id AND p.module='iam' AND p.object='role' AND p.behavior='write'
			WHERE tm.tenant_id=$2 AND tm.user_id=$3 AND tm.status='active'
			LIMIT 1
		), inserted_role AS (
			INSERT INTO %s.tenant_roles
				(id, tenant_id, code, current_version, created_by, created_at, updated_at)
			SELECT $1, $2, $4, 1, $3, $10, $10
			FROM actor
			WHERE actor.role_level < $7
			  AND (SELECT COUNT(*) FROM requested)=(SELECT COUNT(*) FROM valid_permissions)
			RETURNING id, tenant_id, code, created_at
		), inserted_revision AS (
			INSERT INTO %s.tenant_role_revisions
				(id, tenant_role_id, tenant_id, version, name, description, role_level, created_by, created_at)
			SELECT $8, id, tenant_id, 1, $5, $6, $7, $3, $10 FROM inserted_role
			RETURNING id, tenant_role_id, name, COALESCE(description, '') AS description, role_level, version
		), inserted_permissions AS (
			INSERT INTO %s.tenant_role_revision_permissions (tenant_role_revision_id, permission_id, created_at)
			SELECT inserted_revision.id, valid_permissions.permission_id, $10
			FROM inserted_revision CROSS JOIN valid_permissions
			RETURNING permission_id
		)
		SELECT EXISTS(SELECT 1 FROM %s.tenants WHERE id=$2),
		       EXISTS(SELECT 1 FROM actor),
		       COALESCE((SELECT role_level FROM actor), 999) < $7,
		       (SELECT COUNT(*) FROM requested)=(SELECT COUNT(*) FROM valid_permissions),
		       EXISTS(SELECT 1 FROM inserted_role),
		       COALESCE((SELECT id FROM inserted_role), '00000000-0000-0000-0000-000000000000'::uuid),
		       COALESCE((SELECT code FROM inserted_role), ''),
		       COALESCE((SELECT name FROM inserted_revision), ''),
		       COALESCE((SELECT description FROM inserted_revision), ''),
		       COALESCE((SELECT role_level FROM inserted_revision), 0),
		       COALESCE((SELECT version FROM inserted_revision), 0),
		       COALESCE((SELECT created_at FROM inserted_role), $10)
	`, r.schema, r.cfg.SchemaSQL.Hierarchy, r.cfg.SchemaSQL.Hierarchy, r.schema, r.schema, r.schema, r.schema, r.schema, r.schema, r.schema, r.cfg.SchemaSQL.Hierarchy)

	out := &iamEntity.CreateTenantRole{ActorUserID: in.ActorUserID, TenantID: in.TenantID, PermissionIDs: in.PermissionIDs}
	var tenantExists, actorAuthorized, hierarchyOK, permissionsValid, inserted bool
	err := r.db.QueryRow(ctx, query,
		in.ID, in.TenantID, in.ActorUserID, in.Code, in.Name, in.Description,
		in.RoleLevel, in.RevisionID, in.PermissionIDs, in.CreatedAt,
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

func (r *TenantRbacRepository) GetTenantRole(ctx context.Context, in *iamEntity.GetTenantRole) (*iamEntity.GetTenantRole, error) {
	query := fmt.Sprintf(`
		WITH actor AS MATERIALIZED (
			SELECT actor_revision.role_level
			FROM %s.tenant_memberships tm
			JOIN %s.membership_role mr ON mr.membership_id=tm.id AND mr.workspace_id='00000000-0000-0000-0000-000000000000'
			JOIN %s.tenant_role_revisions actor_revision ON actor_revision.id=mr.tenant_role_revision_id
			JOIN %s.tenant_role_revision_permissions mapping ON mapping.tenant_role_revision_id=mr.tenant_role_revision_id
			JOIN %s.permissions permission ON permission.id=mapping.permission_id
			 AND permission.module='iam' AND permission.object='role' AND permission.behavior='read'
			WHERE tm.tenant_id=$1 AND tm.user_id=$2 AND tm.status='active'
			LIMIT 1
		), target AS MATERIALIZED (
			SELECT role.id, role.code, revision.id AS revision_id, revision.name,
			       COALESCE(revision.description, '') AS description, revision.role_level,
			       revision.version, role.created_at,
			       (SELECT count(*) FROM %s.membership_role assignment WHERE assignment.tenant_role_id=role.id) AS assignments_count,
			       (SELECT count(*) FROM %s.membership_role assignment
			        JOIN %s.tenant_role_revisions pinned_revision ON pinned_revision.id=assignment.tenant_role_revision_id
			        WHERE assignment.tenant_role_id=role.id AND pinned_revision.version<>role.current_version) AS outdated_assignments_count
			FROM %s.tenant_roles role
			JOIN %s.tenant_role_revisions revision ON revision.tenant_role_id=role.id AND revision.version=role.current_version
			CROSS JOIN actor
			WHERE role.id=$3 AND role.tenant_id=$1 AND revision.role_level>actor.role_level
		), target_permissions AS (
			SELECT permission.id, permission.module, permission.object, permission.behavior,
			       COALESCE(permission.description, '') AS description
			FROM target
			JOIN %s.tenant_role_revision_permissions mapping ON mapping.tenant_role_revision_id=target.revision_id
			JOIN %s.permissions permission ON permission.id=mapping.permission_id
		)
		SELECT EXISTS(SELECT 1 FROM actor), EXISTS(SELECT 1 FROM target),
		       COALESCE((SELECT id FROM target), '00000000-0000-0000-0000-000000000000'::uuid),
		       COALESCE((SELECT code FROM target), ''), COALESCE((SELECT name FROM target), ''),
		       COALESCE((SELECT description FROM target), ''), COALESCE((SELECT role_level FROM target), 0),
		       COALESCE((SELECT version FROM target), 0), COALESCE((SELECT assignments_count FROM target), 0),
		       COALESCE((SELECT outdated_assignments_count FROM target), 0),
		       COALESCE((SELECT created_at FROM target), '0001-01-01 00:00:00Z'::timestamptz),
		       COALESCE(target_permissions.id, '00000000-0000-0000-0000-000000000000'::uuid),
		       COALESCE(target_permissions.module, ''), COALESCE(target_permissions.object, ''),
		       COALESCE(target_permissions.behavior, ''), COALESCE(target_permissions.description, '')
		FROM (SELECT 1) sentinel LEFT JOIN target_permissions ON true
		ORDER BY target_permissions.module, target_permissions.object, target_permissions.behavior
	`, r.cfg.SchemaSQL.Hierarchy, r.schema, r.schema, r.schema, r.schema, r.schema, r.schema, r.schema, r.schema, r.schema, r.schema, r.schema)
	rows, err := r.db.Query(ctx, query, in.TenantID, in.ActorUserID, in.ID)
	if err != nil {
		return nil, fmt.Errorf("tenant rbac repo: get role: %w", err)
	}
	defer rows.Close()
	out := &iamEntity.GetTenantRole{ActorUserID: in.ActorUserID, TenantID: in.TenantID, ID: in.ID}
	var authorized, found bool
	for rows.Next() {
		var permission iamEntity.TenantRolePermission
		if err := rows.Scan(&authorized, &found, &out.ID, &out.Code, &out.Name, &out.Description,
			&out.RoleLevel, &out.Version, &out.AssignmentsCount, &out.OutdatedAssignmentsCount,
			&out.CreatedAt, &permission.ID, &permission.Module, &permission.Object,
			&permission.Behavior, &permission.Description); err != nil {
			return nil, fmt.Errorf("tenant rbac repo: scan role detail: %w", err)
		}
		if permission.ID != uuid.Nil {
			out.Permissions = append(out.Permissions, permission)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tenant rbac repo: iterate role detail: %w", err)
	}
	if !authorized {
		return nil, iamTaxonomy.ErrActionNotAllowed
	}
	if !found {
		return nil, iamTaxonomy.ErrNotFound
	}
	return out, nil
}

func (r *TenantRbacRepository) CreateTenantRoleRevision(ctx context.Context, in *iamEntity.CreateTenantRoleRevision) (*iamEntity.CreateTenantRoleRevision, error) {
	query := fmt.Sprintf(`
		WITH requested AS MATERIALIZED (
			SELECT DISTINCT permission_id FROM unnest($9::uuid[]) requested(permission_id)
		), valid_permissions AS MATERIALIZED (
			SELECT requested.permission_id FROM requested JOIN %s.permissions permission ON permission.id=requested.permission_id
		), actor AS MATERIALIZED (
			SELECT actor_revision.role_level
			FROM %s.tenant_memberships membership
			JOIN %s.membership_role mr ON mr.membership_id=membership.id AND mr.workspace_id='00000000-0000-0000-0000-000000000000'
			JOIN %s.tenant_role_revisions actor_revision ON actor_revision.id=mr.tenant_role_revision_id
			JOIN %s.tenant_role_revision_permissions mapping ON mapping.tenant_role_revision_id=mr.tenant_role_revision_id
			JOIN %s.permissions permission ON permission.id=mapping.permission_id
			 AND permission.module='iam' AND permission.object='role' AND permission.behavior='write'
			WHERE membership.tenant_id=$2 AND membership.user_id=$3 AND membership.status='active'
			LIMIT 1
		), head AS MATERIALIZED (
			SELECT role.id, role.current_version, current_revision.role_level
			FROM %s.tenant_roles role
			JOIN %s.tenant_role_revisions current_revision
			  ON current_revision.tenant_role_id=role.id AND current_revision.version=role.current_version
			WHERE role.id=$1 AND role.tenant_id=$2 AND role.code<>'tenant_root'
			FOR UPDATE OF role
		), inserted_revision AS (
			INSERT INTO %s.tenant_role_revisions
				(id, tenant_role_id, tenant_id, version, name, description, role_level, created_by, created_at)
			SELECT $4, head.id, $2, head.current_version+1, $6, $7, $8, $3, $10
			FROM head CROSS JOIN actor
			WHERE head.current_version=$5 AND actor.role_level<head.role_level AND actor.role_level<$8
			  AND (SELECT count(*) FROM requested)=(SELECT count(*) FROM valid_permissions)
			RETURNING id, version, name, COALESCE(description, '') AS description, role_level
		), inserted_permissions AS (
			INSERT INTO %s.tenant_role_revision_permissions (tenant_role_revision_id, permission_id, created_at)
			SELECT inserted_revision.id, valid_permissions.permission_id, $10
			FROM inserted_revision CROSS JOIN valid_permissions
			RETURNING permission_id
		), advanced AS (
			UPDATE %s.tenant_roles role SET current_version=inserted_revision.version, updated_at=$10
			FROM inserted_revision
			WHERE role.id=$1 AND EXISTS(SELECT 1 FROM inserted_permissions)
			RETURNING role.id
		)
		SELECT EXISTS(SELECT 1 FROM head), EXISTS(SELECT 1 FROM actor),
		       COALESCE((SELECT current_version FROM head), 0)=$5,
		       COALESCE((SELECT role_level FROM actor), 999)<COALESCE((SELECT role_level FROM head), 0)
		         AND COALESCE((SELECT role_level FROM actor), 999)<$8,
		       (SELECT count(*) FROM requested)=(SELECT count(*) FROM valid_permissions),
		       EXISTS(SELECT 1 FROM advanced),
		       COALESCE((SELECT version FROM inserted_revision), 0),
		       COALESCE((SELECT name FROM inserted_revision), ''),
		       COALESCE((SELECT description FROM inserted_revision), ''),
		       COALESCE((SELECT role_level FROM inserted_revision), 0)
	`, r.schema, r.cfg.SchemaSQL.Hierarchy, r.schema, r.schema, r.schema, r.schema, r.schema, r.schema, r.schema, r.schema, r.schema)
	out := &iamEntity.CreateTenantRoleRevision{ActorUserID: in.ActorUserID, TenantID: in.TenantID, RoleID: in.RoleID, RevisionID: in.RevisionID, PermissionIDs: in.PermissionIDs, CreatedAt: in.CreatedAt}
	var found, authorized, versionMatches, hierarchyOK, permissionsValid, advanced bool
	err := r.db.QueryRow(ctx, query, in.RoleID, in.TenantID, in.ActorUserID, in.RevisionID,
		in.ExpectedVersion, in.Name, in.Description, in.RoleLevel, in.PermissionIDs, in.CreatedAt,
	).Scan(&found, &authorized, &versionMatches, &hierarchyOK, &permissionsValid, &advanced,
		&out.Version, &out.Name, &out.Description, &out.RoleLevel)
	if err != nil {
		return nil, fmt.Errorf("tenant rbac repo: create revision: %w", err)
	}
	if !found {
		return nil, iamTaxonomy.ErrNotFound
	}
	if !authorized || !hierarchyOK {
		return nil, iamTaxonomy.ErrActionNotAllowed
	}
	if !versionMatches || !advanced {
		return nil, iamTaxonomy.ErrConflict
	}
	if !permissionsValid {
		return nil, iamTaxonomy.ErrPreconditionFailed
	}
	return out, nil
}

func (r *TenantRbacRepository) UpgradeTenantRoleAssignments(ctx context.Context, in *iamEntity.UpgradeTenantRoleAssignments) (*iamEntity.UpgradeTenantRoleAssignments, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("tenant rbac repo: begin assignment upgrade: %w", err)
	}
	defer tx.Rollback(ctx)
	query := fmt.Sprintf(`
		WITH actor AS MATERIALIZED (
			SELECT actor_revision.role_level FROM %s.tenant_memberships membership
			JOIN %s.membership_role mr ON mr.membership_id=membership.id AND mr.workspace_id='00000000-0000-0000-0000-000000000000'
			JOIN %s.tenant_role_revisions actor_revision ON actor_revision.id=mr.tenant_role_revision_id
			JOIN %s.tenant_role_revision_permissions mapping ON mapping.tenant_role_revision_id=mr.tenant_role_revision_id
			JOIN %s.permissions permission ON permission.id=mapping.permission_id
			 AND permission.module='iam' AND permission.object='role' AND permission.behavior='assign'
			WHERE membership.tenant_id=$1 AND membership.user_id=$2 AND membership.status='active'
			LIMIT 1
			FOR UPDATE OF membership, mr
		), target AS MATERIALIZED (
			SELECT role.id, revision.id AS revision_id, revision.name, revision.role_level, revision.version
			FROM %s.tenant_roles role
			JOIN %s.tenant_role_revisions revision ON revision.tenant_role_id=role.id AND revision.version=role.current_version
			CROSS JOIN actor
			WHERE role.id=$3 AND role.tenant_id=$1 AND actor.role_level<revision.role_level
			FOR UPDATE OF role
		)
		SELECT EXISTS(SELECT 1 FROM actor),
		       EXISTS(SELECT 1 FROM %s.tenant_roles role WHERE role.id=$3 AND role.tenant_id=$1),
		       EXISTS(SELECT 1 FROM target),
		       COALESCE((SELECT revision_id FROM target), '00000000-0000-0000-0000-000000000000'::uuid),
		       COALESCE((SELECT version FROM target), 0)
	`, r.cfg.SchemaSQL.Hierarchy, r.schema, r.schema, r.schema, r.schema, r.schema, r.schema, r.schema)
	var authorized, roleExists, found bool
	var revisionID uuid.UUID
	var version int64
	if err := tx.QueryRow(ctx, query, in.TenantID, in.ActorUserID, in.RoleID).Scan(
		&authorized, &roleExists, &found, &revisionID, &version,
	); err != nil {
		return nil, fmt.Errorf("tenant rbac repo: resolve assignment upgrade: %w", err)
	}
	if !authorized {
		return nil, iamTaxonomy.ErrActionNotAllowed
	}
	if !roleExists {
		return nil, iamTaxonomy.ErrNotFound
	}
	if !found {
		return nil, iamTaxonomy.ErrConflict
	}
	update := fmt.Sprintf(`
		WITH actor AS MATERIALIZED (
			SELECT actor_revision.role_level
			FROM %s.tenant_memberships membership
			JOIN %s.membership_role mr
			  ON mr.membership_id=membership.id
			 AND mr.workspace_id='00000000-0000-0000-0000-000000000000'
			JOIN %s.tenant_role_revisions actor_revision ON actor_revision.id=mr.tenant_role_revision_id
			JOIN %s.tenant_role_revision_permissions mapping
			  ON mapping.tenant_role_revision_id=mr.tenant_role_revision_id
			JOIN %s.permissions permission
			  ON permission.id=mapping.permission_id
			 AND permission.module='iam'
			 AND permission.object='role'
			 AND permission.behavior='assign'
			WHERE membership.tenant_id=$2
			  AND membership.user_id=$5
			  AND membership.status='active'
			LIMIT 1
		), target AS MATERIALIZED (
			SELECT role.id
			FROM %s.tenant_roles role
			JOIN %s.tenant_role_revisions revision
			  ON revision.tenant_role_id=role.id
			 AND revision.id=$4
			 AND revision.version=role.current_version
			CROSS JOIN actor
			WHERE role.id=$1 AND role.tenant_id=$2 AND role.current_version=$3
			  AND actor.role_level<revision.role_level
		), upgraded AS (
			UPDATE %s.membership_role assignment
			SET tenant_role_revision_id=$4, updated_at=now()
			FROM target, %s.tenant_memberships membership, %s.tenant_role_revisions pinned_revision
			WHERE assignment.tenant_role_id=target.id
			  AND pinned_revision.id=assignment.tenant_role_revision_id
			  AND pinned_revision.version<>$3
			  AND membership.id=assignment.membership_id AND membership.tenant_id=$2
			RETURNING assignment.membership_id
		)
		SELECT count(*) FROM upgraded
	`, r.cfg.SchemaSQL.Hierarchy, r.schema, r.schema, r.schema, r.schema, r.schema, r.schema, r.schema, r.cfg.SchemaSQL.Hierarchy, r.schema)
	out := &iamEntity.UpgradeTenantRoleAssignments{ActorUserID: in.ActorUserID, TenantID: in.TenantID, RoleID: in.RoleID, Version: version}
	if err := tx.QueryRow(ctx, update, in.RoleID, in.TenantID, version, revisionID, in.ActorUserID).Scan(&out.UpdatedCount); err != nil {
		return nil, fmt.Errorf("tenant rbac repo: upgrade assignments: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("tenant rbac repo: commit assignment upgrade: %w", err)
	}
	return out, nil
}

func (r *TenantRbacRepository) ResolveTenantAccess(ctx context.Context, in *iamEntity.ResolveTenantAccess) (*iamEntity.ResolveTenantAccess, error) {
	query := fmt.Sprintf(`
		SELECT revision.role_level
		FROM %s.tenant_memberships tm
		JOIN %s.tenants t ON t.id=tm.tenant_id AND t.status='active'
		JOIN %s.tenant_domains td ON td.tenant_id=t.id AND lower(td.domain)=lower($3)
		JOIN %s.users u ON u.id=tm.user_id AND u.status='active'
		JOIN %s.membership_role mr
		  ON mr.membership_id=tm.id
		 AND mr.workspace_id='00000000-0000-0000-0000-000000000000'
		JOIN %s.tenant_role_revisions revision ON revision.id=mr.tenant_role_revision_id
		WHERE tm.user_id=$1 AND tm.tenant_id=$2 AND tm.status='active'
		ORDER BY revision.role_level, mr.tenant_role_id
		LIMIT 1
	`, r.cfg.SchemaSQL.Hierarchy, r.cfg.SchemaSQL.Hierarchy, r.cfg.SchemaSQL.Hierarchy, r.schema, r.schema, r.schema)
	out := &iamEntity.ResolveTenantAccess{UserID: in.UserID, TenantID: in.TenantID, TenantDomain: in.TenantDomain}
	if err := r.db.QueryRow(ctx, query, in.UserID, in.TenantID, in.TenantDomain).Scan(&out.RoleLevel); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, iamTaxonomy.ErrActionNotAllowed
		}
		return nil, fmt.Errorf("tenant rbac repo: resolve access: %w", err)
	}
	return out, nil
}

func (r *TenantRbacRepository) GetUserTenantBillingPermissions(ctx context.Context, userID uuid.UUID, tenantID uuid.UUID) ([]byte, error) {
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

func (r *TenantRbacRepository) GetUserTenantRolePermissions(ctx context.Context, userID uuid.UUID, tenantID uuid.UUID) ([]byte, error) {
	query := fmt.Sprintf(`
		SELECT mr.workspace_id, permission.module, permission.object, permission.behavior
		FROM %s.tenant_memberships tm
		JOIN %s.tenants t ON t.id=tm.tenant_id AND t.status='active'
		JOIN %s.users u ON u.id=tm.user_id AND u.status='active'
		JOIN %s.membership_role mr ON mr.membership_id=tm.id
		JOIN %s.tenant_role_revisions revision ON revision.id=mr.tenant_role_revision_id
		JOIN %s.tenant_role_revision_permissions mapping
		  ON mapping.tenant_role_revision_id=mr.tenant_role_revision_id
		JOIN %s.permissions permission ON permission.id=mapping.permission_id
		WHERE tm.tenant_id=$1 AND tm.user_id=$2 AND tm.status='active'
		ORDER BY mr.workspace_id, revision.role_level, mr.id,
		         permission.module, permission.object, permission.behavior
	`, r.cfg.SchemaSQL.Hierarchy, r.cfg.SchemaSQL.Hierarchy, r.schema, r.schema, r.schema, r.schema, r.schema)
	rows, err := r.db.Query(ctx, query, tenantID, userID)
	if err != nil {
		return nil, fmt.Errorf("tenant rbac repo: query compiled assignments: %w", err)
	}
	defer rows.Close()
	merged := make([]string, 0, 32)
	for rows.Next() {
		var workspaceID uuid.UUID
		var module, object, behavior string
		if err := rows.Scan(&workspaceID, &module, &object, &behavior); err != nil {
			return nil, fmt.Errorf("tenant rbac repo: scan revision permission: %w", err)
		}
		merged = append(merged, fmt.Sprintf(
			"%s:%s:%s:%s:%s", tenantID, workspaceID, module, object, behavior,
		))
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
