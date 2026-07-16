package service

import (
	"context"

	"cost-manager/api/internal/domain/entity"
	"github.com/google/uuid"
)

// PlanService định nghĩa business logic cho quản lý gói cước và subscription
type PlanService interface {
	// --- Plan Management (admin) ---

	// ListPlans trả về tất cả plans đang ACTIVE
	ListPlans(ctx context.Context) ([]entity.Plan, error)

	// GetPlan lấy plan kèm metrics theo id
	GetPlan(ctx context.Context, id uuid.UUID) (*entity.Plan, error)

	// CreatePlan tạo plan mới (admin only)
	CreatePlan(ctx context.Context, p *entity.Plan) error

	// UpdatePlanStatus bật/tắt gói cước (admin only)
	UpdatePlanStatus(ctx context.Context, id uuid.UUID, status string) error

	// --- Subscription Management ---

	// Subscribe đăng ký gói cho owner — trừ phí tháng đầu từ wallet
	Subscribe(ctx context.Context, ownerID uuid.UUID, ownerType string, planID uuid.UUID) (*entity.Subscription, error)

	// CancelSubscription huỷ gói đang active
	CancelSubscription(ctx context.Context, ownerID uuid.UUID, ownerType string) error

	// GetActiveSubscription lấy gói đang dùng của owner (nil nếu không có)
	GetActiveSubscription(ctx context.Context, ownerID uuid.UUID, ownerType string) (*entity.Subscription, error)
}
