package service_test

import (
	"bytes"
	"encoding/json"
	"testing"

	storagepricingv1 "cost-manager/api/internal/genproto/billing/pricing/storage/v1"

	"google.golang.org/protobuf/proto"
)

func TestStoragePricingSnapshotCacheEntryUsesBinaryProtoAndPreservesBigInt(t *testing.T) {
	effectiveTo := int64(1_786_470_001_234_567)
	rangeEnd := int64(9_223_372_036_854_775_806)
	entry := &storagepricingv1.StoragePricingSnapshotCacheEntryV1{
		PricingScheduleId:       bytes.Repeat([]byte{1}, 16),
		VersionId:               bytes.Repeat([]byte{2}, 16),
		ScheduleCode:            "storage-capacity-payg",
		ChargeKind:              storagepricingv1.StorageChargeKindV1_STORAGE_CHARGE_KIND_V1_CAPACITY_GB_HOUR,
		PricingModel:            storagepricingv1.StoragePricingModelV1_STORAGE_PRICING_MODEL_V1_PROGRESSIVE_UNIT,
		RawInputUnit:            storagepricingv1.StorageRawInputUnitV1_STORAGE_RAW_INPUT_UNIT_V1_BYTE_HOUR,
		VersionNumber:           7,
		EffectiveFromUnixMicros: 1_786_466_401_234_567,
		EffectiveToUnixMicros:   &effectiveTo,
		ChecksumSha256:          bytes.Repeat([]byte{3}, 32),
		Currency:                "USD",
		Brackets: []*storagepricingv1.StoragePricingScalarBracketV1{{
			Id:                       bytes.Repeat([]byte{4}, 16),
			RangeStartQuantity:       0,
			RangeEndQuantity:         &rangeEnd,
			PriceNumeratorMicroUnits: 9_223_372_036_854_775_807,
			PriceDenominatorQuantity: 1,
		}},
	}

	payload, err := proto.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal storage cache entry: %v", err)
	}
	if json.Valid(payload) {
		t.Fatal("Storage L2 cache entry unexpectedly encoded as JSON")
	}

	var decoded storagepricingv1.StoragePricingSnapshotCacheEntryV1
	if err := proto.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal storage cache entry: %v", err)
	}
	if decoded.GetEffectiveToUnixMicros() != effectiveTo || len(decoded.GetChecksumSha256()) != 32 ||
		len(decoded.GetBrackets()) != 1 || decoded.GetBrackets()[0].GetPriceNumeratorMicroUnits() != 9_223_372_036_854_775_807 {
		t.Fatalf("protobuf cache payload lost exact Storage pricing data: version=%d brackets=%d", decoded.GetVersionNumber(), len(decoded.GetBrackets()))
	}
}
