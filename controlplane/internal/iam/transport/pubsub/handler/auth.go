package pubsubHandler

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
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
	verifyCredentialsChannel          = "iam.auth.verify_credentials"
	verifyCredentialsReplyPrefix      = "iam.auth.verify_credentials.reply."
	verifyExternalIdentityChannel     = "iam.auth.verify_external_identity"
	verifyExternalIdentityReplyPrefix = "iam.auth.verify_external_identity.reply."

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
		verifyExternalIdentityChannel,
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
	case verifyExternalIdentityChannel:
		h.handleVerifyExternalIdentity(payload)
	case verifyOpaqueTokenChannel:
		h.handleVerifyOpaqueToken(payload)
	case revokeOpaqueTokenChannel:
		h.handleRevokeOpaqueToken(payload)
	}
}

func (h *AuthRedisHandler) handleVerifyExternalIdentity(payload []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if len(payload) <= 16 {
		logger.SysWarn("Redis.VerifyExternalIdentity", "Missing request id envelope")
		return
	}
	requestID, err := uuid.FromBytes(payload[:16])
	if err != nil || requestID == uuid.Nil {
		logger.SysWarn("Redis.VerifyExternalIdentity", "Invalid request id envelope")
		return
	}
	replyChannel := verifyExternalIdentityReplyPrefix + requestID.String()
	lockKey := "iam:auth:dispatch:verify_external_identity:" + requestID.String()
	acquired, err := h.sharedRedis.SetNX(ctx, lockKey, "1", 30*time.Second).Result()
	if err != nil || !acquired {
		return
	}

	respond := func(resp *iamproto.VerifyExternalIdentityResponse) {
		respData, marshalErr := proto.Marshal(resp)
		if marshalErr != nil {
			logger.SysError("Redis.VerifyExternalIdentity", "Failed to marshal response payload")
			return
		}
		if publishErr := h.sharedRedis.Publish(context.Background(), replyChannel, respData).Err(); publishErr != nil {
			logger.SysError("Redis.VerifyExternalIdentity", "Failed to send Redis reply")
		}
	}
	var req iamproto.VerifyExternalIdentityRequest
	if err := proto.Unmarshal(payload[16:], &req); err != nil {
		respond(&iamproto.VerifyExternalIdentityResponse{
			Valid:        false,
			ErrorMessage: "INVALID_REQUEST_PAYLOAD",
		})
		return
	}
	if req.SchemaVersion != 1 || req.OperationId == "" ||
		req.Provider == "" || req.ProviderSubject == "" ||
		req.ProviderEmail == "" || req.DisplayName == "" ||
		req.PublicKey == "" || req.ZoneCode == "" || req.ZoneCode == "global" {
		respond(&iamproto.VerifyExternalIdentityResponse{
			Valid:        false,
			ErrorMessage: "INVALID_EXTERNAL_IDENTITY",
		})
		return
	}
	decodedPublicKey, publicKeyErr := base64.StdEncoding.DecodeString(req.PublicKey)
	// This is the Redis wire boundary: reject malformed/unbounded input here so
	// service/repository layers can operate on the canonical identity contract.
	if req.Provider != strings.ToLower(req.Provider) ||
		req.ProviderSubject != strings.TrimSpace(req.ProviderSubject) ||
		req.ProviderEmail != strings.ToLower(strings.TrimSpace(req.ProviderEmail)) ||
		req.DisplayName != strings.TrimSpace(req.DisplayName) ||
		req.ZoneCode != strings.ToLower(strings.TrimSpace(req.ZoneCode)) ||
		req.EmailVerifiedAt <= 0 ||
		req.EmailVerifiedAt > time.Now().Add(5*time.Minute).Unix() ||
		publicKeyErr != nil || len(decodedPublicKey) != 32 ||
		base64.StdEncoding.EncodeToString(decodedPublicKey) != req.PublicKey ||
		len(req.ProviderSubject) > 255 || len(req.ProviderEmail) > 320 ||
		len(req.DisplayName) > 120 || len(req.AvatarUrl) > 2048 ||
		len(req.PublicKey) > 128 || len(req.DeviceName) > 120 ||
		len(req.DeviceType) > 64 || len(req.ClientIp) > 128 ||
		len(req.UserAgent) > 1024 ||
		(req.AvatarUrl != "" && !strings.HasPrefix(req.AvatarUrl, "https://")) {
		respond(&iamproto.VerifyExternalIdentityResponse{
			Valid:        false,
			ErrorMessage: "INVALID_EXTERNAL_IDENTITY",
		})
		return
	}
	operationID, err := uuid.Parse(req.OperationId)
	if err != nil {
		respond(&iamproto.VerifyExternalIdentityResponse{
			Valid:        false,
			ErrorMessage: "INVALID_OPERATION_ID",
		})
		return
	}
	var clientDeviceID uuid.UUID
	if req.ClientDeviceId != "" {
		clientDeviceID, err = uuid.Parse(req.ClientDeviceId)
		if err != nil {
			respond(&iamproto.VerifyExternalIdentityResponse{
				Valid:        false,
				ErrorMessage: "INVALID_DEVICE_ID",
			})
			return
		}
	}
	provider := iamEntity.ExternalProvider(req.Provider)
	if provider != iamEntity.ExternalProviderGoogle && provider != iamEntity.ExternalProviderGitHub {
		respond(&iamproto.VerifyExternalIdentityResponse{
			Valid:        false,
			ErrorMessage: "INVALID_PROVIDER",
		})
		return
	}

	loginReq := iamEntity.ExternalLoginRequest{
		OperationID: operationID,
		Identity: iamEntity.VerifiedExternalIdentity{
			Provider:        provider,
			Subject:         req.ProviderSubject,
			Email:           req.ProviderEmail,
			EmailVerifiedAt: time.Unix(req.EmailVerifiedAt, 0).UTC(),
			DisplayName:     req.DisplayName,
			AvatarURL:       optionalString(req.AvatarUrl),
		},
		DevicePublicKey: req.PublicKey,
		TrustDevice:     req.TrustDevice,
		DeviceName:      req.DeviceName,
		DeviceType:      req.DeviceType,
		ClientDeviceID:  clientDeviceID,
		ZoneCode:        req.ZoneCode,
		RemoteIP:        req.ClientIp,
		UserAgent:       req.UserAgent,
	}
	res, err := h.authService.VerifyExternalIdentity(ctx, loginReq)
	if err != nil {
		switch {
		case errors.Is(err, iamTaxonomy.ErrVerificationRequired):
			respond(&iamproto.VerifyExternalIdentityResponse{
				Valid:        false,
				ErrorMessage: "PASSWORD_SETUP_REQUIRED",
			})
		case errors.Is(err, iamTaxonomy.ErrInvalidCredentials):
			respond(&iamproto.VerifyExternalIdentityResponse{
				Valid:        false,
				ErrorMessage: "INVALID_CREDENTIALS",
			})
		case errors.Is(err, iamTaxonomy.ErrRoleRequired):
			respond(&iamproto.VerifyExternalIdentityResponse{
				Valid:        false,
				ErrorMessage: "ROLE_REQUIRED",
			})
		case errors.Is(err, iamTaxonomy.ErrInvalidArgument):
			respond(&iamproto.VerifyExternalIdentityResponse{
				Valid:        false,
				ErrorMessage: "INVALID_EXTERNAL_IDENTITY",
			})
		default:
			logger.SysError("Redis.VerifyExternalIdentity", "External identity verification failed")
			respond(&iamproto.VerifyExternalIdentityResponse{
				Valid:        false,
				ErrorMessage: "AUTHENTICATION_UNAVAILABLE",
			})
		}
		return
	}
	respond(&iamproto.VerifyExternalIdentityResponse{
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
		ZoneCode:              res.ZoneCode,
	})
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
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

	var req iamproto.VerifyUserCredentialsRequest
	if err := proto.Unmarshal(reqData, &req); err != nil {
		logger.SysError("Redis.VerifyUserCredentials", "Failed to unmarshal request data")
		respond(&iamproto.VerifyUserCredentialsResponse{
			Valid:        false,
			ErrorMessage: "invalid request payload",
		})
		return
	}

	if req.Username == "" || req.Password == "" {
		logger.SysWarn("Redis.VerifyUserCredentials", "Username and password are required")
		respond(&iamproto.VerifyUserCredentialsResponse{
			Valid:        false,
			ErrorMessage: "Username and password are required",
		})
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
			respond(&iamproto.VerifyUserCredentialsResponse{
				Valid:        false,
				ErrorMessage: "ACCOUNT_VERIFICATION_REQUIRED",
			})
			return
		}
		if errors.Is(err, iamTaxonomy.ErrRoleRequired) {
			logger.SysWarn("Redis.VerifyUserCredentials", "Login attempt blocked: no active role assigned in target scope")
			respond(&iamproto.VerifyUserCredentialsResponse{
				Valid:        false,
				ErrorMessage: iamTaxonomy.ErrInvalidCredentials.Error(),
			})
			return
		}
		if errors.Is(err, iamTaxonomy.ErrUserNotFound) || errors.Is(err, iamTaxonomy.ErrInvalidCredentials) {
			logger.SysWarn("Redis.VerifyUserCredentials", "Login attempt failed: invalid credentials")
			respond(&iamproto.VerifyUserCredentialsResponse{
				Valid:        false,
				ErrorMessage: iamTaxonomy.ErrInvalidCredentials.Error(),
			})
			return
		}
		logger.SysError("Redis.VerifyUserCredentials", "Failed to verify credentials due to system error")
		respond(&iamproto.VerifyUserCredentialsResponse{
			Valid:        false,
			ErrorMessage: "authentication service temporarily unavailable",
		})
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

	var req iamproto.VerifyOpaqueRefreshTokenRequest
	if err := proto.Unmarshal(reqData, &req); err != nil {
		logger.SysError("Redis.VerifyOpaqueRefreshToken", "Failed to unmarshal request data")
		respond(&iamproto.VerifyOpaqueRefreshTokenResponse{
			Valid:        false,
			ErrorMessage: "invalid request payload",
		})
		return
	}

	userUUID, err := uuid.Parse(req.UserId)
	if err != nil {
		respond(&iamproto.VerifyOpaqueRefreshTokenResponse{
			Valid:        false,
			ErrorMessage: fmt.Sprintf("invalid user id format: %v", err),
		})
		return
	}

	var tenantUUIDPtr *uuid.UUID
	if req.TenantId != nil && *req.TenantId != "" {
		if parsed, err := uuid.Parse(*req.TenantId); err == nil {
			tenantUUIDPtr = &parsed
		} else {
			respond(&iamproto.VerifyOpaqueRefreshTokenResponse{
				Valid:        false,
				ErrorMessage: fmt.Sprintf("invalid tenant id format: %v", err),
			})
			return
		}
	}

	res, err := h.sessionRefreshService.VerifyOpaqueRefreshToken(ctx, req.RefreshToken, tenantUUIDPtr, userUUID)
	if err != nil {
		respond(&iamproto.VerifyOpaqueRefreshTokenResponse{
			Valid:        false,
			ErrorMessage: err.Error(),
		})
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
