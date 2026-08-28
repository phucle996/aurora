package iamSvcImpl

import (
	"context"
	"errors"
	"fmt"
	"time"

	"controlplane/internal/cacheengine"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/internal/observability"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

// [COMMENT]: PersonalRbacService thực thi interface quản lý vai trò và phân quyền cấp hệ thống (platform)
type PersonalRbacService struct {
	repo        iamRepoInterface.PersonalRbacRepository
	cacheEngine *cacheengine.CacheRegistry
	authRedis   *goredis.Client
	sharedRedis *goredis.Client
	metrics     observability.WorkflowRecorder
}

// [COMMENT]: NewPersonalRbacService khởi tạo một thể hiện mới của PersonalRbacService
func NewPersonalRbacService(
	repo iamRepoInterface.PersonalRbacRepository,
	cacheEngine *cacheengine.CacheRegistry,
	authRedis *goredis.Client,
	sharedRedis *goredis.Client,
	metrics observability.WorkflowRecorder,
) iamSvcInterface.PersonalRbacService {
	return &PersonalRbacService{
		repo:        repo,
		cacheEngine: cacheEngine,
		authRedis:   authRedis,
		sharedRedis: sharedRedis,
		metrics:     metrics,
	}
}

// [COMMENT]: AssignUserRole gán vai trò, fence Auth Redis và fan-out L1 invalidation qua Shared Redis.
func (s *PersonalRbacService) AssignUserRole(ctx context.Context, callerLevel uint8, userID uuid.UUID, roleID uuid.UUID) (err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, iamTaxonomy.ErrUserNotFound), errors.Is(err, iamTaxonomy.ErrRoleNotFound), errors.Is(err, iamTaxonomy.ErrNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, iamTaxonomy.ErrActionNotAllowed):
			result, reason = observability.ResultRejected, observability.ReasonForbidden
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	// [COMMENT]: 1. Gọi Repo để thực hiện gán và cập nhật DB (với check phân cấp CTE)
	if err := s.repo.AssignUserRole(ctx, callerLevel, userID, roleID); err != nil {
		return err
	}

	// [COMMENT]: 2. Thu hồi cache user_role của target user trên L1 cục bộ
	s.cacheEngine.L1.Delete("user_role:" + userID.String())

	// [COMMENT]: 3. Tăng generation và xóa snapshot trong một Lua call để reader không ghi lại dữ liệu cũ sau race.
	var invalidationErr error
	tag := "authz:billing:{" + userID.String() + "}"
	if err := s.authRedis.Eval(ctx, `
			redis.call("INCR", KEYS[1])
			redis.call("EXPIRE", KEYS[1], ARGV[1])
			redis.call("DEL", KEYS[2], KEYS[3])
			return 1
	`, []string{tag + ":generation", tag + ":data", tag + ":data_generation"}, int64(86400)).Err(); err != nil {
		invalidationErr = fmt.Errorf("invalidate Billing authorization cache after role assignment: %w", err)
	}

	// [COMMENT]: Shared Redis Pub/Sub chỉ fan-out xóa L1; Auth Redis generation ở trên mới là correctness fence.
	if invalidationErr == nil {
		if err := s.sharedRedis.Publish(ctx, "authz.invalidate.billing", userID.String()).Err(); err != nil {
			invalidationErr = fmt.Errorf("publish Billing authorization invalidation: %w", err)
		}
	}

	return invalidationErr
}

// [COMMENT]: ListPlatformRoles trả về danh sách vai trò hệ thống có level thấp hơn caller
func (s *PersonalRbacService) ListPlatformRoles(ctx context.Context, callerLevel uint8) (out []iamEntity.Role, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
			result, reason = observability.ResultRejected, observability.ReasonForbidden
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.ListPlatformRoles(ctx, callerLevel)
}

// [COMMENT]: CreateRole tạo vai trò hệ thống mới và map permissions kèm kiểm tra sở hữu tập con quyền của caller
func (s *PersonalRbacService) CreateRole(ctx context.Context, callerUserID uuid.UUID, role *iamEntity.Role, permissionIDs []uuid.UUID) (err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, iamTaxonomy.ErrUserAlreadyExist):
			result, reason = observability.ResultRejected, observability.ReasonAlreadyExists
		case errors.Is(err, iamTaxonomy.ErrPermissionNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, iamTaxonomy.ErrActionNotAllowed):
			result, reason = observability.ResultRejected, observability.ReasonForbidden
		case errors.Is(err, iamTaxonomy.ErrInvalidArgument):
			result, reason = observability.ResultRejected, observability.ReasonInvalidArgument
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	role.ID = uuid.New()
	return s.repo.CreateRole(ctx, callerUserID, role, permissionIDs)
}

// [COMMENT]: ListPermissions lấy danh sách permissions catalog hệ thống được lọc theo quyền của caller
func (s *PersonalRbacService) ListPermissions(ctx context.Context, callerUserID uuid.UUID) (out []iamEntity.Permission, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
			result, reason = observability.ResultRejected, observability.ReasonForbidden
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.ListPermissions(ctx, callerUserID)
}

// [COMMENT]: GetUserRoleDetails lấy thông tin chi tiết vai trò của user hệ thống
func (s *PersonalRbacService) GetUserRoleDetails(ctx context.Context, userID uuid.UUID, callerLevel int32) (out *iamEntity.Role, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		} else if errors.Is(err, iamTaxonomy.ErrRoleNotFound) || errors.Is(err, iamTaxonomy.ErrUserNotFound) || errors.Is(err, iamTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
			result, reason = observability.ResultRejected, observability.ReasonForbidden
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.GetUserRoleDetails(ctx, userID, callerLevel)
}

// [COMMENT]: GetUserRolePermissions lấy danh sách permissions binary của user theo user id
func (s *PersonalRbacService) GetUserRolePermissions(ctx context.Context, userID uuid.UUID) (out []byte, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		} else if errors.Is(err, iamTaxonomy.ErrRoleNotFound) || errors.Is(err, iamTaxonomy.ErrUserNotFound) || errors.Is(err, iamTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.GetUserRolePermissions(ctx, userID)
}

func (s *PersonalRbacService) ResolvePersonalRoleLevel(ctx context.Context, userID uuid.UUID) (level int32, err error) {
	return s.repo.ResolvePersonalRoleLevel(ctx, userID)
}

// [COMMENT]: DeleteRolePlatform thực hiện xóa vai trò platform thông qua repository
func (s *PersonalRbacService) DeleteRolePlatform(ctx context.Context, callerLevel uint8, roleID uuid.UUID) (err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, iamTaxonomy.ErrRoleNotFound), errors.Is(err, iamTaxonomy.ErrNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, iamTaxonomy.ErrActionNotAllowed):
			result, reason = observability.ResultRejected, observability.ReasonForbidden
		case errors.Is(err, iamTaxonomy.ErrPreconditionFailed):
			result, reason = observability.ResultRejected, observability.ReasonPreconditionFailed
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.DeleteRolePlatform(ctx, callerLevel, roleID)
}

// [COMMENT]: GetRoleDetails lấy chi tiết một vai trò platform cùng danh sách đối tượng permission bậc 3
func (s *PersonalRbacService) GetRoleDetails(ctx context.Context, callerLevel uint8, roleID uuid.UUID) (role *iamEntity.Role, permissions []iamEntity.Permission, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		} else if errors.Is(err, iamTaxonomy.ErrRoleNotFound) || errors.Is(err, iamTaxonomy.ErrNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
			result, reason = observability.ResultRejected, observability.ReasonForbidden
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.GetRoleDetails(ctx, callerLevel, roleID)
}

// [COMMENT]: UpdateRole cập nhật role rồi fence/fan-out authorization cho toàn bộ user bị ảnh hưởng.
func (s *PersonalRbacService) UpdateRole(ctx context.Context, callerUserID uuid.UUID, callerLevel uint8, input *iamEntity.UpdateRoleInput) (err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, iamTaxonomy.ErrRoleNotFound), errors.Is(err, iamTaxonomy.ErrPermissionNotFound), errors.Is(err, iamTaxonomy.ErrNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, iamTaxonomy.ErrActionNotAllowed):
			result, reason = observability.ResultRejected, observability.ReasonForbidden
		case errors.Is(err, iamTaxonomy.ErrInvalidArgument):
			result, reason = observability.ResultRejected, observability.ReasonInvalidArgument
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	affectedUserIDs, err := s.repo.UpdateRole(ctx, callerUserID, callerLevel, input)
	if err != nil {
		return err
	}

	// [COMMENT]: Thu hồi L1 cục bộ, fence Auth Redis rồi fan-out qua Shared Redis cho từng user.
	var invalidationErr error
	for _, uID := range affectedUserIDs {
		s.cacheEngine.L1.Delete("user_role:" + uID.String())
		userInvalidationSucceeded := true
		tag := "authz:billing:{" + uID.String() + "}"
		if err := s.authRedis.Eval(ctx, `
				redis.call("INCR", KEYS[1])
				redis.call("EXPIRE", KEYS[1], ARGV[1])
				redis.call("DEL", KEYS[2], KEYS[3])
				return 1
		`, []string{tag + ":generation", tag + ":data", tag + ":data_generation"}, int64(86400)).Err(); err != nil {
			userInvalidationSucceeded = false
			if invalidationErr == nil {
				invalidationErr = fmt.Errorf("invalidate Billing authorization cache after role update: %w", err)
			}
		}
		if userInvalidationSucceeded {
			if err := s.sharedRedis.Publish(ctx, "authz.invalidate.billing", uID.String()).Err(); err != nil {
				if invalidationErr == nil {
					invalidationErr = fmt.Errorf("publish Billing authorization invalidation: %w", err)
				}
			}
		}
	}

	return invalidationErr
}
