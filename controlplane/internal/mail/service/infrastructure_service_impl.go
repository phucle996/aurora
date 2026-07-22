package mailSvcImpl

import (
	"context"

	mailEntity "controlplane/internal/mail/domain/entity"
	mailRepoInterface "controlplane/internal/mail/domain/repo"
	mailSvcInterface "controlplane/internal/mail/domain/service"
	mailTaxonomy "controlplane/internal/mail/taxonomy"

	"github.com/google/uuid"
)

type infrastructureServiceImpl struct {
	repo mailRepoInterface.InfrastructureRepository
}

func NewInfrastructureService(repo mailRepoInterface.InfrastructureRepository) mailSvcInterface.InfrastructureService {
	return &infrastructureServiceImpl{repo: repo}
}

func (s *infrastructureServiceImpl) GetByZoneID(ctx context.Context, zoneID uuid.UUID) (*mailEntity.MailInfrastructure, error) {
	if zoneID == uuid.Nil {
		return nil, mailTaxonomy.ErrInvalidArgument
	}
	return s.repo.GetByZoneID(ctx, zoneID)
}
