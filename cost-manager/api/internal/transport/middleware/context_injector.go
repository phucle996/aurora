package middleware

import (
	"strings"

	"cost-manager/api/pkg/logger"
	"cost-manager/api/pkg/pkgcontext"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// [COMMENT]: ContextInjector là middleware toàn cục chịu trách nhiệm trích xuất các thông tin định danh
// (User ID, Zone ID, Username, Tenant ID) được truyền từ Envoy/ACR qua HTTP Headers và lưu trực tiếp vào Gin Context.
// Giúp tối ưu CPU/Memory bằng cách parse UUID 1 lần duy nhất ở đầu pipeline.
func ContextInjector() gin.HandlerFunc {
	return func(c *gin.Context) {
		// [COMMENT]: 1. Parse và inject User ID (x-user-id)
		if idStr := strings.TrimSpace(c.GetHeader("x-user-id")); idStr != "" {
			if id, err := uuid.Parse(idStr); err == nil {
				c.Set(pkgcontext.CtxUserID, id)
				// [COMMENT]: Gán KeyUserID vào Gin Context để logger tự động thêm user_id vào hệ thống log
				c.Set(logger.KeyUserID, idStr)
			} else {
				// [COMMENT]: Lưu lại error để pkgcontext getter phát hiện format không hợp lệ
				c.Set(pkgcontext.CtxUserID, err)
			}
		}

		// [COMMENT]: 2. Parse và inject Zone ID (x-zone-id)
		if idStr := strings.TrimSpace(c.GetHeader("x-zone-id")); idStr != "" {
			if id, err := uuid.Parse(idStr); err == nil {
				c.Set(pkgcontext.CtxZoneID, id)
			} else {
				// [COMMENT]: Lưu lại error nếu UUID zone-id bị lỗi
				c.Set(pkgcontext.CtxZoneID, err)
			}
		}

		// [COMMENT]: 3. Inject Username (x-user-name)
		if username := strings.TrimSpace(c.GetHeader("x-user-name")); username != "" {
			c.Set(pkgcontext.CtxUserName, username)
		}

		// [COMMENT]: 4. Inject Tenant ID (x-tenant-id) nếu có
		if tenantID := strings.TrimSpace(c.GetHeader("x-tenant-id")); tenantID != "" {
			if tenantID == "platform" {
				c.Set(pkgcontext.CtxTenantID, tenantID)
			} else if id, err := uuid.Parse(tenantID); err == nil && id != uuid.Nil {
				c.Set(pkgcontext.CtxTenantID, id)
			} else {
				c.Set(pkgcontext.CtxTenantID, errInvalidTenantContext{})
			}
		}

		// [COMMENT]: Chuyển tiếp request sang các handler/middleware kế tiếp trong chuỗi xử lý
		c.Next()
	}
}

type errInvalidTenantContext struct{}

func (errInvalidTenantContext) Error() string {
	return "invalid tenant identity context"
}
