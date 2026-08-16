package service

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	billingTaxonomy "cost-manager/api/internal/taxonomy"

	"github.com/google/uuid"
)

const hypervisorAdjustmentChecksumTimeLayout = "2006-01-02T15:04:05.000000Z07:00"

type hypervisorZoneAdjustmentPublishService struct {
	repo billingRepoInterface.HypervisorZoneAdjustmentRepository
}

func NewHypervisorZoneAdjustmentPublishService(repo billingRepoInterface.HypervisorZoneAdjustmentRepository) *hypervisorZoneAdjustmentPublishService {
	return &hypervisorZoneAdjustmentPublishService{repo: repo}
}

func (s *hypervisorZoneAdjustmentPublishService) CreateHypervisorZonePriceAdjustment(ctx context.Context, create entity.HypervisorZoneAdjustmentPublishCommand) (*entity.HypervisorZoneAdjustmentPublished, error) {
	create.ChangeReason = strings.TrimSpace(create.ChangeReason)
	create.EffectiveFrom = create.EffectiveFrom.UTC().Truncate(time.Microsecond)
	if create.ZoneID == uuid.Nil || create.CreatedBy == uuid.Nil || create.ExpectedLatestVersion < 0 ||
		create.EffectiveFrom.IsZero() || create.ChangeReason == "" || len(create.ChangeReason) > 2_000 ||
		create.MultiplierNumerator < 0 || create.MultiplierDenominator <= 0 {
		return nil, billingTaxonomy.ErrInvalidArgument
	}
	if create.EffectiveFrom.Before(time.Now().UTC().Add(-time.Minute)) {
		return nil, billingTaxonomy.ErrHypervisorZoneAdjustmentConflict
	}
	create.Checksum = hypervisorZoneAdjustmentChecksum(
		create.ZoneID, create.ExpectedLatestVersion+1, create.EffectiveFrom,
		create.MultiplierNumerator, create.MultiplierDenominator,
	)
	return s.repo.CreateHypervisorZonePriceAdjustment(ctx, create)
}

func hypervisorZoneAdjustmentChecksum(zoneID uuid.UUID, version int, effectiveFrom time.Time, numerator, denominator int64) string {
	hash := sha256.New()
	for _, value := range []string{
		zoneID.String(),
		fmt.Sprintf("%d", version), effectiveFrom.UTC().Format(hypervisorAdjustmentChecksumTimeLayout),
		fmt.Sprintf("%d", numerator), fmt.Sprintf("%d", denominator),
	} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	return fmt.Sprintf("%x", hash.Sum(nil))
}
