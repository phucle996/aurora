// ============================================================================
// 🛡️ ARCHITECTURAL & SYSTEM CONTRACTS - ADMIN API KEY AUTH
// ============================================================================
//
// 🤝 1. SYSTEM CONTRACT
//   - Accepted Cookies: Yêu cầu bắt buộc 3 cookie (access_token, access_key,
//     access_secret). Trả về generic 401 nếu thiếu để chống user enumeration.
//   - Token Validation: Phân tích token bằng danh sách candidates hỗ trợ secret rotation.
//   - Fail-Closed: Nếu thiếu callback khởi tạo hoặc các cấu hình bắt buộc, middleware
//     sẽ tự động từ chối request với mã lỗi 503 Service Unavailable để tự bảo vệ.
//
// 📖 2. SOURCE OF TRUTH
//   - Callback và dependency được app/module.go đọc từ cấu hình và truyền vào
//     InitAdminAPIKeyAuth duy nhất một lần trong quá trình bootstrap hệ thống.
//
// 💡 3. CONTEXT INJECTION (OPTION B)
//   - Di chuyển toàn bộ dữ liệu từ Gin context sang Go standard context.
//   - Nhóm toàn bộ claims (Subject, Level, ZoneID, v.v.) vào struct Identity duy nhất
//     để triệt tiêu sự phình to của context chains.
//
// ============================================================================

package middleware

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"controlplane/internal/cacheengine"
	iamproto "controlplane/internal/iam/transport/rpc/proto"
	"controlplane/internal/security"
	apires "controlplane/pkg/apires"
	"controlplane/pkg/constant"
	"controlplane/pkg/logger"

	"github.com/gin-gonic/gin"
	"google.golang.org/protobuf/proto"
)

// adminAPIKeyRotationTriggerTTL là TTL (khóa chặn) của cờ yêu cầu xoay vòng khóa.
const adminAPIKeyRotationTriggerTTL = 10 * time.Minute

// adminAPIKeyAuthState giữ dependency runtime cho admin API-key middleware.
var adminAPIKeyAuthState = struct {
	mu       sync.RWMutex
	registry *cacheengine.CacheRegistry
}{}

type adminAuthOptions struct {
	injectAccessKey    bool
	injectAccessSecret bool
	injectTokenJTI     bool
}

// AdminAuthOption điều khiển dữ liệu nào được inject vào Identity struct.
type AdminAuthOption func(*adminAuthOptions)

// InitAdminAPIKeyAuth khởi tạo dependency cho AdminAPIKeyAuth.
func InitAdminAPIKeyAuth(registry *cacheengine.CacheRegistry) error {
	if registry == nil {
		return errors.New("admin api key auth: cache registry is required")
	}

	adminAPIKeyAuthState.mu.Lock()
	adminAPIKeyAuthState.registry = registry
	adminAPIKeyAuthState.mu.Unlock()
	return nil
}

// WithInjectAdminAccessKey chỉ định middleware tiêm access_key của Admin vào Identity context struct.
func WithInjectAdminAccessKey() AdminAuthOption {
	return func(options *adminAuthOptions) {
		if options != nil {
			options.injectAccessKey = true
		}
	}
}

// WithInjectAdminAccessSecret chỉ định middleware tiêm access_secret của Admin vào Identity context struct.
func WithInjectAdminAccessSecret() AdminAuthOption {
	return func(options *adminAuthOptions) {
		if options != nil {
			options.injectAccessSecret = true
		}
	}
}

// AdminAPIKeyAuth xác thực admin runtime session bằng 3 cookie.
func AdminAPIKeyAuth(opts ...AdminAuthOption) gin.HandlerFunc {
	options := adminAuthOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	return func(c *gin.Context) {
		isLogout := c.Request.URL.Path == "/admin/auth/logout"

		// Đọc giá trị các cookie bảo mật của Admin
		tokenCookie, errToken := c.Cookie(constant.AdminAPITokenName)
		accessKeyCookie, errKey := c.Cookie(constant.AccessKeyName)
		accessSecretCookie, errSecret := c.Cookie(constant.AccessSecretName)

		token := strings.TrimSpace(tokenCookie)
		accessKey := strings.TrimSpace(accessKeyCookie)
		accessSecret := strings.TrimSpace(accessSecretCookie)

		if errToken != nil || errKey != nil || errSecret != nil || token == "" || accessKey == "" || accessSecret == "" {
			if isLogout {
				c.Next()
				return
			}
			logger.HandlerWarn(c, "admin.auth.cookie", nil, "missing or empty admin cookie")
			abortAdminUnauthorized(c)
			return
		}

		adminAPIKeyAuthState.mu.RLock()
		registry := adminAPIKeyAuthState.registry
		adminAPIKeyAuthState.mu.RUnlock()
		if registry == nil {
			if isLogout {
				c.Next()
				return
			}
			abortAdminAuthUnavailable(c)
			return
		}

		// [ignoring loop detection]
		// --------------------------------------------------------------------
		// Parse JWT bằng Vault.
		// --------------------------------------------------------------------
		claims, parseErr := security.Parse(token, nil)
		if parseErr != nil {
			if errors.Is(parseErr, security.ErrTokenExpired) {
				// Kích hoạt cờ xoay khóa trực tiếp trên L2 Cache
				_ = registry.L2.Set(c.Request.Context(), "iam:admin_key_rotation:required", "1", 1, adminAPIKeyRotationTriggerTTL)
			}
			if isLogout {
				c.Next()
				return
			}
			abortAdminUnauthorized(c)
			return
		}

		// Kiểm tra access_key claim khớp cookie gửi lên để ngăn chặn token hijacking
		if strings.TrimSpace(claims.AccessKey) == "" || claims.AccessKey != accessKey {
			if isLogout {
				c.Next()
				return
			}
			abortAdminUnauthorized(c)
			return
		}

		// --------------------------------------------------------------------
		// Xác thực access_secret trực tiếp thông qua Redis L2 Cache.
		// Bổ sung phân vùng theo Zone ID (Zone-scoped) để triệt tiêu bypass.
		// --------------------------------------------------------------------
		zoneID := strings.TrimSpace(claims.ZoneID)
		if zoneID == "" {
			// Nếu rỗng, fallback về global để đảm bảo tương thích ngược
			zoneID = "global"
		}
		payload, _, exists, err := registry.L2.Get(c.Request.Context(), "admin_access_session:"+accessKey+":"+zoneID)
		if err != nil {
			if isLogout {
				c.Next()
				return
			}
			abortAdminAuthUnavailable(c)
			return
		}
		if !exists {
			if isLogout {
				c.Next()
				return
			}
			abortAdminUnauthorized(c)
			return
		}

		session := &iamproto.AdminAccessSession{}
		err = proto.Unmarshal(payload, session)
		if err != nil {
			if isLogout {
				c.Next()
				return
			}
			abortAdminAuthUnavailable(c)
			return
		}

		incomingHash := security.HashTokenSHA256(accessSecret)
		if session.AccessSecretHash != incomingHash {
			if isLogout {
				c.Next()
				return
			}
			abortAdminUnauthorized(c)
			return
		}

		// --------------------------------------------------------------------
		// Inject thông tin định danh vào struct Identity duy nhất trong Go context.
		// --------------------------------------------------------------------
		ident := &constant.Identity{
			UserID: claims.Subject,
			Level:  claims.Level,
			ZoneID: claims.ZoneID,
		}

		if options.injectAccessKey {
			ident.AccessKey = accessKey
		}
		if options.injectAccessSecret {
			ident.AccessSecret = accessSecret
		}
		if options.injectTokenJTI {
			ident.JTI = claims.TokenID
		}

		// Ghi nhận Identity vào Go standard context
		goCtx := context.WithValue(c.Request.Context(), constant.IdentityKey, ident)
		c.Request = c.Request.WithContext(goCtx)

		// [COMMENT]: Không ghi nhận header X-Session-Expires-In theo thiết kế mới, client sẽ tự dựa trên JWT expiration và cơ chế trượt tại Envoy.

		c.Next()
	}
}

func abortAdminUnauthorized(c *gin.Context) {
	apires.RespondUnauthorized(c, "unauthorized")
	c.Abort()
}

func abortAdminAuthUnavailable(c *gin.Context) {
	apires.RespondServiceUnavailable(c, "authentication temporarily unavailable")
	c.Abort()
}
