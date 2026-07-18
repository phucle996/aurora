package repository

import (
	"context"
	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingTaxonomy "cost-manager/api/internal/taxonomy"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
			t.version,
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
			&t.Version,
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

// UpdateTier khóa parent, kiểm tra OCC rồi reconcile ranges trong cùng một transaction PostgreSQL.
func (r *tierRepository) UpdateTier(ctx context.Context, update entity.TierUpdate) (*entity.TierAggregate, error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("tier repo: begin update transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var tierID uuid.UUID
	var currentVersion int
	// code + service_type là composite business lookup; FOR UPDATE serialize mọi mutation của service Tier duy nhất.
	err = tx.QueryRow(ctx, `
		SELECT id, version
		FROM billing.tiers
		WHERE code = $1 AND service_type = $2
		FOR UPDATE
	`, update.Code, update.ServiceType).Scan(&tierID, &currentVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, billingTaxonomy.ErrTierNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("tier repo: lock tier: %w", err)
	}
	if currentVersion != update.Version {
		return nil, billingTaxonomy.ErrTierVersionConflict
	}

	var nextVersion int
	var updatedAt time.Time
	// Chỉ name và version được mutate; code/service_type không xuất hiện trong SET.
	err = tx.QueryRow(ctx, `
		UPDATE billing.tiers
		SET name = $1, version = version + 1, updated_at = NOW()
		WHERE id = $2
		RETURNING version, updated_at
	`, update.Name, tierID).Scan(&nextVersion, &updatedAt)
	if err != nil {
		return nil, fmt.Errorf("tier repo: update parent tier: %w", err)
	}

	// Đọc ownership và created_at trước khi replace để giữ stable ID/audit timestamp cho range hiện hữu.
	existingCreatedAt := make(map[uuid.UUID]time.Time)
	rows, err := tx.Query(ctx, `SELECT id, created_at FROM billing.tier_ranges WHERE tier_id = $1`, tierID)
	if err != nil {
		return nil, fmt.Errorf("tier repo: list existing ranges: %w", err)
	}
	for rows.Next() {
		var id uuid.UUID
		var createdAt time.Time
		if err = rows.Scan(&id, &createdAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("tier repo: scan existing range: %w", err)
		}
		existingCreatedAt[id] = createdAt
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("tier repo: iterate existing ranges: %w", err)
	}
	rows.Close()

	for _, rangeInput := range update.Ranges {
		if rangeInput.ID != uuid.Nil {
			if _, owned := existingCreatedAt[rangeInput.ID]; !owned {
				return nil, billingTaxonomy.ErrInvalidTierRanges
			}
		}
	}

	// Delete rồi insert full-state tránh vi phạm tạm thời constraint one-infinity khi đổi boundary giữa hai rows.
	if _, err = tx.Exec(ctx, `DELETE FROM billing.tier_ranges WHERE tier_id = $1`, tierID); err != nil {
		return nil, fmt.Errorf("tier repo: clear ranges for reconciliation: %w", err)
	}
	for i := range update.Ranges {
		rangeInput := &update.Ranges[i]
		createdAt := time.Now().UTC()
		if rangeInput.ID == uuid.Nil {
			rangeInput.ID = uuid.New()
		} else {
			createdAt = existingCreatedAt[rangeInput.ID]
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO billing.tier_ranges
				(id, tier_id, range_start, range_end, base_unit_price, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, NOW())
		`, rangeInput.ID, tierID, rangeInput.RangeStart, rangeInput.RangeEnd, rangeInput.BaseUnitPrice, createdAt)
		if err != nil {
			return nil, fmt.Errorf("tier repo: insert reconciled range %s: %w", rangeInput.ID, err)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("tier repo: commit update transaction: %w", err)
	}
	return &entity.TierAggregate{
		ID: tierID, Code: update.Code, ServiceType: update.ServiceType,
		Version: nextVersion, Name: update.Name, Ranges: update.Ranges, UpdatedAt: updatedAt,
	}, nil
}
