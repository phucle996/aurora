package middleware

import (
	"context"
	"strings"

	"cost-manager/api/pkg/apires"
	"cost-manager/api/pkg/pkgcontext"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// [COMMENT]: Alias kiểu dữ liệu Identity từ pkgcontext để duy trì tương thích ngược cho middleware package.
type Identity = pkgcontext.Identity

// [COMMENT]: AuthorizationResolver giữ middleware độc lập với Redis/cache implementation.
type AuthorizationResolver interface {
	Resolve(ctx context.Context, userID uuid.UUID, critical bool) (map[string]struct{}, error)
}

// [COMMENT]: RequireIdentity chặn direct-to-service/bypass-ACR và header malformed trước khi vào handler.
// Sử dụng pkgcontext getter để lấy thông tin định danh an toàn đã được nạp bởi ContextInjector.
func RequireIdentity() gin.HandlerFunc {
	return func(c *gin.Context) {
		const op = "middleware.require_identity"
		// [COMMENT]: Kiểm tra bộ identity hợp lệ từ context. Nếu thiếu hoặc invalid, GetIdentity tự động log & respond 401.
		_, ok := pkgcontext.GetIdentity(c, op)
		if !ok {
			c.Abort()
			return
		}
		c.Next()
	}
}

// [COMMENT]: Authorize resolve server-side rồi so khớp exact permission; alias/JWT không mang quyền.
func Authorize(resolver AuthorizationResolver, permission string, critical bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		const op = "middleware.authorize"
		// [COMMENT]: Lấy UserID an toàn từ context thông qua pkgcontext getter
		userID, ok := pkgcontext.GetUserID(c, op)
		if !ok {
			c.Abort()
			return
		}

		// [COMMENT]: Truy vấn danh sách permission của user từ AuthorizationResolver
		permissions, err := resolver.Resolve(c.Request.Context(), userID, critical)
		if err != nil {
			apires.RespondServiceUnavailable(c, "IAM authorization is temporarily unavailable")
			c.Abort()
			return
		}

		// [COMMENT]: So khớp quyền bắt buộc
		if _, allowed := permissions[permission]; !allowed {
			apires.RespondForbidden(c, "billing permission is required")
			c.Abort()
			return
		}
		c.Next()
	}
}

// [COMMENT]: RequireSessionProof bắt backend từ chối mutation nếu Envoy/ACR không xác nhận nonce Ed25519.
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

// [COMMENT]: IdentityFromContext là hàm tiện ích bổ trợ để lấy Identity struct từ context.
func IdentityFromContext(c *gin.Context) (Identity, bool) {
	const op = "middleware.identity_from_context"
	return pkgcontext.GetIdentity(c, op)
}
