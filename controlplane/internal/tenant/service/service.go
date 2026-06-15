package tenantSvcImpl

import (
	"context"
	"strings"

	tenantEntity "controlplane/internal/tenant/domain/entity"
	tenantRepo "controlplane/internal/tenant/domain/repo"
	tenantSvc "controlplane/internal/tenant/domain/service"
	tenantErrorx "controlplane/internal/tenant/errorx"
	"controlplane/pkg/constant"
)

type Service struct {
	repo tenantRepo.Repository
}

func NewService(repo tenantRepo.Repository) tenantSvc.Service {
	return &Service{repo: repo}
}

func (s *Service) CreateTenant(ctx context.Context, input tenantEntity.CreateTenantInput) (*tenantEntity.CreateTenantResult, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Domain = strings.ToLower(strings.TrimSpace(input.Domain))

	// Trích xuất CreatorID trực tiếp từ Go standard context (được Middleware inject)
	var creatorID string
	if ident, ok := ctx.Value(constant.IdentityKey).(*constant.Identity); ok && ident != nil {
		creatorID = ident.UserID
	}
	input.CreatorID = strings.TrimSpace(creatorID)

	if input.Name == "" || input.Domain == "" || input.CreatorID == "" {
		return nil, tenantErrorx.ErrInvalidArgument
	}
	return s.repo.CreateTenantBootstrapTx(ctx, input)
}

func (s *Service) ResolveLoginContextByDomain(ctx context.Context, domain string, userID string) (*tenantEntity.LoginTenantContext, error) {
	domain = strings.ToLower(strings.TrimSpace(domain))
	userID = strings.TrimSpace(userID)
	if domain == "" || userID == "" {
		return nil, tenantErrorx.ErrInvalidArgument
	}
	tenantDomain, err := s.repo.ResolveTenantByDomain(ctx, domain)
	if err != nil {
		return nil, err
	}
	return s.repo.GetMembershipAndRoles(ctx, tenantDomain.TenantID, userID)
}
