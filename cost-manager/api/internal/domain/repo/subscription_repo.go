package repo

import (
	"context"

	"cost-manager/api/internal/domain/entity"
	"github.com/google/uuid"
)

// SubscriptionRepository định nghĩa contract truy cập dữ liệu cho Subscriptions
type SubscriptionRepository interface {
	// GetActiveSubscription lấy subscription đang ACTIVE của owner (nếu có)
	GetActiveSubscription(ctx context.Context, ownerID uuid.UUID, ownerType string) (*entity.Subscription, error)

	// CreateSubscription tạo subscription mới
	CreateSubscription(ctx context.Context, s *entity.Subscription) error

	// CancelSubscription đánh dấu subscription là CANCELLED
	CancelSubscription(ctx context.Context, id uuid.UUID) error
}
