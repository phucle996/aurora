// ============================================================================
// 📂 PACKAGE: iam/devicehint - Chuẩn hoá & Giải Quyết Mã Thiết Bị Đầu Cuối
// ============================================================================
//
// 📌 CHỨC NĂNG CHÍNH:
//   - Chuẩn hoá hostname và định danh thiết bị (client_device_id) từ HTTP Headers.
//
// 🎯 QUY TẮC PHÂN GIẢI & CHUẨN HOÁ (SPECIFICATION):
//   1. ResolveDeviceName:
//      - Ưu tiên X-Device-Hostname (hostnameHeader). Nếu không hợp lệ hoặc rỗng,
//        sẽ chuyển sang X-Device-Name (hostnameAlias).
//      - Nếu cả hai đều rỗng hoặc không hợp lệ, trả về mặc định "unknown device".
//   2. SanitizeHostname:
//      - Chỉ giữ lại ký tự chữ cái (a-z, A-Z), số (0-9), và các dấu chấm (.), gạch dưới (_), gạch ngang (-).
//      - Độ dài giới hạn từ 2 đến 64 ký tự. Nếu sau khi lọc ít hơn 2 ký tự hoặc rỗng, trả về "".
//   3. SanitizeClientDeviceID:
//      - Chỉ cho phép các ký tự: chữ cái (a-z, A-Z), số (0-9), dấu chấm (.), gạch dưới (_), gạch ngang (-).
//      - Độ dài tối đa là 128 ký tự. Nếu chứa ký tự lạ hoặc vượt giới hạn, trả về chuỗi rỗng "".
//   4. ResolveClientDeviceID:
//      - Kiểm tra tính hợp lệ của mã định danh do Client gửi lên.
//      - Nếu mã hợp lệ, trả về mã đó kèm nguồn gốc (ProvenanceClient).
//      - Nếu mã rỗng/lỗi, tự sinh một chuỗi UUID ngẫu nhiên an toàn kèm nguồn gốc (ProvenanceServerBootstrap).
//
// 🔒 PHẠM VI (BOUNDARY):
//   - Package thuần túy (pure functions), không tương tác mạng, DB hay cache Redis.
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
