package unit

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	hierarchyEntity "controlplane/internal/hierarchy/domain/entity"
	hierarchySvcInterface "controlplane/internal/hierarchy/domain/service"
	hierarchyHandler "controlplane/internal/hierarchy/transport/http/handler"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type managedServiceZoneCapture struct {
	hierarchySvcInterface.ZoneService
	createdInput       *hierarchyEntity.CreateZone
	updatedServiceType hierarchyEntity.ZoneServiceType
}

func (capture *managedServiceZoneCapture) CreateZone(_ context.Context, input *hierarchyEntity.CreateZone) (*hierarchyEntity.CreateZone, error) {
	capture.createdInput = input
	return input, nil
}

func (capture *managedServiceZoneCapture) UpdateZoneService(
	_ context.Context,
	input *hierarchyEntity.UpdateZoneService,
) (*hierarchyEntity.UpdateZoneService, error) {
	capture.updatedServiceType = input.ServiceType
	return &hierarchyEntity.UpdateZoneService{
		ID: uuid.MustParse("10000000-0000-4000-8000-000000000001"), ZoneID: input.ZoneID,
		ServiceType: input.ServiceType, DesiredState: input.DesiredState, ActualState: "unknown",
		CreatedAt: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(1, 0).UTC(),
	}, nil
}

func TestCreateZoneAcceptsManagedServiceCapability(t *testing.T) {
	gin.SetMode(gin.TestMode)
	capture := &managedServiceZoneCapture{}
	zoneHandler := hierarchyHandler.NewZoneHandler(capture)
	router := gin.New()
	router.POST("/admin/critical/hierarchy/zones", zoneHandler.CreateZone)

	request := httptest.NewRequest(http.MethodPost, "/admin/critical/hierarchy/zones", bytes.NewBufferString(
		`{"code":"zone-1","name":"Zone 1","location":"Hanoi","enable_managed_service":true}`,
	))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", response.Code, response.Body.String())
	}
	if capture.createdInput == nil || !capture.createdInput.EnableManagedService {
		t.Fatal("managed_service desired state must reach the create workflow entity")
	}
}

func TestUpdateZoneServiceAcceptsManagedServiceCapability(t *testing.T) {
	gin.SetMode(gin.TestMode)
	capture := &managedServiceZoneCapture{}
	zoneHandler := hierarchyHandler.NewZoneHandler(capture)
	router := gin.New()
	router.PUT("/admin/critical/hierarchy/zones/services", zoneHandler.UpdateZoneService)

	request := httptest.NewRequest(http.MethodPut, "/admin/critical/hierarchy/zones/services", bytes.NewBufferString(
		`{"zone_id":"10000000-0000-4000-8000-000000000002","service_type":"managed_service","enabled":true}`,
	))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if capture.updatedServiceType != hierarchyEntity.ZoneServiceTypeManagedService {
		t.Fatalf("expected managed_service, got %q", capture.updatedServiceType)
	}
}
