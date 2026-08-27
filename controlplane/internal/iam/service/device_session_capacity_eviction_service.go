package iamSvcImpl

import (
	"context"

	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"

	"github.com/google/uuid"
)

type DeviceSessionCapacityEvictionService struct {
	repo iamRepoInterface.DeviceSessionCapacityEvictionRepository
}

func NewDeviceSessionCapacityEvictionService(
	repo iamRepoInterface.DeviceSessionCapacityEvictionRepository,
) iamSvcInterface.DeviceSessionCapacityEvictionService {
	return &DeviceSessionCapacityEvictionService{repo: repo}
}

func (s *DeviceSessionCapacityEvictionService) Evict(
	ctx context.Context,
	userID uuid.UUID,
	clientDeviceIDs []uuid.UUID,
) error {
	return s.repo.Evict(ctx, userID, clientDeviceIDs)
}
