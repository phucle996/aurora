package iamSvcImpl

import (
	"context"
	"fmt"
	"time"

	"controlplane/internal/cacheengine"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	"controlplane/internal/observability"
	"controlplane/internal/security"
	"controlplane/internal/useractivity"
	"controlplane/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

type UserService struct {
	repo        iamRepoInterface.UserRepository
	registry    *cacheengine.CacheRegistry
	authRedis   *goredis.Client
	sharedRedis *goredis.Client
}

func NewUserService(
	repo iamRepoInterface.UserRepository,
	registry *cacheengine.CacheRegistry,
	authRedis *goredis.Client,
	sharedRedis *goredis.Client,
) iamSvcInterface.UserService {
	return &UserService{
		repo:        repo,
		registry:    registry,
		authRedis:   authRedis,
		sharedRedis: sharedRedis,
	}
}

// [COMMENT]: ListUsers lấy danh sách users thô từ repository có role_level lớn hơn caller level (quyền lực nhỏ hơn)
func (s *UserService) ListUsers(ctx context.Context, callerLevel uint8, limit int, offset int) ([]*iamEntity.User, error) {
	start := time.Now()
	users, err := s.repo.ListUsers(ctx, callerLevel, limit, offset)
	observability.CurrentMetrics().ObserveDependency("db", "iam.users.list", time.Since(start), err)
	return users, err
}

// [COMMENT]: UpdateUserStatus cập nhật user, fence Auth Redis và fan-out L1 invalidation qua Shared Redis.
func (s *UserService) UpdateUserStatus(ctx context.Context, callerLevel uint8, targetUserID uuid.UUID, status string) error {
	// [COMMENT]: 1. Gọi Repository để cập nhật status user dưới DB với cơ chế phân cấp
	if err := s.repo.UpdateUserStatus(ctx, callerLevel, targetUserID, status); err != nil {
		return err
	}

	// [COMMENT]: 2. Thu hồi cache user_role của target user trên L1 cục bộ
	s.registry.L1.Delete("user_role:" + targetUserID.String())

	// [COMMENT]: 3. User disable/enable cũng fence Billing L2 để trạng thái cũ không được tái cache sau race.
	var invalidationErr error
	if s.authRedis != nil {
		tag := "authz:billing:{" + targetUserID.String() + "}"
		if err := s.authRedis.Eval(ctx, `
			redis.call("INCR", KEYS[1])
			redis.call("EXPIRE", KEYS[1], ARGV[1])
			redis.call("DEL", KEYS[2], KEYS[3])
			return 1
		`, []string{tag + ":generation", tag + ":data", tag + ":data_generation"}, int64(86400)).Err(); err != nil {
			invalidationErr = fmt.Errorf("invalidate Billing authorization cache after user status update: %w", err)
		}
	}

	// [COMMENT]: Shared Redis fans out L1 invalidation after the Auth Redis generation fence succeeds.
	if invalidationErr == nil && s.sharedRedis != nil {
		if err := s.sharedRedis.Publish(ctx, "authz.invalidate.billing", targetUserID.String()).Err(); err != nil {
			invalidationErr = fmt.Errorf("publish Billing authorization invalidation: %w", err)
		}
	}

	return invalidationErr
}

// [COMMENT]: GetUserProfile trả về thông tin profile hiển thị của user (fullname, avatar, v.v.)
func (s *UserService) GetUserProfile(ctx context.Context, userID uuid.UUID) (*iamEntity.UserProfile, error) {
	return s.repo.GetUserProfile(ctx, userID)
}

// [COMMENT]: ResetUserPassword thực hiện thay đổi mật khẩu của user bởi Admin, hash mật khẩu bằng Argon2id và lưu trữ vào database
func (s *UserService) ResetUserPassword(ctx context.Context, callerLevel uint8, targetUserID uuid.UUID, newPassword string) error {
	// [COMMENT]: 1. Hash password mới bằng Argon2id sử dụng module security nội bộ
	passwordHash, err := security.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("user service: failed to hash new password: %w", err)
	}

	// [COMMENT]: 2. Gọi repository để cập nhật mật khẩu dưới DB bằng 1 CTE an toàn
	start := time.Now()
	err = s.repo.ResetUserPassword(ctx, callerLevel, targetUserID, passwordHash)
	observability.CurrentMetrics().ObserveDependency("db", "iam.users.reset_password", time.Since(start), err)
	if err != nil {
		return err
	}
	if s.sharedRedis != nil {
		if activityErr := useractivity.Append(ctx, s.sharedRedis, useractivity.Event{
			EventID:     uuid.New().String(),
			UserID:      targetUserID.String(),
			Category:    "security",
			Action:      "user.password.reset",
			ActorType:   "admin",
			Outcome:     "succeeded",
			Source:      "controlplane",
			OperationID: uuid.New().String(),
			Title:       "Password changed",
			Summary:     "An administrator changed the account password",
			OccurredAt:  time.Now().UTC(),
			Metadata:    map[string]any{"caller_level": callerLevel},
		}); activityErr != nil {
			// The IAM transaction has already committed; history enqueue is
			// best-effort and must not make a successful password change retry.
			logger.SysError("iam.user_activity.password_reset", activityErr.Error())
		}
	}

	return nil
}
