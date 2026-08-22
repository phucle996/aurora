package middleware

import (
	"strings"

	"cost-manager/api/pkg/apires"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RequireSessionProof là middleware kiểm tra bằng chứng phiên làm việc xác thực một lần (One-Time Session Proof).
// Được áp dụng cho các API Mutation tài chính nhạy cảm (như Nạp tiền ví, Ban hành bảng giá, v.v.).
//
// Cơ chế bảo vệ:
// - Envoy / ACR Security Gateway xác thực chữ ký số Ed25519 từ Challenge Nonce của Client và chèn các Header tin cậy:
//   * `x-session-proof-verified`: "true"
//   * `x-session-proof-challenge-id`: UUID hợp lệ của Challenge
// - Nếu thiếu Header, không phải "true" hoặc Challenge ID không hợp lệ, middleware sẽ từ chối ngay lập tức (Fail-Closed) với HTTP 403 Forbidden.
func RequireSessionProof() gin.HandlerFunc {
	return func(c *gin.Context) {
		challengeID, err := uuid.Parse(strings.TrimSpace(c.GetHeader("x-session-proof-challenge-id")))
		if c.GetHeader("x-session-proof-verified") != "true" || err != nil || challengeID == uuid.Nil {
			apires.RespondForbidden(c, "verified session proof is required")
			c.Abort()
			return
		}
		c.Next()
	}
}
