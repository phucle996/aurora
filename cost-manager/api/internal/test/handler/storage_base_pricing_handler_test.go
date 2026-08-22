package handler_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingSvcInterface "cost-manager/api/internal/domain/service"
	"cost-manager/api/internal/transport/http/handler"
	"cost-manager/api/pkg/pkgcontext"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type storageBasePricingServiceStub struct {
	billingSvcInterface.StoragePricingService
	brackets []entity.StorageBasePricePublishBracket
}

func (s *storageBasePricingServiceStub) CreateStorageBasePriceVersion(
	_ context.Context,
	_ entity.StorageBasePricePublishCommand,
	brackets []entity.StorageBasePricePublishBracket,
) (*entity.StorageBasePricePublished, []entity.StorageBasePricePublishBracket, error) {
	s.brackets = brackets
	effective := time.Date(2026, 8, 15, 6, 30, 0, 0, time.UTC)
	return &entity.StorageBasePricePublished{
		ID: uuid.New(), PricingScheduleID: uuid.New(), ChargeKindCode: entity.ChargeKindStorageCapacity,
		VersionNumber: 1, PricingModel: entity.PricingModelProgressiveUnit,
		Status: "ACTIVE", EffectiveFrom: effective, Checksum: strings.Repeat("a", 64),
	}, brackets, nil
}

func TestStorageBasePriceFirstVersionRequiresExplicitZeroOCC(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
		want int
	}{
		{name: "explicit zero", body: `{"expected_latest_version":0,"effective_from":"2026-08-15T06:30:00Z","change_reason":"first operator price","brackets":[{"range_start_quantity":"0","price_numerator_micro_units":"9223372036854775807","price_denominator_quantity":"1"}]}`, want: http.StatusCreated},
		{name: "missing OCC", body: `{"effective_from":"2026-08-15T06:30:00Z","change_reason":"first operator price","brackets":[{"range_start_quantity":"0","price_numerator_micro_units":"1","price_denominator_quantity":"1"}]}`, want: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(test.body))
			ctx.Request.Header.Set("Content-Type", "application/json")
			ctx.Params = gin.Params{{Key: "code", Value: "storage-capacity-payg"}}
			ctx.Set(pkgcontext.CtxUserID, uuid.New())
			stub := &storageBasePricingServiceStub{}
			handler.NewStoragePricingHandler(stub).CreateBasePriceVersion(ctx)
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.want, recorder.Body.String())
			}
			if test.want == http.StatusCreated {
				if len(stub.brackets) != 1 || stub.brackets[0].PriceNumeratorMicroUnits != int64(9223372036854775807) {
					t.Fatal("transport did not preserve exact int64 bracket value")
				}
				if !strings.Contains(recorder.Body.String(), `"price_numerator_micro_units":"9223372036854775807"`) ||
					!strings.Contains(recorder.Body.String(), `"effective_from":"2026-08-15T06:30:00Z"`) {
					t.Fatalf("response did not preserve decimal-string/UTC contract: %s", recorder.Body.String())
				}
			}
		})
	}
}

func TestStorageBasePriceTransportRejectsInt64Overflow(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"expected_latest_version":0,"effective_from":"2026-08-15T06:30:00Z","change_reason":"price","brackets":[{"range_start_quantity":"9223372036854775808","price_numerator_micro_units":"1","price_denominator_quantity":"1"}]}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Params = gin.Params{{Key: "code", Value: "storage-capacity-payg"}}
	ctx.Set(pkgcontext.CtxUserID, uuid.New())
	handler.NewStoragePricingHandler(&storageBasePricingServiceStub{}).CreateBasePriceVersion(ctx)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}
