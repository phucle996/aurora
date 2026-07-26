package service

import (
	"context"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingTaxonomy "cost-manager/api/internal/taxonomy"
	"cost-manager/api/internal/useractivity"
	"cost-manager/api/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

type accountService struct {
	repo        billingRepoInterface.AccountRepository
	sharedRedis *goredis.Client
}

// [COMMENT]: NewAccountService tạo service activation với campaign cố định phía server.
func NewAccountService(
	repo billingRepoInterface.AccountRepository,
	sharedRedis ...*goredis.Client,
) *accountService {
	var client *goredis.Client
	if len(sharedRedis) > 0 {
		client = sharedRedis[0]
	}
	return &accountService{repo: repo, sharedRedis: client}
}

// [COMMENT]: ActivatePersonalFreeTier không nhận owner từ body, tránh IDOR giữa các wallet.
// [COMMENT]: Nhận ownerID đã được type-check bằng uuid.UUID từ transport/domain contract.
func (s *accountService) ActivatePersonalFreeTier(ctx context.Context, ownerID uuid.UUID, rawIdempotencyKey string) (*entity.FreeTierAccount, error) {

	// [COMMENT]: Gọi repository thực thi transaction kích hoạt free tier và ghi nhận credit
	account, err := s.repo.ActivateFreeTier(ctx, entity.FreeTierActivation{
		OwnerID: ownerID, OwnerType: entity.OwnerTypePersonal, IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return nil, err
	}

	// [COMMENT]: Ghi log audit event bất đồng bộ qua shared Redis stream nếu được cấu hình
	if s.sharedRedis != nil {
		if activityErr := useractivity.Append(ctx, s.sharedRedis, useractivity.Event{
			EventID: uuid.New().String(), UserID: ownerID.String(), Category: "billing",
			Action: "billing.free_tier.activate", ActorType: "self", Outcome: "succeeded",
			Source: "cost-manager", ResourceType: "wallet", ResourceID: account.WalletID.String(),
			OperationID: idempotencyKey, Title: "Free tier activated",
			Summary: "A personal free-tier wallet was activated", OccurredAt: time.Now().UTC(),
			Metadata: map[string]any{"currency": account.Currency},
		}); activityErr != nil {
			// Billing transaction is already committed; history failure must not
			// make the idempotent activation appear unsuccessful to the client.
			logger.SysError("billing.user_activity.free_tier", activityErr.Error())
		}
	}
	return account, nil
}

func (s *accountService) ProvisionPersonalWallet(ctx context.Context, eventID uuid.UUID, ownerID uuid.UUID, payloadHash string) error {
	if eventID == uuid.Nil || ownerID == uuid.Nil || payloadHash == "" {
		return billingTaxonomy.ErrInvalidArgument
	}
	return s.repo.ApplyPersonalWalletProvision(ctx, eventID, ownerID, payloadHash)
}

// GetPersonalWalletSummary giữ owner boundary ở server; UI không thể chọn wallet khác.
func (s *accountService) GetPersonalWalletSummary(ctx context.Context, ownerID uuid.UUID) (*entity.WalletSummary, error) {
	if ownerID == uuid.Nil {
		return nil, billingTaxonomy.ErrInvalidArgument
	}
	return s.repo.GetPersonalWalletSummary(ctx, ownerID)
}
