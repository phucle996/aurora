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
func (s *zoneServiceStub) UpdateZoneService(ctx context.Context, zoneID uuid.UUID, serviceType coreEntity.ZoneServiceType, enabled bool) (*coreEntity.ZoneService, error) {
	if s.upsertErr != nil {
		return nil, s.upsertErr
	}
	return &coreEntity.ZoneService{}, nil
}

func TestZoneHandlerUpdateZoneServiceConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := coreHandler.NewZoneHandler(&zoneServiceStub{upsertErr: coreErrorx.ErrZoneServiceStateConflict})
	r.PUT("/zones/services", h.UpdateZoneService)
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

func TestZoneHandlerUpdateZoneServiceBadType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := coreHandler.NewZoneHandler(&zoneServiceStub{})
	r.PUT("/zones/services", h.UpdateZoneService)
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

func TestZoneHandlerUpdateZoneServiceInternal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := coreHandler.NewZoneHandler(&zoneServiceStub{upsertErr: errors.New("boom")})
	r.PUT("/zones/services", h.UpdateZoneService)
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
	r.PATCH("/zones/:zone_id/status", h.UpdateZoneStatus)
	body, _ := json.Marshal(requestdto.UpdateZoneStatusRequest{
		Status: "active",
	})
	req := httptest.NewRequest(http.MethodPatch, "/zones/0196f3aa-18ae-7a0d-8f74-f7933b6a0e9b/status", bytes.NewReader(body))
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
		path string
		body map[string]interface{}
	}{
		{
			name: "invalid uuid zone_id",
			path: "/zones/invalid-uuid/status",
			body: map[string]interface{}{
				"status": "active",
			},
		},
		{
			name: "invalid status",
			path: "/zones/0196f3aa-18ae-7a0d-8f74-f7933b6a0e9b/status",
			body: map[string]interface{}{
				"status": "invalid-status",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			h := coreHandler.NewZoneHandler(&zoneServiceStub{})
			r.PATCH("/zones/:zone_id/status", h.UpdateZoneStatus)
			body, _ := json.Marshal(tc.body)
			req := httptest.NewRequest(http.MethodPatch, tc.path, bytes.NewReader(body))
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
	r.GET("/zones/:zone_id", h.GetDetailZone)
	req := httptest.NewRequest(http.MethodGet, "/zones/0196f3aa-18ae-7a0d-8f74-f7933b6a0e9b", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

