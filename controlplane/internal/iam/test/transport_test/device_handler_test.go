package transport_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	handler "controlplane/internal/iam/transport/http/handler"
	"controlplane/pkg/constant"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type deviceServiceStub struct {
	listResult      *iamSvcInterface.DeviceListResult
	listErr         error
	revokeErr       error
	logoutOthersN   int64
	logoutOthersErr error
	logoutOthersID  string
	logoutAllN      int64
	logoutAllErr    error
}

func (s *deviceServiceStub) ListMyDevices(ctx context.Context, limit int, offset int) (*iamSvcInterface.DeviceListResult, error) {
	return s.listResult, s.listErr
}
func (s *deviceServiceStub) RevokeMyDevice(ctx context.Context, deviceID uuid.UUID, ip *string, userAgent *string) error {
	return s.revokeErr
}
func (s *deviceServiceStub) LogoutOtherDevices(ctx context.Context, currentTrackedDeviceID *uuid.UUID, ip *string, userAgent *string) (int64, error) {
	if currentTrackedDeviceID != nil {
		s.logoutOthersID = currentTrackedDeviceID.String()
	} else {
		s.logoutOthersID = ""
	}
	return s.logoutOthersN, s.logoutOthersErr
}
func (s *deviceServiceStub) LogoutAllDevices(ctx context.Context, ip *string, userAgent *string) (int64, error) {
	return s.logoutAllN, s.logoutAllErr
}
func (s *deviceServiceStub) RegisterLoginDevice(ctx context.Context, device iamEntity.Device) (*iamEntity.Device, error) {
	return nil, nil
}
func (s *deviceServiceStub) TouchDeviceLastSeen(ctx context.Context, deviceID uuid.UUID, ip *string, userAgent *string) error {
	return nil
}
func (s *deviceServiceStub) EvictExcessDevicesIfNeeded(ctx context.Context, userID uuid.UUID, ip *string, userAgent *string) {}
func (s *deviceServiceStub) ReconcileDeviceCap(ctx context.Context, batch int) (int, error) {
	return 0, nil
}
func (s *deviceServiceStub) PublishDeviceAuditAsync(ctx context.Context, userID uuid.UUID, event string, severity string, ip *string, userAgent *string, extras map[string]string) {}

var _ iamSvcInterface.DeviceService = (*deviceServiceStub)(nil)

func withUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		ident := &constant.Identity{
			UserID:          "3b9f2af0-8d95-4380-9d4e-90f0f7191f4a",
			AccessKey:       "runtime-device-1",
			TrackedDeviceID: "177682fc-3e96-4a5a-84eb-b5e9c71af721",
		}
		ctx := context.WithValue(c.Request.Context(), constant.IdentityKey, ident)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func TestDeviceHandlerListMyDevicesUnauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := handler.NewDeviceHandler(&deviceServiceStub{listErr: iamTaxonomy.ErrInvalidSession})
	router.GET("/devices", h.ListMyDevices)

	req := httptest.NewRequest(http.MethodGet, "/devices", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestDeviceHandlerListMyDevicesSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := handler.NewDeviceHandler(&deviceServiceStub{
		listResult: &iamSvcInterface.DeviceListResult{
			Devices: []iamSvcInterface.DevicePresence{{Device: iamEntity.Device{ID: "dev-1"}, IsOnline: true}},
			Total:   1,
		},
	})
	router.GET("/devices", withUser(), h.ListMyDevices)

	req := httptest.NewRequest(http.MethodGet, "/devices?limit=10&offset=0", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestDeviceHandlerRevokeForbidden(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := handler.NewDeviceHandler(&deviceServiceStub{revokeErr: iamTaxonomy.ErrInvalidSession})
	router.POST("/devices/:device_id/revoke", withUser(), h.RevokeMyDevice)

	req := httptest.NewRequest(http.MethodPost, "/devices/3b9f2af0-8d95-4380-9d4e-90f0f7191f4a/revoke", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestDeviceHandlerLogoutOthersSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	svc := &deviceServiceStub{logoutOthersN: 3}
	h := handler.NewDeviceHandler(svc)
	router.POST("/devices/logout-others", withUser(), h.LogoutOtherDevices)

	req := httptest.NewRequest(http.MethodPost, "/devices/logout-others", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if svc.logoutOthersID != "177682fc-3e96-4a5a-84eb-b5e9c71af721" {
		t.Fatalf("logout-others tracked device id = %q", svc.logoutOthersID)
	}
}

func TestDeviceHandlerLogoutOthersRequiresTrackedDeviceID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := handler.NewDeviceHandler(&deviceServiceStub{logoutOthersN: 3})
	router.POST("/devices/logout-others",
		func(c *gin.Context) {
			ident := &constant.Identity{
				UserID:    "3b9f2af0-8d95-4380-9d4e-90f0f7191f4a",
				AccessKey: "runtime-device-1",
			}
			ctx := context.WithValue(c.Request.Context(), constant.IdentityKey, ident)
			c.Request = c.Request.WithContext(ctx)
			c.Next()
		},
		h.LogoutOtherDevices,
	)

	req := httptest.NewRequest(http.MethodPost, "/devices/logout-others", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestDeviceHandlerLogoutAllInternalError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := handler.NewDeviceHandler(&deviceServiceStub{logoutAllErr: errors.New("boom")})
	router.POST("/devices/logout-all", withUser(), h.LogoutAllDevices)

	req := httptest.NewRequest(http.MethodPost, "/devices/logout-all", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}
