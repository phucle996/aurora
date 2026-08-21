package unit_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
	hypervisorRepoImpl "controlplane/internal/hypervisor/repository"
	hypervisorSvcImpl "controlplane/internal/hypervisor/service"
	hypervisorTaxonomy "controlplane/internal/hypervisor/taxonomy"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func TestHypervisorPricingReadinessProjectionAllowsOnlyFreshReadyState(t *testing.T) {
	redisServer := miniredis.RunT(t)
	redisClient := goredis.NewClient(&goredis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = redisClient.Close() })
	repo := hypervisorRepoImpl.NewHypervisorPricingReadinessProjectionRepo(redisClient)
	projectionService := hypervisorSvcImpl.NewHypervisorPricingReadinessProjectionService(repo)
	gate := hypervisorSvcImpl.NewHypervisorPricingReadinessGateService(repo)
	now := time.Now().UTC()
	if err := projectionService.ApplyPricingReadiness(context.Background(), &hypervisorEntity.PricingReadinessProjectionCommand{
		SchemaVersion: 1, Ready: true, Missing: []string{}, ObservedAt: now,
		ValidUntil: now.Add(45 * time.Second), Fingerprint: strings.Repeat("ab", 32),
	}); err != nil {
		t.Fatal(err)
	}
	if err := gate.RequireHypervisorPricing(context.Background()); err != nil {
		t.Fatalf("fresh ready projection was rejected: %v", err)
	}
}

func TestHypervisorPricingReadinessProjectionFailsClosed(t *testing.T) {
	redisServer := miniredis.RunT(t)
	redisClient := goredis.NewClient(&goredis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = redisClient.Close() })
	repo := hypervisorRepoImpl.NewHypervisorPricingReadinessProjectionRepo(redisClient)
	projectionService := hypervisorSvcImpl.NewHypervisorPricingReadinessProjectionService(repo)
	gate := hypervisorSvcImpl.NewHypervisorPricingReadinessGateService(repo)

	if err := gate.RequireHypervisorPricing(context.Background()); !errors.Is(err, hypervisorTaxonomy.ErrPricingUnavailable) {
		t.Fatalf("missing readiness projection did not fail closed: %v", err)
	}
	now := time.Now().UTC()
	if err := projectionService.ApplyPricingReadiness(context.Background(), &hypervisorEntity.PricingReadinessProjectionCommand{
		SchemaVersion: 1, Ready: false, Missing: []string{"hypervisor.network_out.byte"},
		ObservedAt: now, ValidUntil: now.Add(45 * time.Second), Fingerprint: strings.Repeat("cd", 32),
	}); err != nil {
		t.Fatal(err)
	}
	if err := gate.RequireHypervisorPricing(context.Background()); !errors.Is(err, hypervisorTaxonomy.ErrPricingUnavailable) {
		t.Fatalf("not-ready projection did not fail closed: %v", err)
	}
}

func TestHypervisorPricingReadinessProjectionRejectsOlderReplay(t *testing.T) {
	redisServer := miniredis.RunT(t)
	redisClient := goredis.NewClient(&goredis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = redisClient.Close() })
	repo := hypervisorRepoImpl.NewHypervisorPricingReadinessProjectionRepo(redisClient)
	projectionService := hypervisorSvcImpl.NewHypervisorPricingReadinessProjectionService(repo)
	now := time.Now().UTC()

	if err := projectionService.ApplyPricingReadiness(context.Background(), &hypervisorEntity.PricingReadinessProjectionCommand{
		SchemaVersion: 1, Ready: true, Missing: []string{}, ObservedAt: now,
		ValidUntil: now.Add(45 * time.Second), Fingerprint: strings.Repeat("ef", 32),
	}); err != nil {
		t.Fatal(err)
	}
	if err := projectionService.ApplyPricingReadiness(context.Background(), &hypervisorEntity.PricingReadinessProjectionCommand{
		SchemaVersion: 1, Ready: false, Missing: []string{"hypervisor.vcpu.core"},
		ObservedAt: now.Add(-10 * time.Second), ValidUntil: now.Add(30 * time.Second),
		Fingerprint: strings.Repeat("01", 32),
	}); err != nil {
		t.Fatal(err)
	}

	projection, err := repo.ReadPricingReadiness(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !projection.Ready || projection.Fingerprint != strings.Repeat("ef", 32) {
		t.Fatalf("older replay replaced local winner: %#v", projection)
	}
}
