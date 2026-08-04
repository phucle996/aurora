package hierarchyRepoImpl

import (
	"context"
	"errors"
	"fmt"

	"controlplane/internal/config"
	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
	hierarchyRepoInterface "controlplane/internal/hierarchy/domain/repo"
	hierarchyTaxonomy "controlplane/internal/hierarchy/taxonomy"
	iamproto "controlplane/internal/iam/transport/rpc/proto"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
)

type TenantInvitationRepoImpl struct {
	db              *pgxpool.Pool
	hierarchySchema string
	iamSchema       string
}

func NewTenantInvitationRepository(cfg *config.Config, db *pgxpool.Pool) hierarchyRepoInterface.TenantInvitationRepository {
	return &TenantInvitationRepoImpl{db: db, hierarchySchema: cfg.SchemaSQL.Hierarchy, iamSchema: cfg.SchemaSQL.IAM}
}

func (r *TenantInvitationRepoImpl) CreateTenantInvitation(ctx context.Context, in *hierarchyEntity.CreateTenantInvitation) (*hierarchyEntity.CreateTenantInvitation, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return nil, fmt.Errorf("tenant invitation repo: begin create transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	// [COMMENT]: Lock the actor binding and selected immutable role while the
	// grant is compiled. A concurrent membership revocation cannot race between
	// the hierarchy decision and invitation persistence.
	queryRole := fmt.Sprintf(`
		WITH tenant_state AS MATERIALIZED (
			SELECT id FROM %s.tenants WHERE id=$1 AND status='active'
		), actor AS MATERIALIZED (
			SELECT mr.role_level
			FROM %s.tenant_memberships tm
			JOIN %s.membership_role mr
			  ON mr.membership_id=tm.id
			 AND mr.workspace_id='00000000-0000-0000-0000-000000000000'
			JOIN %s.tenant_role_permissions trp ON trp.tenant_role_id=mr.tenant_role_id
			JOIN %s.permissions permission
			  ON permission.id=trp.permission_id
			 AND permission.module='hierarchy'
			 AND permission.object='tenant-invitation'
			 AND permission.behavior='create'
			WHERE tm.tenant_id=$1 AND tm.user_id=$2 AND tm.status='active'
			FOR KEY SHARE OF tm, mr
		), target_user AS MATERIALIZED (
			SELECT id FROM %s.users
			WHERE status='active'
			  AND (($4 AND lower(email)=lower($3)) OR (NOT $4 AND lower(username)=lower($3)))
			LIMIT 1
			FOR KEY SHARE
		), selected_role AS MATERIALIZED (
			SELECT id, code, name, role_level, version
			FROM %s.tenant_roles
			WHERE id=$5 AND tenant_id=$1
			FOR KEY SHARE
		), role_permissions AS MATERIALIZED (
			SELECT permission.module, permission.object, permission.behavior
			FROM %s.tenant_role_permissions mapping
			JOIN %s.permissions permission ON permission.id=mapping.permission_id
			WHERE mapping.tenant_role_id=$5
			ORDER BY permission.module, permission.object, permission.behavior
		)
		SELECT EXISTS(SELECT 1 FROM tenant_state), EXISTS(SELECT 1 FROM actor),
		       EXISTS(SELECT 1 FROM target_user), EXISTS(SELECT 1 FROM selected_role),
		       COALESCE((SELECT role_level FROM actor), 999) < COALESCE((SELECT role_level FROM selected_role), 0),
		       EXISTS(
		           SELECT 1 FROM %s.tenant_memberships tm
		           WHERE tm.tenant_id=$1 AND tm.user_id=(SELECT id FROM target_user)
		       ),
		       COALESCE((SELECT id FROM target_user), '00000000-0000-0000-0000-000000000000'::uuid),
		       COALESCE((SELECT code FROM selected_role), ''),
		       COALESCE((SELECT name FROM selected_role), ''),
		       COALESCE((SELECT role_level FROM selected_role), 0),
		       COALESCE((SELECT version FROM selected_role), 0),
		       COALESCE(role_permissions.module, ''), COALESCE(role_permissions.object, ''),
		       COALESCE(role_permissions.behavior, '')
		FROM (SELECT 1) sentinel LEFT JOIN role_permissions ON true
	`, r.hierarchySchema, r.hierarchySchema, r.iamSchema, r.iamSchema, r.iamSchema, r.iamSchema, r.iamSchema, r.iamSchema, r.iamSchema, r.hierarchySchema)

	rows, err := tx.Query(ctx, queryRole, in.TenantID, in.InviterUserID, in.TargetIdentifier, in.TargetByEmail, in.TenantRoleID)
	if err != nil {
		return nil, fmt.Errorf("tenant invitation repo: resolve create grant: %w", err)
	}
	defer rows.Close()

	var tenantExists, actorAuthorized, targetExists, roleExists, hierarchyOK, alreadyMember bool
	permissions := make([]string, 0, 32)
	out := &hierarchyEntity.CreateTenantInvitation{
		ID: in.ID, TenantID: in.TenantID, InviterUserID: in.InviterUserID,
		TargetIdentifier: in.TargetIdentifier, TargetByEmail: in.TargetByEmail,
		TenantRoleID: in.TenantRoleID, WorkspaceID: in.WorkspaceID,
		Token: in.Token, TokenHash: in.TokenHash, ExpiresAt: in.ExpiresAt, CreatedAt: in.CreatedAt,
	}
	for rows.Next() {
		var module, object, behavior string
		if err := rows.Scan(
			&tenantExists, &actorAuthorized, &targetExists, &roleExists, &hierarchyOK, &alreadyMember,
			&out.TargetUserID, &out.RoleCode, &out.RoleName, &out.RoleLevel, &out.RoleVersion,
			&module, &object, &behavior,
		); err != nil {
			return nil, fmt.Errorf("tenant invitation repo: scan create grant: %w", err)
		}
		if module != "" && object != "" && behavior != "" {
			permissions = append(permissions, fmt.Sprintf(
				"%s:%s:%s:%s:%s", in.TenantID, in.WorkspaceID, module, object, behavior,
			))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tenant invitation repo: iterate create grant: %w", err)
	}
	rows.Close()
	if !tenantExists || !targetExists || !roleExists {
		return nil, hierarchyTaxonomy.ErrNotFound
	}
	if !actorAuthorized || !hierarchyOK || out.TargetUserID == in.InviterUserID {
		return nil, hierarchyTaxonomy.ErrPreconditionFailed
	}
	if alreadyMember {
		return nil, hierarchyTaxonomy.ErrAlreadyExists
	}
	if len(permissions) == 0 {
		return nil, hierarchyTaxonomy.ErrPreconditionFailed
	}

	compiled, err := proto.MarshalOptions{Deterministic: true}.Marshal(&iamproto.RoleEntry{Permissions: permissions})
	if err != nil {
		return nil, fmt.Errorf("tenant invitation repo: marshal compiled grant: %w", err)
	}
	queryInsert := fmt.Sprintf(`
		WITH actor AS MATERIALIZED (
			SELECT mr.role_level
			FROM %s.tenant_memberships tm
			JOIN %s.membership_role mr
			  ON mr.membership_id=tm.id
			 AND mr.workspace_id='00000000-0000-0000-0000-000000000000'
			JOIN %s.tenant_role_permissions trp ON trp.tenant_role_id=mr.tenant_role_id
			JOIN %s.permissions permission
			  ON permission.id=trp.permission_id
			 AND permission.module='hierarchy'
			 AND permission.object='tenant-invitation'
			 AND permission.behavior='create'
			WHERE tm.tenant_id=$2 AND tm.user_id=$3 AND tm.status='active'
			LIMIT 1
		), selected_role AS MATERIALIZED (
			SELECT id, role_level, version FROM %s.tenant_roles
			WHERE id=$5 AND tenant_id=$2
		), target_user AS MATERIALIZED (
			SELECT id FROM %s.users
			WHERE id=$4 AND status='active'
			FOR KEY SHARE
		), expired_deleted AS (
			DELETE FROM %s.tenant_invitations
			WHERE tenant_id=$2 AND target_user_id=$4 AND expires_at <= $10
			RETURNING id
		), expired_delete_fence AS MATERIALIZED (
			SELECT count(*) AS deleted_count FROM expired_deleted
		), inserted AS (
			INSERT INTO %s.tenant_invitations
				(id, tenant_id, inviter_user_id, target_user_id, tenant_role_id, workspace_id,
				 role_version, role_level, list_perm, token_hash, expires_at, created_at)
			SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $11, $12, $10
			FROM actor CROSS JOIN selected_role CROSS JOIN target_user CROSS JOIN expired_delete_fence
			WHERE actor.role_level < selected_role.role_level
			  AND selected_role.version=$7
			  AND NOT EXISTS (
			      SELECT 1 FROM %s.tenant_memberships tm WHERE tm.tenant_id=$2 AND tm.user_id=$4
			  )
			RETURNING id
		)
		SELECT EXISTS(SELECT 1 FROM inserted)
	`, r.hierarchySchema, r.iamSchema, r.iamSchema, r.iamSchema, r.iamSchema, r.iamSchema, r.iamSchema, r.iamSchema, r.hierarchySchema)
	var inserted bool
	err = tx.QueryRow(ctx, queryInsert,
		in.ID, in.TenantID, in.InviterUserID, out.TargetUserID, in.TenantRoleID, in.WorkspaceID,
		out.RoleVersion, out.RoleLevel, compiled, in.CreatedAt, in.TokenHash, in.ExpiresAt,
	).Scan(&inserted)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, hierarchyTaxonomy.ErrAlreadyExists
		}
		return nil, fmt.Errorf("tenant invitation repo: insert invitation: %w", err)
	}
	if !inserted {
		return nil, hierarchyTaxonomy.ErrPreconditionFailed
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("tenant invitation repo: commit create transaction: %w", err)
	}
	return out, nil
}

func (r *TenantInvitationRepoImpl) PreviewTenantInvitation(ctx context.Context, in *hierarchyEntity.PreviewTenantInvitation) (*hierarchyEntity.PreviewTenantInvitation, error) {
	query := fmt.Sprintf(`
		SELECT invitation.tenant_id, tenant.code, tenant.name,
		       COALESCE(profile.fullname, inviter.username), role.code, role.name,
		       role.role_level, role.version, invitation.expires_at
		FROM %s.tenant_invitations invitation
		JOIN %s.tenants tenant ON tenant.id=invitation.tenant_id AND tenant.status='active'
		JOIN %s.tenant_roles role
		  ON role.id=invitation.tenant_role_id
		 AND role.tenant_id=invitation.tenant_id
		 AND role.version=invitation.role_version
		JOIN %s.users inviter ON inviter.id=invitation.inviter_user_id AND inviter.status='active'
		LEFT JOIN %s.user_profiles profile ON profile.user_id=inviter.id
		WHERE invitation.token_hash=$1 AND invitation.target_user_id=$2 AND invitation.expires_at>now()
	`, r.iamSchema, r.hierarchySchema, r.iamSchema, r.iamSchema, r.iamSchema)
	out := &hierarchyEntity.PreviewTenantInvitation{UserID: in.UserID, TokenHash: in.TokenHash}
	if err := r.db.QueryRow(ctx, query, in.TokenHash, in.UserID).Scan(
		&out.TenantID, &out.TenantCode, &out.TenantName, &out.InviterName,
		&out.RoleCode, &out.RoleName, &out.RoleLevel, &out.RoleVersion, &out.ExpiresAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, hierarchyTaxonomy.ErrNotFound
		}
		return nil, fmt.Errorf("tenant invitation repo: preview: %w", err)
	}
	return out, nil
}

func (r *TenantInvitationRepoImpl) RevokeTenantInvitation(ctx context.Context, in *hierarchyEntity.RevokeTenantInvitation) (*hierarchyEntity.RevokeTenantInvitation, error) {
	query := fmt.Sprintf(`
		WITH invitation AS MATERIALIZED (
			SELECT id, target_user_id, tenant_role_id, role_level
			FROM %s.tenant_invitations
			WHERE id=$1 AND tenant_id=$2
			FOR UPDATE
		), actor AS MATERIALIZED (
			SELECT mr.role_level
			FROM %s.tenant_memberships tm
			JOIN %s.membership_role mr
			  ON mr.membership_id=tm.id
			 AND mr.workspace_id='00000000-0000-0000-0000-000000000000'
			JOIN %s.tenant_role_permissions mapping ON mapping.tenant_role_id=mr.tenant_role_id
			JOIN %s.permissions permission
			  ON permission.id=mapping.permission_id
			 AND permission.module='hierarchy'
			 AND permission.object='tenant-invitation'
			 AND permission.behavior='delete'
			WHERE tm.tenant_id=$2 AND tm.user_id=$3 AND tm.status='active'
			LIMIT 1
		), deleted AS (
			DELETE FROM %s.tenant_invitations target
			USING invitation, actor
			WHERE target.id=invitation.id AND actor.role_level < invitation.role_level
			RETURNING target.target_user_id, target.tenant_role_id
		)
		SELECT EXISTS(SELECT 1 FROM invitation), EXISTS(SELECT 1 FROM actor),
		       COALESCE((SELECT role_level FROM actor), 999) < COALESCE((SELECT role_level FROM invitation), 0),
		       EXISTS(SELECT 1 FROM deleted),
		       COALESCE((SELECT target_user_id FROM deleted), '00000000-0000-0000-0000-000000000000'::uuid),
		       COALESCE((SELECT tenant_role_id FROM deleted), '00000000-0000-0000-0000-000000000000'::uuid)
	`, r.iamSchema, r.hierarchySchema, r.iamSchema, r.iamSchema, r.iamSchema, r.iamSchema)
	var invitationExists, actorAuthorized, hierarchyOK, deleted bool
	out := &hierarchyEntity.RevokeTenantInvitation{ID: in.ID, TenantID: in.TenantID, ActorUserID: in.ActorUserID}
	if err := r.db.QueryRow(ctx, query, in.ID, in.TenantID, in.ActorUserID).Scan(
		&invitationExists, &actorAuthorized, &hierarchyOK, &deleted, &out.TargetUserID, &out.TenantRoleID,
	); err != nil {
		return nil, fmt.Errorf("tenant invitation repo: revoke: %w", err)
	}
	if !invitationExists {
		return nil, hierarchyTaxonomy.ErrNotFound
	}
	if !actorAuthorized || !hierarchyOK {
		return nil, hierarchyTaxonomy.ErrPreconditionFailed
	}
	if !deleted {
		return nil, hierarchyTaxonomy.ErrConflict
	}
	return out, nil
}

func (r *TenantInvitationRepoImpl) JoinTenantInvitation(ctx context.Context, in *hierarchyEntity.JoinTenantInvitation) (*hierarchyEntity.JoinTenantInvitation, error) {
	query := fmt.Sprintf(`
		WITH invitation AS MATERIALIZED (
			SELECT invitation.*, role.code AS role_code, role.name AS role_name,
			       tenant.code AS tenant_code, tenant.name AS tenant_name,
			       tenant.status='active' AS tenant_active,
			       role.version=invitation.role_version AS role_current
			FROM %s.tenant_invitations invitation
			JOIN %s.tenants tenant ON tenant.id=invitation.tenant_id
			JOIN %s.tenant_roles role ON role.id=invitation.tenant_role_id AND role.tenant_id=invitation.tenant_id
			JOIN %s.users target_user
			  ON target_user.id=invitation.target_user_id
			 AND target_user.id=$2
			 AND target_user.status='active'
			WHERE invitation.token_hash=$1
			FOR UPDATE OF invitation
		), inviter AS MATERIALIZED (
			SELECT mr.role_level
			FROM invitation
			JOIN %s.tenant_memberships tm
			  ON tm.tenant_id=invitation.tenant_id
			 AND tm.user_id=invitation.inviter_user_id
			 AND tm.status='active'
			JOIN %s.membership_role mr
			  ON mr.membership_id=tm.id
			 AND mr.workspace_id='00000000-0000-0000-0000-000000000000'
			JOIN %s.tenant_role_permissions mapping ON mapping.tenant_role_id=mr.tenant_role_id
			JOIN %s.permissions permission
			  ON permission.id=mapping.permission_id
			 AND permission.module='hierarchy'
			 AND permission.object='tenant-invitation'
			 AND permission.behavior='create'
			WHERE mr.role_level < invitation.role_level
			LIMIT 1
		), existing_membership AS MATERIALIZED (
			SELECT tm.id FROM invitation
			JOIN %s.tenant_memberships tm
			  ON tm.tenant_id=invitation.tenant_id AND tm.user_id=$2
		), membership_inserted AS (
			INSERT INTO %s.tenant_memberships
				(id, tenant_id, user_id, status, is_ownership, created_at, updated_at)
			SELECT $3, invitation.tenant_id, $2, 'active', false, now(), now()
			FROM invitation CROSS JOIN inviter
			WHERE invitation.target_user_id=$2
			  AND invitation.expires_at>now()
			  AND invitation.tenant_active
			  AND invitation.role_current
			  AND NOT EXISTS (SELECT 1 FROM existing_membership)
			RETURNING id
		), assignment_inserted AS (
			INSERT INTO %s.membership_role
				(id, membership_id, tenant_role_id, workspace_id, role_name, role_level,
				 role_version, list_perm, created_at, updated_at)
			SELECT $4, membership_inserted.id, invitation.tenant_role_id,
			       invitation.workspace_id, invitation.role_name, invitation.role_level,
			       invitation.role_version, invitation.list_perm, now(), now()
			FROM membership_inserted CROSS JOIN invitation
			RETURNING id
		), invitation_deleted AS (
			DELETE FROM %s.tenant_invitations target
			USING invitation, assignment_inserted
			WHERE target.id=invitation.id
			RETURNING target.id
		)
		SELECT EXISTS(SELECT 1 FROM invitation),
		       COALESCE((SELECT target_user_id=$2 AND expires_at>now() AND tenant_active AND role_current FROM invitation), false),
		       EXISTS(SELECT 1 FROM inviter), EXISTS(SELECT 1 FROM existing_membership),
		       EXISTS(SELECT 1 FROM assignment_inserted), EXISTS(SELECT 1 FROM invitation_deleted),
		       COALESCE((SELECT tenant_id FROM invitation), '00000000-0000-0000-0000-000000000000'::uuid),
		       COALESCE((SELECT tenant_code FROM invitation), ''),
		       COALESCE((SELECT tenant_name FROM invitation), ''),
		       COALESCE((SELECT tenant_role_id FROM invitation), '00000000-0000-0000-0000-000000000000'::uuid),
		       COALESCE((SELECT role_code FROM invitation), ''),
		       COALESCE((SELECT role_name FROM invitation), ''),
		       COALESCE((SELECT role_level FROM invitation), 0)
	`, r.iamSchema, r.hierarchySchema, r.iamSchema, r.iamSchema, r.hierarchySchema, r.iamSchema, r.iamSchema, r.iamSchema, r.hierarchySchema, r.hierarchySchema, r.iamSchema, r.iamSchema)

	var invitationExists, invitationValid, inviterAuthorized, alreadyMember, assigned, consumed bool
	out := &hierarchyEntity.JoinTenantInvitation{
		UserID: in.UserID, TokenHash: in.TokenHash,
		MembershipID: in.MembershipID, MembershipRoleID: in.MembershipRoleID,
	}
	err := r.db.QueryRow(ctx, query, in.TokenHash, in.UserID, in.MembershipID, in.MembershipRoleID).Scan(
		&invitationExists, &invitationValid, &inviterAuthorized, &alreadyMember, &assigned, &consumed,
		&out.TenantID, &out.TenantCode, &out.TenantName, &out.TenantRoleID,
		&out.RoleCode, &out.RoleName, &out.RoleLevel,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, hierarchyTaxonomy.ErrAlreadyExists
		}
		return nil, fmt.Errorf("tenant invitation repo: join: %w", err)
	}
	if !invitationExists || !invitationValid {
		return nil, hierarchyTaxonomy.ErrNotFound
	}
	if !inviterAuthorized {
		return nil, hierarchyTaxonomy.ErrPreconditionFailed
	}
	if alreadyMember {
		return nil, hierarchyTaxonomy.ErrAlreadyExists
	}
	if !assigned || !consumed {
		return nil, hierarchyTaxonomy.ErrConflict
	}
	return out, nil
}
