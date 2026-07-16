package repo

import (
	"context"

	"cost-manager/api/internal/domain/entity"
	"github.com/google/uuid"
)

// PlanRepository định nghĩa contract truy cập dữ liệu cho Plans và Subscriptions
type PlanRepository interface {
	// ListPlans trả về tất cả gói cước đang ACTIVE
	ListPlans(ctx context.Context) ([]entity.Plan, error)

	// GetPlanByID lấy Plan kèm Metrics theo id
	GetPlanByID(ctx context.Context, id uuid.UUID) (*entity.Plan, error)

	// CreatePlan tạo plan mới kèm plan_metrics (trong 1 transaction)
	CreatePlan(ctx context.Context, p *entity.Plan) error

	// UpdatePlanStatus cập nhật status (ACTIVE/DEPRECATED)
	UpdatePlanStatus(ctx context.Context, id uuid.UUID, status string) error
}
