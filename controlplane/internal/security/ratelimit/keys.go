package ratelimit

import "strings"

const (
	DefaultPrefix = "rl"

	ScopeIP       = "ip"
	ScopeDevice   = "device"
	ScopeUser     = "user"
	ScopeTenant   = "tenant"
	ScopeIPUser   = "ip_user"
	ScopeIPDevice = "ip_device"
)

// Key builds a consistent Redis key for a scope + identifier.
func Key(prefix, scope, id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	if prefix == "" {
		prefix = DefaultPrefix
	}
	if scope == "" {
		scope = "generic"
	}
	return prefix + ":" + scope + ":" + id
}

func KeyIP(prefix, ip string) string {
	return Key(prefix, ScopeIP, ip)
}

func KeyDevice(prefix, deviceID string) string {
	return Key(prefix, ScopeDevice, deviceID)
}

func KeyUser(prefix, userID string) string {
	return Key(prefix, ScopeUser, userID)
}

func KeyTenant(prefix, tenantID string) string {
	return Key(prefix, ScopeTenant, tenantID)
}

// KeyIPUser builds a composite subject key to reduce NAT false positives.
// Ref: internal/security/docs/spec/controlplane-anti-probing-v1-spec.md#4.3
func KeyIPUser(prefix, ip, userID string) string {
	ip = strings.TrimSpace(ip)
	userID = strings.TrimSpace(userID)
	if ip == "" || userID == "" {
		return ""
	}
	return Key(prefix, ScopeIPUser, ip+":"+userID)
}

// KeyIPDevice builds identity-aware composite key for device-scoped enforcement.
// Ref: internal/security/docs/spec/controlplane-anti-probing-v1-spec.md#3.2
func KeyIPDevice(prefix, ip, deviceID string) string {
	ip = strings.TrimSpace(ip)
	deviceID = strings.TrimSpace(deviceID)
	if ip == "" || deviceID == "" {
		return ""
	}
	return Key(prefix, ScopeIPDevice, ip+":"+deviceID)
}
