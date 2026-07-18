package repository

import (
	"context"
	"fmt"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// planRepository triển khai interface billingRepoInterface.PlanRepository sử dụng pgxpool làm driver kết nối Postgres
type planRepository struct {
	dbPool *pgxpool.Pool // Connection pool tới Postgres
}

// NewPlanRepository khởi tạo và trả về instance của billingRepoInterface.PlanRepository
func NewPlanRepository(dbPool *pgxpool.Pool) billingRepoInterface.PlanRepository {
	return &planRepository{
		dbPool: dbPool,
	}
}

// List truy vấn danh sách các Plan từ cơ sở dữ liệu dựa theo các điều kiện lọc trong entity.Plan và phân trang dạng Cursor
func (r *planRepository) List(ctx context.Context, filter entity.Plan, cursorTime time.Time, cursorID uuid.UUID, limit int) ([]entity.Plan, error) {
	// Khởi tạo câu truy vấn SQL cơ bản. Sử dụng COALESCE cho description đề phòng trường hợp null
	query := `
		SELECT id, name, code, service_type, zone_id, monthly_price, currency, status, COALESCE(description, '') AS description, created_at, updated_at
		FROM billing.plans
		WHERE 1=1
	`
	var args []any
	placeholderIdx := 1

	// Thêm điều kiện lọc service_type nếu được truyền vào
	if filter.ServiceType != "" {
		query += fmt.Sprintf(" AND service_type = $%d", placeholderIdx)
		args = append(args, string(filter.ServiceType))
		placeholderIdx++
	}

	// Thêm điều kiện lọc zone_id nếu được truyền vào (chỉ lọc khi zone_id không phải nil UUID)
	if filter.ZoneID != uuid.Nil {
		query += fmt.Sprintf(" AND zone_id = $%d", placeholderIdx)
		args = append(args, filter.ZoneID)
		placeholderIdx++
	}

	// Thêm điều kiện lọc status nếu được truyền vào
	if filter.Status != "" {
		query += fmt.Sprintf(" AND status = $%d", placeholderIdx)
		args = append(args, filter.Status)
		placeholderIdx++
	}

	// Thêm điều kiện lọc phân trang dạng Cursor (created_at DESC, id ASC)
	if !cursorTime.IsZero() {
		query += fmt.Sprintf(" AND (created_at < $%d OR (created_at = $%d AND id > $%d))", placeholderIdx, placeholderIdx+1, placeholderIdx+2)
		args = append(args, cursorTime, cursorTime, cursorID)
		placeholderIdx += 3
	}

	// Sắp xếp theo thứ tự deterministic (created_at giảm dần, id tăng dần) để đảm bảo không bị trôi lệch phân trang
	query += " ORDER BY created_at DESC, id ASC"

	// Thêm giới hạn số lượng bản ghi (Limit)
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", placeholderIdx)
		args = append(args, limit)
		placeholderIdx++
	}

	// Thực hiện truy vấn cơ sở dữ liệu với context được truyền từ lớp trên
	rows, err := r.dbPool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query plans: %w", err)
	}
	defer rows.Close()

	var plans []entity.Plan

	// Lặp qua từng bản ghi kết quả và scan vào entity struct
	for rows.Next() {
		var plan entity.Plan
		var rawServiceType string
		err := rows.Scan(
			&plan.ID,
			&plan.Name,
			&plan.Code,
			&rawServiceType,
			&plan.ZoneID,
			&plan.MonthlyPrice,
			&plan.Currency,
			&plan.Status,
			&plan.Description,
			&plan.CreatedAt,
			&plan.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan plan row: %w", err)
		}
		plan.ServiceType = entity.ServiceType(rawServiceType)
		plans = append(plans, plan)
	}

	// Kiểm tra lỗi phát sinh trong quá trình lặp duyệt rows
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error during rows iteration: %w", err)
	}

	return plans, nil
}
