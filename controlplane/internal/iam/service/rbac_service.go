package iamSvcImpl

import (
	"context"
	"errors"
	"fmt"
	"strings"

	iamCache "controlplane/internal/iam/cache"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamErrorx "controlplane/internal/iam/errorx"
	iamMetrics "controlplane/internal/iam/metrics"
	"controlplane/pkg/apperr"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type RbacService struct {
	repo     iamRepoInterface.RbacRepository
	registry *RoleRegistry
	bus      iamCache.RbacCacheBus
}

func NewRbacService(repo iamRepoInterface.RbacRepository, registry *RoleRegistry, bus iamCache.RbacCacheBus) *RbacService {
	return &RbacService{repo: repo, registry: registry, bus: bus}
}

func (s *RbacService) Authorize(ctx context.Context, roleCode, permission string) (iamSvcInterface.AuthorizeResult, error) {
	entry, err := s.LoadRole(ctx, roleCode)
	if err != nil {
		iamMetrics.ObserveRbacAuthorize("error")
		return iamSvcInterface.AuthorizeError, err
	}
	perm := strings.ToLower(strings.TrimSpace(permission))
	for _, p := range entry.Permissions {
		if strings.EqualFold(strings.TrimSpace(p), perm) {
			iamMetrics.ObserveRbacAuthorize("allow")
			return iamSvcInterface.AuthorizeAllow, nil
		}
	}
	iamMetrics.ObserveRbacAuthorize("deny")
	iamMetrics.ObserveRbacDeny("permission_missing")
	return iamSvcInterface.AuthorizeDeny, nil
}

func (s *RbacService) LoadRole(ctx context.Context, role string) (iamSvcInterface.RoleEntry, error) {
	roleCode := strings.TrimSpace(strings.ToLower(role))
	if roleCode == "" {
		return iamSvcInterface.RoleEntry{}, apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, nil)
	}
	if s.registry != nil {
		if entry, ok := s.registry.Get(roleCode); ok {
			iamMetrics.ObserveRbacCacheHit("local")
			return entry, nil
		}
	}
	iamMetrics.ObserveRbacCacheMiss("local")
	rp, err := s.repo.GetRoleByCode(ctx, roleCode)
	if err != nil {
		if errors.Is(err, iamErrorx.ErrRoleNotFound) || errors.Is(err, iamErrorx.ErrPermissionNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return iamSvcInterface.RoleEntry{}, apperr.Wrap(iamErrorx.ErrRoleNotFound, iamErrorx.ReasonRbacRoleNotFound, err)
		}
		if errors.Is(err, iamErrorx.ErrInvalidArgument) {
			return iamSvcInterface.RoleEntry{}, apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, err)
		}
		return iamSvcInterface.RoleEntry{}, apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRbacDependencyError, fmt.Errorf("iam rbac service: load role: %w", err))
	}
	entry := iamSvcInterface.RoleEntry{Permissions: rp.Permissions}
	if s.registry != nil {
		s.registry.Set(roleCode, entry)
	}
	return entry, nil
}

func (s *RbacService) InvalidateRole(ctx context.Context, role string) {
	if s.registry != nil {
		s.registry.Invalidate(role)
	}
	if s.bus != nil {
		_ = s.bus.PublishInvalidateRole(ctx, role)
	}
	iamMetrics.ObserveRbacInvalidation("role", "success")
}

func (s *RbacService) InvalidateAll(ctx context.Context) {
	if s.registry != nil {
		s.registry.InvalidateAll()
	}
	if s.bus != nil {
		_ = s.bus.PublishInvalidateAll(ctx)
	}
	iamMetrics.ObserveRbacInvalidation("all", "success")
}

func (s *RbacService) WarmUp(ctx context.Context) error {
	entries, err := s.repo.ListRoleEntries(ctx)
	if err != nil {
		return apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRbacDependencyError, err)
	}
	for _, item := range entries {
		if item == nil || item.Role == nil {
			continue
		}
		s.registry.Set(item.Role.Code, iamSvcInterface.RoleEntry{Permissions: item.Permissions})
	}
	return nil
}

func (s *RbacService) ListRoles(ctx context.Context) ([]*iamEntity.Role, error) {
	roles, err := s.repo.ListRoles(ctx)
	if err != nil {
		return nil, apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRbacDependencyError, err)
	}
	return roles, nil
}
func (s *RbacService) GetRole(ctx context.Context, id string) (*iamEntity.RoleWithPermissions, error) {
	normalizedID := strings.TrimSpace(id)
	parsedID, parseErr := uuid.Parse(normalizedID)
	if normalizedID == "" || parseErr != nil {
		return nil, apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, parseErr)
	}
	normalizedID = parsedID.String()
	role, err := s.repo.GetRoleByID(ctx, normalizedID)
	if err != nil {
		if errors.Is(err, iamErrorx.ErrRoleNotFound) || errors.Is(err, iamErrorx.ErrPermissionNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.Wrap(iamErrorx.ErrRoleNotFound, iamErrorx.ReasonRbacRoleNotFound, err)
		}
		if errors.Is(err, iamErrorx.ErrInvalidArgument) {
			return nil, apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, err)
		}
		return nil, apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRbacDependencyError, err)
	}
	roleWithPerms, getErr := s.repo.GetRoleByCode(ctx, role.Code)
	if getErr != nil {
		if errors.Is(getErr, iamErrorx.ErrRoleNotFound) || errors.Is(getErr, iamErrorx.ErrPermissionNotFound) || errors.Is(getErr, pgx.ErrNoRows) {
			return nil, apperr.Wrap(iamErrorx.ErrRoleNotFound, iamErrorx.ReasonRbacRoleNotFound, getErr)
		}
		if errors.Is(getErr, iamErrorx.ErrInvalidArgument) {
			return nil, apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, getErr)
		}
		return nil, apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRbacDependencyError, getErr)
	}
	return roleWithPerms, nil
}
func (s *RbacService) CreateRole(ctx context.Context, role *iamEntity.Role) error {
	if role == nil {
		return apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, nil)
	}
	if err := s.repo.CreateRole(ctx, role); err != nil {
		if errors.Is(err, iamErrorx.ErrInvalidArgument) {
			return apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, err)
		}
		return apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRbacDependencyError, err)
	}
	s.InvalidateRole(ctx, role.Code)
	return nil
}
func (s *RbacService) UpdateRole(ctx context.Context, role *iamEntity.Role) error {
	if role == nil || role.ID == uuid.Nil {
		return apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, nil)
	}
	if err := s.repo.UpdateRole(ctx, role); err != nil {
		if errors.Is(err, iamErrorx.ErrInvalidArgument) {
			return apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, err)
		}
		if errors.Is(err, iamErrorx.ErrRoleNotFound) || errors.Is(err, iamErrorx.ErrPermissionNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return apperr.Wrap(iamErrorx.ErrRoleNotFound, iamErrorx.ReasonRbacRoleNotFound, err)
		}
		return apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRbacDependencyError, err)
	}
	s.InvalidateRole(ctx, role.Code)
	return nil
}
func (s *RbacService) DeleteRole(ctx context.Context, id string) error {
	normalizedID := strings.TrimSpace(id)
	parsedID, parseErr := uuid.Parse(normalizedID)
	if normalizedID == "" || parseErr != nil {
		return apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, parseErr)
	}
	normalizedID = parsedID.String()
	role, err := s.repo.GetRoleByID(ctx, normalizedID)
	if err != nil {
		if errors.Is(err, iamErrorx.ErrInvalidArgument) {
			return apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, err)
		}
		if errors.Is(err, iamErrorx.ErrRoleNotFound) || errors.Is(err, iamErrorx.ErrPermissionNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return apperr.Wrap(iamErrorx.ErrRoleNotFound, iamErrorx.ReasonRbacRoleNotFound, err)
		}
		return apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRbacDependencyError, err)
	}
	if err := s.repo.DeleteRole(ctx, normalizedID); err != nil {
		if errors.Is(err, iamErrorx.ErrInvalidArgument) {
			return apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, err)
		}
		if errors.Is(err, iamErrorx.ErrRoleNotFound) || errors.Is(err, iamErrorx.ErrPermissionNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return apperr.Wrap(iamErrorx.ErrRoleNotFound, iamErrorx.ReasonRbacRoleNotFound, err)
		}
		return apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRbacDependencyError, err)
	}
	s.InvalidateRole(ctx, role.Code)
	return nil
}
func (s *RbacService) ListPermissions(ctx context.Context) ([]*iamEntity.Permission, error) {
	permissions, err := s.repo.ListPermissions(ctx)
	if err != nil {
		return nil, apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRbacDependencyError, err)
	}
	return permissions, nil
}
func (s *RbacService) CreatePermission(ctx context.Context, perm *iamEntity.Permission) error {
	if perm == nil {
		return apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, nil)
	}
	if err := s.repo.CreatePermission(ctx, perm); err != nil {
		if errors.Is(err, iamErrorx.ErrInvalidArgument) {
			return apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, err)
		}
		return apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRbacDependencyError, err)
	}
	return nil
}
func (s *RbacService) AssignPermission(ctx context.Context, roleID, permID string) error {
	normalizedRoleID := strings.TrimSpace(roleID)
	parsedRoleID, parseRoleErr := uuid.Parse(normalizedRoleID)
	if normalizedRoleID == "" || parseRoleErr != nil {
		return apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, parseRoleErr)
	}
	normalizedRoleID = parsedRoleID.String()
	normalizedPermID := strings.TrimSpace(permID)
	parsedPermID, parsePermErr := uuid.Parse(normalizedPermID)
	if normalizedPermID == "" || parsePermErr != nil {
		return apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, parsePermErr)
	}
	normalizedPermID = parsedPermID.String()
	role, err := s.repo.GetRoleByID(ctx, normalizedRoleID)
	if err != nil {
		if errors.Is(err, iamErrorx.ErrInvalidArgument) {
			return apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, err)
		}
		if errors.Is(err, iamErrorx.ErrRoleNotFound) || errors.Is(err, iamErrorx.ErrPermissionNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return apperr.Wrap(iamErrorx.ErrRoleNotFound, iamErrorx.ReasonRbacRoleNotFound, err)
		}
		return apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRbacDependencyError, err)
	}
	if err := s.repo.AssignPermission(ctx, normalizedRoleID, normalizedPermID); err != nil {
		if errors.Is(err, iamErrorx.ErrInvalidArgument) {
			return apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, err)
		}
		if errors.Is(err, iamErrorx.ErrRoleNotFound) || errors.Is(err, iamErrorx.ErrPermissionNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return apperr.Wrap(iamErrorx.ErrPermissionNotFound, iamErrorx.ReasonRbacPermissionNotFound, err)
		}
		return apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRbacDependencyError, err)
	}
	s.InvalidateRole(ctx, role.Code)
	return nil
}
func (s *RbacService) RevokePermission(ctx context.Context, roleID, permID string) error {
	normalizedRoleID := strings.TrimSpace(roleID)
	parsedRoleID, parseRoleErr := uuid.Parse(normalizedRoleID)
	if normalizedRoleID == "" || parseRoleErr != nil {
		return apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, parseRoleErr)
	}
	normalizedRoleID = parsedRoleID.String()
	normalizedPermID := strings.TrimSpace(permID)
	parsedPermID, parsePermErr := uuid.Parse(normalizedPermID)
	if normalizedPermID == "" || parsePermErr != nil {
		return apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, parsePermErr)
	}
	normalizedPermID = parsedPermID.String()
	role, err := s.repo.GetRoleByID(ctx, normalizedRoleID)
	if err != nil {
		if errors.Is(err, iamErrorx.ErrInvalidArgument) {
			return apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, err)
		}
		if errors.Is(err, iamErrorx.ErrRoleNotFound) || errors.Is(err, iamErrorx.ErrPermissionNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return apperr.Wrap(iamErrorx.ErrRoleNotFound, iamErrorx.ReasonRbacRoleNotFound, err)
		}
		return apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRbacDependencyError, err)
	}
	if err := s.repo.RevokePermission(ctx, normalizedRoleID, normalizedPermID); err != nil {
		if errors.Is(err, iamErrorx.ErrInvalidArgument) {
			return apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, err)
		}
		if errors.Is(err, iamErrorx.ErrRoleNotFound) || errors.Is(err, iamErrorx.ErrPermissionNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return apperr.Wrap(iamErrorx.ErrPermissionNotFound, iamErrorx.ReasonRbacPermissionNotFound, err)
		}
		return apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRbacDependencyError, err)
	}
	s.InvalidateRole(ctx, role.Code)
	return nil
}
func (s *RbacService) AssignUserRole(ctx context.Context, userID, roleID string) error {
	normalizedUserID := strings.TrimSpace(userID)
	parsedUserID, parseUserErr := uuid.Parse(normalizedUserID)
	if normalizedUserID == "" || parseUserErr != nil {
		return apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, parseUserErr)
	}
	normalizedUserID = parsedUserID.String()
	normalizedRoleID := strings.TrimSpace(roleID)
	parsedRoleID, parseRoleErr := uuid.Parse(normalizedRoleID)
	if normalizedRoleID == "" || parseRoleErr != nil {
		return apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, parseRoleErr)
	}
	normalizedRoleID = parsedRoleID.String()
	if err := s.repo.AssignUserRole(ctx, normalizedUserID, normalizedRoleID); err != nil {
		if errors.Is(err, iamErrorx.ErrInvalidArgument) {
			return apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, err)
		}
		return apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRbacDependencyError, err)
	}
	return nil
}
func (s *RbacService) RevokeUserRole(ctx context.Context, userID, roleID string) error {
	normalizedUserID := strings.TrimSpace(userID)
	parsedUserID, parseUserErr := uuid.Parse(normalizedUserID)
	if normalizedUserID == "" || parseUserErr != nil {
		return apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, parseUserErr)
	}
	normalizedUserID = parsedUserID.String()
	normalizedRoleID := strings.TrimSpace(roleID)
	parsedRoleID, parseRoleErr := uuid.Parse(normalizedRoleID)
	if normalizedRoleID == "" || parseRoleErr != nil {
		return apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, parseRoleErr)
	}
	normalizedRoleID = parsedRoleID.String()
	if err := s.repo.RevokeUserRole(ctx, normalizedUserID, normalizedRoleID); err != nil {
		if errors.Is(err, iamErrorx.ErrInvalidArgument) {
			return apperr.Wrap(iamErrorx.ErrInvalidArgument, iamErrorx.ReasonRbacInvalidArgument, err)
		}
		return apperr.Wrap(iamErrorx.ErrAuthenticationUnavailable, iamErrorx.ReasonRbacDependencyError, err)
	}
	return nil
}
