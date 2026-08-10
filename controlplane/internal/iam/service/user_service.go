package iamSvcImpl

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"controlplane/internal/cacheengine"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
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
	metrics     observability.WorkflowRecorder
}

// [COMMENT]: Khởi tạo UserService; dependency availability đã được bảo đảm tại IAM module.
func NewUserService(
	repo iamRepoInterface.UserRepository,
	registry *cacheengine.CacheRegistry,
	authRedis *goredis.Client,
	sharedRedis *goredis.Client,
	metrics observability.WorkflowRecorder,
) iamSvcInterface.UserService {
	return &UserService{
		repo:        repo,
		registry:    registry,
		authRedis:   authRedis,
		sharedRedis: sharedRedis,
		metrics:     metrics,
	}
}

// [COMMENT]: ListUsers chỉ nhận entity của riêng workflow user-directory.
func (s *UserService) ListUsers(
	ctx context.Context,
	query iamEntity.ListUsers,
) ([]iamEntity.ListUsers, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()
	users, err := s.repo.ListUsers(ctx, query)
	if err == nil {
		result, reason = observability.ResultSuccess, observability.ReasonNone
	}
	return users, err
}

// [COMMENT]: UpdateUserStatus chỉ nhận entity của riêng workflow status mutation.
func (s *UserService) UpdateUserStatus(ctx context.Context, workflow iamEntity.UpdateUserStatus) error {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	if err := s.repo.UpdateUserStatus(ctx, workflow); err != nil {
		return err
	}

	s.registry.L1.Delete("user_role:" + workflow.TargetUserID.String())
	tag := "authz:billing:{" + workflow.TargetUserID.String() + "}"
	if err := s.authRedis.Eval(ctx, `
		redis.call("INCR", KEYS[1])
		redis.call("EXPIRE", KEYS[1], ARGV[1])
		redis.call("DEL", KEYS[2], KEYS[3])
		return 1
	`, []string{tag + ":generation", tag + ":data", tag + ":data_generation"}, int64(86400)).Err(); err != nil {
		return fmt.Errorf("invalidate Billing authorization cache after user status update: %w", err)
	}
	if err := s.sharedRedis.Publish(ctx, "authz.invalidate.billing", workflow.TargetUserID.String()).Err(); err != nil {
		return fmt.Errorf("publish Billing authorization invalidation: %w", err)
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return nil
}

// [COMMENT]: GetMyProfile để repository populate cùng entity workflow, không
// chuyển qua UserProfile dùng chung với register/auth.
func (s *UserService) GetMyProfile(ctx context.Context, workflow *iamEntity.GetMyProfile) error {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()
	err := s.repo.GetMyProfile(ctx, workflow)
	if err == nil {
		result, reason = observability.ResultSuccess, observability.ReasonNone
	}
	return err
}

// [COMMENT]: UpdateMyProfile giữ một entity phẳng xuyên suốt ba tầng.
func (s *UserService) UpdateMyProfile(ctx context.Context, workflow *iamEntity.UpdateMyProfile) error {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()
	err := s.repo.UpdateMyProfile(ctx, workflow)
	if err == nil {
		result, reason = observability.ResultSuccess, observability.ReasonNone
	}
	return err
}

// [COMMENT]: GetMySocialLinks trả về các row cùng một entity workflow,
// handler chỉ chịu trách nhiệm shape thành JSON response.
func (s *UserService) GetMySocialLinks(
	ctx context.Context,
	workflow *iamEntity.GetMySocialLinks,
) ([]iamEntity.GetMySocialLinks, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()
	links, err := s.repo.GetMySocialLinks(ctx, workflow)
	if err == nil {
		result, reason = observability.ResultSuccess, observability.ReasonNone
	}
	return links, err
}

// [COMMENT]: LinkExternalIdentity nhận identity đã được Redis boundary xác thực.
func (s *UserService) LinkExternalIdentity(
	ctx context.Context,
	workflow iamEntity.LinkExternalIdentity,
) error {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()
	err := s.repo.LinkExternalIdentity(ctx, workflow)
	if err != nil {
		return err
	}

	if activityErr := useractivity.Append(ctx, s.sharedRedis, useractivity.Event{
		EventID:     uuid.New().String(),
		UserID:      workflow.UserID.String(),
		Category:    "security",
		Action:      "user.social_link.linked",
		ActorType:   "user",
		Outcome:     "succeeded",
		Source:      "controlplane",
		OperationID: workflow.OperationID.String(),
		Title:       "Social account linked",
		Summary:     workflow.Provider + " was linked as an additional sign-in method",
		OccurredAt:  time.Now().UTC(),
		Metadata:    map[string]any{"provider": workflow.Provider},
	}); activityErr != nil {
		logger.SysErrorCtx(ctx, "iam.user_activity.social_link", activityErr.Error())
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return nil
}

// [COMMENT]: UnlinkMySocialLink chỉ dùng entity phẳng của chính workflow unlink.
func (s *UserService) UnlinkMySocialLink(
	ctx context.Context,
	workflow iamEntity.UnlinkMySocialLink,
) error {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	linkSlot := fmt.Sprintf("%x", sha256.Sum256([]byte(workflow.UserID.String())))
	pendingKey := "iam:oauth:link:{" + linkSlot + "}:" + workflow.Provider
	lockKey := pendingKey + ":lock"
	lockToken := uuid.New().String()
	acquired, err := s.authRedis.SetNX(ctx, lockKey, lockToken, 15*time.Second).Result()
	if err != nil || !acquired {
		return iamTaxonomy.ErrAuthenticationUnavailable
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = s.authRedis.Eval(releaseCtx, `
			if redis.call("GET", KEYS[1]) == ARGV[1] then
				return redis.call("DEL", KEYS[1])
			end
			return 0
		`, []string{lockKey}, lockToken).Err()
	}()

	if err := s.authRedis.Del(ctx, pendingKey).Err(); err != nil {
		return fmt.Errorf("%w: invalidate pending social link: %v", iamTaxonomy.ErrAuthenticationUnavailable, err)
	}

	err = s.repo.UnlinkMySocialLink(ctx, workflow)
	if err != nil {
		return err
	}

	if activityErr := useractivity.Append(ctx, s.sharedRedis, useractivity.Event{
		EventID:     uuid.New().String(),
		UserID:      workflow.UserID.String(),
		Category:    "security",
		Action:      "user.social_link.unlinked",
		ActorType:   "user",
		Outcome:     "succeeded",
		Source:      "controlplane",
		OperationID: workflow.OperationID.String(),
		Title:       "Social account unlinked",
		Summary:     workflow.Provider + " was removed as a sign-in method",
		OccurredAt:  time.Now().UTC(),
		Metadata:    map[string]any{"provider": workflow.Provider},
	}); activityErr != nil {
		logger.SysErrorCtx(ctx, "iam.user_activity.social_unlink", activityErr.Error())
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return nil
}

// [COMMENT]: GetUserAuthMethods trả về một row phẳng cho mỗi provider.
func (s *UserService) GetUserAuthMethods(
	ctx context.Context,
	query iamEntity.GetUserAuthMethods,
) ([]iamEntity.GetUserAuthMethods, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()
	methods, err := s.repo.GetUserAuthMethods(ctx, query)
	if err == nil {
		result, reason = observability.ResultSuccess, observability.ReasonNone
	}
	return methods, err
}

// [COMMENT]: Hash password trong service, sau đó xóa plaintext trước khi
// entity được chuyển xuống repository.
func (s *UserService) ResetUserPassword(
	ctx context.Context,
	workflow iamEntity.ResetUserPassword,
) error {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	passwordHash, err := security.HashPassword(workflow.Password)
	if err != nil {
		return fmt.Errorf("user service: failed to hash new password: %w", err)
	}
	workflow.Password = ""
	workflow.PasswordHash = passwordHash

	err = s.repo.ResetUserPassword(ctx, workflow)
	if err != nil {
		return err
	}

	if activityErr := useractivity.Append(ctx, s.sharedRedis, useractivity.Event{
		EventID:     uuid.New().String(),
		UserID:      workflow.TargetUserID.String(),
		Category:    "security",
		Action:      "user.password.reset",
		ActorType:   "admin",
		Outcome:     "succeeded",
		Source:      "controlplane",
		OperationID: workflow.OperationID.String(),
		Title:       "Password changed",
		Summary:     "An administrator changed the account password",
		OccurredAt:  time.Now().UTC(),
		Metadata:    map[string]any{"caller_level": workflow.CallerLevel},
	}); activityErr != nil {
		return fmt.Errorf("iam.user_activity.password_reset: %w", activityErr)
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return nil
}
