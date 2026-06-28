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
	return &TenantRepoImpl{
		db:     db,
		schema: schema,

		// [COMMENT]: Atomic CTE statement: insert bảng tenants trước, lấy ID vừa tạo rồi insert
		// bảng tenant_memberships để gắn quyền quản trị đầu tiên cho owner_id.
		createTenantQuery: fmt.Sprintf(`
			WITH inserted_tenant AS (
				INSERT INTO %s.tenants (id, code, name, status, created_at, updated_at)
				VALUES ($1, $2, $3, $4, now(), now())
				RETURNING id, code, name, status, created_at, updated_at
			), inserted_membership AS (
				INSERT INTO %s.tenant_memberships (id, tenant_id, user_id, status, created_at, updated_at)
				SELECT gen_random_uuid(), id, $5, 'active', now(), now()
				FROM inserted_tenant
				RETURNING id
			)
			SELECT id, code, name, status, created_at, updated_at FROM inserted_tenant
		`, schema, schema),
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
