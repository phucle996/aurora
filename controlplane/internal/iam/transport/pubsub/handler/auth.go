package pubsubHandler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	iamproto "controlplane/internal/iam/transport/rpc/proto"
	"controlplane/internal/observability"
	"controlplane/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

const (
	verifyCredentialsChannel     = "iam.auth.verify_credentials"
	verifyCredentialsReplyPrefix = "iam.auth.verify_credentials.reply."

	verifyOpaqueTokenChannel     = "iam.auth.verify_opaque_token"
	verifyOpaqueTokenReplyPrefix = "iam.auth.verify_opaque_token.reply."

	revokeOpaqueTokenChannel     = "iam.auth.revoke_opaque_token"
	revokeOpaqueTokenReplyPrefix = "iam.auth.revoke_opaque_token.reply."
)

// [COMMENT]: AuthRedisHandler quản lý các Redis PubSub subscription liên quan đến nghiệp vụ Auth
type AuthRedisHandler struct {
	cfg                   *config.Config
	sharedRedis           *goredis.Client
	authService           iamSvcInterface.AuthService
	sessionRefreshService iamSvcInterface.SessionRefreshService
	otel                  *observability.OTel

	cancel context.CancelFunc
	pubsub *goredis.PubSub
	loopWG sync.WaitGroup
	workWG sync.WaitGroup
	slots  chan struct{}
}

// [COMMENT]: NewAuthRedisHandler khởi tạo handler lắng nghe các sự kiện qua Shared Redis PubSub cho Auth domain
func NewAuthRedisHandler(
	cfg *config.Config,
	sharedRedis *goredis.Client,
	authService iamSvcInterface.AuthService,
	sessionRefreshService iamSvcInterface.SessionRefreshService,
	otel *observability.OTel,
) (*AuthRedisHandler, error) {
	if sharedRedis == nil || authService == nil || sessionRefreshService == nil {
		return nil, errors.New("auth Redis handler requires Shared Redis, AuthService and SessionRefreshService")
	}
	return &AuthRedisHandler{
		cfg:                   cfg,
		sharedRedis:           sharedRedis,
		authService:           authService,
		sessionRefreshService: sessionRefreshService,
		otel:                  otel,
		slots:                 make(chan struct{}, 64),
	}, nil
}

// [COMMENT]: Start bắt đầu đăng ký các channel xác thực qua Shared Redis PubSub.
func (h *AuthRedisHandler) Start() error {
	if h == nil {
		return errors.New("auth Redis handler is nil")
	}
	ctx, cancel := context.WithCancel(context.Background())
	pubsub := h.sharedRedis.Subscribe(ctx,
		verifyCredentialsChannel,
		verifyOpaqueTokenChannel,
		revokeOpaqueTokenChannel,
	)
	if _, err := pubsub.Receive(ctx); err != nil {
		cancel()
		_ = pubsub.Close()
		return fmt.Errorf("subscribe Auth Redis channels: %w", err)
	}
	h.cancel = cancel
	h.pubsub = pubsub

	h.loopWG.Add(1)
	go func() {
		defer h.loopWG.Done()
		channel := pubsub.Channel(goredis.WithChannelSize(256))
		for {
			select {
			case <-ctx.Done():
				return
			case message, ok := <-channel:
				if !ok {
					return
				}
				select {
				case h.slots <- struct{}{}:
					h.workWG.Add(1)
					go func(msg *goredis.Message) {
						defer h.workWG.Done()
						defer func() { <-h.slots }()
						h.dispatch(msg)
					}(message)
				default:
					// [COMMENT]: CP replica bị quá tải sẽ skip tin nhắn mà không làm nghẽn DB
				}
			}
		}
	}()
	return nil
}

func (h *AuthRedisHandler) dispatch(msg *goredis.Message) {
	if msg == nil {
		return
	}
	payload := []byte(msg.Payload)
	switch msg.Channel {
	case verifyCredentialsChannel:
		h.handleVerifyCredentials(payload)
	case verifyOpaqueTokenChannel:
		h.handleVerifyOpaqueToken(payload)
	case revokeOpaqueTokenChannel:
		h.handleRevokeOpaqueToken(payload)
	}
}

// =========================================================================
// 1. LUỒNG XÁC THỰC CREDENTIALS (VerifyUserCredentials)
// =========================================================================
func (h *AuthRedisHandler) handleVerifyCredentials(payload []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var span trace.Span
	if h.otel != nil {
		ctx, span = h.otel.StartServerSpan(ctx, "Redis iam.auth.verify_credentials")
		defer span.End()
		span.SetAttributes(
			attribute.String("messaging.system", "redis"),
			attribute.String("messaging.destination", verifyCredentialsChannel),
		)
	}

	// [COMMENT]: PubSub fan-out tới mọi CP replica, vì vậy request_id là bắt buộc để chỉ một
	// replica được phép chạm DB và phát refresh token cho cùng một lần đăng nhập.
	if len(payload) <= 16 {
		logger.SysWarn("Redis.VerifyUserCredentials", "Missing request id envelope")
		return
	}
	requestID, err := uuid.FromBytes(payload[:16])
	if err != nil || requestID == uuid.Nil {
		logger.SysWarn("Redis.VerifyUserCredentials", "Invalid request id envelope")
		return
	}
	reqData := payload[16:]
	replyChannel := verifyCredentialsReplyPrefix + requestID.String()
	lockKey := "iam:auth:dispatch:verify_credentials:" + requestID.String()
	acquired, err := h.sharedRedis.SetNX(ctx, lockKey, "1", 30*time.Second).Result()
	if err != nil || !acquired {
		// [COMMENT]: Redis lỗi thì fail-close; replica thua lock im lặng để winner trả lời ACR.
		return
	}

	respond := func(resp *iamproto.VerifyUserCredentialsResponse) {
		if replyChannel == "" {
			return
		}
		respData, err := proto.Marshal(resp)
		if err != nil {
			logger.SysError("Redis.VerifyUserCredentials", "Failed to marshal response payload")
			return
		}
		if err := h.sharedRedis.Publish(context.Background(), replyChannel, respData).Err(); err != nil {
			logger.SysError("Redis.VerifyUserCredentials", "Failed to send Redis reply")
		}
	}

	respondError := func(errMsg string) {
		respond(&iamproto.VerifyUserCredentialsResponse{
			Valid:        false,
			ErrorMessage: errMsg,
		})
	}

	var req iamproto.VerifyUserCredentialsRequest
	if err := proto.Unmarshal(reqData, &req); err != nil {
		logger.SysError("Redis.VerifyUserCredentials", "Failed to unmarshal request data")
		respondError("invalid request payload")
		return
	}

	if req.Username == "" || req.Password == "" {
		logger.SysWarn("Redis.VerifyUserCredentials", "Username and password are required")
		respondError("Username and password are required")
		return
	}

	var clientDeviceID uuid.UUID
	if req.ClientDeviceId != "" {
		if parsed, err := uuid.Parse(req.ClientDeviceId); err == nil {
			clientDeviceID = parsed
		}
	}

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

	res, err := h.authService.VerifyUserCredentials(ctx, loginReq)
	if err != nil {
		if errors.Is(err, iamTaxonomy.ErrVerificationRequired) {
			respondError("ACCOUNT_VERIFICATION_REQUIRED")
			return
		}
		if errors.Is(err, iamTaxonomy.ErrRoleRequired) {
			logger.SysWarn("Redis.VerifyUserCredentials", "Login attempt blocked: no active role assigned in target scope")
			respondError(iamTaxonomy.ErrInvalidCredentials.Error())
			return
		}
		if errors.Is(err, iamTaxonomy.ErrUserNotFound) || errors.Is(err, iamTaxonomy.ErrInvalidCredentials) {
			logger.SysWarn("Redis.VerifyUserCredentials", "Login attempt failed: invalid credentials")
			respondError(iamTaxonomy.ErrInvalidCredentials.Error())
			return
		}
		logger.SysError("Redis.VerifyUserCredentials", "Failed to verify credentials due to system error")
		respondError("authentication service temporarily unavailable")
		return
	}

	resp := &iamproto.VerifyUserCredentialsResponse{
		Valid:                 res.Valid,
		UserId:                res.UserID,
		RoleId:                res.RoleID,
		Level:                 res.Level,
		TenantId:              res.TenantID,
		ClientDeviceId:        res.ClientDeviceID,
		RefreshToken:          res.RefreshToken,
		RefreshTokenExpiresAt: res.RefreshTokenExpiresAt.Unix(),
		Username:              res.Username,
		ClientProofPublicKey:  res.ClientProofPublicKey,
	}
	respond(resp)
}

// =========================================================================
// 2. LUỒNG XÁC THỰC OPAQUE REFRESH TOKEN (VerifyOpaqueRefreshToken)
// =========================================================================
func (h *AuthRedisHandler) handleVerifyOpaqueToken(payload []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var span trace.Span
	if h.otel != nil {
		ctx, span = h.otel.StartServerSpan(ctx, "Redis iam.auth.verify_opaque_token")
		defer span.End()
		span.SetAttributes(
			attribute.String("messaging.system", "redis"),
			attribute.String("messaging.destination", verifyOpaqueTokenChannel),
		)
	}

	// [COMMENT]: Refresh rotation là side effect; khóa request ngăn nhiều CP replica cùng
	// xác minh/rotate một opaque token khi nhận chung một Redis PubSub message.
	if len(payload) <= 16 {
		logger.SysWarn("Redis.VerifyOpaqueRefreshToken", "Missing request id envelope")
		return
	}
	requestID, err := uuid.FromBytes(payload[:16])
	if err != nil || requestID == uuid.Nil {
		logger.SysWarn("Redis.VerifyOpaqueRefreshToken", "Invalid request id envelope")
		return
	}
	reqData := payload[16:]
	replyChannel := verifyOpaqueTokenReplyPrefix + requestID.String()
	lockKey := "iam:auth:dispatch:verify_opaque_token:" + requestID.String()
	acquired, err := h.sharedRedis.SetNX(ctx, lockKey, "1", 30*time.Second).Result()
	if err != nil || !acquired {
		return
	}

	respond := func(resp *iamproto.VerifyOpaqueRefreshTokenResponse) {
		if replyChannel == "" {
			return
		}
		respData, err := proto.Marshal(resp)
		if err != nil {
			logger.SysError("Redis.VerifyOpaqueRefreshToken", "Failed to marshal response payload")
			return
		}
		if err := h.sharedRedis.Publish(context.Background(), replyChannel, respData).Err(); err != nil {
			logger.SysError("Redis.VerifyOpaqueRefreshToken", "Failed to send Redis reply")
		}
	}

	respondError := func(errMsg string) {
		respond(&iamproto.VerifyOpaqueRefreshTokenResponse{
			Valid:        false,
			ErrorMessage: errMsg,
		})
	}

	var req iamproto.VerifyOpaqueRefreshTokenRequest
	if err := proto.Unmarshal(reqData, &req); err != nil {
		logger.SysError("Redis.VerifyOpaqueRefreshToken", "Failed to unmarshal request data")
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
		if parsed, err := uuid.Parse(*req.TenantId); err == nil {
			tenantUUIDPtr = &parsed
		} else {
			respondError(fmt.Sprintf("invalid tenant id format: %v", err))
			return
		}
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
	respond(resp)
}

// =========================================================================
// 3. LUỒNG THU HỒI REFRESH TOKEN (RevokeOpaqueRefreshToken)
// =========================================================================
func (h *AuthRedisHandler) handleRevokeOpaqueToken(payload []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var span trace.Span
	if h.otel != nil {
		ctx, span = h.otel.StartServerSpan(ctx, "Redis iam.auth.revoke_opaque_token")
		defer span.End()
		span.SetAttributes(
			attribute.String("messaging.system", "redis"),
			attribute.String("messaging.destination", revokeOpaqueTokenChannel),
		)
	}

	// [COMMENT]: Revoke cũng dùng request envelope và distributed lock để không khuếch đại
	// write load theo số lượng CP replica.
	if len(payload) <= 16 {
		logger.SysWarn("Redis.RevokeOpaqueRefreshToken", "Missing request id envelope")
		return
	}
	requestID, err := uuid.FromBytes(payload[:16])
	if err != nil || requestID == uuid.Nil {
		logger.SysWarn("Redis.RevokeOpaqueRefreshToken", "Invalid request id envelope")
		return
	}
	reqData := payload[16:]
	replyChannel := revokeOpaqueTokenReplyPrefix + requestID.String()
	lockKey := "iam:auth:dispatch:revoke_opaque_token:" + requestID.String()
	acquired, err := h.sharedRedis.SetNX(ctx, lockKey, "1", 30*time.Second).Result()
	if err != nil || !acquired {
		return
	}

	var req iamproto.RevokeOpaqueRefreshTokenRequest
	if err := proto.Unmarshal(reqData, &req); err != nil {
		logger.SysError("Redis.RevokeOpaqueRefreshToken", "Failed to unmarshal request data")
		return
	}

	err = h.sessionRefreshService.RevokeOpaqueRefreshToken(ctx, req.RefreshToken)
	if err != nil {
		logger.SysError("Redis.RevokeOpaqueRefreshToken", fmt.Sprintf("Failed to revoke refresh token: %s", err.Error()))
	}

	if replyChannel != "" {
		resp := &iamproto.RevokeOpaqueRefreshTokenResponse{}
		respData, _ := proto.Marshal(resp)
		_ = h.sharedRedis.Publish(context.Background(), replyChannel, respData).Err()
	}
}

func (h *AuthRedisHandler) Stop() {
	if h == nil {
		return
	}
	if h.cancel != nil {
		h.cancel()
	}
	if h.pubsub != nil {
		_ = h.pubsub.Close()
	}
	h.loopWG.Wait()
	h.workWG.Wait()
}
