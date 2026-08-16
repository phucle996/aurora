package service_test

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"testing"
	"time"

	"cost-manager/api/internal/domain/entity"
	"cost-manager/api/internal/service"

	"github.com/google/uuid"
)

type mailSnapshotRepoStub struct{ snapshot *entity.PricingSnapshot }

func (s mailSnapshotRepoStub) GetActivePricingSnapshot(context.Context, entity.ChargeKindCode, time.Time) (*entity.PricingSnapshot, error) {
	return s.snapshot, nil
}

type mailAdjustmentRepoStub struct {
	adjustment *entity.MailZoneAdjustmentSnapshot
}

func (s mailAdjustmentRepoStub) GetActiveMailZonePriceAdjustment(context.Context, uuid.UUID, time.Time) (*entity.MailZoneAdjustmentSnapshot, error) {
	return s.adjustment, nil
}

func (s mailAdjustmentRepoStub) CreateMailZonePriceAdjustment(_ context.Context, command entity.MailZoneAdjustmentPublishCommand) (*entity.MailZoneAdjustmentPublished, error) {
	return &entity.MailZoneAdjustmentPublished{ID: uuid.New(), ZoneID: command.ZoneID, VersionNumber: command.ExpectedLatestVersion + 1, EffectiveFrom: command.EffectiveFrom, MultiplierNumerator: command.MultiplierNumerator, MultiplierDenominator: command.MultiplierDenominator, Checksum: command.Checksum}, nil
}

func TestMailEstimateAppliesMailZoneAdjustmentToAcceptedRecipients(t *testing.T) {
	zoneID := uuid.New()
	effective := time.Now().UTC().Truncate(time.Microsecond)
	adjustmentService := service.NewMailZoneAdjustmentPublishService(mailAdjustmentRepoStub{})
	published, err := adjustmentService.CreateMailZonePriceAdjustment(context.Background(), entity.MailZoneAdjustmentPublishCommand{ZoneID: zoneID, CreatedBy: uuid.New(), EffectiveFrom: effective, ChangeReason: "zone discount", MultiplierNumerator: 80, MultiplierDenominator: 100})
	if err != nil {
		t.Fatal(err)
	}
	adjustment := &entity.MailZoneAdjustmentSnapshot{ID: published.ID, ZoneID: zoneID, VersionNumber: published.VersionNumber, EffectiveFrom: effective, MultiplierNumerator: 80, MultiplierDenominator: 100, Checksum: published.Checksum}
	scheduleID, versionID := uuid.New(), uuid.New()
	brackets := []entity.PricingSnapshotBracket{{RangeStartQuantity: 0, PriceNumeratorMicroUnits: 5, PriceDenominatorQuantity: 1_000}}
	hash := sha256.New()
	for _, value := range []string{"mail-accepted-recipient-payg", string(entity.ChargeKindMailAcceptedRecipient), string(entity.PricingModelProgressiveUnit), "USD", effective.UTC().Format("2006-01-02T15:04:05.000000Z07:00"), "1", "0", "infinity", "5", "1000"} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(value))
	}
	estimateService := service.NewMailEstimateService(mailSnapshotRepoStub{snapshot: &entity.PricingSnapshot{
		PricingScheduleID: scheduleID, VersionID: versionID, Code: "mail-accepted-recipient-payg",
		ChargeKindCode: entity.ChargeKindMailAcceptedRecipient, PricingModel: entity.PricingModelProgressiveUnit,
		ModuleCode: "mail", RawInputUnit: "RECIPIENT", Currency: "USD", VersionNumber: 1,
		Checksum: fmt.Sprintf("%x", hash.Sum(nil)), EffectiveFrom: effective, Brackets: brackets,
	}}, mailAdjustmentRepoStub{adjustment: adjustment}, nil)
	estimate, err := estimateService.EstimateMail(context.Background(), 1_000, zoneID)
	if err != nil {
		t.Fatal(err)
	}
	if estimate.EstimateMicroUnits != 4 || estimate.RateAdjustmentNumerator != 80 || estimate.PricingScheduleVersionID != versionID {
		t.Fatalf("unexpected Mail estimate: %#v", estimate)
	}
}
