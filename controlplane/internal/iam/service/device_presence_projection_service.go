package iamSvcImpl

import (
	"context"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
)

type DevicePresenceProjectionService struct {
	repo iamRepoInterface.DevicePresenceProjectionRepository
}

func NewDevicePresenceProjectionService(
	repo iamRepoInterface.DevicePresenceProjectionRepository,
) iamSvcInterface.DevicePresenceProjectionService {
	return &DevicePresenceProjectionService{repo: repo}
}

func (s *DevicePresenceProjectionService) Apply(
	ctx context.Context,
	updates []iamEntity.DevicePresenceUpdate,
) error {
	return s.repo.Apply(ctx, updates)
}
