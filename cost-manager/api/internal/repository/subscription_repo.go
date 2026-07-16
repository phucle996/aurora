package repository

import (
	"context"
	"fmt"

	"cost-manager/api/internal/domain/entity"
	"cost-manager/api/internal/domain/repo"
	"cost-manager/api/pkg/apperr"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type subscriptionRepo struct {
	db *pgxpool.Pool
}

// NewSubscriptionRepository khởi tạo SubscriptionRepository với pgx pool
func NewSubscriptionRepository(db *pgxpool.Pool) repo.SubscriptionRepository {
	return &subscriptionRepo{db: db}
}

// GetActiveSubscription lấy subscription ACTIVE của owner, JOIN với plans + plan_metrics
func (r *subscriptionRepo) GetActiveSubscription(ctx context.Context, ownerID uuid.UUID, ownerType string) (*entity.Subscription, error) {
	const op = "repo.subscription.get_active"

	var s entity.Subscription
	var planID uuid.UUID

	err := r.db.QueryRow(ctx, `
		SELECT s.id, s.owner_id, s.owner_type, s.plan_id, s.status, s.started_at,
		       s.expires_at, s.renewed_at, s.cancelled_at, s.created_at
		FROM billing.subscriptions s
		WHERE s.owner_id = $1 AND s.owner_type = $2 AND s.status = 'ACTIVE'
		LIMIT 1
	`, ownerID, ownerType).Scan(
		&s.ID, &s.OwnerID, &s.OwnerType, &planID, &s.Status,
		&s.StartedAt, &s.ExpiresAt, &s.RenewedAt, &s.CancelledAt, &s.CreatedAt,
	)
	if err != nil {
		// Không có subscription active là trường hợp hợp lệ → trả nil, nil
		return nil, nil //nolint:nilerr
	}

	s.PlanID = planID

	// Populate Plan + Metrics để service có thể check quota
	plan, err := (&planRepo{db: r.db}).GetPlanByID(ctx, planID)
	if err != nil {
		return nil, apperr.Wrap(apperr.ErrInternalServer, fmt.Errorf("%s: fetch plan: %w", op, err), "db_error")
	}
	s.Plan = plan

	return &s, nil
}

// CreateSubscription tạo subscription mới
func (r *subscriptionRepo) CreateSubscription(ctx context.Context, s *entity.Subscription) error {
	const op = "repo.subscription.create"

	_, err := r.db.Exec(ctx, `
		INSERT INTO billing.subscriptions (id, owner_id, owner_type, plan_id, status, started_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, s.ID, s.OwnerID, s.OwnerType, s.PlanID, s.Status, s.StartedAt, s.ExpiresAt)
	if err != nil {
		return apperr.Wrap(apperr.ErrInternalServer, fmt.Errorf("%s: insert: %w", op, err), "db_error")
	}
	return nil
}

// CancelSubscription đánh dấu subscription CANCELLED
func (r *subscriptionRepo) CancelSubscription(ctx context.Context, id uuid.UUID) error {
	const op = "repo.subscription.cancel"

	_, err := r.db.Exec(ctx, `
		UPDATE billing.subscriptions
		SET status = 'CANCELLED', cancelled_at = NOW()
		WHERE id = $1
	`, id)
	if err != nil {
		return apperr.Wrap(apperr.ErrInternalServer, fmt.Errorf("%s: update: %w", op, err), "db_error")
	}
	return nil
}
