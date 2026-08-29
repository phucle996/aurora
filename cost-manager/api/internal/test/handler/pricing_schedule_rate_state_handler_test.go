package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingSvcInterface "cost-manager/api/internal/domain/service"
	"cost-manager/api/internal/transport/http/handler"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type pricingScheduleRateStateServiceStub struct {
	billingSvcInterface.PricingScheduleRateStateService
	rows []entity.PricingScheduleRateStateRow
}

func (s *pricingScheduleRateStateServiceStub) GetPricingScheduleRateState(_ context.Context, _ string) ([]entity.PricingScheduleRateStateRow, error) {
	return s.rows, nil
}

func TestPricingScheduleRateStateSeparatesEffectiveAndNextScheduledVersions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	scheduleID := uuid.New()
	effectiveID := uuid.New()
	nextID := uuid.New()
	effectiveNumber, nextNumber, latestNumber := 4, 5, 5
	start, numerator, denominator := int64(0), int64(9223372036854775807), int64(1)
	effectiveRole, nextRole := "EFFECTIVE", "NEXT_SCHEDULED"
	active, scheduled := "ACTIVE", "SCHEDULED"
	checksum, reason := strings.Repeat("a", 64), "regional capacity review"
	now := time.Date(2026, 8, 30, 7, 0, 0, 0, time.UTC)
	nextAt := now.Add(24 * time.Hour)
	stub := &pricingScheduleRateStateServiceStub{rows: []entity.PricingScheduleRateStateRow{
		{
			ScheduleID: scheduleID, Code: "storage-capacity-payg", DisplayName: "Storage capacity", ChargeKindCode: entity.ChargeKindStorageCapacity,
			PricingModel: entity.PricingModelProgressiveUnit, Currency: "USD", MetadataVersion: 3, ObservedAt: now, LatestVersionNumber: &latestNumber,
			VersionRole: &effectiveRole, VersionID: &effectiveID, VersionNumber: &effectiveNumber, VersionStatus: &active, EffectiveFrom: &now, Checksum: &checksum, ChangeReason: &reason,
			BracketID: &effectiveID, RangeStartQuantity: &start, PriceNumerator: &numerator, PriceDenominator: &denominator,
		},
		{
			ScheduleID: scheduleID, Code: "storage-capacity-payg", DisplayName: "Storage capacity", ChargeKindCode: entity.ChargeKindStorageCapacity,
			PricingModel: entity.PricingModelProgressiveUnit, Currency: "USD", MetadataVersion: 3, ObservedAt: now, LatestVersionNumber: &latestNumber,
			VersionRole: &nextRole, VersionID: &nextID, VersionNumber: &nextNumber, VersionStatus: &scheduled, EffectiveFrom: &nextAt, Checksum: &checksum, ChangeReason: &reason,
			BracketID: &nextID, RangeStartQuantity: &start, PriceNumerator: &numerator, PriceDenominator: &denominator,
		},
	}}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	ctx.Params = gin.Params{{Key: "code", Value: "storage-capacity-payg"}}
	handler.NewPricingScheduleRateStateHandler(stub).GetPricingScheduleRateState(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, expected := range []string{
		`"latest_version_number":5`,
		`"effective_version"`,
		`"next_scheduled_version"`,
		`"price_numerator_micro_units":"9223372036854775807"`,
		`"effective_from":"2026-08-30T07:00:00Z"`,
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("response missing %s: %s", expected, body)
		}
	}
}
