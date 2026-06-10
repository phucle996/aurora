package iamEntity

import (
	"strings"
	"time"

	"controlplane/internal/security"
)

// UserAccessSession đại diện cho trạng thái hoạt động thực tế (live runtime session) của thiết bị user trong Redis.
type UserAccessSession struct {
	AccessKey         string `json:"access_key"`
	AccessSecretHash  string `json:"access_secret_hash"`
	CurrentJTI        string `json:"current_jti"`
	PreviousJTI       string `json:"previous_jti,omitempty"`
	PreviousIssuedAt  int64  `json:"previous_issued_at,omitempty"`
	CurrentIssuedAt   int64  `json:"current_issued_at,omitempty"`
	TrackedDeviceID   string `json:"tracked_device_id"`
	UserID            string `json:"user_id"`
	Status            string `json:"status,omitempty"`
	Version           int64  `json:"version"`
	LastSeenAt        int64  `json:"last_seen_at"`
	LastSeenIP        string `json:"last_seen_ip,omitempty"`
	LastSeenUserAgent string `json:"last_seen_user_agent,omitempty"`
	LastSeenDirty     bool   `json:"last_seen_dirty,omitempty"`
}

// MatchAccessSession thực hiện so khớp thông tin phiên làm việc hiện tại hoặc trong khoảng thời gian ân hạn (grace window) cho phép xoay vòng JTI.
func MatchAccessSession(session *UserAccessSession, accessKey, rawAccessSecret, jti string, graceWindow time.Duration) bool {
	if session == nil {
		return false
	}
	if strings.TrimSpace(accessKey) == "" || strings.TrimSpace(rawAccessSecret) == "" || strings.TrimSpace(jti) == "" {
		return false
	}
	if session.Status == "revoked" {
		return false
	}
	if session.AccessKey != accessKey {
		return false
	}
	if session.AccessSecretHash != security.HashTokenSHA256(rawAccessSecret) {
		return false
	}
	if session.CurrentJTI == jti {
		return true
	}
	if graceWindow > 0 && session.PreviousJTI != "" && session.PreviousJTI == jti {
		issuedAt := session.PreviousIssuedAt
		if issuedAt > 0 && time.Since(time.Unix(issuedAt, 0)) <= graceWindow {
			return true
		}
	}
	return false
}
