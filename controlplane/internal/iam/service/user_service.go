package iamSvcImpl

import (
	"context"
	"time"

	"controlplane/internal/cacheengine"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	"controlplane/internal/observability"

	"github.com/google/uuid"
)

type UserService struct {
	repo     iamRepoInterface.UserRepository
	registry *cacheengine.CacheRegistry
}

func NewUserService(
	repo iamRepoInterface.UserRepository,
	registry *cacheengine.CacheRegistry,
) iamSvcInterface.UserService {
	return &UserService{
		repo:     repo,
		registry: registry,
	}
}

// [COMMENT]: ListUsers lấy danh sách users thô từ repository có role_level lớn hơn caller level (quyền lực nhỏ hơn)
func (s *UserService) ListUsers(ctx context.Context, callerLevel uint8, limit int, offset int) ([]*iamEntity.User, error) {
	start := time.Now()
	users, err := s.repo.ListUsers(ctx, callerLevel, limit, offset)
	observability.CurrentMetrics().ObserveDependency("db", "iam.users.list", time.Since(start), err)
	return users, err
}

// [COMMENT]: UpdateUserStatus thực hiện vô hiệu hóa (disable) trạng thái của user, dọn dẹp cache L1 cục bộ và truyền tin fanout invalidate cache
func (s *UserService) UpdateUserStatus(ctx context.Context, callerLevel uint8, targetUserID uuid.UUID) error {
	// [COMMENT]: 1. Gọi Repository để cập nhật status user sang 'disabled' dưới DB
	if err := s.repo.UpdateUserStatus(ctx, targetUserID, "disabled"); err != nil {
		return err
	}

	// [COMMENT]: 2. Thu hồi cache user_role của target user trên L1 cục bộ
	s.registry.L1.Delete("user_role:" + targetUserID.String())

	// [COMMENT]: 3. Phát tán lệnh invalidation cache (Delete) qua Redis Pub/Sub Fanout đến các instances khác trong cụm HA
	_, err := s.registry.Fanout.Publish(ctx, "user_role:"+targetUserID.String(), nil)
	if err != nil {
		return err
	}

	return nil
}

// [COMMENT]: GetUserProfile trả về thông tin profile hiển thị của user (fullname, avatar, v.v.)
func (s *UserService) GetUserProfile(ctx context.Context, userID uuid.UUID) (*iamEntity.UserProfile, error) {
	return s.repo.GetUserProfile(ctx, userID)
}
