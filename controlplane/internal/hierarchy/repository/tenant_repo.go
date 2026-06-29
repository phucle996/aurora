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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// [COMMENT]: TenantRepoImpl triển khai TenantRepository với pre-compiled SQL queries
type TenantRepoImpl struct {
	db                *pgxpool.Pool
	schema            string
	createTenantQuery string
}

// [COMMENT]: NewTenantRepoImpl khởi tạo repo và biên dịch sẵn các câu SQL
func NewTenantRepoImpl(cfg *config.Config, db *pgxpool.Pool) coreRepoInterface.TenantRepository {
	schema := cfg.SchemaSQL.Hierarchy
	iamSchema := cfg.SchemaSQL.IAM
	return &TenantRepoImpl{
		db:     db,
		schema: schema,

		// [COMMENT]: Atomic CTE statement: insert bảng tenants trước, lấy ID vừa tạo rồi insert
		// bảng tenant_memberships để gắn quyền quản trị đầu tiên cho owner_id.
		// Đồng thời gán role tenant_owner mặc định cho owner trong iam.user_role_assignments.
		createTenantQuery: fmt.Sprintf(`
			WITH inserted_tenant AS (
				INSERT INTO %s.tenants (id, code, name, status, created_at, updated_at)
				VALUES ($1, $2, $3, $4, now(), now())
				RETURNING id, code, name, status, created_at, updated_at
			), inserted_membership AS (
				INSERT INTO %s.tenant_memberships (id, tenant_id, user_id, status, created_at, updated_at)
				SELECT gen_random_uuid(), id, $5, 'active', now(), now()
				FROM inserted_tenant
				RETURNING tenant_id, user_id
			), inserted_role_assignment AS (
				INSERT INTO %s.user_role_assignments (id, user_id, role_id, scope_type, tenant_id, workspace_id, assigned_at)
				SELECT gen_random_uuid(), im.user_id, r.id, 'tenant', im.tenant_id, NULL, NOW()
				FROM inserted_membership im
				CROSS JOIN %s.roles r
				WHERE r.code = 'tenant_owner'
				LIMIT 1
			)
			SELECT id, code, name, status, created_at, updated_at FROM inserted_tenant
		`, schema, schema, iamSchema, iamSchema),
	}
}

// [COMMENT]: CreateTenant tạo tenant và tự động thêm owner làm member đầu tiên một cách atomic
func (r *TenantRepoImpl) CreateTenant(ctx context.Context, tenant coreEntity.Tenant, ownerID uuid.UUID) (*coreEntity.Tenant, error) {
	var m coreModel.Tenant

	err := r.db.QueryRow(ctx, r.createTenantQuery,
		tenant.ID,             // $1: tenant id (UUIDv7)
		tenant.Code,           // $2: tenant code (lowercase, unique)
		tenant.Name,           // $3: tên hiển thị
		string(tenant.Status), // $4: trạng thái mặc định (active)
		ownerID,               // $5: owner user id
	).Scan(
		&m.ID, &m.Code, &m.Name, &m.Status, &m.CreatedAt, &m.UpdatedAt,
	)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, coreTaxonomy.ErrCodeAlreadyExists
		}
		if err == pgx.ErrNoRows {
			return nil, coreTaxonomy.ErrNoRowAffected
		}
		return nil, err
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
	`, r.schema, r.schema)

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
	`, r.schema, r.schema)

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
	`, r.schema)

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
