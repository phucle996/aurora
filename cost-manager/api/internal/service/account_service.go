package service

import (
	"context"
	"strings"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingTaxonomy "cost-manager/api/internal/taxonomy"

	"github.com/google/uuid"
)

type accountService struct {
	repo billingRepoInterface.AccountRepository
}

// [COMMENT]: NewAccountService tạo service activation với campaign cố định phía server.
func NewAccountService(repo billingRepoInterface.AccountRepository) *accountService {
	return &accountService{repo: repo}
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
	return s.repo.ActivateFreeTier(ctx, entity.FreeTierActivation{
		OwnerID: ownerID, OwnerType: entity.OwnerTypePersonal, IdempotencyKey: idempotencyKey,
	})
}
