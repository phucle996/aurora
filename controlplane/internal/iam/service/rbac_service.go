package iamSvcImpl

import (
	"context"
	"errors"
	"time"

	"controlplane/internal/cacheengine"
	"controlplane/internal/cacheengine/codec"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamMetrics "controlplane/internal/iam/metrics"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	iamproto "controlplane/internal/iam/transport/rpc/proto"
	"controlplane/pkg/apperr"
	"controlplane/pkg/constant"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type RbacService struct {
	repo        iamRepoInterface.RbacRepository
	cacheEngine *cacheengine.CacheRegistry // Tích hợp CacheRegistry từ cache-engine chứa toàn bộ L1, L2, Fanout, Exec
}

func NewRbacService(
	repo iamRepoInterface.RbacRepository,
	cacheEngine *cacheengine.CacheRegistry,
) iamSvcInterface.RbacService {
	return &RbacService{
		repo:        repo,
		cacheEngine: cacheEngine,
	}
}

// getActorLevel Lấy max privilege level của Actor từ Go Context (level nhỏ nhất đại diện quyền cao nhất)
// Tránh truy xuất database dư thừa trên mỗi request nghiệp vụ.
func getActorLevel(ctx context.Context) (int, error) {
	// Lấy giá trị Level từ Go Standard Context (đã được middleware Access inject sẵn)
	if ident, ok := ctx.Value(constant.IdentityKey).(*constant.Identity); ok && ident != nil {
		return ident.Level, nil
	}
	// Mặc định trả về level thấp nhất và báo lỗi hành động không được phép nếu thiếu context
	return 999999, iamTaxonomy.ErrActionNotAllowed
}

func (s *RbacService) ListRoles(ctx context.Context) (roles []*iamEntity.Role, err error) {
	defer func() {
		outcome := iamMetrics.OutcomeSuccess
		if err != nil {
			if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
				outcome = iamMetrics.OutcomeFailure
			} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
				outcome = iamMetrics.OutcomePreConditionFailed
			} else if errors.Is(err, iamTaxonomy.ErrNotFound) {
				outcome = iamMetrics.OutcomeFailure
			} else if errors.Is(err, iamTaxonomy.ErrPermissionNotFound) {
				outcome = iamMetrics.OutcomeFailure
			} else {
				outcome = iamMetrics.OutcomeFailureUnknown
			}
		}
		iamMetrics.ServiceCall(ctx, outcome)
	}()

	// Lấy actorLevel trực tiếp từ Go Context thay vì query DB
	actorLevel, err := getActorLevel(ctx)
	if err != nil {
		return nil, err
	}

	startRepo := time.Now()
	repoRoles, err := s.repo.ListRoles(ctx)
	if err != nil {
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "ListRoles", iamMetrics.OutcomeFailureUnknown, time.Since(startRepo), err)
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamMetrics.OutcomeFailureUnknown)
	}
	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "ListRoles", iamMetrics.OutcomeSuccess, time.Since(startRepo), nil)

	var out []*iamEntity.Role
	for _, r := range repoRoles {
		if r != nil && r.RoleLevel > actorLevel {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *RbacService) GetRole(ctx context.Context, id uuid.UUID) (res *iamEntity.RoleWithPermissions, err error) {
	defer func() {
		outcome := iamMetrics.OutcomeSuccess
		if err != nil {
			if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
				outcome = iamMetrics.OutcomeFailure
			} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
				outcome = iamMetrics.OutcomePreConditionFailed
			} else if errors.Is(err, iamTaxonomy.ErrNotFound) {
				outcome = iamMetrics.OutcomeFailure
			} else if errors.Is(err, iamTaxonomy.ErrPermissionNotFound) {
				outcome = iamMetrics.OutcomeFailure
			} else {
				outcome = iamMetrics.OutcomeFailureUnknown
			}
		}
		iamMetrics.ServiceCall(ctx, outcome)
	}()

	// Lấy actorLevel trực tiếp từ Go Context thay vì query DB
	actorLevel, err := getActorLevel(ctx)
	if err != nil {
		return nil, err
	}

	startRepo := time.Now()
	role, err := s.repo.GetRoleByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, iamTaxonomy.ErrNotFound) {
			iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "GetRoleByID", "role_not_found", time.Since(startRepo), err)
			return nil, iamTaxonomy.ErrNotFound
		}
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "GetRoleByID", iamMetrics.OutcomeFailureUnknown, time.Since(startRepo), err)
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamMetrics.OutcomeFailureUnknown)
	}
	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "GetRoleByID", iamMetrics.OutcomeSuccess, time.Since(startRepo), nil)

	if role.RoleLevel <= actorLevel {
		return nil, iamTaxonomy.ErrActionNotAllowed
	}

	startRepo2 := time.Now()
	roleWithPerms, err := s.repo.GetRoleByCode(ctx, role.Code)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, iamTaxonomy.ErrNotFound) {
			iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "GetRoleByCode", "role_not_found", time.Since(startRepo2), err)
			return nil, iamTaxonomy.ErrNotFound
		}
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "GetRoleByCode", iamMetrics.OutcomeFailureUnknown, time.Since(startRepo2), err)
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamMetrics.OutcomeFailureUnknown)
	}
	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "GetRoleByCode", iamMetrics.OutcomeSuccess, time.Since(startRepo2), nil)

	return roleWithPerms, nil
}

func (s *RbacService) CreateRole(ctx context.Context, role *iamEntity.Role) (err error) {
	defer func() {
		outcome := iamMetrics.OutcomeSuccess
		if err != nil {
			if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
				outcome = iamMetrics.OutcomeFailure
			} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
				outcome = iamMetrics.OutcomePreConditionFailed
			} else if errors.Is(err, iamTaxonomy.ErrNotFound) {
				outcome = iamMetrics.OutcomeFailure
			} else if errors.Is(err, iamTaxonomy.ErrPermissionNotFound) {
				outcome = iamMetrics.OutcomeFailure
			} else {
				outcome = iamMetrics.OutcomeFailureUnknown
			}
		}
		iamMetrics.ServiceCall(ctx, outcome)
	}()

	// Lấy actorLevel trực tiếp từ Go Context thay vì query DB
	actorLevel, err := getActorLevel(ctx)
	if err != nil {
		return err
	}
	if role.RoleLevel <= actorLevel {
		return iamTaxonomy.ErrActionNotAllowed
	}

	startRepo := time.Now()
	err = s.repo.CreateRole(ctx, role)
	if err != nil {
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "CreateRole", iamMetrics.OutcomeFailureUnknown, time.Since(startRepo), err)
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamMetrics.OutcomeFailureUnknown)
	}
	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "CreateRole", iamMetrics.OutcomeSuccess, time.Since(startRepo), nil)

	return nil
}

func (s *RbacService) UpdateRole(ctx context.Context, role *iamEntity.Role) (err error) {
	defer func() {
		outcome := iamMetrics.OutcomeSuccess
		if err != nil {
			if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
				outcome = iamMetrics.OutcomeFailure
			} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
				outcome = iamMetrics.OutcomePreConditionFailed
			} else if errors.Is(err, iamTaxonomy.ErrNotFound) {
				outcome = iamMetrics.OutcomeFailure
			} else if errors.Is(err, iamTaxonomy.ErrPermissionNotFound) {
				outcome = iamMetrics.OutcomeFailure
			} else {
				outcome = iamMetrics.OutcomeFailureUnknown
			}
		}
		iamMetrics.ServiceCall(ctx, outcome)
	}()

	// Lấy actorLevel trực tiếp từ Go Context thay vì query DB
	actorLevel, err := getActorLevel(ctx)
	if err != nil {
		return err
	}

	startRepo1 := time.Now()
	existing, err := s.repo.GetRoleByID(ctx, role.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, iamTaxonomy.ErrNotFound) {
			iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "GetRoleByID", "role_not_found", time.Since(startRepo1), err)
			return iamTaxonomy.ErrNotFound
		}
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "GetRoleByID", iamMetrics.OutcomeFailureUnknown, time.Since(startRepo1), err)
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamMetrics.OutcomeFailureUnknown)
	}
	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "GetRoleByID", iamMetrics.OutcomeSuccess, time.Since(startRepo1), nil)

	if existing.IsSystem || existing.IsProtected {
		return iamTaxonomy.ErrActionNotAllowed
	}
	if existing.RoleLevel <= actorLevel || role.RoleLevel <= actorLevel {
		return iamTaxonomy.ErrActionNotAllowed
	}

	startRepo2 := time.Now()
	err = s.repo.UpdateRole(ctx, role)
	if err != nil {
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "UpdateRole", iamMetrics.OutcomeFailureUnknown, time.Since(startRepo2), err)
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamMetrics.OutcomeFailureUnknown)
	}
	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "UpdateRole", iamMetrics.OutcomeSuccess, time.Since(startRepo2), nil)

	return nil
}

func (s *RbacService) DeleteRole(ctx context.Context, id uuid.UUID) (err error) {
	defer func() {
		outcome := iamMetrics.OutcomeSuccess
		if err != nil {
			if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
				outcome = iamMetrics.OutcomeFailure
			} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
				outcome = iamMetrics.OutcomePreConditionFailed
			} else if errors.Is(err, iamTaxonomy.ErrNotFound) {
				outcome = iamMetrics.OutcomeFailure
			} else if errors.Is(err, iamTaxonomy.ErrPermissionNotFound) {
				outcome = iamMetrics.OutcomeFailure
			} else {
				outcome = iamMetrics.OutcomeFailureUnknown
			}
		}
		iamMetrics.ServiceCall(ctx, outcome)
	}()

	// Lấy actorLevel trực tiếp từ Go Context thay vì query DB
	actorLevel, err := getActorLevel(ctx)
	if err != nil {
		return err
	}

	startRepo1 := time.Now()
	existing, err := s.repo.GetRoleByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, iamTaxonomy.ErrNotFound) {
			iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "GetRoleByID", "role_not_found", time.Since(startRepo1), err)
			return iamTaxonomy.ErrNotFound
		}
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "GetRoleByID", iamMetrics.OutcomeFailureUnknown, time.Since(startRepo1), err)
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamMetrics.OutcomeFailureUnknown)
	}
	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "GetRoleByID", iamMetrics.OutcomeSuccess, time.Since(startRepo1), nil)

	if existing.IsSystem || existing.IsProtected {
		return iamTaxonomy.ErrActionNotAllowed
	}
	if existing.RoleLevel <= actorLevel {
		return iamTaxonomy.ErrActionNotAllowed
	}

	startRepo2 := time.Now()
	err = s.repo.DeleteRole(ctx, id)
	if err != nil {
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "DeleteRole", iamMetrics.OutcomeFailureUnknown, time.Since(startRepo2), err)
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamMetrics.OutcomeFailureUnknown)
	}
	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "DeleteRole", iamMetrics.OutcomeSuccess, time.Since(startRepo2), nil)

	return nil
}

func (s *RbacService) ListPermissions(ctx context.Context) (perms []*iamEntity.Permission, err error) {
	defer func() {
		outcome := iamMetrics.OutcomeSuccess
		if err != nil {
			if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
				outcome = iamMetrics.OutcomeFailure
			} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
				outcome = iamMetrics.OutcomePreConditionFailed
			} else if errors.Is(err, iamTaxonomy.ErrNotFound) {
				outcome = iamMetrics.OutcomeFailure
			} else if errors.Is(err, iamTaxonomy.ErrPermissionNotFound) {
				outcome = iamMetrics.OutcomeFailure
			} else {
				outcome = iamMetrics.OutcomeFailureUnknown
			}
		}
		iamMetrics.ServiceCall(ctx, outcome)
	}()

	startRepo := time.Now()
	permissions, err := s.repo.ListPermissions(ctx)
	if err != nil {
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "ListPermissions", iamMetrics.OutcomeFailureUnknown, time.Since(startRepo), err)
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamMetrics.OutcomeFailureUnknown)
	}
	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "ListPermissions", iamMetrics.OutcomeSuccess, time.Since(startRepo), nil)

	return permissions, nil
}

func (s *RbacService) AssignPermission(ctx context.Context, roleID, permID uuid.UUID) (err error) {
	defer func() {
		outcome := iamMetrics.OutcomeSuccess
		if err != nil {
			if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
				outcome = iamMetrics.OutcomeFailure
			} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
				outcome = iamMetrics.OutcomePreConditionFailed
			} else if errors.Is(err, iamTaxonomy.ErrNotFound) {
				outcome = iamMetrics.OutcomeFailure
			} else if errors.Is(err, iamTaxonomy.ErrPermissionNotFound) {
				outcome = iamMetrics.OutcomeFailure
			} else {
				outcome = iamMetrics.OutcomeFailureUnknown
			}
		}
		iamMetrics.ServiceCall(ctx, outcome)
	}()

	// Lấy actorLevel trực tiếp từ Go Context thay vì query DB
	actorLevel, err := getActorLevel(ctx)
	if err != nil {
		return err
	}

	startRepo1 := time.Now()
	role, err := s.repo.GetRoleByID(ctx, roleID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, iamTaxonomy.ErrNotFound) {
			iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "GetRoleByID", "role_not_found", time.Since(startRepo1), err)
			return iamTaxonomy.ErrNotFound
		}
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "GetRoleByID", iamMetrics.OutcomeFailureUnknown, time.Since(startRepo1), err)
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamMetrics.OutcomeFailureUnknown)
	}
	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "GetRoleByID", iamMetrics.OutcomeSuccess, time.Since(startRepo1), nil)

	if role.IsSystem || role.IsProtected {
		return iamTaxonomy.ErrActionNotAllowed
	}
	if role.RoleLevel <= actorLevel {
		return iamTaxonomy.ErrActionNotAllowed
	}

	startRepo2 := time.Now()
	err = s.repo.AssignPermission(ctx, roleID, permID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, iamTaxonomy.ErrPermissionNotFound) {
			iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "AssignPermission", "permission_not_found", time.Since(startRepo2), err)
			return iamTaxonomy.ErrPermissionNotFound
		}
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "AssignPermission", iamMetrics.OutcomeFailureUnknown, time.Since(startRepo2), err)
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamMetrics.OutcomeFailureUnknown)
	}
	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "AssignPermission", iamMetrics.OutcomeSuccess, time.Since(startRepo2), nil)

	// ------------------------------------------------------------------------
	// 🔄 ĐỒNG BỘ HÓA CACHE L1 + FANOUT (COPY-ON-WRITE)
	// ------------------------------------------------------------------------
	// Truy vấn danh sách Permission Code mới của role từ DB sau khi gán
	newPerms, pErr := s.repo.GetPermissionCodesByRoleCode(ctx, role.Code)
	if pErr == nil {
		roleEntry := &iamproto.RoleEntry{Permissions: newPerms}
		payloadBytes, jsonErr := codec.MarshalData(roleEntry)
		if jsonErr == nil && s.cacheEngine.Fanout != nil {
			startFanout := time.Now()
			cacheKey := "rbac_role:" + role.Code
			_, fanoutErr := s.cacheEngine.Fanout.Publish(ctx, cacheKey, payloadBytes)
			if fanoutErr != nil {
				iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineFanout, "Publish", iamMetrics.OutcomeFailureUnknown, time.Since(startFanout), fanoutErr)
			} else {
				iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineFanout, "Publish", iamMetrics.OutcomeSuccess, time.Since(startFanout), nil)
			}
		}
	}

	return nil
}

func (s *RbacService) RevokePermission(ctx context.Context, roleID, permID uuid.UUID) (err error) {
	defer func() {
		outcome := iamMetrics.OutcomeSuccess
		if err != nil {
			if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
				outcome = iamMetrics.OutcomeFailure
			} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
				outcome = iamMetrics.OutcomePreConditionFailed
			} else if errors.Is(err, iamTaxonomy.ErrNotFound) {
				outcome = iamMetrics.OutcomeFailure
			} else if errors.Is(err, iamTaxonomy.ErrPermissionNotFound) {
				outcome = iamMetrics.OutcomeFailure
			} else {
				outcome = iamMetrics.OutcomeFailureUnknown
			}
		}
		iamMetrics.ServiceCall(ctx, outcome)
	}()

	// Lấy actorLevel trực tiếp từ Go Context thay vì query DB
	actorLevel, err := getActorLevel(ctx)
	if err != nil {
		return err
	}

	startRepo1 := time.Now()
	role, err := s.repo.GetRoleByID(ctx, roleID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, iamTaxonomy.ErrNotFound) {
			iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "GetRoleByID", "role_not_found", time.Since(startRepo1), err)
			return iamTaxonomy.ErrNotFound
		}
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "GetRoleByID", iamMetrics.OutcomeFailureUnknown, time.Since(startRepo1), err)
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamMetrics.OutcomeFailureUnknown)
	}
	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "GetRoleByID", iamMetrics.OutcomeSuccess, time.Since(startRepo1), nil)

	if role.IsSystem || role.IsProtected {
		return iamTaxonomy.ErrActionNotAllowed
	}
	if role.RoleLevel <= actorLevel {
		return iamTaxonomy.ErrActionNotAllowed
	}

	startRepo2 := time.Now()
	err = s.repo.RevokePermission(ctx, roleID, permID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, iamTaxonomy.ErrPermissionNotFound) {
			iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "RevokePermission", "permission_not_found", time.Since(startRepo2), err)
			return iamTaxonomy.ErrPermissionNotFound
		}
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "RevokePermission", iamMetrics.OutcomeFailureUnknown, time.Since(startRepo2), err)
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamMetrics.OutcomeFailureUnknown)
	}
	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "RevokePermission", iamMetrics.OutcomeSuccess, time.Since(startRepo2), nil)

	// ------------------------------------------------------------------------
	// 🔄 ĐỒNG BỘ HÓA CACHE L1 + FANOUT (COPY-ON-WRITE)
	// ------------------------------------------------------------------------
	// Truy vấn danh sách Permission Code mới của role từ DB sau khi thu hồi
	newPerms, pErr := s.repo.GetPermissionCodesByRoleCode(ctx, role.Code)
	if pErr == nil {
		roleEntry := &iamproto.RoleEntry{Permissions: newPerms}
		payloadBytes, jsonErr := codec.MarshalData(roleEntry)
		if jsonErr == nil && s.cacheEngine.Fanout != nil {
			startFanout := time.Now()
			cacheKey := "rbac_role:" + role.Code
			_, fanoutErr := s.cacheEngine.Fanout.Publish(ctx, cacheKey, payloadBytes)
			if fanoutErr != nil {
				iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineFanout, "Publish", iamMetrics.OutcomeFailureUnknown, time.Since(startFanout), fanoutErr)
			} else {
				iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineFanout, "Publish", iamMetrics.OutcomeSuccess, time.Since(startFanout), nil)
			}
		}
	}

	return nil
}

func (s *RbacService) AssignUserRole(ctx context.Context, userID, roleID uuid.UUID, scopeType iamEntity.RoleScopeType, tenantID, workspaceID *uuid.UUID, expiresAt *time.Time) (err error) {
	defer func() {
		outcome := iamMetrics.OutcomeSuccess
		if err != nil {
			if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
				outcome = iamMetrics.OutcomeFailure
			} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
				outcome = iamMetrics.OutcomePreConditionFailed
			} else if errors.Is(err, iamTaxonomy.ErrNotFound) {
				outcome = iamMetrics.OutcomeFailure
			} else if errors.Is(err, iamTaxonomy.ErrPermissionNotFound) {
				outcome = iamMetrics.OutcomeFailure
			} else {
				outcome = iamMetrics.OutcomeFailureUnknown
			}
		}
		iamMetrics.ServiceCall(ctx, outcome)
	}()

	// Lấy actorLevel trực tiếp từ Go Context thay vì query DB
	actorLevel, err := getActorLevel(ctx)
	if err != nil {
		return err
	}

	startRepo1 := time.Now()
	role, err := s.repo.GetRoleByID(ctx, roleID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, iamTaxonomy.ErrNotFound) {
			iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "GetRoleByID", "role_not_found", time.Since(startRepo1), err)
			return iamTaxonomy.ErrNotFound
		}
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "GetRoleByID", iamMetrics.OutcomeFailureUnknown, time.Since(startRepo1), err)
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamMetrics.OutcomeFailureUnknown)
	}
	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "GetRoleByID", iamMetrics.OutcomeSuccess, time.Since(startRepo1), nil)

	if role.RoleLevel <= actorLevel {
		return iamTaxonomy.ErrActionNotAllowed
	}

	startRepo2 := time.Now()
	err = s.repo.AssignUserRole(ctx, userID, roleID, scopeType, tenantID, workspaceID, expiresAt)
	if err != nil {
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "AssignUserRole", iamMetrics.OutcomeFailureUnknown, time.Since(startRepo2), err)
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamMetrics.OutcomeFailureUnknown)
	}
	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "AssignUserRole", iamMetrics.OutcomeSuccess, time.Since(startRepo2), nil)

	// Invalidate Cache cho User trong môi trường HA (Inline)
	cacheKey := "rbac:user:permissions:" + userID.String()
	startL2 := time.Now()
	if deleteErr := s.cacheEngine.L2.Delete(ctx, cacheKey); deleteErr != nil {
		iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineL2, "Delete", iamMetrics.OutcomeFailureUnknown, time.Since(startL2), deleteErr)
	} else {
		iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineL2, "Delete", iamMetrics.OutcomeSuccess, time.Since(startL2), nil)
	}

	if s.cacheEngine.Fanout != nil {
		startFanout := time.Now()
		if _, pubErr := s.cacheEngine.Fanout.Publish(ctx, cacheKey, nil); pubErr != nil {
			iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineFanout, "Publish", iamMetrics.OutcomeFailureUnknown, time.Since(startFanout), pubErr)
		} else {
			iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineFanout, "Publish", iamMetrics.OutcomeSuccess, time.Since(startFanout), nil)
		}
	}
	return nil
}

func (s *RbacService) RevokeUserRole(ctx context.Context, userID, roleID uuid.UUID) (err error) {
	defer func() {
		outcome := iamMetrics.OutcomeSuccess
		if err != nil {
			if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
				outcome = iamMetrics.OutcomeFailure
			} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
				outcome = iamMetrics.OutcomePreConditionFailed
			} else if errors.Is(err, iamTaxonomy.ErrNotFound) {
				outcome = iamMetrics.OutcomeFailure
			} else if errors.Is(err, iamTaxonomy.ErrPermissionNotFound) {
				outcome = iamMetrics.OutcomeFailure
			} else {
				outcome = iamMetrics.OutcomeFailureUnknown
			}
		}
		iamMetrics.ServiceCall(ctx, outcome)
	}()

	// Lấy actorLevel trực tiếp từ Go Context thay vì query DB
	actorLevel, err := getActorLevel(ctx)
	if err != nil {
		return err
	}

	startRepo1 := time.Now()
	role, err := s.repo.GetRoleByID(ctx, roleID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, iamTaxonomy.ErrNotFound) {
			iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "GetRoleByID", "role_not_found", time.Since(startRepo1), err)
			return iamTaxonomy.ErrNotFound
		}
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "GetRoleByID", iamMetrics.OutcomeFailureUnknown, time.Since(startRepo1), err)
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamMetrics.OutcomeFailureUnknown)
	}
	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "GetRoleByID", iamMetrics.OutcomeSuccess, time.Since(startRepo1), nil)

	if role.RoleLevel <= actorLevel {
		return iamTaxonomy.ErrActionNotAllowed
	}

	startRepo2 := time.Now()
	err = s.repo.RevokeUserRole(ctx, userID, roleID)
	if err != nil {
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "RevokeUserRole", iamMetrics.OutcomeFailureUnknown, time.Since(startRepo2), err)
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamMetrics.OutcomeFailureUnknown)
	}
	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "RevokeUserRole", iamMetrics.OutcomeSuccess, time.Since(startRepo2), nil)

	// Invalidate Cache cho User trong môi trường HA (Inline)
	cacheKey := "rbac:user:permissions:" + userID.String()
	startL2 := time.Now()
	if deleteErr := s.cacheEngine.L2.Delete(ctx, cacheKey); deleteErr != nil {
		iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineL2, "Delete", iamMetrics.OutcomeFailureUnknown, time.Since(startL2), deleteErr)
	} else {
		iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineL2, "Delete", iamMetrics.OutcomeSuccess, time.Since(startL2), nil)
	}

	if s.cacheEngine.Fanout != nil {
		startFanout := time.Now()
		if _, pubErr := s.cacheEngine.Fanout.Publish(ctx, cacheKey, nil); pubErr != nil {
			iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineFanout, "Publish", iamMetrics.OutcomeFailureUnknown, time.Since(startFanout), pubErr)
		} else {
			iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineFanout, "Publish", iamMetrics.OutcomeSuccess, time.Since(startFanout), nil)
		}
	}
	return nil
}
