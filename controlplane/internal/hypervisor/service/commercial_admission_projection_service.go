package hypervisorSvcImpl

import (
	"context"
	"strings"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
	hypervisorRepoInterface "controlplane/internal/hypervisor/domain/repo"
	hypervisorSvcInterface "controlplane/internal/hypervisor/domain/service"
	hypervisorTaxonomy "controlplane/internal/hypervisor/taxonomy"
)

type HypervisorCommercialAdmissionProjectionService struct {
	repo hypervisorRepoInterface.CommercialAdmissionProjectionRepository
}

func NewHypervisorCommercialAdmissionProjectionService(
	repo hypervisorRepoInterface.CommercialAdmissionProjectionRepository,
) hypervisorSvcInterface.CommercialAdmissionProjectionService {
	return &HypervisorCommercialAdmissionProjectionService{repo: repo}
}

func (s *HypervisorCommercialAdmissionProjectionService) Apply(
	ctx context.Context,
	command *hypervisorEntity.CommercialAdmissionProjectionCommand,
) error {
	if command.PolicyVersion <= 0 ||
		(command.OwnerType != "PERSONAL" && command.OwnerType != "TENANT") ||
		(command.Decision != "ALLOW" && command.Decision != "SUSPEND_BILLABLE") ||
		(command.Decision == "ALLOW" && command.RestrictionReason != "") ||
		(command.Decision == "SUSPEND_BILLABLE" && strings.TrimSpace(command.RestrictionReason) == "") {
		return hypervisorTaxonomy.ErrInvalidCommercialAdmissionProjection
	}
	if command.ValidUntil != nil && !command.ValidUntil.After(command.EffectiveAt) {
		return hypervisorTaxonomy.ErrInvalidCommercialAdmissionProjection
	}
	var reason *string
	if command.RestrictionReason != "" {
		reason = &command.RestrictionReason
	}
	return s.repo.Upsert(ctx, &hypervisorEntity.CommercialAdmissionProjection{
		EventID: command.EventID, OwnerID: command.OwnerID,
		OwnerType: command.OwnerType, PolicyVersion: command.PolicyVersion,
		Decision: command.Decision, RestrictionReason: reason,
		EffectiveAt: command.EffectiveAt, ValidUntil: command.ValidUntil,
	})
}
