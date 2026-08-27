package mailSvcImpl

import (
	"context"
	"strings"

	mailEntity "controlplane/internal/mail/domain/entity"
	mailRepoInterface "controlplane/internal/mail/domain/repo"
	mailSvcInterface "controlplane/internal/mail/domain/service"
	mailTaxonomy "controlplane/internal/mail/taxonomy"
)

type MailCommercialAdmissionProjectionService struct {
	repo mailRepoInterface.CommercialAdmissionProjectionRepository
}

func NewMailCommercialAdmissionProjectionService(
	repo mailRepoInterface.CommercialAdmissionProjectionRepository,
) mailSvcInterface.CommercialAdmissionProjectionService {
	return &MailCommercialAdmissionProjectionService{repo: repo}
}

func (s *MailCommercialAdmissionProjectionService) Apply(
	ctx context.Context,
	command *mailEntity.CommercialAdmissionProjectionCommand,
) error {
	if command.PolicyVersion <= 0 ||
		(command.OwnerType != "PERSONAL" && command.OwnerType != "TENANT") ||
		(command.Decision != "ALLOW" && command.Decision != "SUSPEND_BILLABLE") ||
		(command.Decision == "ALLOW" && command.RestrictionReason != "") ||
		(command.Decision == "SUSPEND_BILLABLE" && strings.TrimSpace(command.RestrictionReason) == "") ||
		(command.ValidUntil != nil && !command.ValidUntil.After(command.EffectiveAt)) {
		return mailTaxonomy.ErrInvalidCommercialAdmissionProjection
	}
	var reason *string
	if command.RestrictionReason != "" {
		reason = &command.RestrictionReason
	}
	return s.repo.Upsert(ctx, &mailEntity.CommercialAdmissionProjection{
		EventID: command.EventID, OwnerID: command.OwnerID,
		OwnerType: command.OwnerType, PolicyVersion: command.PolicyVersion,
		Decision: command.Decision, RestrictionReason: reason,
		EffectiveAt: command.EffectiveAt, ValidUntil: command.ValidUntil,
	})
}
