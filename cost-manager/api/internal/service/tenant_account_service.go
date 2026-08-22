package service

import (
	"context"

	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingSvcInterface "cost-manager/api/internal/domain/service"

	"github.com/google/uuid"
)

// tenantAccountService là Domain Service điều phối các nghiệp vụ tài khoản tổ chức (Tenant Account):
// - Tiếp nhận sự kiện tạo ví tổ chức (`TenantWalletProvisionRequestedV1`) từ Hierarchy/IAM.
// - Khởi tạo ví tiền doanh nghiệp (`billing.wallets` với owner_type = 'TENANT') kèm chế độ kiểm soát hạn mức.
type tenantAccountService struct {
	repo billingRepoInterface.TenantAccountRepository
}

// NewTenantAccountService khởi tạo một instance mới của tenantAccountService, trả về interface TenantAccountService.
func NewTenantAccountService(
	repo billingRepoInterface.TenantAccountRepository,
) billingSvcInterface.TenantAccountService {
	return &tenantAccountService{repo: repo}
}

func (s *tenantAccountService) ProvisionTenantWallet(
	ctx context.Context,
	eventID uuid.UUID,
	tenantID uuid.UUID,
	actorID uuid.UUID,
	payloadHash string,
) error {
	return s.repo.ApplyTenantWalletProvision(ctx, eventID, tenantID, actorID, payloadHash)
}
