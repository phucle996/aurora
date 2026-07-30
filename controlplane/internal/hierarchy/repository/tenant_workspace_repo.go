package hierarchyRepoImpl

import (
	"context"
	"errors"
	"fmt"

	"controlplane/internal/config"
	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
	hierarchyRepoInterface "controlplane/internal/hierarchy/domain/repo"
	hierarchyTaxonomy "controlplane/internal/hierarchy/taxonomy"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TenantWorkspaceRepoImpl struct {
	db               *pgxpool.Pool
	createQuery      string
	listQuery        string
	listCatalogQuery string
	deleteQuery      string
}

func NewTenantWorkspaceRepoImpl(cfg *config.Config, db *pgxpool.Pool) hierarchyRepoInterface.TenantWorkspaceRepository {
	schema := cfg.SchemaSQL.Hierarchy
	return &TenantWorkspaceRepoImpl{
		db: db,
		// FOR SHARE keeps concurrent parent status transitions from invalidating
		// either durable precondition between the checks and insert.
		createQuery: fmt.Sprintf(`
			WITH target_zone AS MATERIALIZED (
				SELECT id, status FROM %s.zones WHERE id = $5 FOR SHARE
			), target_tenant AS MATERIALIZED (
				SELECT id, status FROM %s.tenants WHERE id = $6 FOR SHARE
			), inserted AS (
				INSERT INTO %s.tenant_workspaces
					(id, name, code, description, zone_id, tenant_id, owner_id, created_at, updated_at)
				SELECT $1, $2, $3, $4, target_zone.id, target_tenant.id, $7, $8, $9
				FROM target_zone CROSS JOIN target_tenant
				WHERE target_zone.status = 'active' AND target_tenant.status = 'active'
				RETURNING id, name, code, COALESCE(description, ''), zone_id, tenant_id, owner_id, created_at, updated_at
			)
			SELECT EXISTS(SELECT 1 FROM target_zone),
				COALESCE((SELECT status = 'active' FROM target_zone), false),
				EXISTS(SELECT 1 FROM target_tenant),
				COALESCE((SELECT status = 'active' FROM target_tenant), false),
				EXISTS(SELECT 1 FROM inserted),
				COALESCE((SELECT id FROM inserted), '00000000-0000-0000-0000-000000000000'::uuid),
				COALESCE((SELECT name FROM inserted), ''),
				COALESCE((SELECT code FROM inserted), ''),
				COALESCE((SELECT description FROM inserted), ''),
				COALESCE((SELECT zone_id FROM inserted), '00000000-0000-0000-0000-000000000000'::uuid),
				COALESCE((SELECT tenant_id FROM inserted), '00000000-0000-0000-0000-000000000000'::uuid),
				COALESCE((SELECT owner_id FROM inserted), '00000000-0000-0000-0000-000000000000'::uuid),
				COALESCE((SELECT created_at FROM inserted), now()),
				COALESCE((SELECT updated_at FROM inserted), now())
		`, schema, schema, schema),
		listQuery: fmt.Sprintf(`
			SELECT id, name, code, COALESCE(description, ''), zone_id, owner_id, created_at
			FROM %s.tenant_workspaces
			WHERE tenant_id = $1 AND ($2 OR id = ANY($3))
			ORDER BY created_at, id
		`, schema),
		listCatalogQuery: fmt.Sprintf(`
			SELECT id, code, name
			FROM %s.tenant_workspaces
			WHERE tenant_id = $1 AND zone_id = $2 AND ($3 OR id = ANY($4))
			ORDER BY created_at, id
		`, schema),
		// Locking the complete tenant scope serializes concurrent deletes so two
		// replicas cannot both observe a count of two and remove the final pair.
		deleteQuery: fmt.Sprintf(`
			WITH locked_workspaces AS MATERIALIZED (
				SELECT id FROM %s.tenant_workspaces WHERE tenant_id = $2 FOR UPDATE
			), target AS MATERIALIZED (
				SELECT id FROM locked_workspaces WHERE id = $1
			), workspace_count AS MATERIALIZED (
				SELECT COUNT(*) AS total FROM locked_workspaces
			), deleted AS (
				DELETE FROM %s.tenant_workspaces
				WHERE id = $1 AND tenant_id = $2 AND (SELECT total FROM workspace_count) > 1
				RETURNING id
			)
			SELECT EXISTS(SELECT 1 FROM target),
				COALESCE((SELECT total FROM workspace_count), 0),
				EXISTS(SELECT 1 FROM deleted)
		`, schema, schema),
	}
}

func (r *TenantWorkspaceRepoImpl) CreateWorkspaceForTenant(ctx context.Context, in *hierarchyEntity.CreateTenantWorkspace) (*hierarchyEntity.CreateTenantWorkspace, error) {
	var zoneExists, zoneActive, tenantExists, tenantActive, inserted bool
	out := &hierarchyEntity.CreateTenantWorkspace{}
	err := r.db.QueryRow(ctx, r.createQuery,
		in.ID, in.Name, in.Code, in.Description, in.ZoneID, in.TenantID, in.OwnerID, in.CreatedAt, in.UpdatedAt,
	).Scan(
		&zoneExists, &zoneActive, &tenantExists, &tenantActive, &inserted, &out.ID, &out.Name, &out.Code,
		&out.Description, &out.ZoneID, &out.TenantID, &out.OwnerID, &out.CreatedAt, &out.UpdatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, hierarchyTaxonomy.ErrAlreadyExists
		}
		return nil, err
	}
	if !zoneExists || !tenantExists {
		return nil, hierarchyTaxonomy.ErrNotFound
	}
	if !zoneActive || !tenantActive {
		return nil, hierarchyTaxonomy.ErrPreconditionFailed
	}
	if !inserted || out.ID == uuid.Nil {
		return nil, fmt.Errorf("create tenant workspace returned no row")
	}
	return out, nil
}

func (r *TenantWorkspaceRepoImpl) ListWorkspacesForTenant(ctx context.Context, in *hierarchyEntity.ListTenantWorkspaces) ([]hierarchyEntity.ListTenantWorkspaces, error) {
	rows, err := r.db.Query(ctx, r.listQuery, in.TenantID, in.AllWorkspaces, in.AllowedWorkspaceIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]hierarchyEntity.ListTenantWorkspaces, 0)
	for rows.Next() {
		item := hierarchyEntity.ListTenantWorkspaces{TenantID: in.TenantID, RoleID: in.RoleID}
		if err := rows.Scan(&item.ID, &item.Name, &item.Code, &item.Description, &item.ZoneID, &item.OwnerID, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *TenantWorkspaceRepoImpl) ListWorkspaceCatalogForTenant(ctx context.Context, in *hierarchyEntity.ListTenantWorkspaceCatalog) ([]hierarchyEntity.ListTenantWorkspaceCatalog, error) {
	rows, err := r.db.Query(ctx, r.listCatalogQuery, in.TenantID, in.ZoneID, in.AllWorkspaces, in.AllowedWorkspaceIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]hierarchyEntity.ListTenantWorkspaceCatalog, 0)
	for rows.Next() {
		item := hierarchyEntity.ListTenantWorkspaceCatalog{TenantID: in.TenantID, ZoneID: in.ZoneID, RoleID: in.RoleID}
		if err := rows.Scan(&item.ID, &item.Code, &item.Name); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *TenantWorkspaceRepoImpl) DeleteWorkspaceForTenant(ctx context.Context, in *hierarchyEntity.DeleteTenantWorkspace) error {
	var exists, deleted bool
	var total int
	err := r.db.QueryRow(ctx, r.deleteQuery, in.ID, in.TenantID).Scan(&exists, &total, &deleted)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return hierarchyTaxonomy.ErrPreconditionFailed
		}
		return err
	}
	if !exists {
		return hierarchyTaxonomy.ErrNotFound
	}
	if total <= 1 || !deleted {
		return hierarchyTaxonomy.ErrPreconditionFailed
	}
	return nil
}
