// ======================================================================================================
// 📂 MODULE: controlplane/internal/hierarchy/repository/workspace_repo.go
//            Đặc Tả Hạ Tầng Lưu Trữ & Truy Vấn Workspace
// ======================================================================================================
//
// 📜 HIỆP ĐỒNG THIẾT KẾ:
//   - Sử dụng single-query validation pattern: kiểm tra ràng buộc zone/tenant trong cùng câu SQL
//     để tránh race condition giữa check và insert.
//   - Tất cả truy vấn SQL được biên dịch trước (pre-compiled) tại hàm khởi tạo.
//
// ======================================================================================================

package coreRepoImpl

import (
	"context"
	"fmt"

	"controlplane/internal/config"
	coreEntity "controlplane/internal/hierarchy/domain/entity"
	coreRepoInterface "controlplane/internal/hierarchy/domain/repo"
	coreModel "controlplane/internal/hierarchy/model"
	coreTaxonomy "controlplane/internal/hierarchy/taxonomy"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// [COMMENT]: WorkspaceRepoImpl triển khai WorkspaceRepository với pre-compiled SQL queries
type WorkspaceRepoImpl struct {
	db                   *pgxpool.Pool
	schema               string
	createWorkspaceQuery string
}

// [COMMENT]: NewWorkspaceRepoImpl khởi tạo repo và biên dịch sẵn tất cả câu SQL tránh fmt.Sprintf runtime
func NewWorkspaceRepoImpl(cfg *config.Config,
	db *pgxpool.Pool) coreRepoInterface.WorkspaceRepository {
	schema := cfg.SchemaSQL.Hierarchy
	return &WorkspaceRepoImpl{
		db:     db,
		schema: schema,

		// [COMMENT]: Single-query validation pattern — kiểm tra zone active + tenant active (nếu có)
		// trong cùng câu INSERT để tránh TOCTOU race condition.
		// Nếu zone không tồn tại/không active → không insert → zone_exists = false.
		// Nếu tenant_id != NULL nhưng tenant không tồn tại/không active → không insert → tenant_valid = false.
		createWorkspaceQuery: fmt.Sprintf(`
			WITH zone_check AS (
				SELECT id FROM %s.zones WHERE id = $4 AND status = 'active'
			), tenant_check AS (
				SELECT CASE
					WHEN $5::uuid IS NULL THEN true
					ELSE EXISTS(SELECT 1 FROM %s.tenants WHERE id = $5 AND status = 'active')
				END AS valid
			), inserted AS (
				INSERT INTO %s.workspaces (id, name, status, zone_id, tenant_id, owner_id, created_at, updated_at)
				SELECT $1, $2, $3, $4, $5, $6, now(), now()
				WHERE EXISTS(SELECT 1 FROM zone_check)
				  AND (SELECT valid FROM tenant_check)
				RETURNING id, name, status, zone_id, tenant_id, owner_id, created_at, updated_at
			)
			SELECT
				(SELECT COUNT(*) FROM zone_check) AS zone_exists,
				(SELECT valid FROM tenant_check) AS tenant_valid,
				i.id, i.name, i.status, i.zone_id, i.tenant_id, i.owner_id, i.created_at, i.updated_at
			FROM (SELECT 1) AS dummy
			LEFT JOIN inserted i ON true
		`, schema, schema, schema),
	}
}

// [COMMENT]: CreateWorkspace tạo workspace mới với ràng buộc atomic — zone phải active, tenant (nếu có) phải active
func (r *WorkspaceRepoImpl) CreateWorkspace(ctx context.Context, workspace coreEntity.Workspace) (*coreEntity.Workspace, error) {
	var zoneExists int
	var tenantValid bool
	var m coreModel.Workspace

	// [COMMENT]: Thực thi single-query đồng thời check ràng buộc và insert trong 1 round-trip
	err := r.db.QueryRow(ctx, r.createWorkspaceQuery,
		workspace.ID,             // $1: workspace id (UUIDv7)
		workspace.Name,           // $2: tên workspace
		string(workspace.Status), // $3: trạng thái mặc định (active)
		workspace.ZoneID,         // $4: zone id bắt buộc
		workspace.TenantID,       // $5: tenant id (nullable)
		workspace.OwnerID,        // $6: owner user id
	).Scan(
		&zoneExists,
		&tenantValid,
		&m.ID, &m.Name, &m.Status, &m.ZoneID, &m.TenantID, &m.OwnerID, &m.CreatedAt, &m.UpdatedAt,
	)

	// [COMMENT]: Nếu không có row trả về (LEFT JOIN → NULL columns) thì pgx trả ErrNoRows
	// Nhưng với LEFT JOIN, err sẽ nil và cần check zoneExists/tenantValid
	if err != nil && err != pgx.ErrNoRows {
		return nil, fmt.Errorf("workspace repo: create: %w", err)
	}

	// [COMMENT]: Zone không tồn tại hoặc không ở trạng thái active
	if zoneExists == 0 {
		return nil, coreTaxonomy.ErrWorkspaceZoneNotFound
	}

	// [COMMENT]: Tenant được chỉ định nhưng không tồn tại hoặc không active
	if !tenantValid {
		return nil, coreTaxonomy.ErrWorkspaceTenantNotFound
	}

	// [COMMENT]: Insert không thành công do ràng buộc khác (edge case)
	if m.ID.String() == "00000000-0000-0000-0000-000000000000" {
		return nil, coreTaxonomy.ErrWorkspaceInsertFailed
	}

	// [COMMENT]: Chuyển đổi DB model sang domain entity trước khi trả về
	result := coreModel.WorkspaceModelToEntity(m)
	return &result, nil
}
