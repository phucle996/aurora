package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cost-manager/api/internal/domain/entity"
	"cost-manager/api/internal/transport/http/handler"
	"cost-manager/api/pkg/pkgcontext"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type storageZoneAdjustmentListServiceStub struct {
	query entity.StorageZoneAdjustmentListQuery
	calls int
}

func (s *storageZoneAdjustmentListServiceStub) ListStorageZonePriceAdjustments(_ context.Context, query entity.StorageZoneAdjustmentListQuery) (*entity.StorageZoneAdjustmentListResult, error) {
	s.calls++
	s.query = query
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	return &entity.StorageZoneAdjustmentListResult{
		ZoneID: query.ZoneID,
		Items: []entity.StorageZoneAdjustmentListItem{{
			ID: uuid.New(), ZoneID: query.ZoneID, VersionNumber: 3, Status: "ACTIVE",
			EffectiveFrom: now, MultiplierNumerator: 9_223_372_036_854_775_807,
			MultiplierDenominator: 100, Checksum: "checksum", ChangeReason: "operator price adjustment",
			CreatedBy: uuid.New(), CreatedAt: now, IsLatest: true, IsEffective: true,
		}},
		ObservedAt: now,
	}, nil
}

func TestStorageZoneAdjustmentListUsesTrustedContextAndSerializesBigIntAsString(t *testing.T) {
	gin.SetMode(gin.TestMode)
	trustedZoneID := uuid.New()
	attackerZoneID := uuid.New()
	service := &storageZoneAdjustmentListServiceStub{}
	storageHandler := handler.NewStoragePricingHandler(nil, nil, service)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(pkgcontext.CtxZoneID, trustedZoneID)
		c.Next()
	})
	router.GET("/api/v1/billing/storage/zone-price-adjustments", storageHandler.ListZonePriceAdjustments)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/billing/storage/zone-price-adjustments?limit=25&zone_id="+attackerZoneID.String(), nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if service.calls != 1 || service.query.ZoneID != trustedZoneID || service.query.Limit != 25 {
		t.Fatalf("handler did not isolate the trusted Zone query: calls=%d query=%#v", service.calls, service.query)
	}
	body := response.Body.String()
	if strings.Contains(body, attackerZoneID.String()) || !strings.Contains(body, trustedZoneID.String()) {
		t.Fatalf("response leaked caller-selected Zone: %s", body)
	}
	if !strings.Contains(body, `"multiplier_numerator":"9223372036854775807"`) ||
		!strings.Contains(body, `"observed_at":"2026-08-16T10:00:00Z"`) {
		t.Fatalf("response broke BIGINT string or UTC contracts: %s", body)
	}
}

func TestStorageZoneAdjustmentListRejectsInvalidLimitBeforeService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &storageZoneAdjustmentListServiceStub{}
	storageHandler := handler.NewStoragePricingHandler(nil, nil, service)
	router := gin.New()
	router.GET("/api/v1/billing/storage/zone-price-adjustments", storageHandler.ListZonePriceAdjustments)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/billing/storage/zone-price-adjustments?limit=101", nil))

	if response.Code != http.StatusBadRequest || service.calls != 0 {
		t.Fatalf("invalid transport input reached service: status=%d calls=%d", response.Code, service.calls)
	}
}
