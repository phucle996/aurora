package iamSvcImpl

import (
	"context"
	"errors"
	"strings"

	iamCache "controlplane/internal/iam/cache"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamMetrics "controlplane/internal/iam/metrics"
	"controlplane/internal/iam/taxonomy"
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
		return iamSvcInterface.RoleEntry{}, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, nil, "invalid_argument")
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
		if errors.Is(err, iamTaxonomy.ErrRoleNotFound) || errors.Is(err, iamTaxonomy.ErrPermissionNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return iamSvcInterface.RoleEntry{}, apperr.Wrap(iamTaxonomy.ErrRoleNotFound, err, "role_not_found")
		}
		if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
			return iamSvcInterface.RoleEntry{}, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, err, "invalid_argument")
		}
		return iamSvcInterface.RoleEntry{}, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "dependency_error")
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
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "dependency_error")
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
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "dependency_error")
	}
	return roles, nil
}
func (s *RbacService) GetRole(ctx context.Context, id string) (*iamEntity.RoleWithPermissions, error) {
	normalizedID := strings.TrimSpace(id)
	parsedID, parseErr := uuid.Parse(normalizedID)
	if normalizedID == "" || parseErr != nil {
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, parseErr, "invalid_argument")
	}
	normalizedID = parsedID.String()
	role, err := s.repo.GetRoleByID(ctx, normalizedID)
	if err != nil {
		if errors.Is(err, iamTaxonomy.ErrRoleNotFound) || errors.Is(err, iamTaxonomy.ErrPermissionNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.Wrap(iamTaxonomy.ErrRoleNotFound, err, "role_not_found")
		}
		if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
			return nil, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, err, "invalid_argument")
		}
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "dependency_error")
	}
	roleWithPerms, getErr := s.repo.GetRoleByCode(ctx, role.Code)
	if getErr != nil {
		if errors.Is(getErr, iamTaxonomy.ErrRoleNotFound) || errors.Is(getErr, iamTaxonomy.ErrPermissionNotFound) || errors.Is(getErr, pgx.ErrNoRows) {
			return nil, apperr.Wrap(iamTaxonomy.ErrRoleNotFound, getErr, "role_not_found")
		}
		if errors.Is(getErr, iamTaxonomy.ErrInvalidArgument) {
			return nil, apperr.Wrap(iamTaxonomy.ErrInvalidArgument, getErr, "invalid_argument")
		}
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, getErr, "dependency_error")
	}
	return roleWithPerms, nil
}
func (s *RbacService) CreateRole(ctx context.Context, role *iamEntity.Role) error {
	if role == nil {
		return apperr.Wrap(iamTaxonomy.ErrInvalidArgument, nil, "invalid_argument")
	}
	if err := s.repo.CreateRole(ctx, role); err != nil {
		if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
			return apperr.Wrap(iamTaxonomy.ErrInvalidArgument, err, "invalid_argument")
		}
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "dependency_error")
	}
	s.InvalidateRole(ctx, role.Code)
	return nil
}
func (s *RbacService) UpdateRole(ctx context.Context, role *iamEntity.Role) error {
	if role == nil || role.ID == uuid.Nil {
		return apperr.Wrap(iamTaxonomy.ErrInvalidArgument, nil, "invalid_argument")
	}
	if err := s.repo.UpdateRole(ctx, role); err != nil {
		if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
			return apperr.Wrap(iamTaxonomy.ErrInvalidArgument, err, "invalid_argument")
		}
		if errors.Is(err, iamTaxonomy.ErrRoleNotFound) || errors.Is(err, iamTaxonomy.ErrPermissionNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return apperr.Wrap(iamTaxonomy.ErrRoleNotFound, err, "role_not_found")
		}
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "dependency_error")
	}
	s.InvalidateRole(ctx, role.Code)
	return nil
}
func (s *RbacService) DeleteRole(ctx context.Context, id string) error {
	normalizedID := strings.TrimSpace(id)
	parsedID, parseErr := uuid.Parse(normalizedID)
	if normalizedID == "" || parseErr != nil {
		return apperr.Wrap(iamTaxonomy.ErrInvalidArgument, parseErr, "invalid_argument")
	}
	normalizedID = parsedID.String()
	role, err := s.repo.GetRoleByID(ctx, normalizedID)
	if err != nil {
		if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
			return apperr.Wrap(iamTaxonomy.ErrInvalidArgument, err, "invalid_argument")
		}
		if errors.Is(err, iamTaxonomy.ErrRoleNotFound) || errors.Is(err, iamTaxonomy.ErrPermissionNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return apperr.Wrap(iamTaxonomy.ErrRoleNotFound, err, "role_not_found")
		}
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "dependency_error")
	}
	if err := s.repo.DeleteRole(ctx, normalizedID); err != nil {
		if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
			return apperr.Wrap(iamTaxonomy.ErrInvalidArgument, err, "invalid_argument")
		}
		if errors.Is(err, iamTaxonomy.ErrRoleNotFound) || errors.Is(err, iamTaxonomy.ErrPermissionNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return apperr.Wrap(iamTaxonomy.ErrRoleNotFound, err, "role_not_found")
		}
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "dependency_error")
	}
	s.InvalidateRole(ctx, role.Code)
	return nil
}
func (s *RbacService) ListPermissions(ctx context.Context) ([]*iamEntity.Permission, error) {
	permissions, err := s.repo.ListPermissions(ctx)
	if err != nil {
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "dependency_error")
	}
	return permissions, nil
}
func (s *RbacService) CreatePermission(ctx context.Context, perm *iamEntity.Permission) error {
	if perm == nil {
		return apperr.Wrap(iamTaxonomy.ErrInvalidArgument, nil, "invalid_argument")
	}
	if err := s.repo.CreatePermission(ctx, perm); err != nil {
		if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
			return apperr.Wrap(iamTaxonomy.ErrInvalidArgument, err, "invalid_argument")
		}
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "dependency_error")
	}
	return nil
}
func (s *RbacService) AssignPermission(ctx context.Context, roleID, permID string) error {
	normalizedRoleID := strings.TrimSpace(roleID)
	parsedRoleID, parseRoleErr := uuid.Parse(normalizedRoleID)
	if normalizedRoleID == "" || parseRoleErr != nil {
		return apperr.Wrap(iamTaxonomy.ErrInvalidArgument, parseRoleErr, "invalid_argument")
	}
	normalizedRoleID = parsedRoleID.String()
	normalizedPermID := strings.TrimSpace(permID)
	parsedPermID, parsePermErr := uuid.Parse(normalizedPermID)
	if normalizedPermID == "" || parsePermErr != nil {
		return apperr.Wrap(iamTaxonomy.ErrInvalidArgument, parsePermErr, "invalid_argument")
	}
	normalizedPermID = parsedPermID.String()
	role, err := s.repo.GetRoleByID(ctx, normalizedRoleID)
	if err != nil {
		if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
			return apperr.Wrap(iamTaxonomy.ErrInvalidArgument, err, "invalid_argument")
		}
		if errors.Is(err, iamTaxonomy.ErrRoleNotFound) || errors.Is(err, iamTaxonomy.ErrPermissionNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return apperr.Wrap(iamTaxonomy.ErrRoleNotFound, err, "role_not_found")
		}
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "dependency_error")
	}
	if err := s.repo.AssignPermission(ctx, normalizedRoleID, normalizedPermID); err != nil {
		if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
			return apperr.Wrap(iamTaxonomy.ErrInvalidArgument, err, "invalid_argument")
		}
		if errors.Is(err, iamTaxonomy.ErrRoleNotFound) || errors.Is(err, iamTaxonomy.ErrPermissionNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return apperr.Wrap(iamTaxonomy.ErrPermissionNotFound, err, "permission_not_found")
		}
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "dependency_error")
	}
	s.InvalidateRole(ctx, role.Code)
	return nil
}
func (s *RbacService) RevokePermission(ctx context.Context, roleID, permID string) error {
	normalizedRoleID := strings.TrimSpace(roleID)
	parsedRoleID, parseRoleErr := uuid.Parse(normalizedRoleID)
	if normalizedRoleID == "" || parseRoleErr != nil {
		return apperr.Wrap(iamTaxonomy.ErrInvalidArgument, parseRoleErr, "invalid_argument")
	}
	normalizedRoleID = parsedRoleID.String()
	normalizedPermID := strings.TrimSpace(permID)
	parsedPermID, parsePermErr := uuid.Parse(normalizedPermID)
	if normalizedPermID == "" || parsePermErr != nil {
		return apperr.Wrap(iamTaxonomy.ErrInvalidArgument, parsePermErr, "invalid_argument")
	}
	normalizedPermID = parsedPermID.String()
	role, err := s.repo.GetRoleByID(ctx, normalizedRoleID)
	if err != nil {
		if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
			return apperr.Wrap(iamTaxonomy.ErrInvalidArgument, err, "invalid_argument")
		}
		if errors.Is(err, iamTaxonomy.ErrRoleNotFound) || errors.Is(err, iamTaxonomy.ErrPermissionNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return apperr.Wrap(iamTaxonomy.ErrRoleNotFound, err, "role_not_found")
		}
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "dependency_error")
	}
	if err := s.repo.RevokePermission(ctx, normalizedRoleID, normalizedPermID); err != nil {
		if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
			return apperr.Wrap(iamTaxonomy.ErrInvalidArgument, err, "invalid_argument")
		}
		if errors.Is(err, iamTaxonomy.ErrRoleNotFound) || errors.Is(err, iamTaxonomy.ErrPermissionNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return apperr.Wrap(iamTaxonomy.ErrPermissionNotFound, err, "permission_not_found")
		}
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "dependency_error")
	}
	s.InvalidateRole(ctx, role.Code)
	return nil
}
func (s *RbacService) AssignUserRole(ctx context.Context, userID, roleID string) error {
	normalizedUserID := strings.TrimSpace(userID)
	parsedUserID, parseUserErr := uuid.Parse(normalizedUserID)
	if normalizedUserID == "" || parseUserErr != nil {
		return apperr.Wrap(iamTaxonomy.ErrInvalidArgument, parseUserErr, "invalid_argument")
	}
	normalizedUserID = parsedUserID.String()
	normalizedRoleID := strings.TrimSpace(roleID)
	parsedRoleID, parseRoleErr := uuid.Parse(normalizedRoleID)
	if normalizedRoleID == "" || parseRoleErr != nil {
		return apperr.Wrap(iamTaxonomy.ErrInvalidArgument, parseRoleErr, "invalid_argument")
	}
	normalizedRoleID = parsedRoleID.String()
	if err := s.repo.AssignUserRole(ctx, normalizedUserID, normalizedRoleID); err != nil {
		if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
			return apperr.Wrap(iamTaxonomy.ErrInvalidArgument, err, "invalid_argument")
		}
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "dependency_error")
	}
	return nil
}
func (s *RbacService) RevokeUserRole(ctx context.Context, userID, roleID string) error {
	normalizedUserID := strings.TrimSpace(userID)
	parsedUserID, parseUserErr := uuid.Parse(normalizedUserID)
	if normalizedUserID == "" || parseUserErr != nil {
		return apperr.Wrap(iamTaxonomy.ErrInvalidArgument, parseUserErr, "invalid_argument")
	}
	normalizedUserID = parsedUserID.String()
	normalizedRoleID := strings.TrimSpace(roleID)
	parsedRoleID, parseRoleErr := uuid.Parse(normalizedRoleID)
	if normalizedRoleID == "" || parseRoleErr != nil {
		return apperr.Wrap(iamTaxonomy.ErrInvalidArgument, parseRoleErr, "invalid_argument")
	}
	normalizedRoleID = parsedRoleID.String()
	if err := s.repo.RevokeUserRole(ctx, normalizedUserID, normalizedRoleID); err != nil {
		if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
			return apperr.Wrap(iamTaxonomy.ErrInvalidArgument, err, "invalid_argument")
		}
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "dependency_error")
	}
	return nil
}
