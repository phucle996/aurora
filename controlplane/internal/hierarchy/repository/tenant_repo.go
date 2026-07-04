// ======================================================================================================
// 📂 MODULE: controlplane/internal/hierarchy/repository/tenant_repo.go
//            Đặc Tả Hạ Tầng Lưu Trữ & Truy Vấn Tenant
// ======================================================================================================

package coreRepoImpl

import (
	"context"
	"errors"
	"fmt"

	"controlplane/internal/config"
	coreEntity "controlplane/internal/hierarchy/domain/entity"
	coreRepoInterface "controlplane/internal/hierarchy/domain/repo"
	coreModel "controlplane/internal/hierarchy/model"
	coreTaxonomy "controlplane/internal/hierarchy/taxonomy"
	iamproto "controlplane/internal/iam/transport/rpc/proto"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"
)

// [COMMENT]: TenantRepoImpl triển khai TenantRepository
type TenantRepoImpl struct {
	db              *pgxpool.Pool
	hierarchySchema string
	iamSchema       string
}

// [COMMENT]: NewTenantRepoImpl khởi tạo repo
func NewTenantRepoImpl(cfg *config.Config, db *pgxpool.Pool) coreRepoInterface.TenantRepository {
	return &TenantRepoImpl{
		db:              db,
		hierarchySchema: cfg.SchemaSQL.Hierarchy,
		iamSchema:       cfg.SchemaSQL.IAM,
	}
}

// [COMMENT]: CreateTenant tạo tenant và tự động thêm owner làm member đầu tiên cùng với seeding 5 tenant roles trong 1 transaction
func (r *TenantRepoImpl) CreateTenant(ctx context.Context, tenant coreEntity.Tenant, ownerID uuid.UUID) (*coreEntity.Tenant, error) {
	// [COMMENT]: Khởi động database transaction để đảm bảo tính atomic và toàn vẹn dữ liệu
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("tenant repo: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// 1. Insert Tenant mới vào bảng tenants
	queryTenant := fmt.Sprintf(`
		INSERT INTO %s.tenants (id, code, name, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, now(), now())
		RETURNING id, code, name, status, created_at, updated_at
	`, r.hierarchySchema)

	var m coreModel.Tenant
	err = tx.QueryRow(ctx, queryTenant,
		tenant.ID,
		tenant.Code,
		tenant.Name,
		string(tenant.Status),
	).Scan(&m.ID, &m.Code, &m.Name, &m.Status, &m.CreatedAt, &m.UpdatedAt)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, coreTaxonomy.ErrCodeAlreadyExists
		}
		return nil, err
	}

	// 2. Insert tenant_membership cho Owner, đánh dấu is_ownership = true
	queryMembership := fmt.Sprintf(`
		INSERT INTO %s.tenant_memberships (id, tenant_id, user_id, status, is_ownership, created_at, updated_at)
		VALUES (gen_random_uuid(), $1, $2, 'active', true, now(), now())
	`, r.hierarchySchema)

	if _, err := tx.Exec(ctx, queryMembership, m.ID, ownerID); err != nil {
		return nil, fmt.Errorf("tenant repo: insert owner membership: %w", err)
	}

	// 3. Truy vấn các role của tenant và permissions đi kèm từ schema IAM
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
			// [COMMENT]: Ghép thành key 5 cấp định dạng: <tenant_id>:<workspace_id>:<module>:<object>:<behavior>
			// WorkspaceID sử dụng nil UUID đại diện cho platform-wide scope của tenant
			permKey := fmt.Sprintf("%s:00000000-0000-0000-0000-000000000000:%s:%s:%s", m.ID.String(), mod, obj, beh)
			rd.Perms = append(rd.Perms, permKey)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 4. Duyệt qua các vai trò đã gom quyền và seed vào bảng tenant_role của tenant đó
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
			m.ID,
			rd.ID,
			rd.Name,
			rd.Level,
			binaryData,
		)
		if err != nil {
			return nil, fmt.Errorf("tenant repo: insert tenant_role assignment (%s): %w", rd.Code, err)
		}
	}

	// 5. Commit Transaction
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("tenant repo: commit tx: %w", err)
	}

	result := coreModel.TenantModelToEntity(m)
	return &result, nil
}

// ResolveTenantByDomain tìm Tenant dựa vào domain liên kết.
func (r *TenantRepoImpl) ResolveTenantByDomain(ctx context.Context, domain string) (*coreEntity.Tenant, error) {
	query := fmt.Sprintf(`
		SELECT t.id, t.code, t.name, t.status, t.created_at, t.updated_at
		FROM %s.tenants t
		JOIN %s.tenant_domains td ON td.tenant_id = t.id
		WHERE td.domain = $1 AND t.status = 'active'
		LIMIT 1
	`, r.hierarchySchema, r.hierarchySchema)

	var m coreModel.Tenant
	err := r.db.QueryRow(ctx, query, domain).Scan(
		&m.ID, &m.Code, &m.Name, &m.Status, &m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, coreTaxonomy.ErrTenantNotFound
		}
		return nil, err
	}

	result := coreModel.TenantModelToEntity(m)
	return &result, nil
}

// ListTenantsPaged lấy danh sách tenants phân trang để phục vụ warmup chunk.
// Trả về: danh sách tenant, cờ hasMore để biết còn trang sau không, error.
func (r *TenantRepoImpl) ListTenantsPaged(ctx context.Context, limit, offset int) ([]coreEntity.Tenant, bool, error) {
	// Query thêm 1 dòng để kiểm tra xem còn trang sau (hasMore) hay không
	query := fmt.Sprintf(`
		SELECT t.id, t.code, t.name, t.status, t.created_at, t.updated_at, td.domain
		FROM %s.tenants t
		LEFT JOIN %s.tenant_domains td ON td.tenant_id = t.id AND td.is_primary = true
		ORDER BY t.created_at ASC
		LIMIT $1 OFFSET $2
	`, r.hierarchySchema, r.hierarchySchema)

	rows, err := r.db.Query(ctx, query, limit+1, offset)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	var out []coreEntity.Tenant
	for rows.Next() {
		var m coreModel.Tenant
		var domainOpt *string
		if err := rows.Scan(
			&m.ID, &m.Code, &m.Name, &m.Status, &m.CreatedAt, &m.UpdatedAt, &domainOpt,
		); err != nil {
			return nil, false, err
		}

		ent := coreModel.TenantModelToEntity(m)
		// Trích xuất domain chính gắn vào entity
		if domainOpt != nil {
			ent.Domain = *domainOpt
		}
		out = append(out, ent)
	}

	hasMore := false
	if len(out) > limit {
		hasMore = true
		out = out[:limit] // Cắt bớt phần dư
	}

	return out, hasMore, nil
}

// CheckMembership kiểm tra user có thuộc tenant không và lấy role tương ứng.
// Tra vào bảng tenant_memberships để xác định membership status.
func (r *TenantRepoImpl) CheckMembership(ctx context.Context, tenantID, userID uuid.UUID) (isMember bool, role string, err error) {
	query := fmt.Sprintf(`
		SELECT role, status
		FROM %s.tenant_memberships
		WHERE tenant_id = $1 AND user_id = $2
		LIMIT 1
	`, r.hierarchySchema)

	var memberRole, status string
	err = r.db.QueryRow(ctx, query, tenantID, userID).Scan(&memberRole, &status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// [COMMENT]: Không có record → user không thuộc tenant này
			return false, "", nil
		}
		return false, "", err
	}

	// [COMMENT]: Chỉ xác nhận membership khi status = active
	if status != "active" {
		return false, "", nil
	}

	return true, memberRole, nil
}
