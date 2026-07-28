package iamSvcInterface

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"

	"github.com/google/uuid"
)

type AuthService interface {
	RegisterAccount(ctx context.Context, user iamEntity.User, profile iamEntity.UserProfile, password string) error

	// [COMMENT]: VerifyAccount thực hiện kiểm tra token kích hoạt tài khoản
	// và tiến hành active trạng thái của user kèm theo gán role mặc định.
	VerifyAccount(ctx context.Context, userID, eventID uuid.UUID, token string) error

	// Xác thực thông tin đăng nhập và thông tin thiết bị thô của người dùng qua gRPC
	VerifyUserCredentials(ctx context.Context, req iamEntity.LoginRequest) (*iamEntity.VerifyUserCredentialsResult, error)
	VerifyExternalIdentity(ctx context.Context, req iamEntity.ExternalLoginRequest) (*iamEntity.ExternalLoginResult, error)
}

// BillingOutboxNotifier chỉ là tín hiệu đánh thức best-effort sau commit;
// durability vẫn thuộc PostgreSQL outbox và reconciliation fallback của relay.
type BillingOutboxNotifier interface {
	Notify()
}

// AccountVerificationPublisher là outbound port của IAM cho mail xác minh.
// Adapter hạ tầng tự chịu trách nhiệm chọn transport và encode wire contract.
type AccountVerificationPublisher interface {
	PublishAccountVerification(ctx context.Context, dispatch iamEntity.AccountVerificationDispatch) error
}
