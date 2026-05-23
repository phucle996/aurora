package ratelimit

import "strings"

const (
	DefaultPrefix = "rl"

	ScopeIP     = "ip"
	ScopeDevice = "device"
	ScopeUser   = "user"
	ScopeTenant = "tenant"
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
