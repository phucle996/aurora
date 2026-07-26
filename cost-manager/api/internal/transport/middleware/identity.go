package middleware

import (
	"context"
	"strings"

	"cost-manager/api/pkg/apires"
	"cost-manager/api/pkg/pkgcontext"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// [COMMENT]: AuthorizationResolver giữ middleware độc lập với Redis/cache implementation.
type AuthorizationResolver interface {
	Resolve(ctx context.Context, userID uuid.UUID, critical bool) (map[string]struct{}, error)
	ResolveTenant(ctx context.Context, userID uuid.UUID, tenantID uuid.UUID, critical bool) (map[string]struct{}, error)
}

// AuthorizeTenant preserves the canonical five-part permission. Dropping the
// tenant/workspace prefix here would let authority from one tenant escape into
// another tenant's wallet.
func AuthorizeTenant(
	resolver AuthorizationResolver,
	permission string,
	critical bool,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		const op = "middleware.authorize_tenant"
		userID, ok := pkgcontext.GetUserID(c, op)
		if !ok {
			c.Abort()
			return
		}
		// [COMMENT]: Lấy tenantID đã validate UUID từ context
		tenantID, ok := pkgcontext.GetTenantID(c, op)
		if !ok {
			apires.RespondForbidden(c, "verified tenant context is required")
			c.Abort()
			return
		}
		permissions, err := resolver.ResolveTenant(c.Request.Context(), userID, tenantID, critical)
		if err != nil {
			apires.RespondServiceUnavailable(c, "IAM tenant authorization is temporarily unavailable")
			c.Abort()
			return
		}
		required := tenantID.String() + ":00000000-0000-0000-0000-000000000000:" + permission
		if _, allowed := permissions[required]; !allowed {
			apires.RespondForbidden(c, "tenant billing permission is required")
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
