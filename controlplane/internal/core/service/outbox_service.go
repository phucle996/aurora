package coreSvcImpl

import (
	"context"
	"fmt"
	"time"

	coreEntity "controlplane/internal/core/domain/entity"
	coreRepoInterface "controlplane/internal/core/domain/repo"
	coreSvcInterface "controlplane/internal/core/domain/service"
	"controlplane/pkg/logger"
	"github.com/google/uuid"
)

type OutboxServiceImpl struct {
	repo      coreRepoInterface.OutboxRepository
	publisher func(ctx context.Context, entity string, op string, payload []byte, version uint64) error
}

// NewOutboxServiceImpl khởi tạo OutboxService với cơ chế Dependency Injection cho publisher.
func NewOutboxServiceImpl(
	repo coreRepoInterface.OutboxRepository,
	publisher func(ctx context.Context, entity string, op string, payload []byte, version uint64) error,
) coreSvcInterface.OutboxService {
	return &OutboxServiceImpl{
		repo:      repo,
		publisher: publisher,
	}
}

func (s *OutboxServiceImpl) PublishEvent(ctx context.Context, entity string, op string, payload []byte, version uint64) (*coreEntity.OutboxRecord, error) {
	record := &coreEntity.OutboxRecord{
		EventID:   uuid.New().String(),
		Entity:    entity,
		Op:        op,
		Payload:   payload,
		Version:   version,
		Status:    coreEntity.OutboxStatusPending,
		CreatedAt: time.Now(),
	}

	err := s.repo.Save(ctx, record)
	if err != nil {
		return nil, fmt.Errorf("outbox_service: failed to save outbox event: %w", err)
	}

	return record, nil
}

func (s *OutboxServiceImpl) ProcessPending(ctx context.Context, limit int) error {
	pending, err := s.repo.FetchPending(ctx, limit)
	if err != nil {
		return fmt.Errorf("outbox_service: failed to fetch pending events: %w", err)
	}

	for _, rec := range pending {
		err := s.publisher(ctx, rec.Entity, rec.Op, rec.Payload, rec.Version)
		if err != nil {
			logger.SysWarnFields("outbox_service", "failed to publish outbox event to bus", err, logger.Fields{
				"event_id": rec.EventID,
				"entity":   rec.Entity,
			})
			_ = s.repo.MarkFailed(ctx, rec.ID, err.Error())
			continue
		}

		err = s.repo.MarkPublished(ctx, rec.ID)
		if err != nil {
			logger.SysErrorFields("outbox_service", "failed to mark outbox event as published", err, logger.Fields{
				"event_id": rec.EventID,
			})
		}
	}

	return nil
}
