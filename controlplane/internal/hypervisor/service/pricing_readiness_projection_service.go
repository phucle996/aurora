package hypervisorSvcImpl

import (
	"context"
	"fmt"
	"time"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
	hypervisorRepoInterface "controlplane/internal/hypervisor/domain/repo"
	hypervisorSvcInterface "controlplane/internal/hypervisor/domain/service"
	hypervisorTaxonomy "controlplane/internal/hypervisor/taxonomy"
)

type HypervisorPricingReadinessProjectionService struct {
	repo hypervisorRepoInterface.PricingReadinessProjectionWriter
}

func NewHypervisorPricingReadinessProjectionService(repo hypervisorRepoInterface.PricingReadinessProjectionWriter) hypervisorSvcInterface.PricingReadinessProjectionService {
	return &HypervisorPricingReadinessProjectionService{repo: repo}
}

func (s *HypervisorPricingReadinessProjectionService) ApplyPricingReadiness(ctx context.Context, command *hypervisorEntity.PricingReadinessProjectionCommand) error {
	now := time.Now().UTC()
	if command.SchemaVersion != 1 || command.Ready != (len(command.Missing) == 0) ||
		command.ObservedAt.After(now.Add(time.Minute)) ||
		command.ValidUntil.After(command.ObservedAt.Add(time.Minute)) ||
		!command.ValidUntil.After(command.ObservedAt) {
		return fmt.Errorf("%w: invalid Hypervisor pricing readiness", hypervisorTaxonomy.ErrPricingUnavailable)
	}
	if !command.ValidUntil.After(now) {
		return nil
	}
	return s.repo.UpsertPricingReadiness(ctx, &hypervisorEntity.PricingReadinessProjection{
		SchemaVersion: command.SchemaVersion, Ready: command.Ready,
		Missing:    append([]string(nil), command.Missing...),
		ObservedAt: command.ObservedAt, ValidUntil: command.ValidUntil,
		Fingerprint: command.Fingerprint,
	})
}

type HypervisorPricingReadinessGateService struct {
	repo hypervisorRepoInterface.PricingReadinessProjectionReader
}

func NewHypervisorPricingReadinessGateService(repo hypervisorRepoInterface.PricingReadinessProjectionReader) hypervisorSvcInterface.PricingReadinessGate {
	return &HypervisorPricingReadinessGateService{repo: repo}
}

func (s *HypervisorPricingReadinessGateService) RequireHypervisorPricing(ctx context.Context) error {
	projection, err := s.repo.ReadPricingReadiness(ctx)
	now := time.Now().UTC()
	if err != nil || projection == nil || projection.SchemaVersion != 1 ||
		!projection.Ready || len(projection.Missing) != 0 ||
		projection.ObservedAt.After(now.Add(time.Minute)) ||
		!projection.ValidUntil.After(now) ||
		projection.ValidUntil.After(projection.ObservedAt.Add(time.Minute)) {
		return fmt.Errorf("%w: local readiness projection unavailable", hypervisorTaxonomy.ErrPricingUnavailable)
	}
	return nil
}
