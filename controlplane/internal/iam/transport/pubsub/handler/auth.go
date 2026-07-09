package pubsubHandler

import (
	"context"
	"errors"
	"fmt"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	iamproto "controlplane/internal/iam/transport/rpc/proto"
	"controlplane/internal/observability"
	"controlplane/pkg/logger"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

// [COMMENT]: AuthNatsHandler quản lý các NATS subscription liên quan đến nghiệp vụ Auth
type AuthNatsHandler struct {
	cfg         *config.Config
	authService iamSvcInterface.AuthService
	otel        *observability.OTel
}

// [COMMENT]: NewAuthNatsHandler khởi tạo handler lắng nghe các sự kiện qua NATS Core cho Auth domain
func NewAuthNatsHandler(
	cfg *config.Config,
	authService iamSvcInterface.AuthService,
	otel *observability.OTel,
) *AuthNatsHandler {
	return &AuthNatsHandler{
		cfg:         cfg,
		authService: authService,
		otel:        otel,
	}
}

// [COMMENT]: Subscribe đăng ký luồng xác thực VerifyUserCredentials qua NATS Core.
func (h *AuthNatsHandler) Subscribe(nc *nats.Conn) (*nats.Subscription, error) {
	const subject = "iam.auth.verify_credentials"
	const queueGroup = "iam_auth_service" // Đảm bảo HA bằng cách chia tải qua Queue Group

	sub, err := nc.QueueSubscribe(subject, queueGroup, func(msg *nats.Msg) {
		ctx := context.Background()

		// [COMMENT]: 1. Trích xuất distributed trace context (traceparent) từ NATS headers
		if msg.Header != nil {
			traceparent := msg.Header.Get("traceparent")
			if traceparent != "" {
				ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(msg.Header))
			}
		}

		// [COMMENT]: 2. Khởi tạo server span để giám sát hiệu năng bằng OTel
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

		// [COMMENT]: 3. Giải mã nhị phân request payload (Protobuf)
		var req iamproto.VerifyUserCredentialsRequest
		if err := proto.Unmarshal(msg.Data, &req); err != nil {
			logger.SysError("NATS.VerifyUserCredentials", "Failed to unmarshal request data")
			respondError("invalid request payload")
			return
		}

		// [COMMENT]: 4. Kiểm tra tham số cơ bản
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

		// [COMMENT]: 5. Map dữ liệu sang LoginRequest của Domain Entity
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

		// [COMMENT]: 6. Gọi AuthService xử lý kiểm tra credentials & đăng ký thiết bị dưới DB
		res, err := h.authService.VerifyUserCredentials(ctx, loginReq)
		if err != nil {
			if errors.Is(err, iamTaxonomy.ErrRoleRequired) {
				logger.SysWarn("NATS.VerifyUserCredentials", fmt.Sprintf("Login attempt blocked: user '%s' has no active role assigned in target scope", req.Username))
				respondError(iamTaxonomy.ErrInvalidCredentials.Error())
				return
			}

			if errors.Is(err, iamTaxonomy.ErrUserNotFound) || errors.Is(err, iamTaxonomy.ErrInvalidCredentials) {
				logger.SysWarn("NATS.VerifyUserCredentials", fmt.Sprintf("Login attempt failed: invalid credentials for user '%s'", req.Username))
				respondError(iamTaxonomy.ErrInvalidCredentials.Error())
				return
			}

			logger.SysError("NATS.VerifyUserCredentials", "Failed to verify credentials due to system error")
			respondError("authentication service temporarily unavailable")
			return
		}

		// [COMMENT]: 7. Chuẩn bị response: chỉ trả tenant_id, không trả tenant_code để bảo vệ an toàn định danh
		resp := &iamproto.VerifyUserCredentialsResponse{
			Valid:          res.Valid,
			UserId:         res.UserID,
			RoleId:         res.RoleID,
			Level:          res.Level,
			TenantId:       res.TenantID, // Trả tenant_id như yêu cầu
			ClientDeviceId: res.ClientDeviceID,
			RefreshToken:   res.RefreshToken,
			Username:       res.Username,
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
		return nil, fmt.Errorf("auth_nats_handler: failed to subscribe to %s: %w", subject, err)
	}

	logger.SysInfo("nats", fmt.Sprintf("AuthNATSHandler: successfully subscribed to %s on queue group %s", subject, queueGroup))
	return sub, nil
}
