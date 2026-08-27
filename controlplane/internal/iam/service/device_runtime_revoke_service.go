package iamSvcImpl

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/pkg/apperr"

	"github.com/google/uuid"
)

// DeviceRuntimeRevokeService keeps the business invariant separate from the
// HTTP device listing workflow. The repository CTE atomically persists the
// resource mutation and its runtime delivery intent.
type DeviceRuntimeRevokeService struct {
	repo iamRepoInterface.DeviceRuntimeRevokeRepository
}

func NewDeviceRuntimeRevokeService(
	repo iamRepoInterface.DeviceRuntimeRevokeRepository,
) iamSvcInterface.DeviceRuntimeRevokeService {
	return &DeviceRuntimeRevokeService{repo: repo}
}

func (s *DeviceRuntimeRevokeService) RevokeDevice(
	ctx context.Context,
	userID uuid.UUID,
	clientDeviceID uuid.UUID,
	currentDeviceID uuid.UUID,
) error {
	result, err := s.repo.RevokeDevice(ctx, iamEntity.DeviceRuntimeRevokeDevice{
		EventID:         uuid.New(),
		UserID:          userID,
		ClientDeviceID:  clientDeviceID,
		CurrentDeviceID: currentDeviceID,
	})
	if err != nil {
		return apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "dependency_error")
	}
	if result.CurrentDevice {
		return apperr.Wrap(iamTaxonomy.ErrActionNotAllowed, nil, "action_not_allowed")
	}
	if !result.TargetExists {
		return apperr.Wrap(iamTaxonomy.ErrInvalidSession, nil, "invalid_session")
	}
	return nil
}

func (s *DeviceRuntimeRevokeService) RevokeOtherDevices(
	ctx context.Context,
	userID uuid.UUID,
	currentDeviceID uuid.UUID,
) (int64, error) {
	affected, err := s.repo.RevokeOtherDevices(ctx, iamEntity.DeviceRuntimeRevokeOthers{
		EventID:         uuid.New(),
		UserID:          userID,
		CurrentDeviceID: currentDeviceID,
	})
	if err != nil {
		return 0, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "dependency_error")
	}
	return affected, nil
}

func (s *DeviceRuntimeRevokeService) Claim(ctx context.Context, limit int) ([]iamEntity.DeviceRuntimeRevokeOutboxEvent, error) {
	events, err := s.repo.Claim(ctx, limit)
	if err != nil {
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, "dependency_error")
	}
	return events, nil
}

func (s *DeviceRuntimeRevokeService) MarkPublished(ctx context.Context, id int64) error {
	return s.repo.MarkPublished(ctx, id)
}

func (s *DeviceRuntimeRevokeService) MarkFailed(ctx context.Context, id int64, message string) error {
	return s.repo.MarkFailed(ctx, id, message)
}

func (s *DeviceRuntimeRevokeService) MarkDead(ctx context.Context, id int64, message string) error {
	return s.repo.MarkDead(ctx, id, message)
}
