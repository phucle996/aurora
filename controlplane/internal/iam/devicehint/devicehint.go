// Package iamDeviceHint implements client-supplied device naming + persistent
// client device identifier rules per idea v0.3
// (controlplane/internal/iam/docs/idea/iam-device-name-from-client-header-idea.md).
//
// Boundary: this package is pure (no Redis/DB). It is consumed by service
// layer to derive sanitized device_name and client_device_id from headers.
package iamDeviceHint

import (
	"strings"

	"github.com/google/uuid"
)

const DefaultDeviceName = "unknown device"
const MaxDeviceNameLen = 64
const MaxClientDeviceIDLen = 128

const (
	HeaderDeviceHostname = "X-Device-Hostname"
	HeaderDeviceNameAlt  = "X-Device-Name"
	HeaderClientDeviceID = "X-Client-Device-Id"
)

func SanitizeHostname(raw string) string {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" {
		return ""
	}
	var builder strings.Builder
	builder.Grow(len(cleaned))
	for _, r := range cleaned {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			builder.WriteRune(r)
		default:
		}
	}
	candidate := builder.String()
	if len(candidate) > MaxDeviceNameLen {
		candidate = candidate[:MaxDeviceNameLen]
	}
	if len(candidate) < 2 {
		return ""
	}
	return candidate
}

func ResolveDeviceName(hostnameHeader, hostnameAlias string) string {
	if name := SanitizeHostname(hostnameHeader); name != "" {
		return name
	}
	if name := SanitizeHostname(hostnameAlias); name != "" {
		return name
	}
	return DefaultDeviceName
}

func SanitizeClientDeviceID(raw string) string {
	cleaned := strings.TrimSpace(raw)
	if cleaned == "" {
		return ""
	}
	if len(cleaned) > MaxClientDeviceIDLen {
		return ""
	}
	for _, r := range cleaned {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
		default:
			return ""
		}
	}
	return cleaned
}

type Provenance string

const (
	ProvenanceClient          Provenance = "client"
	ProvenanceServerBootstrap Provenance = "server-bootstrap"
)

func ResolveClientDeviceID(rawHeader string) (string, Provenance) {
	if cleaned := SanitizeClientDeviceID(rawHeader); cleaned != "" {
		return cleaned, ProvenanceClient
	}
	return uuid.NewString(), ProvenanceServerBootstrap
}
