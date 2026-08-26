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
	iamproto "controlplane/internal/iam/transport/proto"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
)

var tenantWalletProvisionEventNamespace = uuid.MustParse("24bbad2a-d35b-5e77-b548-31b81dbac82c")

const tenantWalletProvisionRequestedEventType = "billing.tenant_wallet.provision.requested.v1"

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
	// [COMMENT]: Permission catalog and the compiled RoleEntry must be read from
	// one snapshot. A concurrent catalog change is handled by a later explicit
	// role-version workflow, never by silently mixing two permission sets.
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return nil, fmt.Errorf("tenant repo: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, fmt.Sprintf(`
		SELECT module, object, behavior
		FROM %s.permissions
		ORDER BY module, object, behavior
	`, r.iamSchema))
	if err != nil {
		return nil, fmt.Errorf("tenant repo: read permission catalog: %w", err)
	}
	defer rows.Close()

	permissions := make([]string, 0, 64)
	for rows.Next() {
		var module, object, behavior string
		if err := rows.Scan(&module, &object, &behavior); err != nil {
			return nil, fmt.Errorf("tenant repo: scan permission catalog: %w", err)
		}
		permissions = append(permissions, fmt.Sprintf(
			"%s:00000000-0000-0000-0000-000000000000:%s:%s:%s",
			in.ID.String(), module, object, behavior,
		))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tenant repo: iterate permission catalog: %w", err)
	}
	rows.Close()
	if len(permissions) == 0 {
		return nil, hierarchyTaxonomy.ErrPreconditionFailed
	}
	binaryData, err := proto.MarshalOptions{Deterministic: true}.Marshal(&iamproto.RoleEntry{Permissions: permissions})
	if err != nil {
		return nil, fmt.Errorf("tenant repo: marshal tenant root role entry: %w", err)
	}

	// Shared Redis is only a bounded relay after commit; the outbox row is the durability
	// boundary if either the producer or any downstream consumer is unavailable.
	occurredAt := time.Now().UTC()
	eventID := uuid.NewSHA1(tenantWalletProvisionEventNamespace, in.ID[:])
	eventPayload, err := proto.Marshal(&iamproto.TenantWalletProvisionRequestedV1{
		EventId:       eventID[:],
		SchemaVersion: 1,
		TenantId:      in.ID[:],
		ActorUserId:   in.OwnerID[:],
		Currency:      "USD",
		OccurredAt:    occurredAt.Format(time.RFC3339Nano),
	})
	if err != nil {
		return nil, fmt.Errorf("tenant repo: marshal tenant wallet provision event: %w", err)
	}

	// [COMMENT]: Tenant, owner membership, normalized root definition, compiled
	// assignment and the Cost outbox command cross schemas but share one PostgreSQL commit.
	query := fmt.Sprintf(`
		WITH tenant_inserted AS (
			INSERT INTO %s.tenants (id, code, name, status, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $5)
			RETURNING id, code, name, status, created_at, updated_at
		), domain_inserted AS (
			INSERT INTO %s.tenant_domains (id, tenant_id, domain, is_primary, created_at)
			SELECT $14, id, $15, true, $5 FROM tenant_inserted
			RETURNING id
		), membership_inserted AS (
			INSERT INTO %s.tenant_memberships
				(id, tenant_id, user_id, status, is_ownership, created_at, updated_at)
			SELECT $7, id, $6, 'active', true, $5, $5 FROM tenant_inserted
			RETURNING id
		), role_inserted AS (
			INSERT INTO %s.tenant_roles
				(id, tenant_id, code, name, description, role_level, version, created_by, created_at, updated_at)
			SELECT $8, id, 'tenant_root', 'Tenant Root', 'Highest authority inside this tenant', 3, 1, $6, $5, $5
			FROM tenant_inserted
			RETURNING id
		), role_permissions_inserted AS (
			INSERT INTO %s.tenant_role_permissions (tenant_role_id, permission_id, created_at)
			SELECT role_inserted.id, permissions.id, $5
			FROM role_inserted CROSS JOIN %s.permissions
			RETURNING permission_id
		), assignment_inserted AS (
			INSERT INTO %s.membership_role
				(id, membership_id, tenant_role_id, workspace_id, role_name, role_level, role_version, list_perm, created_at, updated_at)
			SELECT $9, membership_inserted.id, role_inserted.id,
			       '00000000-0000-0000-0000-000000000000'::uuid,
			       'Tenant Root', 3, 1, $10, $5, $5
			FROM membership_inserted CROSS JOIN role_inserted
			WHERE EXISTS (SELECT 1 FROM role_permissions_inserted)
			RETURNING id
		), outbox_inserted AS (
			INSERT INTO %s.cost_outbox_records
				(event_id, event_type, aggregate_type, aggregate_id, aggregate_version,
				 owner_id, owner_type, actor_user_id, payload, occurred_at)
			SELECT $11, '%s', 'TENANT', tenant_inserted.id, 1,
			       tenant_inserted.id, 'TENANT', $6, $12, $13
			FROM tenant_inserted CROSS JOIN assignment_inserted
			RETURNING event_id
		)
		SELECT tenant_inserted.id, tenant_inserted.code, tenant_inserted.name,
		       tenant_inserted.status, tenant_inserted.created_at, tenant_inserted.updated_at
		FROM tenant_inserted CROSS JOIN domain_inserted CROSS JOIN outbox_inserted
	`, r.hierarchySchema, r.hierarchySchema, r.hierarchySchema, r.iamSchema, r.iamSchema, r.iamSchema, r.iamSchema, r.hierarchySchema, tenantWalletProvisionRequestedEventType)

	out := &hierarchyEntity.CreateTenant{
		OwnerID:           in.OwnerID,
		OwnerMembershipID: in.OwnerMembershipID,
		TenantRootRoleID:  in.TenantRootRoleID,
		MembershipRoleID:  in.MembershipRoleID,
		DomainID:          in.DomainID,
		PrimaryDomain:     in.PrimaryDomain,
	}
	err = tx.QueryRow(ctx, query,
		in.ID, in.Code, in.Name, string(in.Status), in.CreatedAt, in.OwnerID,
		in.OwnerMembershipID, in.TenantRootRoleID, in.MembershipRoleID, binaryData,
		eventID, eventPayload, occurredAt, in.DomainID, in.PrimaryDomain,
	).Scan(&out.ID, &out.Code, &out.Name, &out.Status, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, hierarchyTaxonomy.ErrAlreadyExists
		}
		return nil, fmt.Errorf("tenant repo: create tenant aggregate: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("tenant repo: commit tx: %w", err)
	}

	return out, nil
}

func (r *TenantRepoImpl) ClaimTenantWalletProvisionOutbox(ctx context.Context, limit int) ([]hierarchyEntity.TenantWalletProvisionOutbox, error) {
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		WITH candidates AS (
			SELECT id
			FROM %s.cost_outbox_records
			WHERE event_type=$2
				AND ((status='PENDING' AND available_at<=NOW()) OR (status='PUBLISHING' AND lease_until<NOW()))
			ORDER BY available_at,id
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE %s.cost_outbox_records outbox
		SET status='PUBLISHING', lease_until=NOW()+INTERVAL '30 seconds', attempts=attempts+1, updated_at=NOW()
		FROM candidates
		WHERE outbox.id=candidates.id AND outbox.event_type=$2
		RETURNING outbox.id,outbox.event_id,outbox.owner_id,outbox.actor_user_id,outbox.payload
	`, r.hierarchySchema, r.hierarchySchema), limit, tenantWalletProvisionRequestedEventType)
	if err != nil {
		return nil, fmt.Errorf("tenant wallet provision outbox: claim: %w", err)
	}
	defer rows.Close()

	out := make([]hierarchyEntity.TenantWalletProvisionOutbox, 0, limit)
	for rows.Next() {
		var event hierarchyEntity.TenantWalletProvisionOutbox
		if err := rows.Scan(&event.ID, &event.EventID, &event.TenantID, &event.ActorUserID, &event.Payload); err != nil {
			return nil, fmt.Errorf("tenant wallet provision outbox: scan claim: %w", err)
		}
		out = append(out, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tenant wallet provision outbox: iterate claim: %w", err)
	}
	return out, nil
}

func (r *TenantRepoImpl) MarkTenantWalletProvisionPublished(ctx context.Context, id int64) error {
	_, err := r.db.Exec(ctx, fmt.Sprintf(`
		UPDATE %s.cost_outbox_records
		SET status='PUBLISHED',published_at=NOW(),lease_until=NULL,updated_at=NOW()
		WHERE id=$1 AND event_type=$2 AND status='PUBLISHING'
	`, r.hierarchySchema), id, tenantWalletProvisionRequestedEventType)
	return err
}

func (r *TenantRepoImpl) MarkTenantWalletProvisionFailed(ctx context.Context, id int64, message string) error {
	_, err := r.db.Exec(ctx, fmt.Sprintf(`
		UPDATE %s.cost_outbox_records
		SET status=CASE WHEN attempts>=25 THEN 'DEAD' ELSE 'PENDING' END,
			available_at=NOW()+make_interval(secs => LEAST(30, 1 << LEAST(GREATEST(attempts - 1, 0), 5))),
			lease_until=NULL,last_error=LEFT($3,2000),updated_at=NOW()
		WHERE id=$1 AND event_type=$2 AND status='PUBLISHING'
	`, r.hierarchySchema), id, tenantWalletProvisionRequestedEventType, message)
	return err
}

func (r *TenantRepoImpl) MarkTenantWalletProvisionDead(ctx context.Context, id int64, message string) error {
	_, err := r.db.Exec(ctx, fmt.Sprintf(`
		UPDATE %s.cost_outbox_records
		SET status='DEAD',lease_until=NULL,last_error=LEFT($3,2000),updated_at=NOW()
		WHERE id=$1 AND event_type=$2 AND status='PUBLISHING'
	`, r.hierarchySchema), id, tenantWalletProvisionRequestedEventType, message)
	return err
}

func (r *TenantRepoImpl) ListTenantsForUser(ctx context.Context, userID uuid.UUID) ([]hierarchyEntity.TenantCatalogItem, error) {
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT t.id, t.code, t.name, d.domain, COALESCE(mr.role_name, ''), COALESCE(mr.role_level, 99)
		FROM %s.tenant_memberships m
		JOIN %s.tenants t ON t.id = m.tenant_id AND t.status = 'active'
		JOIN %s.tenant_domains d ON d.tenant_id = t.id AND d.is_primary = true
		LEFT JOIN %s.membership_role mr ON mr.membership_id = m.id
			AND mr.workspace_id = '00000000-0000-0000-0000-000000000000'::uuid
		WHERE m.user_id = $1 AND m.status = 'active'
		ORDER BY lower(t.name), t.id
	`, r.hierarchySchema, r.hierarchySchema, r.hierarchySchema, r.iamSchema), userID)
	if err != nil {
		return nil, fmt.Errorf("tenant repo: list user tenant catalog: %w", err)
	}
	defer rows.Close()

	out := make([]hierarchyEntity.TenantCatalogItem, 0)
	for rows.Next() {
		var item hierarchyEntity.TenantCatalogItem
		if err := rows.Scan(&item.ID, &item.Code, &item.Name, &item.PrimaryDomain, &item.RoleName, &item.RoleLevel); err != nil {
			return nil, fmt.Errorf("tenant repo: scan user tenant catalog: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("tenant repo: iterate user tenant catalog: %w", err)
	}
	return out, nil
}
