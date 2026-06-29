package iamRepoInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"
)

type AuthRepository interface {
	LoginUserByUsername(ctx context.Context, username string) (*iamEntity.LoginUser, error)
	// [COMMENT]: LoginUserByUsernameAndTenantDomain dùng cho flow login username@tenant_domain.
	// JOIN users → tenant_memberships → tenant_domains để xác minh user là thành viên active của tenant đó.
	// Trả về LoginUser chứa thông tin user kèm TenantID và TenantCode trong struct.
	LoginUserByUsernameAndTenantDomain(ctx context.Context, username, tenantDomain string) (*iamEntity.LoginUser, error)
	CreateRegisteredUser(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile) error
}
