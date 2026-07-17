package billingSvcInterface

import (
	"context"
	"time"

	"cost-manager/api/internal/domain/entity"

	"github.com/google/uuid"
)

// PlanService định nghĩa các nghiệp vụ xử lý logic cốt lõi liên quan tới Plan ở mức Domain
type PlanService interface {
	// ListPlans trả về danh sách các Plan theo điều kiện lọc và phân trang dạng Cursor
	ListPlans(ctx context.Context, filter entity.Plan, cursorTime time.Time, cursorID uuid.UUID, limit int) ([]entity.Plan, string, error)
}
