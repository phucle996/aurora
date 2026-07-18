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
	CreateRegisteredUser(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile, verificationMail *iamEntity.IamOutboxRecord) error
	IsUserActive(ctx context.Context, userID uuid.UUID) (bool, error)
	// [COMMENT]: ActivateUser thực hiện kích hoạt tài khoản (chuyển trạng thái sang active)
	// và gán vai trò tương ứng cho tài khoản trong một transaction nguyên tử để bảo toàn dữ liệu.
	ActivateUser(ctx context.Context, userID uuid.UUID, roleCode string, eventID uuid.UUID, eventPayload []byte) error
}
