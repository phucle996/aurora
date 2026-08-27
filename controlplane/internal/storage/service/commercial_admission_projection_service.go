package storageSvcImpl

import (
	"context"
	"strings"

	storageEntity "controlplane/internal/storage/domain/entity"
	storageRepoInterface "controlplane/internal/storage/domain/repo"
	storageSvcInterface "controlplane/internal/storage/domain/service"
	storageTaxonomy "controlplane/internal/storage/taxonomy"
)

type StorageCommercialAdmissionProjectionService struct {
	repo storageRepoInterface.CommercialAdmissionProjectionRepository
}

func NewStorageCommercialAdmissionProjectionService(
	repo storageRepoInterface.CommercialAdmissionProjectionRepository,
) storageSvcInterface.CommercialAdmissionProjectionService {
	return &StorageCommercialAdmissionProjectionService{repo: repo}
}

func (s *StorageCommercialAdmissionProjectionService) Apply(
	ctx context.Context,
	command *storageEntity.CommercialAdmissionProjectionCommand,
) error {
	if command.PolicyVersion <= 0 ||
		(command.OwnerType != "PERSONAL" && command.OwnerType != "TENANT") ||
		(command.Decision != "ALLOW" && command.Decision != "SUSPEND_BILLABLE") ||
		(command.Decision == "ALLOW" && command.RestrictionReason != "") ||
		(command.Decision == "SUSPEND_BILLABLE" && strings.TrimSpace(command.RestrictionReason) == "") {
		return storageTaxonomy.ErrInvalidCommercialAdmissionProjection
	}

	if command.ValidUntil != nil && !command.ValidUntil.After(command.EffectiveAt) {
		return storageTaxonomy.ErrInvalidCommercialAdmissionProjection
	}
	var reason *string
	if command.RestrictionReason != "" {
		reason = &command.RestrictionReason
	}
	return s.repo.Apply(ctx, &storageEntity.CommercialAdmissionProjection{
		EventID: command.EventID, OwnerID: command.OwnerID,
		OwnerType: command.OwnerType, PolicyVersion: command.PolicyVersion,
		Decision: command.Decision, RestrictionReason: reason,
		EffectiveAt: command.EffectiveAt, ValidUntil: command.ValidUntil,
	})
}
