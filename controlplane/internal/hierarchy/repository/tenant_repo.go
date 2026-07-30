package hierarchyRepoImpl

import (
	"context"
	"errors"
	"fmt"
	"time"

	"controlplane/internal/config"
	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
	hierarchyRepoInterface "controlplane/internal/hierarchy/domain/repo"
	hierarchyTaxonomy "controlplane/internal/hierarchy/taxonomy"
	iamproto "controlplane/internal/iam/transport/rpc/proto"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
)

var tenantWalletProvisionEventNamespace = uuid.MustParse("24bbad2a-d35b-5e77-b548-31b81dbac82c")

type TenantRepoImpl struct {
	db              *pgxpool.Pool
	hierarchySchema string
	iamSchema       string
}

func NewTenantRepoImpl(cfg *config.Config, db *pgxpool.Pool) hierarchyRepoInterface.TenantRepository {
	return &TenantRepoImpl{
		db:              db,
		hierarchySchema: cfg.SchemaSQL.Hierarchy,
		iamSchema:       cfg.SchemaSQL.IAM,
	}
}

func (r *TenantRepoImpl) CreateTenant(ctx context.Context, in *hierarchyEntity.CreateTenant) (*hierarchyEntity.CreateTenant, error) {
	// Tenant, owner membership, role snapshots and billing intent form one
	// aggregate creation boundary; none may commit without the others.
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("tenant repo: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	queryTenant := fmt.Sprintf(`
		INSERT INTO %s.tenants (id, code, name, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, code, name, status, created_at, updated_at
	`, r.hierarchySchema)

	out := &hierarchyEntity.CreateTenant{OwnerID: in.OwnerID}
	err = tx.QueryRow(ctx, queryTenant,
		in.ID,
		in.Code,
		in.Name,
		string(in.Status),
		in.CreatedAt,
		in.UpdatedAt,
	).Scan(&out.ID, &out.Code, &out.Name, &out.Status, &out.CreatedAt, &out.UpdatedAt)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, hierarchyTaxonomy.ErrAlreadyExists
		}
		return nil, err
	}

	queryMembership := fmt.Sprintf(`
		INSERT INTO %s.tenant_memberships
			(id, tenant_id, user_id, role, status, is_ownership, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, $2, 'tenant_owner', 'active', true, now(), now())
	`, r.hierarchySchema)

	if _, err := tx.Exec(ctx, queryMembership, out.ID, in.OwnerID); err != nil {
		return nil, fmt.Errorf("tenant repo: insert owner membership: %w", err)
	}

	queryRoles := fmt.Sprintf(`
		SELECT r.id, r.code, r.name, r.role_level, 
		       COALESCE(p.module, ''), COALESCE(p.object, ''), COALESCE(p.behavior, '')
		FROM %s.roles r
		LEFT JOIN %s.role_permissions rp ON rp.role_id = r.id
		LEFT JOIN %s.permissions p ON p.id = rp.permission_id
		WHERE r.code IN ('tenant_owner', 'tenant_admin', 'tenant_manager', 'tenant_member', 'tenant_viewer')
	`, r.iamSchema, r.iamSchema, r.iamSchema)

	rows, err := tx.Query(ctx, queryRoles)
	if err != nil {
		return nil, fmt.Errorf("tenant repo: query role permissions for tenant seed: %w", err)
	}
	defer rows.Close()

	type RoleData struct {
		ID    uuid.UUID
		Code  string
		Name  string
		Level int32
		Perms []string
	}
	rolesMap := make(map[uuid.UUID]*RoleData)

	for rows.Next() {
		var roleID uuid.UUID
		var code, name, mod, obj, beh string
		var level int32
		if err := rows.Scan(&roleID, &code, &name, &level, &mod, &obj, &beh); err != nil {
			return nil, fmt.Errorf("tenant repo: scan role permission row: %w", err)
		}

		rd, ok := rolesMap[roleID]
		if !ok {
			rd = &RoleData{
				ID:    roleID,
				Code:  code,
				Name:  name,
				Level: level,
			}
			rolesMap[roleID] = rd
		}

		if mod != "" && obj != "" && beh != "" {
			// Nil workspace is the stable platform-wide scope in the five-part
			// permission key consumed by authorization caches.
			permKey := fmt.Sprintf("%s:00000000-0000-0000-0000-000000000000:%s:%s:%s", out.ID.String(), mod, obj, beh)
			rd.Perms = append(rd.Perms, permKey)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	queryInsertTenantRole := fmt.Sprintf(`
		INSERT INTO %s.tenant_role (id, tenant_id, workspace_id, role_id, role_name, role_level, list_perm, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, '00000000-0000-0000-0000-000000000000', $2, $3, $4, $5, now(), now())
	`, r.iamSchema)

	for _, rd := range rolesMap {
		roleEntry := &iamproto.RoleEntry{
			Permissions: rd.Perms,
		}
		binaryData, err := proto.Marshal(roleEntry)
		if err != nil {
			return nil, fmt.Errorf("tenant repo: marshal tenant role entry (%s): %w", rd.Code, err)
		}

		_, err = tx.Exec(ctx, queryInsertTenantRole,
			out.ID,
			rd.ID,
			rd.Name,
			rd.Level,
			binaryData,
		)
		if err != nil {
			return nil, fmt.Errorf("tenant repo: insert tenant_role assignment (%s): %w", rd.Code, err)
		}
	}

	// Shared Redis is only a bounded relay after commit; the outbox row is the durability
	// boundary if either Controlplane or Cost Manager is unavailable.
	occurredAt := time.Now().UTC()
	eventID := uuid.NewSHA1(tenantWalletProvisionEventNamespace, out.ID[:])
	eventPayload, err := proto.Marshal(&iamproto.TenantWalletProvisionRequestedV1{
		EventId:       eventID[:],
		SchemaVersion: 1,
		TenantId:      out.ID[:],
		ActorUserId:   in.OwnerID[:],
		Currency:      "USD",
		OccurredAt:    occurredAt.Format(time.RFC3339Nano),
	})
	if err != nil {
		return nil, fmt.Errorf("tenant repo: marshal tenant wallet provision event: %w", err)
	}
	if _, err = tx.Exec(ctx, fmt.Sprintf(`
		INSERT INTO %s.billing_outbox_records
			(event_id, event_type, schema_version, aggregate_type, aggregate_id, aggregate_version,
			 owner_id, owner_type, actor_user_id, payload, occurred_at)
		VALUES ($1, 'billing.wallet.tenant.provision.requested.v1', 1, 'TENANT', $2, 1,
		        $2, 'TENANT', $3, $4, $5)
		ON CONFLICT (event_id) DO NOTHING
	`, r.iamSchema), eventID, out.ID, in.OwnerID, eventPayload, occurredAt); err != nil {
		return nil, fmt.Errorf("tenant repo: insert wallet provision outbox: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("tenant repo: commit tx: %w", err)
	}

	return out, nil
}
