// ======================================================================================================
// 📂 MODULE: controlplane/internal/hierarchy/repository/personal_workspace_repo.go
//            Đặc Tả Hạ Tầng Lưu Trữ & Truy Vấn Workspace Cá Nhân (Personal Scope)
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

// [COMMENT]: PersonalWorkspaceRepoImpl triển khai PersonalWorkspaceRepository
type PersonalWorkspaceRepoImpl struct {
	db                         *pgxpool.Pool
	schema                     string
	createWorkspaceQuery       string
	getWorkspaceByIDQuery      string
	listWorkspacesByOwnerQuery string
	listCatalogByOwnerQuery    string
	updateWorkspaceQuery       string
	deleteWorkspaceQuery       string
}

// [COMMENT]: NewPersonalWorkspaceRepoImpl khởi tạo repo và biên dịch sẵn tất cả câu SQL tránh fmt.Sprintf runtime
func NewPersonalWorkspaceRepoImpl(cfg *config.Config, db *pgxpool.Pool) *PersonalWorkspaceRepoImpl {
	schema := cfg.SchemaSQL.Hierarchy
	return &PersonalWorkspaceRepoImpl{
		db:     db,
		schema: schema,

		// [COMMENT]: Single-query validation pattern — kiểm tra zone active trong cùng câu INSERT để tránh race condition.
		createWorkspaceQuery: fmt.Sprintf(`
			WITH zone_check AS (
				SELECT id FROM %s.zones WHERE id = $5 AND status = 'active'
			), inserted AS (
				INSERT INTO %s.personal_workspaces (id, name, code, description, zone_id, owner_id, created_at, updated_at)
				SELECT $1, $2, $3, $4, $5, $6, now(), now()
				WHERE EXISTS(SELECT 1 FROM zone_check)
				RETURNING id, name, code, COALESCE(description, '') AS description, zone_id, owner_id, created_at, updated_at
			)
			SELECT
				(SELECT COUNT(*) FROM zone_check) AS zone_exists,
				true AS tenant_valid,
				COALESCE(i.id, '00000000-0000-0000-0000-000000000000'::uuid) AS id,
				COALESCE(i.name, '') AS name,
				COALESCE(i.code, '') AS code,
				COALESCE(i.description, '') AS description,
				COALESCE(i.zone_id, '00000000-0000-0000-0000-000000000000'::uuid) AS zone_id,
				COALESCE(i.owner_id, '00000000-0000-0000-0000-000000000000'::uuid) AS owner_id,
				COALESCE(i.created_at, now()) AS created_at,
				COALESCE(i.updated_at, now()) AS updated_at
			FROM (SELECT 1) AS dummy
			LEFT JOIN inserted i ON true
		`, schema, schema),

		getWorkspaceByIDQuery: fmt.Sprintf(`
			SELECT id, name, code, COALESCE(description, '') AS description, zone_id, owner_id, created_at, updated_at 
			FROM %s.personal_workspaces 
			WHERE id = $1
		`, schema),

		listWorkspacesByOwnerQuery: fmt.Sprintf(`
			SELECT id, name, code, COALESCE(description, '') AS description, created_at
			FROM %s.personal_workspaces
			WHERE owner_id = $1
		`, schema),

		// [COMMENT]: Catalog query chỉ SELECT 3 cột (id, code, name) lọc theo zone_id — hot path danh sách workspace cá nhân
		listCatalogByOwnerQuery: fmt.Sprintf(`
			SELECT id, code, name
			FROM %s.personal_workspaces
			WHERE owner_id = $1 AND zone_id = $2
		`, schema),

		updateWorkspaceQuery: fmt.Sprintf(`
			UPDATE %s.personal_workspaces 
			SET name = $2, description = $3, updated_at = now() 
			WHERE id = $1 
			RETURNING id, name, code, COALESCE(description, '') AS description, zone_id, owner_id, created_at, updated_at
		`, schema),

		deleteWorkspaceQuery: fmt.Sprintf(`
			WITH workspace_check AS (
				SELECT id, owner_id FROM %s.personal_workspaces WHERE id = $1 AND owner_id = $2
			), total_count AS (
				SELECT COUNT(*) AS total FROM %s.personal_workspaces WHERE owner_id = $2
			), deleted AS (
				DELETE FROM %s.personal_workspaces
				WHERE id = $1 AND owner_id = $2 AND (SELECT total FROM total_count) > 1
				RETURNING id
			)
			SELECT 
				EXISTS(SELECT 1 FROM workspace_check) AS exists,
				(SELECT total FROM total_count) AS total_count,
				EXISTS(SELECT 1 FROM deleted) AS deleted
		`, schema, schema, schema),
	}
}

func (r *PersonalWorkspaceRepoImpl) Create(ctx context.Context, workspace coreEntity.PersonalWorkspace) (*coreEntity.PersonalWorkspace, error) {
	var zoneExists int
	var tenantValid bool
	var m coreModel.PersonalWorkspace

	err := r.db.QueryRow(ctx, r.createWorkspaceQuery,
		workspace.ID,
		workspace.Name,
		workspace.Code,
		workspace.Description,
		workspace.ZoneID,
		workspace.OwnerID,
	).Scan(
		&zoneExists,
		&tenantValid,
		&m.ID, &m.Name, &m.Code, &m.Description, &m.ZoneID, &m.OwnerID, &m.CreatedAt, &m.UpdatedAt,
	)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, coreTaxonomy.ErrWorkspaceCodeAlreadyExists
		}
		return nil, fmt.Errorf("database query error: %w", err)
	}

	if zoneExists == 0 {
		return nil, coreTaxonomy.ErrZoneNotFound
	}
	if m.ID == uuid.Nil {
		return nil, coreTaxonomy.ErrWorkspaceInsertFailed
	}

	result := coreModel.PersonalWorkspaceModelToEntity(m)
	return &result, nil
}

func (r *PersonalWorkspaceRepoImpl) GetByID(ctx context.Context, id uuid.UUID) (*coreEntity.PersonalWorkspace, error) {
	var m coreModel.PersonalWorkspace
	err := r.db.QueryRow(ctx, r.getWorkspaceByIDQuery, id).Scan(
		&m.ID, &m.Name, &m.Code, &m.Description, &m.ZoneID, &m.OwnerID, &m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, coreTaxonomy.ErrWorkspaceNotFound
		}
		return nil, err
	}
	result := coreModel.PersonalWorkspaceModelToEntity(m)
	return &result, nil
}

func (r *PersonalWorkspaceRepoImpl) ListByOwner(ctx context.Context, ownerID uuid.UUID) ([]*coreEntity.WorkspacePersonalListItem, error) {
	rows, err := r.db.Query(ctx, r.listWorkspacesByOwnerQuery, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []*coreEntity.WorkspacePersonalListItem
	for rows.Next() {
		var item coreEntity.WorkspacePersonalListItem
		err := rows.Scan(
			&item.ID, &item.Name, &item.Code, &item.Description, &item.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		list = append(list, &item)
	}
	return list, nil
}

func (r *PersonalWorkspaceRepoImpl) Update(ctx context.Context, workspace coreEntity.PersonalWorkspace) (*coreEntity.PersonalWorkspace, error) {
	var m coreModel.PersonalWorkspace
	err := r.db.QueryRow(ctx, r.updateWorkspaceQuery, workspace.ID, workspace.Name, workspace.Description).Scan(
		&m.ID, &m.Name, &m.Code, &m.Description, &m.ZoneID, &m.OwnerID, &m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	result := coreModel.PersonalWorkspaceModelToEntity(m)
	return &result, nil
}

func (r *PersonalWorkspaceRepoImpl) Delete(ctx context.Context, id uuid.UUID, ownerID uuid.UUID) error {
	var exists bool
	var totalCount int
	var deleted bool

	err := r.db.QueryRow(ctx, r.deleteWorkspaceQuery, id, ownerID).Scan(&exists, &totalCount, &deleted)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return coreTaxonomy.ErrWorkspaceNotEmpty
		}
		return err
	}

	if !exists {
		return coreTaxonomy.ErrWorkspaceNotFound
	}
	if !deleted {
		return coreTaxonomy.ErrLastWorkspaceDeletionBlocked
	}
	return nil
}

// [COMMENT]: ListCatalogByOwner lấy catalog tối giản (id, code, name) của workspace cá nhân trong Zone — hot path
func (r *PersonalWorkspaceRepoImpl) ListCatalogByOwner(ctx context.Context, ownerID uuid.UUID, zoneID uuid.UUID) ([]coreEntity.WorkspaceCatalog, error) {
	rows, err := r.db.Query(ctx, r.listCatalogByOwnerQuery, ownerID, zoneID)
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
