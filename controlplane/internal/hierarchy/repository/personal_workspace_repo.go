// ======================================================================================================
// 📂 MODULE: controlplane/internal/hierarchy/repository/personal_workspace_repo.go
//            Đặc Tả Hạ Tầng Lưu Trữ & Truy Vấn Workspace Cá Nhân (Personal Scope)
// ======================================================================================================

package coreRepoImpl

import (
	"context"
	"errors"
	"fmt"
	"time"

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
				i.id, i.name, i.code, i.description, i.zone_id, i.owner_id, i.created_at, i.updated_at
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
			DELETE FROM %s.personal_workspaces 
			WHERE id = $1
		`, schema),
	}
}

func (r *PersonalWorkspaceRepoImpl) Create(ctx context.Context, workspace coreEntity.PersonalWorkspace) (*coreEntity.PersonalWorkspace, error) {
	var zoneExists int
	var tenantValid bool
	var m coreModel.PersonalWorkspace

	var (
		sID        *uuid.UUID
		sName      *string
		sCode      *string
		sDesc      *string
		sZoneID    *uuid.UUID
		sOwnerID   *uuid.UUID
		sCreatedAt *time.Time
		sUpdatedAt *time.Time
	)

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
		&sID, &sName, &sCode, &sDesc, &sZoneID, &sOwnerID, &sCreatedAt, &sUpdatedAt,
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
	if sID == nil {
		return nil, coreTaxonomy.ErrWorkspaceInsertFailed
	}

	m.ID = *sID
	m.Name = *sName
	m.Code = *sCode
	m.Description = *sDesc
	m.ZoneID = *sZoneID
	m.OwnerID = *sOwnerID
	m.CreatedAt = *sCreatedAt
	m.UpdatedAt = *sUpdatedAt

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

func (r *PersonalWorkspaceRepoImpl) Delete(ctx context.Context, id uuid.UUID) error {
	cmd, err := r.db.Exec(ctx, r.deleteWorkspaceQuery, id)
	if err != nil {
		return err
	}
	if cmd.RowsAffected() == 0 {
		return coreTaxonomy.ErrWorkspaceNotFound
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
