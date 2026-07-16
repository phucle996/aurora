package service

import (
	"context"
	"fmt"
	"time"

	"cost-manager/api/internal/domain/entity"
	domainrepo "cost-manager/api/internal/domain/repo"
	domainservice "cost-manager/api/internal/domain/service"
	"cost-manager/api/pkg/apperr"

	"github.com/google/uuid"
)

type planServiceImpl struct {
	planRepo    domainrepo.PlanRepository
	subRepo     domainrepo.SubscriptionRepository
	walletRepo  domainrepo.WalletRepository
}

// NewPlanService khởi tạo PlanService — cần planRepo, subRepo và walletRepo để trừ phí đăng ký
func NewPlanService(
	planRepo domainrepo.PlanRepository,
	subRepo domainrepo.SubscriptionRepository,
	walletRepo domainrepo.WalletRepository,
) domainservice.PlanService {
	return &planServiceImpl{
		planRepo:   planRepo,
		subRepo:    subRepo,
		walletRepo: walletRepo,
	}
}

// ListPlans trả về tất cả gói cước đang ACTIVE
func (s *planServiceImpl) ListPlans(ctx context.Context) ([]entity.Plan, error) {
	return s.planRepo.ListPlans(ctx)
}

// GetPlan lấy plan theo ID
func (s *planServiceImpl) GetPlan(ctx context.Context, id uuid.UUID) (*entity.Plan, error) {
	p, err := s.planRepo.GetPlanByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// CreatePlan tạo gói cước mới (admin only)
func (s *planServiceImpl) CreatePlan(ctx context.Context, p *entity.Plan) error {
	if p.ID == uuid.Nil {
		var err error
		p.ID, err = uuid.NewV7()
		if err != nil {
			return apperr.Wrap(apperr.ErrInternalServer, fmt.Errorf("uuid gen: %w", err), "uuid_failed")
		}
	}
	if p.Status == "" {
		p.Status = "ACTIVE"
	}
	return s.planRepo.CreatePlan(ctx, p)
}

// UpdatePlanStatus bật/tắt gói cước
func (s *planServiceImpl) UpdatePlanStatus(ctx context.Context, id uuid.UUID, status string) error {
	return s.planRepo.UpdatePlanStatus(ctx, id, status)
}

// Subscribe đăng ký gói cho owner.
// Race condition guard: trừ phí từ wallet bằng SELECT FOR UPDATE bên trong walletRepo.Debit.
func (s *planServiceImpl) Subscribe(ctx context.Context, ownerID uuid.UUID, ownerType string, planID uuid.UUID) (*entity.Subscription, error) {
	// 1. Kiểm tra plan tồn tại và active
	plan, err := s.planRepo.GetPlanByID(ctx, planID)
	if err != nil {
		return nil, err
	}
	if plan.Status != "ACTIVE" {
		return nil, apperr.Wrap(apperr.ErrBadRequest, fmt.Errorf("plan %s is not active", planID), "plan_not_active")
	}

	// 2. Lấy/tạo wallet của owner
	wallet, err := s.walletRepo.GetOrCreateWallet(ctx, ownerID, ownerType)
	if err != nil {
		return nil, err
	}

	// 3. Kiểm tra số dư đủ để trả phí tháng đầu
	if wallet.Balance+wallet.OverdraftLimit < plan.MonthlyPrice {
		return nil, apperr.Wrap(apperr.ErrInsufficientFunds,
			fmt.Errorf("balance %.2f insufficient for plan %.2f", wallet.Balance, plan.MonthlyPrice),
			"insufficient_funds")
	}

	// 4. Trừ phí subscription từ wallet (SUBSCRIPTION_FEE)
	if err := s.walletRepo.Debit(ctx, wallet.ID, plan.MonthlyPrice, "SUBSCRIPTION_FEE", "SYSTEM",
		fmt.Sprintf("Subscription fee for plan: %s", plan.Name)); err != nil {
		return nil, err
	}

	// 5. Tạo subscription record
	sub := &entity.Subscription{
		ID:        uuid.New(),
		OwnerID:   ownerID,
		OwnerType: entity.OwnerType(ownerType),
		PlanID:    planID,
		Plan:      plan,
		Status:    entity.SubActive,
		StartedAt: time.Now(),
		// expires_at: nil — không tự hết hạn, cancel thủ công hoặc renew
	}

	if err := s.subRepo.CreateSubscription(ctx, sub); err != nil {
		return nil, err
	}

	return sub, nil
}

// CancelSubscription huỷ subscription đang active của owner
func (s *planServiceImpl) CancelSubscription(ctx context.Context, ownerID uuid.UUID, ownerType string) error {
	sub, err := s.subRepo.GetActiveSubscription(ctx, ownerID, ownerType)
	if err != nil {
		return err
	}
	if sub == nil {
		return apperr.Wrap(apperr.ErrBadRequest, fmt.Errorf("no active subscription for owner %s", ownerID), "no_active_subscription")
	}
	return s.subRepo.CancelSubscription(ctx, sub.ID)
}

// GetActiveSubscription lấy subscription đang active của owner (nil nếu không có)
func (s *planServiceImpl) GetActiveSubscription(ctx context.Context, ownerID uuid.UUID, ownerType string) (*entity.Subscription, error) {
	return s.subRepo.GetActiveSubscription(ctx, ownerID, ownerType)
}
