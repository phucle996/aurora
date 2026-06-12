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
	actorLevelVal := ctx.Value(constant.ContextKeyLevel)
	if actorLevelVal == nil {
		// Mặc định trả về level thấp nhất và báo lỗi hành động không được phép nếu thiếu context
		return 999999, iamTaxonomy.ErrActionNotAllowed
	}
	// Hỗ trợ duy nhất kiểu dữ liệu int
	if lvl, ok := actorLevelVal.(int); ok {
		return lvl, nil
	}
	return 999999, iamTaxonomy.ErrActionNotAllowed
}

func (s *RbacService) ListRoles(ctx context.Context) (roles []*iamEntity.Role, err error) {
	workflow := "rbac_list_roles"
	defer func() {
		outcome := iamTaxonomy.Success
		if err != nil {
			if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
				outcome = iamTaxonomy.InvalidArgument
			} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
				outcome = "action_not_allowed"
			} else if errors.Is(err, iamTaxonomy.ErrRoleNotFound) {
				outcome = "role_not_found"
			} else if errors.Is(err, iamTaxonomy.ErrPermissionNotFound) {
				outcome = "permission_not_found"
			} else {
				outcome = iamTaxonomy.Failure
			}
		}
		iamMetrics.ServiceCall(workflow, outcome)
	}()

	// Lấy actorLevel trực tiếp từ Go Context thay vì query DB
	actorLevel, err := getActorLevel(ctx)
	if err != nil {
		return nil, err
	}

	startRepo := time.Now()
	repoRoles, err := s.repo.ListRoles(ctx)
	if err != nil {
		iamMetrics.Downstream("repo", workflow, "ListRoles", iamTaxonomy.Failure, time.Since(startRepo), err)
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.Failure)
	}
	iamMetrics.Downstream("repo", workflow, "ListRoles", iamTaxonomy.Success, time.Since(startRepo), nil)

	var out []*iamEntity.Role
	for _, r := range repoRoles {
		if r != nil && r.RoleLevel > actorLevel {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *RbacService) GetRole(ctx context.Context, id uuid.UUID) (res *iamEntity.RoleWithPermissions, err error) {
	workflow := "rbac_get_role"
	defer func() {
		outcome := iamTaxonomy.Success
		if err != nil {
			if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
				outcome = iamTaxonomy.InvalidArgument
			} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
				outcome = "action_not_allowed"
			} else if errors.Is(err, iamTaxonomy.ErrRoleNotFound) {
				outcome = "role_not_found"
			} else if errors.Is(err, iamTaxonomy.ErrPermissionNotFound) {
				outcome = "permission_not_found"
			} else {
				outcome = iamTaxonomy.Failure
			}
		}
		iamMetrics.ServiceCall(workflow, outcome)
	}()

	// Lấy actorLevel trực tiếp từ Go Context thay vì query DB
	actorLevel, err := getActorLevel(ctx)
	if err != nil {
		return nil, err
	}

	startRepo := time.Now()
	role, err := s.repo.GetRoleByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, iamTaxonomy.ErrRoleNotFound) {
			iamMetrics.Downstream("repo", workflow, "GetRoleByID", "role_not_found", time.Since(startRepo), err)
			return nil, iamTaxonomy.ErrRoleNotFound
		}
		iamMetrics.Downstream("repo", workflow, "GetRoleByID", iamTaxonomy.Failure, time.Since(startRepo), err)
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.Failure)
	}
	iamMetrics.Downstream("repo", workflow, "GetRoleByID", iamTaxonomy.Success, time.Since(startRepo), nil)

	if role.RoleLevel <= actorLevel {
		return nil, iamTaxonomy.ErrActionNotAllowed
	}

	startRepo2 := time.Now()
	roleWithPerms, err := s.repo.GetRoleByCode(ctx, role.Code)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, iamTaxonomy.ErrRoleNotFound) {
			iamMetrics.Downstream("repo", workflow, "GetRoleByCode", "role_not_found", time.Since(startRepo2), err)
			return nil, iamTaxonomy.ErrRoleNotFound
		}
		iamMetrics.Downstream("repo", workflow, "GetRoleByCode", iamTaxonomy.Failure, time.Since(startRepo2), err)
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.Failure)
	}
	iamMetrics.Downstream("repo", workflow, "GetRoleByCode", iamTaxonomy.Success, time.Since(startRepo2), nil)

	return roleWithPerms, nil
}

func (s *RbacService) CreateRole(ctx context.Context, role *iamEntity.Role) (err error) {
	workflow := "rbac_create_role"
	defer func() {
		outcome := iamTaxonomy.Success
		if err != nil {
			if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
				outcome = iamTaxonomy.InvalidArgument
			} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
				outcome = "action_not_allowed"
			} else if errors.Is(err, iamTaxonomy.ErrRoleNotFound) {
				outcome = "role_not_found"
			} else if errors.Is(err, iamTaxonomy.ErrPermissionNotFound) {
				outcome = "permission_not_found"
			} else {
				outcome = iamTaxonomy.Failure
			}
		}
		iamMetrics.ServiceCall(workflow, outcome)
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
		iamMetrics.Downstream("repo", workflow, "CreateRole", iamTaxonomy.Failure, time.Since(startRepo), err)
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.Failure)
	}
	iamMetrics.Downstream("repo", workflow, "CreateRole", iamTaxonomy.Success, time.Since(startRepo), nil)

	return nil
}

func (s *RbacService) UpdateRole(ctx context.Context, role *iamEntity.Role) (err error) {
	workflow := "rbac_update_role"
	defer func() {
		outcome := iamTaxonomy.Success
		if err != nil {
			if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
				outcome = iamTaxonomy.InvalidArgument
			} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
				outcome = "action_not_allowed"
			} else if errors.Is(err, iamTaxonomy.ErrRoleNotFound) {
				outcome = "role_not_found"
			} else if errors.Is(err, iamTaxonomy.ErrPermissionNotFound) {
				outcome = "permission_not_found"
			} else {
				outcome = iamTaxonomy.Failure
			}
		}
		iamMetrics.ServiceCall(workflow, outcome)
	}()

	// Lấy actorLevel trực tiếp từ Go Context thay vì query DB
	actorLevel, err := getActorLevel(ctx)
	if err != nil {
		return err
	}

	startRepo1 := time.Now()
	existing, err := s.repo.GetRoleByID(ctx, role.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, iamTaxonomy.ErrRoleNotFound) {
			iamMetrics.Downstream("repo", workflow, "GetRoleByID", "role_not_found", time.Since(startRepo1), err)
			return iamTaxonomy.ErrRoleNotFound
		}
		iamMetrics.Downstream("repo", workflow, "GetRoleByID", iamTaxonomy.Failure, time.Since(startRepo1), err)
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.Failure)
	}
	iamMetrics.Downstream("repo", workflow, "GetRoleByID", iamTaxonomy.Success, time.Since(startRepo1), nil)

	if existing.IsSystem || existing.IsProtected {
		return iamTaxonomy.ErrActionNotAllowed
	}
	if existing.RoleLevel <= actorLevel || role.RoleLevel <= actorLevel {
		return iamTaxonomy.ErrActionNotAllowed
	}

	startRepo2 := time.Now()
	err = s.repo.UpdateRole(ctx, role)
	if err != nil {
		iamMetrics.Downstream("repo", workflow, "UpdateRole", iamTaxonomy.Failure, time.Since(startRepo2), err)
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.Failure)
	}
	iamMetrics.Downstream("repo", workflow, "UpdateRole", iamTaxonomy.Success, time.Since(startRepo2), nil)

	return nil
}

func (s *RbacService) DeleteRole(ctx context.Context, id uuid.UUID) (err error) {
	workflow := "rbac_delete_role"
	defer func() {
		outcome := iamTaxonomy.Success
		if err != nil {
			if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
				outcome = iamTaxonomy.InvalidArgument
			} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
				outcome = "action_not_allowed"
			} else if errors.Is(err, iamTaxonomy.ErrRoleNotFound) {
				outcome = "role_not_found"
			} else if errors.Is(err, iamTaxonomy.ErrPermissionNotFound) {
				outcome = "permission_not_found"
			} else {
				outcome = iamTaxonomy.Failure
			}
		}
		iamMetrics.ServiceCall(workflow, outcome)
	}()

	// Lấy actorLevel trực tiếp từ Go Context thay vì query DB
	actorLevel, err := getActorLevel(ctx)
	if err != nil {
		return err
	}

	startRepo1 := time.Now()
	existing, err := s.repo.GetRoleByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, iamTaxonomy.ErrRoleNotFound) {
			iamMetrics.Downstream("repo", workflow, "GetRoleByID", "role_not_found", time.Since(startRepo1), err)
			return iamTaxonomy.ErrRoleNotFound
		}
		iamMetrics.Downstream("repo", workflow, "GetRoleByID", iamTaxonomy.Failure, time.Since(startRepo1), err)
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.Failure)
	}
	iamMetrics.Downstream("repo", workflow, "GetRoleByID", iamTaxonomy.Success, time.Since(startRepo1), nil)

	if existing.IsSystem || existing.IsProtected {
		return iamTaxonomy.ErrActionNotAllowed
	}
	if existing.RoleLevel <= actorLevel {
		return iamTaxonomy.ErrActionNotAllowed
	}

	startRepo2 := time.Now()
	err = s.repo.DeleteRole(ctx, id)
	if err != nil {
		iamMetrics.Downstream("repo", workflow, "DeleteRole", iamTaxonomy.Failure, time.Since(startRepo2), err)
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.Failure)
	}
	iamMetrics.Downstream("repo", workflow, "DeleteRole", iamTaxonomy.Success, time.Since(startRepo2), nil)

	return nil
}

func (s *RbacService) ListPermissions(ctx context.Context) (perms []*iamEntity.Permission, err error) {
	workflow := "rbac_list_permissions"
	defer func() {
		outcome := iamTaxonomy.Success
		if err != nil {
			if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
				outcome = iamTaxonomy.InvalidArgument
			} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
				outcome = "action_not_allowed"
			} else if errors.Is(err, iamTaxonomy.ErrRoleNotFound) {
				outcome = "role_not_found"
			} else if errors.Is(err, iamTaxonomy.ErrPermissionNotFound) {
				outcome = "permission_not_found"
			} else {
				outcome = iamTaxonomy.Failure
			}
		}
		iamMetrics.ServiceCall(workflow, outcome)
	}()

	startRepo := time.Now()
	permissions, err := s.repo.ListPermissions(ctx)
	if err != nil {
		iamMetrics.Downstream("repo", workflow, "ListPermissions", iamTaxonomy.Failure, time.Since(startRepo), err)
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.Failure)
	}
	iamMetrics.Downstream("repo", workflow, "ListPermissions", iamTaxonomy.Success, time.Since(startRepo), nil)

	return permissions, nil
}

func (s *RbacService) AssignPermission(ctx context.Context, roleID, permID uuid.UUID) (err error) {
	workflow := "rbac_assign_permission"
	defer func() {
		outcome := iamTaxonomy.Success
		if err != nil {
			if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
				outcome = iamTaxonomy.InvalidArgument
			} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
				outcome = "action_not_allowed"
			} else if errors.Is(err, iamTaxonomy.ErrRoleNotFound) {
				outcome = "role_not_found"
			} else if errors.Is(err, iamTaxonomy.ErrPermissionNotFound) {
				outcome = "permission_not_found"
			} else {
				outcome = iamTaxonomy.Failure
			}
		}
		iamMetrics.ServiceCall(workflow, outcome)
	}()

	// Lấy actorLevel trực tiếp từ Go Context thay vì query DB
	actorLevel, err := getActorLevel(ctx)
	if err != nil {
		return err
	}

	startRepo1 := time.Now()
	role, err := s.repo.GetRoleByID(ctx, roleID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, iamTaxonomy.ErrRoleNotFound) {
			iamMetrics.Downstream("repo", workflow, "GetRoleByID", "role_not_found", time.Since(startRepo1), err)
			return iamTaxonomy.ErrRoleNotFound
		}
		iamMetrics.Downstream("repo", workflow, "GetRoleByID", iamTaxonomy.Failure, time.Since(startRepo1), err)
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.Failure)
	}
	iamMetrics.Downstream("repo", workflow, "GetRoleByID", iamTaxonomy.Success, time.Since(startRepo1), nil)

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
			iamMetrics.Downstream("repo", workflow, "AssignPermission", "permission_not_found", time.Since(startRepo2), err)
			return iamTaxonomy.ErrPermissionNotFound
		}
		iamMetrics.Downstream("repo", workflow, "AssignPermission", iamTaxonomy.Failure, time.Since(startRepo2), err)
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.Failure)
	}
	iamMetrics.Downstream("repo", workflow, "AssignPermission", iamTaxonomy.Success, time.Since(startRepo2), nil)

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
				iamMetrics.Downstream("fanout", workflow, "Publish", iamTaxonomy.Failure, time.Since(startFanout), fanoutErr)
			} else {
				iamMetrics.Downstream("fanout", workflow, "Publish", iamTaxonomy.Success, time.Since(startFanout), nil)
			}
		}
	}

	return nil
}

func (s *RbacService) RevokePermission(ctx context.Context, roleID, permID uuid.UUID) (err error) {
	workflow := "rbac_revoke_permission"
	defer func() {
		outcome := iamTaxonomy.Success
		if err != nil {
			if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
				outcome = iamTaxonomy.InvalidArgument
			} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
				outcome = "action_not_allowed"
			} else if errors.Is(err, iamTaxonomy.ErrRoleNotFound) {
				outcome = "role_not_found"
			} else if errors.Is(err, iamTaxonomy.ErrPermissionNotFound) {
				outcome = "permission_not_found"
			} else {
				outcome = iamTaxonomy.Failure
			}
		}
		iamMetrics.ServiceCall(workflow, outcome)
	}()

	// Lấy actorLevel trực tiếp từ Go Context thay vì query DB
	actorLevel, err := getActorLevel(ctx)
	if err != nil {
		return err
	}

	startRepo1 := time.Now()
	role, err := s.repo.GetRoleByID(ctx, roleID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, iamTaxonomy.ErrRoleNotFound) {
			iamMetrics.Downstream("repo", workflow, "GetRoleByID", "role_not_found", time.Since(startRepo1), err)
			return iamTaxonomy.ErrRoleNotFound
		}
		iamMetrics.Downstream("repo", workflow, "GetRoleByID", iamTaxonomy.Failure, time.Since(startRepo1), err)
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.Failure)
	}
	iamMetrics.Downstream("repo", workflow, "GetRoleByID", iamTaxonomy.Success, time.Since(startRepo1), nil)

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
			iamMetrics.Downstream("repo", workflow, "RevokePermission", "permission_not_found", time.Since(startRepo2), err)
			return iamTaxonomy.ErrPermissionNotFound
		}
		iamMetrics.Downstream("repo", workflow, "RevokePermission", iamTaxonomy.Failure, time.Since(startRepo2), err)
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.Failure)
	}
	iamMetrics.Downstream("repo", workflow, "RevokePermission", iamTaxonomy.Success, time.Since(startRepo2), nil)

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
				iamMetrics.Downstream("fanout", workflow, "Publish", iamTaxonomy.Failure, time.Since(startFanout), fanoutErr)
			} else {
				iamMetrics.Downstream("fanout", workflow, "Publish", iamTaxonomy.Success, time.Since(startFanout), nil)
			}
		}
	}

	return nil
}

func (s *RbacService) AssignUserRole(ctx context.Context, userID, roleID uuid.UUID, scopeType iamEntity.RoleScopeType, tenantID, workspaceID *uuid.UUID, expiresAt *time.Time) (err error) {
	workflow := "rbac_assign_user_role"
	defer func() {
		outcome := iamTaxonomy.Success
		if err != nil {
			if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
				outcome = iamTaxonomy.InvalidArgument
			} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
				outcome = "action_not_allowed"
			} else if errors.Is(err, iamTaxonomy.ErrRoleNotFound) {
				outcome = "role_not_found"
			} else if errors.Is(err, iamTaxonomy.ErrPermissionNotFound) {
				outcome = "permission_not_found"
			} else {
				outcome = iamTaxonomy.Failure
			}
		}
		iamMetrics.ServiceCall(workflow, outcome)
	}()

	// Lấy actorLevel trực tiếp từ Go Context thay vì query DB
	actorLevel, err := getActorLevel(ctx)
	if err != nil {
		return err
	}

	startRepo1 := time.Now()
	role, err := s.repo.GetRoleByID(ctx, roleID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, iamTaxonomy.ErrRoleNotFound) {
			iamMetrics.Downstream("repo", workflow, "GetRoleByID", "role_not_found", time.Since(startRepo1), err)
			return iamTaxonomy.ErrRoleNotFound
		}
		iamMetrics.Downstream("repo", workflow, "GetRoleByID", iamTaxonomy.Failure, time.Since(startRepo1), err)
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.Failure)
	}
	iamMetrics.Downstream("repo", workflow, "GetRoleByID", iamTaxonomy.Success, time.Since(startRepo1), nil)

	if role.RoleLevel <= actorLevel {
		return iamTaxonomy.ErrActionNotAllowed
	}

	startRepo2 := time.Now()
	err = s.repo.AssignUserRole(ctx, userID, roleID, scopeType, tenantID, workspaceID, expiresAt)
	if err != nil {
		iamMetrics.Downstream("repo", workflow, "AssignUserRole", iamTaxonomy.Failure, time.Since(startRepo2), err)
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.Failure)
	}
	iamMetrics.Downstream("repo", workflow, "AssignUserRole", iamTaxonomy.Success, time.Since(startRepo2), nil)

	// Invalidate Cache cho User trong môi trường HA (Inline)
	cacheKey := "rbac:user:permissions:" + userID.String()
	startL2 := time.Now()
	if deleteErr := s.cacheEngine.L2.Delete(ctx, cacheKey); deleteErr != nil {
		iamMetrics.Downstream("cache-engine-l2", workflow, "Delete", iamTaxonomy.Failure, time.Since(startL2), deleteErr)
	} else {
		iamMetrics.Downstream("cache-engine-l2", workflow, "Delete", iamTaxonomy.Success, time.Since(startL2), nil)
	}

	if s.cacheEngine.Fanout != nil {
		startFanout := time.Now()
		if _, pubErr := s.cacheEngine.Fanout.Publish(ctx, cacheKey, nil); pubErr != nil {
			iamMetrics.Downstream("cache-engine-fanout", workflow, "Publish", iamTaxonomy.Failure, time.Since(startFanout), pubErr)
		} else {
			iamMetrics.Downstream("cache-engine-fanout", workflow, "Publish", iamTaxonomy.Success, time.Since(startFanout), nil)
		}
	}
	return nil
}

func (s *RbacService) RevokeUserRole(ctx context.Context, userID, roleID uuid.UUID) (err error) {
	workflow := "rbac_revoke_user_role"
	defer func() {
		outcome := iamTaxonomy.Success
		if err != nil {
			if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
				outcome = iamTaxonomy.InvalidArgument
			} else if errors.Is(err, iamTaxonomy.ErrActionNotAllowed) {
				outcome = "action_not_allowed"
			} else if errors.Is(err, iamTaxonomy.ErrRoleNotFound) {
				outcome = "role_not_found"
			} else if errors.Is(err, iamTaxonomy.ErrPermissionNotFound) {
				outcome = "permission_not_found"
			} else {
				outcome = iamTaxonomy.Failure
			}
		}
		iamMetrics.ServiceCall(workflow, outcome)
	}()

	// Lấy actorLevel trực tiếp từ Go Context thay vì query DB
	actorLevel, err := getActorLevel(ctx)
	if err != nil {
		return err
	}

	startRepo1 := time.Now()
	role, err := s.repo.GetRoleByID(ctx, roleID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, iamTaxonomy.ErrRoleNotFound) {
			iamMetrics.Downstream("repo", workflow, "GetRoleByID", "role_not_found", time.Since(startRepo1), err)
			return iamTaxonomy.ErrRoleNotFound
		}
		iamMetrics.Downstream("repo", workflow, "GetRoleByID", iamTaxonomy.Failure, time.Since(startRepo1), err)
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.Failure)
	}
	iamMetrics.Downstream("repo", workflow, "GetRoleByID", iamTaxonomy.Success, time.Since(startRepo1), nil)

	if role.RoleLevel <= actorLevel {
		return iamTaxonomy.ErrActionNotAllowed
	}

	startRepo2 := time.Now()
	err = s.repo.RevokeUserRole(ctx, userID, roleID)
	if err != nil {
		iamMetrics.Downstream("repo", workflow, "RevokeUserRole", iamTaxonomy.Failure, time.Since(startRepo2), err)
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.Failure)
	}
	iamMetrics.Downstream("repo", workflow, "RevokeUserRole", iamTaxonomy.Success, time.Since(startRepo2), nil)

	// Invalidate Cache cho User trong môi trường HA (Inline)
	cacheKey := "rbac:user:permissions:" + userID.String()
	startL2 := time.Now()
	if deleteErr := s.cacheEngine.L2.Delete(ctx, cacheKey); deleteErr != nil {
		iamMetrics.Downstream("cache-engine-l2", workflow, "Delete", iamTaxonomy.Failure, time.Since(startL2), deleteErr)
	} else {
		iamMetrics.Downstream("cache-engine-l2", workflow, "Delete", iamTaxonomy.Success, time.Since(startL2), nil)
	}

	if s.cacheEngine.Fanout != nil {
		startFanout := time.Now()
		if _, pubErr := s.cacheEngine.Fanout.Publish(ctx, cacheKey, nil); pubErr != nil {
			iamMetrics.Downstream("cache-engine-fanout", workflow, "Publish", iamTaxonomy.Failure, time.Since(startFanout), pubErr)
		} else {
			iamMetrics.Downstream("cache-engine-fanout", workflow, "Publish", iamTaxonomy.Success, time.Since(startFanout), nil)
		}
	}
	return nil
}
