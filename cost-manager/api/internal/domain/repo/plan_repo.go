package billingRepoInterface

import (
	"context"
	"time"

	"cost-manager/api/internal/domain/entity"

	"github.com/google/uuid"
)

// PlanRepository định nghĩa các giao tiếp với cơ sở dữ liệu liên quan đến Plan ở mức Domain
type PlanRepository interface {
	// List trả về danh sách các Plan phù hợp với bộ lọc truyền vào, phân trang theo dạng Cursor
	List(ctx context.Context, filter entity.Plan, cursorTime time.Time, cursorID uuid.UUID, limit int) ([]entity.Plan, error)
}
