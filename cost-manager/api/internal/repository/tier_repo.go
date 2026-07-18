package repository

import (
	"context"
	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type tierRepository struct {
	db *pgxpool.Pool
}

// [COMMENT]: NewTierRepository khởi tạo instance của tierRepository.
func NewTierRepository(db *pgxpool.Pool) billingRepoInterface.TierRepository {
	return &tierRepository{db: db}
}

// [COMMENT]: ListTiers thực hiện duy nhất 1 câu query SQL JOIN phân trang trực tiếp trên cấu trúc Flat Entity.
// Tránh lỗi N+1, loại bỏ hoàn toàn JSON parsing và map tạm, đảm bảo hiệu năng tối đa.
func (r *tierRepository) ListTiers(ctx context.Context, page, limit int, serviceType, search string) ([]*entity.Tier, int64, error) {
	offset := (page - 1) * limit
	searchPattern := ""
	if search != "" {
		searchPattern = "%" + search + "%"
	}

	// 1. Tính tổng số dòng cước chi tiết khớp bộ lọc phục vụ phân trang ở Client
	countQuery := `
		SELECT count(*) 
		FROM billing.tier_ranges r
		JOIN billing.tiers t ON r.tier_id = t.id
		WHERE ($1 = '' OR t.service_type = $1)
		  AND ($2 = '' OR t.name ILIKE $3 OR t.code ILIKE $3)
	`
	var total int64
	err := r.db.QueryRow(ctx, countQuery, serviceType, search, searchPattern).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("tier repo: count flat tiers: %w", err)
	}

	if total == 0 {
		return []*entity.Tier{}, 0, nil
	}

	// 2. Query 1 câu SQL JOIN duy nhất để lấy thông tin chi tiết từng dòng cước phẳng
	flatQuery := `
		SELECT 
			r.id, 
			r.tier_id, 
			t.name, 
			t.code, 
			t.service_type, 
			r.range_start, 
			r.range_end, 
			r.base_unit_price, 
			r.created_at, 
			t.updated_at
		FROM billing.tier_ranges r
		JOIN billing.tiers t ON r.tier_id = t.id
		WHERE ($1 = '' OR t.service_type = $1)
		  AND ($2 = '' OR t.name ILIKE $3 OR t.code ILIKE $3)
		ORDER BY t.created_at DESC, r.range_start ASC
		LIMIT $4 OFFSET $5
	`
	rows, err := r.db.Query(ctx, flatQuery, serviceType, search, searchPattern, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("tier repo: query flat tiers: %w", err)
	}
	defer rows.Close()

	tiers := make([]*entity.Tier, 0)
	for rows.Next() {
		var t entity.Tier
		// Scan trực tiếp dữ liệu dạng native từ các cột DB
		err := rows.Scan(
			&t.ID,
			&t.TierID,
			&t.Name,
			&t.Code,
			&t.ServiceType,
			&t.RangeStart,
			&t.RangeEnd,
			&t.BaseUnitPrice,
			&t.CreatedAt,
			&t.UpdatedAt,
		)
		if err != nil {
			return nil, 0, fmt.Errorf("tier repo: scan flat tier: %w", err)
		}
		tiers = append(tiers, &t)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("tier repo: rows flat tiers iteration: %w", err)
	}

	return tiers, total, nil
}
