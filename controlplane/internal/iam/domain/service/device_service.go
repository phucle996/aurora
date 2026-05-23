package iamSvcInterface

import (
	"context"
	"time"

	"controlplane/internal/iam/domain/entity"
)

type DevicePresence struct {
	Device     iamEntity.Device
	IsOnline   bool
	LastSeenAt *time.Time
	LastIP     *string
	LastUA     *string
}

type DeviceListResult struct {
	Devices []DevicePresence
	Total   int64
}

type DeviceService interface {
	ListMyDevices(ctx context.Context, userID string, limit int, offset int) (*DeviceListResult, error)
	RevokeMyDevice(ctx context.Context, userID string, deviceID string, ip *string, userAgent *string) error
	LogoutOtherDevices(ctx context.Context, userID string, currentTrackedDeviceID string, ip *string, userAgent *string) (int64, error)
	LogoutAllDevices(ctx context.Context, userID string, ip *string, userAgent *string) (int64, error)
}
