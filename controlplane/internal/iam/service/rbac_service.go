package iamSvcImpl

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"controlplane/internal/cacheengine"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamMetrics "controlplane/internal/iam/metrics"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/pkg/apperr"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type RbacService struct {
	repo       iamRepoInterface.RbacRepository
	l1registry *cacheengine.CacheRegistry // Tích hợp CacheRegistry từ cache-engine chứa toàn bộ L1, L2, Fanout, Exec
}

func NewRbacService(
	repo iamRepoInterface.RbacRepository,
	l1registry *cacheengine.CacheRegistry,
) iamSvcInterface.RbacService {
	return &RbacService{
		repo:       repo,
		l1registry: l1registry,
	}
}

func (s *RbacService) Authorize(ctx context.Context, roleCode, permission string) (iamSvcInterface.AuthorizeResult, error) {
	entry, err := s.LoadRole(ctx, roleCode)
	if err != nil {
		iamMetrics.ServiceCall("rbac_authorize", "error", "n/a")
		return iamSvcInterface.AuthorizeError, err
	}
	perm := strings.ToLower(strings.TrimSpace(permission))
	for _, p := range entry.Permissions {
		if strings.EqualFold(strings.TrimSpace(p), perm) {
			iamMetrics.ServiceCall("rbac_authorize", "allow", "n/a")
			return iamSvcInterface.AuthorizeAllow, nil
		}
	}
	iamMetrics.ServiceCall("rbac_authorize", "deny", "n/a")
	iamMetrics.ServiceCall("rbac_authorize", "permission_missing", "n/a")
	return iamSvcInterface.AuthorizeDeny, nil
}

func (s *RbacService) LoadRole(ctx context.Context, role string) (iamSvcInterface.RoleEntry, error) {
	roleCode := strings.TrimSpace(strings.ToLower(role))
	if roleCode == "" {
		return iamSvcInterface.RoleEntry{}, iamTaxonomy.ErrInvalidArgument
	}
	if s.l1registry == nil {
		rp, err := s.repo.GetRoleByCode(ctx, roleCode)
		if err != nil {
			if errors.Is(err, iamTaxonomy.ErrRoleNotFound) || errors.Is(err, iamTaxonomy.ErrPermissionNotFound) || errors.Is(err, pgx.ErrNoRows) {
				return iamSvcInterface.RoleEntry{}, iamTaxonomy.ErrRoleNotFound
			}
			return iamSvcInterface.RoleEntry{}, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.Failure)
		}
		return iamSvcInterface.RoleEntry{Permissions: rp.Permissions}, nil
	}

	val, err := s.l1registry.GetOrLoad(ctx, "rbac_role", roleCode)
	if err != nil {
		if errors.Is(err, iamTaxonomy.ErrRoleNotFound) || errors.Is(err, iamTaxonomy.ErrPermissionNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return iamSvcInterface.RoleEntry{}, iamTaxonomy.ErrRoleNotFound
		}
		if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
			return iamSvcInterface.RoleEntry{}, iamTaxonomy.ErrInvalidArgument
		}
		return iamSvcInterface.RoleEntry{}, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.Failure)
	}

	if entry, ok := val.(iamSvcInterface.RoleEntry); ok {
		return entry, nil
	}
	if pEntry, ok := val.(*iamSvcInterface.RoleEntry); ok && pEntry != nil {
		return *pEntry, nil
	}
	return iamSvcInterface.RoleEntry{}, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, nil, iamTaxonomy.Failure)
}

func (s *RbacService) WarmUp(ctx context.Context) error {
	entries, err := s.repo.ListRoleEntries(ctx)
	if err != nil {
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.Failure)
	}
	for _, item := range entries {
		if item == nil || item.Role == nil {
			continue
		}
		if s.l1registry != nil {
			cacheKey := fmt.Sprintf("rbac_role:%s", strings.TrimSpace(strings.ToLower(item.Role.Code)))
			envelope := &cacheengine.L1Envelope{
				Key:     cacheKey,
				Version: time.Now().UnixNano(),
				Value:   iamSvcInterface.RoleEntry{Permissions: item.Permissions},
			}
			s.l1registry.L1.Set(cacheKey, envelope, 15*time.Minute)
		}
	}
	return nil
}

func (s *RbacService) ListRoles(ctx context.Context) ([]*iamEntity.Role, error) {
	roles, err := s.repo.ListRoles(ctx)
	if err != nil {
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.Failure)
	}
	return roles, nil
}

func (s *RbacService) GetRole(ctx context.Context, id string) (*iamEntity.RoleWithPermissions, error) {
	normalizedID := strings.TrimSpace(id)
	parsedID, parseErr := uuid.Parse(normalizedID)
	if normalizedID == "" || parseErr != nil {
		return nil, iamTaxonomy.ErrInvalidArgument
	}
	normalizedID = parsedID.String()
	role, err := s.repo.GetRoleByID(ctx, normalizedID)
	if err != nil {
		if errors.Is(err, iamTaxonomy.ErrRoleNotFound) || errors.Is(err, iamTaxonomy.ErrPermissionNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return nil, iamTaxonomy.ErrRoleNotFound
		}
		if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
			return nil, iamTaxonomy.ErrInvalidArgument
		}
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.Failure)
	}
	roleWithPerms, getErr := s.repo.GetRoleByCode(ctx, role.Code)
	if getErr != nil {
		if errors.Is(getErr, iamTaxonomy.ErrRoleNotFound) || errors.Is(getErr, iamTaxonomy.ErrPermissionNotFound) || errors.Is(getErr, pgx.ErrNoRows) {
			return nil, iamTaxonomy.ErrRoleNotFound
		}
		if errors.Is(getErr, iamTaxonomy.ErrInvalidArgument) {
			return nil, iamTaxonomy.ErrInvalidArgument
		}
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, getErr, iamTaxonomy.Failure)
	}
	return roleWithPerms, nil
}

func (s *RbacService) CreateRole(ctx context.Context, role *iamEntity.Role) error {
	if role == nil {
		return iamTaxonomy.ErrInvalidArgument
	}
	if err := s.repo.CreateRole(ctx, role); err != nil {
		if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
			return iamTaxonomy.ErrInvalidArgument
		}
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.Failure)
	}
	return nil
}

func (s *RbacService) UpdateRole(ctx context.Context, role *iamEntity.Role) error {
	if err := s.repo.UpdateRole(ctx, role); err != nil {
		if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
			return iamTaxonomy.ErrInvalidArgument
		}
		if errors.Is(err, iamTaxonomy.ErrRoleNotFound) || errors.Is(err, iamTaxonomy.ErrPermissionNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return iamTaxonomy.ErrRoleNotFound
		}
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.Failure)
	}
	return nil
}

func (s *RbacService) DeleteRole(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.DeleteRole(ctx, id); err != nil {
		if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
			return iamTaxonomy.ErrInvalidArgument
		}
		if errors.Is(err, iamTaxonomy.ErrRoleNotFound) || errors.Is(err, iamTaxonomy.ErrPermissionNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return iamTaxonomy.ErrRoleNotFound
		}
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.Failure)
	}
	return nil
}

func (s *RbacService) ListPermissions(ctx context.Context) ([]*iamEntity.Permission, error) {
	permissions, err := s.repo.ListPermissions(ctx)
	if err != nil {
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.Failure)
	}
	return permissions, nil
}

func (s *RbacService) AssignPermission(ctx context.Context, roleID, permID string) error {
	normalizedRoleID := strings.TrimSpace(roleID)
	parsedRoleID, parseRoleErr := uuid.Parse(normalizedRoleID)
	if normalizedRoleID == "" || parseRoleErr != nil {
		return iamTaxonomy.ErrInvalidArgument
	}
	normalizedPermID := strings.TrimSpace(permID)
	parsedPermID, parsePermErr := uuid.Parse(normalizedPermID)
	if normalizedPermID == "" || parsePermErr != nil {
		return iamTaxonomy.ErrInvalidArgument
	}
	if err := s.repo.AssignPermission(ctx, parsedRoleID, parsedPermID); err != nil {
		if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
			return iamTaxonomy.ErrInvalidArgument
		}
		if errors.Is(err, iamTaxonomy.ErrRoleNotFound) || errors.Is(err, iamTaxonomy.ErrPermissionNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return iamTaxonomy.ErrPermissionNotFound
		}
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.Failure)
	}
	return nil
}

func (s *RbacService) RevokePermission(ctx context.Context, roleID, permID string) error {
	normalizedRoleID := strings.TrimSpace(roleID)
	parsedRoleID, parseRoleErr := uuid.Parse(normalizedRoleID)
	if normalizedRoleID == "" || parseRoleErr != nil {
		return iamTaxonomy.ErrInvalidArgument
	}
	normalizedPermID := strings.TrimSpace(permID)
	parsedPermID, parsePermErr := uuid.Parse(normalizedPermID)
	if normalizedPermID == "" || parsePermErr != nil {
		return iamTaxonomy.ErrInvalidArgument
	}
	if err := s.repo.RevokePermission(ctx, parsedRoleID, parsedPermID); err != nil {
		if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
			return iamTaxonomy.ErrInvalidArgument
		}
		if errors.Is(err, iamTaxonomy.ErrRoleNotFound) || errors.Is(err, iamTaxonomy.ErrPermissionNotFound) || errors.Is(err, pgx.ErrNoRows) {
			return iamTaxonomy.ErrPermissionNotFound
		}
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.Failure)
	}
	return nil
}

func (s *RbacService) AssignUserRole(ctx context.Context, userID, roleID string) error {
	normalizedUserID := strings.TrimSpace(userID)
	parsedUserID, parseUserErr := uuid.Parse(normalizedUserID)
	if normalizedUserID == "" || parseUserErr != nil {
		return iamTaxonomy.ErrInvalidArgument
	}
	normalizedUserID = parsedUserID.String()
	normalizedRoleID := strings.TrimSpace(roleID)
	parsedRoleID, parseRoleErr := uuid.Parse(normalizedRoleID)
	if normalizedRoleID == "" || parseRoleErr != nil {
		return iamTaxonomy.ErrInvalidArgument
	}
	normalizedRoleID = parsedRoleID.String()
	if err := s.repo.AssignUserRole(ctx, normalizedUserID, normalizedRoleID); err != nil {
		if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
			return iamTaxonomy.ErrInvalidArgument
		}
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.Failure)
	}
	return nil
}

func (s *RbacService) RevokeUserRole(ctx context.Context, userID, roleID string) error {
	normalizedUserID := strings.TrimSpace(userID)
	parsedUserID, parseUserErr := uuid.Parse(normalizedUserID)
	if normalizedUserID == "" || parseUserErr != nil {
		return iamTaxonomy.ErrInvalidArgument
	}
	normalizedUserID = parsedUserID.String()
	normalizedRoleID := strings.TrimSpace(roleID)
	parsedRoleID, parseRoleErr := uuid.Parse(normalizedRoleID)
	if normalizedRoleID == "" || parseRoleErr != nil {
		return iamTaxonomy.ErrInvalidArgument
	}
	normalizedRoleID = parsedRoleID.String()
	if err := s.repo.RevokeUserRole(ctx, normalizedUserID, normalizedRoleID); err != nil {
		if errors.Is(err, iamTaxonomy.ErrInvalidArgument) {
			return iamTaxonomy.ErrInvalidArgument
		}
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamTaxonomy.Failure)
	}
	return nil
}
