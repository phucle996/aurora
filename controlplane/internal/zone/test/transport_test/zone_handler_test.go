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

	coreEntity "controlplane/internal/zone/domain/entity"
	coreErrorx "controlplane/internal/zone/taxonomy"
	requestdto "controlplane/internal/zone/transport/http/dto/req"
	coreHandler "controlplane/internal/zone/transport/http/handler"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type zoneServiceStub struct {
	listErr   error
	upsertErr error
}

func (s *zoneServiceStub) ListZones(ctx context.Context) ([]coreEntity.Zone, error) {
	return []coreEntity.Zone{}, nil
}
func (s *zoneServiceStub) RPCListZones(ctx context.Context) ([]coreEntity.RPCZone, error) {
	return []coreEntity.RPCZone{}, nil
}
func (s *zoneServiceStub) GetZoneDetailByID(ctx context.Context, id uuid.UUID) (*coreEntity.ZoneDetail, error) {
	return &coreEntity.ZoneDetail{
		Zone:     coreEntity.Zone{},
		Services: []coreEntity.ZoneService{},
	}, nil
}
func (s *zoneServiceStub) CreateZone(ctx context.Context, input coreEntity.CreateZoneInput) error {
	return nil
}
func (s *zoneServiceStub) UpdateZoneStatus(ctx context.Context, zoneID uuid.UUID, status coreEntity.ZoneStatus) error {
	return nil
}
func (s *zoneServiceStub) DeleteZone(ctx context.Context, zoneID uuid.UUID) error { return nil }
func (s *zoneServiceStub) ListZoneServices(ctx context.Context, zoneID uuid.UUID) ([]coreEntity.ZoneService, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return []coreEntity.ZoneService{}, nil
}
func (s *zoneServiceStub) UpsertZoneService(ctx context.Context, zoneID uuid.UUID, serviceType coreEntity.ZoneServiceType, enabled bool) (*coreEntity.ZoneService, error) {
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
	r.PUT("/zones/services", h.UpsertZoneService)
	body, _ := json.Marshal(map[string]interface{}{
		"zone_id":      "0196f3aa-18ae-7a0d-8f74-f7933b6a0e9b",
		"service_type": "mail",
		"enabled":      true,
	})
	req := httptest.NewRequest(http.MethodPut, "/zones/services", bytes.NewReader(body))
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
	r.PUT("/zones/services", h.UpsertZoneService)
	body, _ := json.Marshal(map[string]interface{}{
		"zone_id":      "0196f3aa-18ae-7a0d-8f74-f7933b6a0e9b",
		"service_type": "nope",
		"enabled":      true,
	})
	req := httptest.NewRequest(http.MethodPut, "/zones/services", bytes.NewReader(body))
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
	r.PUT("/zones/services", h.UpsertZoneService)
	body, _ := json.Marshal(map[string]interface{}{
		"zone_id":      "0196f3aa-18ae-7a0d-8f74-f7933b6a0e9b",
		"service_type": "mail",
		"enabled":      true,
	})
	req := httptest.NewRequest(http.MethodPut, "/zones/services", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
	_ = time.Second
}

func TestZoneHandlerUpdateZoneStatusSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := coreHandler.NewZoneHandler(&zoneServiceStub{})
	r.PATCH("/zones/status", h.UpdateZoneStatus)
	body, _ := json.Marshal(requestdto.UpdateZoneStatusRequest{
		ZoneID: uuid.MustParse("0196f3aa-18ae-7a0d-8f74-f7933b6a0e9b"),
		Status: "active",
	})
	req := httptest.NewRequest(http.MethodPatch, "/zones/status", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestZoneHandlerUpdateZoneStatusBadRequest(t *testing.T) {
	tests := []struct {
		name string
		body map[string]interface{}
	}{
		{
			name: "missing zone_id",
			body: map[string]interface{}{
				"status": "active",
			},
		},
		{
			name: "invalid uuid zone_id",
			body: map[string]interface{}{
				"zone_id": "invalid-uuid",
				"status":  "active",
			},
		},
		{
			name: "invalid status",
			body: map[string]interface{}{
				"zone_id": "0196f3aa-18ae-7a0d-8f74-f7933b6a0e9b",
				"status":  "invalid-status",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			h := coreHandler.NewZoneHandler(&zoneServiceStub{})
			r.PATCH("/zones/status", h.UpdateZoneStatus)
			body, _ := json.Marshal(tc.body)
			req := httptest.NewRequest(http.MethodPatch, "/zones/status", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d, test case: %s", w.Code, tc.name)
			}
		})
	}
}

func TestZoneHandlerGetZoneSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := coreHandler.NewZoneHandler(&zoneServiceStub{})
	r.GET("/zones/:zone_id", h.GetZone)
	req := httptest.NewRequest(http.MethodGet, "/zones/0196f3aa-18ae-7a0d-8f74-f7933b6a0e9b", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

