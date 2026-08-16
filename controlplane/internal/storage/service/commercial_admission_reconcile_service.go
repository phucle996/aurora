package storageSvcImpl

import (
	"context"

	storageRepoInterface "controlplane/internal/storage/domain/repo"
	storageSvcInterface "controlplane/internal/storage/domain/service"
)

type StorageCommercialAdmissionReconcileService struct {
	repo storageRepoInterface.CommercialAdmissionReconcileRepository
}

func NewStorageCommercialAdmissionReconcileService(repo storageRepoInterface.CommercialAdmissionReconcileRepository) storageSvcInterface.CommercialAdmissionReconcileService {
	return &StorageCommercialAdmissionReconcileService{repo: repo}
}

func (s *StorageCommercialAdmissionReconcileService) ReconcileBatch(ctx context.Context) (int, error) {
	return s.repo.ReconcileBatch(ctx, 100)
}
