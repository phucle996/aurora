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
	"cost-manager/api/pkg/pkgcontext"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type hypervisorZoneAdjustmentListServiceStub struct {
	billingSvcInterface.HypervisorPricingService
	query entity.HypervisorZoneAdjustmentListQuery
}

func (s *hypervisorZoneAdjustmentListServiceStub) ListHypervisorZonePriceAdjustments(_ context.Context, query entity.HypervisorZoneAdjustmentListQuery) (*entity.HypervisorZoneAdjustmentListResult, error) {
	s.query = query
	now := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)
	return &entity.HypervisorZoneAdjustmentListResult{
		ZoneID: query.ZoneID,
		Items: []entity.HypervisorZoneAdjustmentListItem{{
			ID: uuid.New(), ZoneID: query.ZoneID, VersionNumber: 3, Status: "ACTIVE", EffectiveFrom: now,
			MultiplierNumerator: 9_223_372_036_854_775_807, MultiplierDenominator: 100,
			Checksum: "checksum", ChangeReason: "operator price adjustment", CreatedBy: uuid.New(), CreatedAt: now,
			IsLatest: true, IsEffective: true,
		}},
		ObservedAt: now,
	}, nil
}

func TestHypervisorZoneAdjustmentListUsesTrustedZoneAndSerializesBigInt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	trustedZoneID := uuid.New()
	service := &hypervisorZoneAdjustmentListServiceStub{}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(pkgcontext.CtxZoneID, trustedZoneID)
		c.Next()
	})
	router.GET("/api/v1/billing/hypervisor/zone-price-adjustments", handler.NewHypervisorPricingHandler(service).ListZonePriceAdjustments)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/billing/hypervisor/zone-price-adjustments?limit=25", nil))

	if response.Code != http.StatusOK || service.query.ZoneID != trustedZoneID || service.query.Limit != 25 {
		t.Fatalf("unexpected response/query: status=%d query=%#v body=%s", response.Code, service.query, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"multiplier_numerator":"9223372036854775807"`) || !strings.Contains(response.Body.String(), `"observed_at":"2026-08-23T10:00:00Z"`) {
		t.Fatalf("response broke BIGINT or UTC contracts: %s", response.Body.String())
	}
}
