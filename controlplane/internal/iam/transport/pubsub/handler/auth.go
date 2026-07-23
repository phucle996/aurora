package pubsubHandler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	iamproto "controlplane/internal/iam/transport/rpc/proto"
	"controlplane/internal/observability"
	"controlplane/pkg/logger"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	goredis "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

// [COMMENT]: AuthNatsHandler quản lý các NATS subscription liên quan đến nghiệp vụ Auth
type AuthNatsHandler struct {
	cfg                   *config.Config
	authService           iamSvcInterface.AuthService
	sessionRefreshService iamSvcInterface.SessionRefreshService
	rbacPlatformRepo      iamRepoInterface.RbacPlatformRepository
	redis                 *goredis.Client
	otel                  *observability.OTel
}

// [COMMENT]: NewAuthNatsHandler khởi tạo handler lắng nghe các sự kiện qua NATS Core cho Auth domain
func NewAuthNatsHandler(
	cfg *config.Config,
	authService iamSvcInterface.AuthService,
	sessionRefreshService iamSvcInterface.SessionRefreshService,
	rbacPlatformRepo iamRepoInterface.RbacPlatformRepository,
	redisClient *goredis.Client,
	otel *observability.OTel,
) *AuthNatsHandler {
	return &AuthNatsHandler{
		cfg:                   cfg,
		authService:           authService,
		sessionRefreshService: sessionRefreshService,
		rbacPlatformRepo:      rbacPlatformRepo,
		redis:                 redisClient,
		otel:                  otel,
	}
}

// [COMMENT]: Subscribe đăng ký các luồng xác thực qua NATS Core.
func (h *AuthNatsHandler) Subscribe(nc *nats.Conn) ([]*nats.Subscription, error) {
	const queueGroup = "iam_auth_service" // Đảm bảo HA bằng cách chia tải qua Queue Group
	var subs []*nats.Subscription

	// =========================================================================
	// 1. LUỒNG XÁC THỰC CREDENTIALS (VerifyUserCredentials)
	// =========================================================================
	subCredentials, err := nc.QueueSubscribe("iam.auth.verify_credentials", queueGroup, func(msg *nats.Msg) {
		ctx := context.Background()

		// [COMMENT]: Trích xuất distributed trace context (traceparent) từ NATS headers
		if msg.Header != nil {
			traceparent := msg.Header.Get("traceparent")
			if traceparent != "" {
				ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(msg.Header))
			}
		}

		// [COMMENT]: Khởi tạo server span để giám sát hiệu năng bằng OTel
		var span trace.Span
		if h.otel != nil {
			ctx, span = h.otel.StartServerSpan(ctx, "NATS iam.auth.verify_credentials")
			defer span.End()
			span.SetAttributes(
				attribute.String("messaging.system", "nats"),
				attribute.String("messaging.destination", "iam.auth.verify_credentials"),
			)
		}

		// [COMMENT]: Định nghĩa hàm inline để respond error nhanh
		respondError := func(errMsg string) {
			resp := &iamproto.VerifyUserCredentialsResponse{
				Valid:        false,
				ErrorMessage: errMsg,
			}
			respData, err := proto.Marshal(resp)
			if err != nil {
				logger.SysError("NATS.VerifyUserCredentials", "Failed to marshal error response")
				return
			}
			_ = msg.Respond(respData)
		}

		// [COMMENT]: Giải mã nhị phân request payload (Protobuf)
		var req iamproto.VerifyUserCredentialsRequest
		if err := proto.Unmarshal(msg.Data, &req); err != nil {
			logger.SysError("NATS.VerifyUserCredentials", "Failed to unmarshal request data")
			respondError("invalid request payload")
			return
		}

		// [COMMENT]: Kiểm tra tham số cơ bản
		if req.Username == "" || req.Password == "" {
			logger.SysWarn("NATS.VerifyUserCredentials", "Username and password are required")
			respondError("Username and password are required")
			return
		}

		var clientDeviceID uuid.UUID
		if req.ClientDeviceId != "" {
			parsed, err := uuid.Parse(req.ClientDeviceId)
			if err == nil {
				clientDeviceID = parsed
			}
		}

		// [COMMENT]: Map dữ liệu sang LoginRequest của Domain Entity
		loginReq := iamEntity.LoginRequest{
			Username:        req.Username,
			Password:        req.Password,
			DevicePublicKey: req.PublicKey,
			TrustDevice:     req.TrustDevice,
			DeviceName:      req.DeviceName,
			ClientDeviceID:  clientDeviceID,
			TenantDomain:    req.TenantDomain,
			RemoteIP:        req.ClientIp,
			UserAgent:       req.UserAgent,
		}

		// [COMMENT]: Gọi AuthService xử lý kiểm tra credentials & đăng ký thiết bị dưới DB
		res, err := h.authService.VerifyUserCredentials(ctx, loginReq)
		if err != nil {
			if errors.Is(err, iamTaxonomy.ErrVerificationRequired) {
				// [COMMENT]: Password đã đúng và mail đã queue/cooldown; ACR dùng code ổn định này để không cấp session.
				respondError("ACCOUNT_VERIFICATION_REQUIRED")
				return
			}
			if errors.Is(err, iamTaxonomy.ErrRoleRequired) {
				logger.SysWarn("NATS.VerifyUserCredentials", "Login attempt blocked: no active role assigned in target scope")
				respondError(iamTaxonomy.ErrInvalidCredentials.Error())
				return
			}

			if errors.Is(err, iamTaxonomy.ErrUserNotFound) || errors.Is(err, iamTaxonomy.ErrInvalidCredentials) {
				logger.SysWarn("NATS.VerifyUserCredentials", "Login attempt failed: invalid credentials")
				respondError(iamTaxonomy.ErrInvalidCredentials.Error())
				return
			}

			logger.SysError("NATS.VerifyUserCredentials", "Failed to verify credentials due to system error")
			respondError("authentication service temporarily unavailable")
			return
		}

		// [COMMENT]: Chuẩn bị response: chỉ trả tenant_id, không trả tenant_code để bảo vệ an toàn định danh
		resp := &iamproto.VerifyUserCredentialsResponse{
			Valid:                 res.Valid,
			UserId:                res.UserID,
			RoleId:                res.RoleID,
			Level:                 res.Level,
			TenantId:              res.TenantID, // Trả tenant_id như yêu cầu
			ClientDeviceId:        res.ClientDeviceID,
			RefreshToken:          res.RefreshToken,
			RefreshTokenExpiresAt: res.RefreshTokenExpiresAt.Unix(),
			Username:              res.Username,
			ClientProofPublicKey:  res.ClientProofPublicKey,
		}

		respData, err := proto.Marshal(resp)
		if err != nil {
			logger.SysError("NATS.VerifyUserCredentials", "Failed to marshal response payload")
			return
		}

		if err := msg.Respond(respData); err != nil {
			logger.SysError("NATS.VerifyUserCredentials", "Failed to send NATS response")
		}
	})
	if err != nil {
		return nil, fmt.Errorf("auth_nats_handler: failed to subscribe to iam.auth.verify_credentials: %w", err)
	}
	subs = append(subs, subCredentials)
	logger.SysInfo("nats", fmt.Sprintf("AuthNATSHandler: successfully subscribed to iam.auth.verify_credentials on queue group %s", queueGroup))

	// =========================================================================
	// 2. LUỒNG XÁC THỰC OPAQUE REFRESH TOKEN (VerifyOpaqueRefreshToken)
	// =========================================================================
	subVerifyToken, err := nc.QueueSubscribe("iam.auth.verify_opaque_token", queueGroup, func(msg *nats.Msg) {
		ctx := context.Background()

		if msg.Header != nil {
			traceparent := msg.Header.Get("traceparent")
			if traceparent != "" {
				ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(msg.Header))
			}
		}

		var span trace.Span
		if h.otel != nil {
			ctx, span = h.otel.StartServerSpan(ctx, "NATS iam.auth.verify_opaque_token")
			defer span.End()
			span.SetAttributes(
				attribute.String("messaging.system", "nats"),
				attribute.String("messaging.destination", "iam.auth.verify_opaque_token"),
			)
		}

		respondError := func(errMsg string) {
			resp := &iamproto.VerifyOpaqueRefreshTokenResponse{
				Valid:        false,
				ErrorMessage: errMsg,
			}
			respData, err := proto.Marshal(resp)
			if err != nil {
				logger.SysError("NATS.VerifyOpaqueRefreshToken", "Failed to marshal error response")
				return
			}
			_ = msg.Respond(respData)
		}

		var req iamproto.VerifyOpaqueRefreshTokenRequest
		if err := proto.Unmarshal(msg.Data, &req); err != nil {
			logger.SysError("NATS.VerifyOpaqueRefreshToken", "Failed to unmarshal request data")
			respondError("invalid request payload")
			return
		}

		userUUID, err := uuid.Parse(req.UserId)
		if err != nil {
			respondError(fmt.Sprintf("invalid user id format: %v", err))
			return
		}

		var tenantUUIDPtr *uuid.UUID
		if req.TenantId != nil && *req.TenantId != "" {
			parsed, err := uuid.Parse(*req.TenantId)
			if err != nil {
				respondError(fmt.Sprintf("invalid tenant id format: %v", err))
				return
			}
			tenantUUIDPtr = &parsed
		}

		res, err := h.sessionRefreshService.VerifyOpaqueRefreshToken(ctx, req.RefreshToken, tenantUUIDPtr, userUUID)
		if err != nil {
			respondError(err.Error())
			return
		}

		resp := &iamproto.VerifyOpaqueRefreshTokenResponse{
			Valid:    res.Valid,
			UserId:   res.UserID,
			TenantId: res.TenantID,
			Role:     res.RoleID,
			Level:    res.Level,
			Username: res.Username,
		}

		respData, err := proto.Marshal(resp)
		if err != nil {
			logger.SysError("NATS.VerifyOpaqueRefreshToken", "Failed to marshal response payload")
			return
		}

		if err := msg.Respond(respData); err != nil {
			logger.SysError("NATS.VerifyOpaqueRefreshToken", "Failed to send NATS response")
		}
	})
	if err != nil {
		return nil, fmt.Errorf("auth_nats_handler: failed to subscribe to iam.auth.verify_opaque_token: %w", err)
	}
	subs = append(subs, subVerifyToken)
	logger.SysInfo("nats", fmt.Sprintf("AuthNATSHandler: successfully subscribed to iam.auth.verify_opaque_token on queue group %s", queueGroup))

	// =========================================================================
	// 3. LUỒNG THU HỒI REFRESH TOKEN (RevokeOpaqueRefreshToken)
	// =========================================================================
	subRevokeToken, err := nc.QueueSubscribe("iam.auth.revoke_opaque_token", queueGroup, func(msg *nats.Msg) {
		ctx := context.Background()

		if msg.Header != nil {
			traceparent := msg.Header.Get("traceparent")
			if traceparent != "" {
				ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(msg.Header))
			}
		}

		var span trace.Span
		if h.otel != nil {
			ctx, span = h.otel.StartServerSpan(ctx, "NATS iam.auth.revoke_opaque_token")
			defer span.End()
			span.SetAttributes(
				attribute.String("messaging.system", "nats"),
				attribute.String("messaging.destination", "iam.auth.revoke_opaque_token"),
			)
		}

		var req iamproto.RevokeOpaqueRefreshTokenRequest
		if err := proto.Unmarshal(msg.Data, &req); err != nil {
			logger.SysError("NATS.RevokeOpaqueRefreshToken", "Failed to unmarshal request data")
			return
		}

		err := h.sessionRefreshService.RevokeOpaqueRefreshToken(ctx, req.RefreshToken)
		if err != nil {
			logger.SysError("NATS.RevokeOpaqueRefreshToken", fmt.Sprintf("Failed to revoke refresh token: %s", err.Error()))
		}

		resp := &iamproto.RevokeOpaqueRefreshTokenResponse{}
		respData, _ := proto.Marshal(resp)
		_ = msg.Respond(respData)
	})
	if err != nil {
		return nil, fmt.Errorf("auth_nats_handler: failed to subscribe to iam.auth.revoke_opaque_token: %w", err)
	}
	subs = append(subs, subRevokeToken)
	logger.SysInfo("nats", fmt.Sprintf("AuthNATSHandler: successfully subscribed to iam.auth.revoke_opaque_token on queue group %s", queueGroup))

	// =========================================================================
	// 4. RESOLVE BILLING AUTHORIZATION CHO COST MANAGER
	// =========================================================================
	// [COMMENT]: ACR chỉ xác thực alias. Cost lấy permission binary từ shared L2 hoặc request IAM khi miss.
	subBillingAuthorization, err := nc.QueueSubscribe("iam.authorization.billing.get", queueGroup, func(msg *nats.Msg) {
		respondError := func(message string) {
			if msg.Reply == "" {
				return
			}
			response := nats.NewMsg(msg.Reply)
			response.Header.Set("Aurora-Error", message)
			_ = nc.PublishMsg(response)
		}

		userID, err := uuid.Parse(strings.TrimSpace(string(msg.Data)))
		if err != nil || h.rbacPlatformRepo == nil || h.redis == nil {
			respondError("authorization context is invalid")
			return
		}

		// [COMMENT]: Queue callback có deadline ngắn để một DB stall không giữ dispatcher NATS vô hạn.
		ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
		defer cancel()

		dataKey := fmt.Sprintf("{iam:authz:billing:%s}:data", userID)
		generationKey := fmt.Sprintf("{iam:authz:billing:%s}:generation", userID)
		dataGenerationKey := fmt.Sprintf("{iam:authz:billing:%s}:data_generation", userID)
		var responseBinary []byte
		for attempt := 0; attempt < 2; attempt++ {
			expectedGeneration, generationErr := h.redis.Get(ctx, generationKey).Result()
			if errors.Is(generationErr, goredis.Nil) {
				expectedGeneration = "0"
			} else if generationErr != nil {
				respondError("authorization cache is unavailable")
				return
			}

			binaryEntry, loadErr := h.rbacPlatformRepo.GetUserRolePermissions(ctx, userID)
			if loadErr != nil {
				logger.SysError("NATS.BillingAuthorization", "Failed to load user authorization")
				respondError("authorization service unavailable")
				return
			}
			var roleEntry iamproto.RoleEntry
			if proto.Unmarshal(binaryEntry, &roleEntry) != nil {
				respondError("authorization snapshot is invalid")
				return
			}

			// [COMMENT]: Quyền workspace cụ thể không được nâng thành quyền Billing global; chỉ platform nil/wildcard hợp lệ.
			permissions := make([]string, 0, len(roleEntry.Permissions))
			seen := make(map[string]struct{}, len(roleEntry.Permissions))
			for _, raw := range roleEntry.Permissions {
				parts := strings.Split(raw, ":")
				permission := ""
				switch {
				case len(parts) == 3 && parts[0] == "billing":
					permission = raw
				case len(parts) == 5 && parts[2] == "billing" &&
					(parts[1] == "*" || parts[1] == uuid.Nil.String()):
					permission = strings.Join(parts[2:], ":")
				default:
					continue
				}
				if _, exists := seen[permission]; !exists {
					seen[permission] = struct{}{}
					permissions = append(permissions, permission)
				}
			}
			sort.Strings(permissions)
			if len(permissions) == 0 {
				respondError("billing permission is required")
				return
			}
			responseBinary, err = proto.Marshal(&iamproto.RoleEntry{Permissions: permissions})
			if err != nil {
				respondError("authorization snapshot is invalid")
				return
			}

			// [COMMENT]: Generation fence loại stale write nếu RBAC đổi trong lúc callback đang query PostgreSQL.
			written, writeErr := h.redis.Eval(ctx, `
				local current = redis.call("GET", KEYS[2]) or "0"
				if current ~= ARGV[2] then return 0 end
				redis.call("SET", KEYS[1], ARGV[1], "EX", ARGV[3])
				redis.call("SET", KEYS[3], ARGV[2], "EX", ARGV[3])
				if redis.call("EXISTS", KEYS[2]) == 0 then
					redis.call("SET", KEYS[2], ARGV[2], "EX", ARGV[4])
				end
				return 1
			`, []string{dataKey, generationKey, dataGenerationKey},
				responseBinary, expectedGeneration, int64(120), int64(86400)).Int()
			if writeErr != nil {
				respondError("authorization cache is unavailable")
				return
			}
			if written == 1 {
				if msg.Reply != "" {
					_ = nc.Publish(msg.Reply, responseBinary)
				}
				return
			}
		}
		respondError("authorization changed while it was being resolved")
	})
	if err != nil {
		return nil, fmt.Errorf("auth_nats_handler: failed to subscribe to iam.authorization.billing.get: %w", err)
	}
	subs = append(subs, subBillingAuthorization)
	logger.SysInfo("nats", fmt.Sprintf("AuthNATSHandler: subscribed to iam.authorization.billing.get on queue group %s", queueGroup))

	return subs, nil
}
