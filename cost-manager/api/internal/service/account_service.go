package service

import (
	"context"
	"strings"
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
func (s *accountService) ActivatePersonalFreeTier(ctx context.Context, rawOwnerID string, rawIdempotencyKey string) (*entity.FreeTierAccount, error) {
	ownerID, err := uuid.Parse(strings.TrimSpace(rawOwnerID))
	if err != nil || ownerID == uuid.Nil {
		return nil, billingTaxonomy.ErrInvalidArgument
	}
	idempotencyKey := strings.TrimSpace(rawIdempotencyKey)
	if idempotencyKey == "" || len(idempotencyKey) > 128 {
		return nil, billingTaxonomy.ErrInvalidArgument
	}
	account, err := s.repo.ActivateFreeTier(ctx, entity.FreeTierActivation{
		OwnerID: ownerID, OwnerType: entity.OwnerTypePersonal, IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return nil, err
	}
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
