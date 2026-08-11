package iamRepoInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

type AuthRepository interface {
	LoginUserGlobal(ctx context.Context, username string) (*iamEntity.LoginUser, error)
	// [COMMENT]: LoginUserByUsernameAndTenantDomain dùng cho flow login username@tenant_domain.
	// JOIN users → tenant_memberships → tenant_domains để xác minh user là thành viên active của tenant đó.
	// Trả về LoginUser chứa thông tin user kèm TenantID và TenantCode trong struct.
	LoginUserTenant(ctx context.Context, username, tenantDomain string) (*iamEntity.LoginUser, error)
	CreateRegisteredUser(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile) error
	IsUserActive(ctx context.Context, userID uuid.UUID) (bool, error)
	// ActivateUser commits account activation and its personal-workspace bootstrap
	// together. The two workflow commands stay separate while one transaction
	// preserves the no-active-user-without-workspace invariant.
	ActivateUser(ctx context.Context, activation iamEntity.AccountActivation, workspaces iamEntity.BootstrapPersonalWorkspaces) error
	VerifyExternalIdentity(ctx context.Context, req iamEntity.ExternalLoginRequest) (*iamEntity.ExternalIdentity, *iamEntity.LoginUser, error)
}
