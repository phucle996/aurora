package iamEntity

// DevicePresenceUpdate is one normalized heartbeat accepted at the Shared Redis
// boundary and applied to IAM's durable device-presence projection.
type DevicePresenceUpdate struct {
	DeviceID          string
	LastSeenAt        int64
	LastSeenIP        string
	LastSeenUserAgent string
}
