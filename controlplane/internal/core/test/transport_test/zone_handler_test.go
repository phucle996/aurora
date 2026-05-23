package transport_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	coreEntity "controlplane/internal/core/domain/entity"
	coreErrorx "controlplane/internal/core/errorx"
	requestdto "controlplane/internal/core/transport/http/dto/req"
	coreHandler "controlplane/internal/core/transport/http/handler"

	"github.com/gin-gonic/gin"
)

type zoneServiceStub struct {
	listErr   error
	upsertErr error
}

func (s *zoneServiceStub) ListZones(ctx context.Context) ([]coreEntity.Zone, error) {
	return []coreEntity.Zone{}, nil
}

func (s *zoneServiceStub) CreateZone(ctx context.Context, code, name string, status *coreEntity.ZoneStatus) (*coreEntity.Zone, error) {
	return &coreEntity.Zone{}, nil
}
func (s *zoneServiceStub) UpdateZoneStatus(ctx context.Context, zoneID string, status coreEntity.ZoneStatus) (*coreEntity.Zone, error) {
	return &coreEntity.Zone{}, nil
}
func (s *zoneServiceStub) DeleteZone(ctx context.Context, zoneID string) error { return nil }
func (s *zoneServiceStub) ListZoneServices(ctx context.Context, zoneID string) ([]coreEntity.ZoneService, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return []coreEntity.ZoneService{}, nil
}
func (s *zoneServiceStub) UpsertZoneService(ctx context.Context, zoneID string, serviceType string, enabled bool) (*coreEntity.ZoneService, error) {
	if s.upsertErr != nil {
		return nil, s.upsertErr
	}
	return &coreEntity.ZoneService{}, nil
}

func TestZoneHandlerListZoneServicesBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := coreHandler.NewZoneHandler(&zoneServiceStub{listErr: coreErrorx.ErrZoneServiceInvalidInput})
	r.GET("/zones/:zone_id/services", h.ListZoneServices)
	req := httptest.NewRequest(http.MethodGet, "/zones/not-uuid/services", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestZoneHandlerUpsertZoneServiceConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := coreHandler.NewZoneHandler(&zoneServiceStub{upsertErr: coreErrorx.ErrZoneServiceStateConflict})
	r.PUT("/zones/:zone_id/services", h.UpsertZoneService)
	body, _ := json.Marshal(requestdto.UpsertZoneServiceRequest{ServiceType: "mail", Enabled: true})
	req := httptest.NewRequest(http.MethodPut, "/zones/0196f3aa-18ae-7a0d-8f74-f7933b6a0e9b/services", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
}

func TestZoneHandlerUpsertZoneServiceBadType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := coreHandler.NewZoneHandler(&zoneServiceStub{})
	r.PUT("/zones/:zone_id/services", h.UpsertZoneService)
	body, _ := json.Marshal(requestdto.UpsertZoneServiceRequest{ServiceType: "nope", Enabled: true})
	req := httptest.NewRequest(http.MethodPut, "/zones/0196f3aa-18ae-7a0d-8f74-f7933b6a0e9b/services", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestZoneHandlerUpsertZoneServiceInternal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := coreHandler.NewZoneHandler(&zoneServiceStub{upsertErr: errors.New("boom")})
	r.PUT("/zones/:zone_id/services", h.UpsertZoneService)
	body, _ := json.Marshal(requestdto.UpsertZoneServiceRequest{ServiceType: "mail", Enabled: true})
	req := httptest.NewRequest(http.MethodPut, "/zones/0196f3aa-18ae-7a0d-8f74-f7933b6a0e9b/services", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
	_ = time.Second
}
