// ======================================================================================================
// 📂 MODULE: controlplane/internal/hierarchy/repository/tenant_workspace_repo.go
//            Đặc Tả Hạ Tầng Lưu Trữ & Truy Vấn Workspace Doanh Nghiệp (Tenant Scope)
// ======================================================================================================

package coreRepoImpl

import (
	"context"
	"errors"
	"fmt"

	"controlplane/internal/config"
	coreEntity "controlplane/internal/hierarchy/domain/entity"
	coreModel "controlplane/internal/hierarchy/model"
	coreTaxonomy "controlplane/internal/hierarchy/taxonomy"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// [COMMENT]: TenantWorkspaceRepoImpl triển khai TenantWorkspaceRepository
type TenantWorkspaceRepoImpl struct {
	db                                *pgxpool.Pool
	schema                            string
	createWorkspaceQuery              string
	getWorkspaceByIDQuery             string
	listAllWorkspacesByTenantQuery    string
	listWorkspacesByTenantAndIDsQuery string
	listCatalogAllByTenantQuery       string
	listCatalogByTenantAndIDsQuery    string
	updateWorkspaceQuery              string
	deleteWorkspaceQuery              string
}

// [COMMENT]: NewTenantWorkspaceRepoImpl khởi tạo repo và biên dịch sẵn tất cả câu SQL tránh fmt.Sprintf runtime
func NewTenantWorkspaceRepoImpl(cfg *config.Config, db *pgxpool.Pool) *TenantWorkspaceRepoImpl {
	schema := cfg.SchemaSQL.Hierarchy
	return &TenantWorkspaceRepoImpl{
		db:     db,
		schema: schema,

		// [COMMENT]: Single-query validation pattern — kiểm tra zone active + tenant active trong cùng INSERT để tránh TOCTOU race condition
		createWorkspaceQuery: fmt.Sprintf(`
			WITH zone_check AS (
				SELECT id FROM %s.zones WHERE id = $5 AND status = 'active'
			), tenant_check AS (
				SELECT EXISTS(SELECT 1 FROM %s.tenants WHERE id = $6 AND status = 'active') AS valid
			), inserted AS (
				INSERT INTO %s.workspaces (id, name, code, status, zone_id, tenant_id, owner_id, created_at, updated_at)
				SELECT $1, $2, $3, $4, $5, $6, $7, now(), now()
				WHERE EXISTS(SELECT 1 FROM zone_check) AND (SELECT valid FROM tenant_check)
				RETURNING id, name, code, status, zone_id, tenant_id, owner_id, created_at, updated_at
			)
			SELECT
				(SELECT COUNT(*) FROM zone_check) AS zone_exists,
				(SELECT valid FROM tenant_check) AS tenant_valid,
				i.id, i.name, i.code, i.status, i.zone_id, i.tenant_id, i.owner_id, i.created_at, i.updated_at
			FROM (SELECT 1) AS dummy
			LEFT JOIN inserted i ON true
		`, schema, schema, schema),

		getWorkspaceByIDQuery: fmt.Sprintf(`
			SELECT id, name, code, status, zone_id, tenant_id, owner_id, created_at, updated_at 
			FROM %s.workspaces 
			WHERE id = $1
		`, schema),

		listAllWorkspacesByTenantQuery: fmt.Sprintf(`
			SELECT id, name, code, status, zone_id, tenant_id, owner_id, created_at, updated_at
			FROM %s.workspaces
			WHERE tenant_id = $1
		`, schema),

		listWorkspacesByTenantAndIDsQuery: fmt.Sprintf(`
			SELECT id, name, code, status, zone_id, tenant_id, owner_id, created_at, updated_at
			FROM %s.workspaces
			WHERE tenant_id = $1 AND id = ANY($2)
		`, schema),

		// [COMMENT]: Catalog queries chỉ SELECT 3 cột tối giản để phục vụ hot path — lọc theo zone_id giúp trả đúng ngữ cảnh deployment hiện tại
		listCatalogAllByTenantQuery: fmt.Sprintf(`
			SELECT id, code, name
			FROM %s.workspaces
			WHERE tenant_id = $1 AND zone_id = $2
		`, schema),

		listCatalogByTenantAndIDsQuery: fmt.Sprintf(`
			SELECT id, code, name
			FROM %s.workspaces
			WHERE tenant_id = $1 AND zone_id = $2 AND id = ANY($3)
		`, schema),

		updateWorkspaceQuery: fmt.Sprintf(`
			UPDATE %s.workspaces 
			SET name = $2, status = $3, updated_at = now() 
			WHERE id = $1 
			RETURNING id, name, code, status, zone_id, tenant_id, owner_id, created_at, updated_at
		`, schema),

		deleteWorkspaceQuery: fmt.Sprintf(`
			DELETE FROM %s.workspaces 
			WHERE id = $1
		`, schema),
	}
}

func (r *TenantWorkspaceRepoImpl) Create(ctx context.Context, workspace coreEntity.Workspace) (*coreEntity.Workspace, error) {
	var zoneExists int
	var tenantValid bool
	var m coreModel.Workspace

	err := r.db.QueryRow(ctx, r.createWorkspaceQuery,
		workspace.ID,
		workspace.Name,
		workspace.Code,
		string(workspace.Status),
		workspace.ZoneID,
		workspace.TenantID,
		workspace.OwnerID,
	).Scan(
		&zoneExists,
		&tenantValid,
		&m.ID, &m.Name, &m.Code, &m.Status, &m.ZoneID, &m.TenantID, &m.OwnerID, &m.CreatedAt, &m.UpdatedAt,
	)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, coreTaxonomy.ErrWorkspaceCodeAlreadyExists
		}
		if err != pgx.ErrNoRows {
			return nil, coreTaxonomy.ErrNoRowAffected
		}
	}

	if zoneExists == 0 {
		return nil, coreTaxonomy.ErrZoneNotFound
	}
	if !tenantValid {
		return nil, coreTaxonomy.ErrTenantNotFound
	}
	if m.ID.String() == "00000000-0000-0000-0000-000000000000" {
		return nil, coreTaxonomy.ErrWorkspaceInsertFailed
	}

	result := coreModel.WorkspaceModelToEntity(m)
	return &result, nil
}

func (r *TenantWorkspaceRepoImpl) GetByID(ctx context.Context, id uuid.UUID) (*coreEntity.Workspace, error) {
	var m coreModel.Workspace
	err := r.db.QueryRow(ctx, r.getWorkspaceByIDQuery, id).Scan(
		&m.ID, &m.Name, &m.Code, &m.Status, &m.ZoneID, &m.TenantID, &m.OwnerID, &m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, coreTaxonomy.ErrWorkspaceNotFound
		}
		return nil, err
	}
	result := coreModel.WorkspaceModelToEntity(m)
	return &result, nil
}

func (r *TenantWorkspaceRepoImpl) ListAllByTenant(ctx context.Context, tenantID uuid.UUID) ([]*coreEntity.Workspace, error) {
	rows, err := r.db.Query(ctx, r.listAllWorkspacesByTenantQuery, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*coreEntity.Workspace
	for rows.Next() {
		var m coreModel.Workspace
		err := rows.Scan(
			&m.ID, &m.Name, &m.Code, &m.Status, &m.ZoneID, &m.TenantID, &m.OwnerID, &m.CreatedAt, &m.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		result := coreModel.WorkspaceModelToEntity(m)
		list = append(list, &result)
	}
	return list, nil
}

func (r *TenantWorkspaceRepoImpl) ListByTenantAndIDs(ctx context.Context, tenantID uuid.UUID, ids []uuid.UUID) ([]*coreEntity.Workspace, error) {
	rows, err := r.db.Query(ctx, r.listWorkspacesByTenantAndIDsQuery, tenantID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*coreEntity.Workspace
	for rows.Next() {
		var m coreModel.Workspace
		err := rows.Scan(
			&m.ID, &m.Name, &m.Code, &m.Status, &m.ZoneID, &m.TenantID, &m.OwnerID, &m.CreatedAt, &m.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		result := coreModel.WorkspaceModelToEntity(m)
		list = append(list, &result)
	}
	return list, nil
}

func (r *TenantWorkspaceRepoImpl) Update(ctx context.Context, workspace coreEntity.Workspace) (*coreEntity.Workspace, error) {
	var m coreModel.Workspace
	err := r.db.QueryRow(ctx, r.updateWorkspaceQuery, workspace.ID, workspace.Name, string(workspace.Status)).Scan(
		&m.ID, &m.Name, &m.Code, &m.Status, &m.ZoneID, &m.TenantID, &m.OwnerID, &m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	result := coreModel.WorkspaceModelToEntity(m)
	return &result, nil
}

func (r *TenantWorkspaceRepoImpl) Delete(ctx context.Context, id uuid.UUID) error {
	cmd, err := r.db.Exec(ctx, r.deleteWorkspaceQuery, id)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() == 0 {
		return coreTaxonomy.ErrWorkspaceNotFound
	}
	return nil
}

// [COMMENT]: ListCatalogAllByTenant lấy catalog tối giản (id, code, name) của toàn bộ workspace thuộc Tenant trong Zone — hot path SELECT 3 cột
func (r *TenantWorkspaceRepoImpl) ListCatalogAllByTenant(ctx context.Context, tenantID uuid.UUID, zoneID uuid.UUID) ([]coreEntity.WorkspaceCatalog, error) {
	rows, err := r.db.Query(ctx, r.listCatalogAllByTenantQuery, tenantID, zoneID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []coreEntity.WorkspaceCatalog
	for rows.Next() {
		var c coreEntity.WorkspaceCatalog
		if err := rows.Scan(&c.ID, &c.Code, &c.Name); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, nil
}

// [COMMENT]: ListCatalogByTenantAndIDs lấy catalog tối giản (id, code, name) theo danh sách IDs trong Zone — hot path permission-aware
func (r *TenantWorkspaceRepoImpl) ListCatalogByTenantAndIDs(ctx context.Context, tenantID uuid.UUID, zoneID uuid.UUID, ids []uuid.UUID) ([]coreEntity.WorkspaceCatalog, error) {
	rows, err := r.db.Query(ctx, r.listCatalogByTenantAndIDsQuery, tenantID, zoneID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []coreEntity.WorkspaceCatalog
	for rows.Next() {
		var c coreEntity.WorkspaceCatalog
		if err := rows.Scan(&c.ID, &c.Code, &c.Name); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, nil
}

