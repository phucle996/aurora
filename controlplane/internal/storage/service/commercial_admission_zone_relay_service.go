package storageSvcImpl

import (
	"context"
	"fmt"

	storageRepoInterface "controlplane/internal/storage/domain/repo"
	storageSvcInterface "controlplane/internal/storage/domain/service"

	"github.com/google/uuid"
)

type StorageCommercialAdmissionZoneRelayService struct {
	repo      storageRepoInterface.CommercialAdmissionZoneRelayRepository
	publisher storageSvcInterface.CommercialAdmissionZonePublisher
}

func NewStorageCommercialAdmissionZoneRelayService(
	repo storageRepoInterface.CommercialAdmissionZoneRelayRepository,
	publisher storageSvcInterface.CommercialAdmissionZonePublisher,
) storageSvcInterface.CommercialAdmissionZoneRelayService {
	return &StorageCommercialAdmissionZoneRelayService{repo: repo, publisher: publisher}
}

func (s *StorageCommercialAdmissionZoneRelayService) RelayBatch(ctx context.Context) (int, error) {
	deliveries, err := s.repo.Claim(ctx, uuid.New(), 100)
	if err != nil {
		return 0, err
	}
	published := 0
	var batchErr error
	for _, delivery := range deliveries {
		if err := s.publisher.Publish(ctx, delivery); err != nil {
			message := err.Error()
			if len(message) > 1_024 {
				message = message[:1_024]
			}
			if releaseErr := s.repo.Release(ctx, delivery, message); releaseErr != nil {
				if batchErr == nil {
					batchErr = fmt.Errorf("release Storage commercial admission Zone delivery: %w", releaseErr)
				}
			}
			continue
		}
		if err := s.repo.MarkPublished(ctx, delivery); err != nil {
			if batchErr == nil {
				batchErr = fmt.Errorf("mark Storage commercial admission Zone delivery published: %w", err)
			}
			continue
		}
		published++
	}
	return published, batchErr
}
