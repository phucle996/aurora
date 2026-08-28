package iamEntity

import (
	"time"

	"github.com/google/uuid"
)

// PersonalDeviceListItem is the flat durable projection returned by the
// platform-authorized personal device audit workflow.
type PersonalDeviceListItem struct {
	ID         uuid.UUID
	DeviceName string
	IsOnline   bool
	LastSeenAt *time.Time
	LastIP     *string
	LastUA     *string
	RevokedAt  *time.Time
}

// PersonalDeviceListResult belongs only to the platform-authorized personal
// device audit workflow.
type PersonalDeviceListResult struct {
	Devices []PersonalDeviceListItem
	Total   int64
}
