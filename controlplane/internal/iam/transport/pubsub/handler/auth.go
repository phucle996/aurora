package pubsubHandler

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"

	"controlplane/internal/config"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	iamproto "controlplane/internal/iam/transport/rpc/proto"
	"controlplane/internal/observability"
	pkgcontext "controlplane/pkg/context"
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
	linkExternalIdentityChannel       = "iam.auth.link_external_identity"
	linkExternalIdentityReplyPrefix   = "iam.auth.link_external_identity.reply."
	verifyMfaChallengeChannel         = "iam.auth.verify_mfa_challenge"
	verifyMfaChallengeReplyPrefix     = "iam.auth.verify_mfa_challenge.reply."

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
	userService           iamSvcInterface.UserService
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
	userService iamSvcInterface.UserService,
	sessionRefreshService iamSvcInterface.SessionRefreshService,
	otel *observability.OTel,
) (*AuthRedisHandler, error) {
	if sharedRedis == nil || authService == nil || userService == nil || sessionRefreshService == nil {
		return nil, errors.New("auth Redis handler requires Shared Redis, AuthService, UserService and SessionRefreshService")
	}
	return &AuthRedisHandler{
		cfg:                   cfg,
		sharedRedis:           sharedRedis,
		authService:           authService,
		userService:           userService,
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
	ctx, cancel := context.WithCancel(pkgcontext.WithOperation(context.Background(), "iam.auth.pubsub.subscribe"))
	pubsub := h.sharedRedis.Subscribe(ctx,
		verifyCredentialsChannel,
		verifyExternalIdentityChannel,
		linkExternalIdentityChannel,
		verifyMfaChallengeChannel,
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
	case linkExternalIdentityChannel:
		h.handleLinkExternalIdentity(payload)
	case verifyMfaChallengeChannel:
		h.handleVerifyMfaChallenge(payload)
	case verifyOpaqueTokenChannel:
		h.handleVerifyOpaqueToken(payload)
	case revokeOpaqueTokenChannel:
		h.handleRevokeOpaqueToken(payload)
	}
}

func (h *AuthRedisHandler) handleLinkExternalIdentity(payload []byte) {
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(context.Background(), "iam.auth.link_external_identity"), 10*time.Second)
	defer cancel()

	if len(payload) <= 16 {
		logger.SysWarnCtx(ctx, "Redis.LinkExternalIdentity", "Missing request id envelope")
		return
	}
	requestID, err := uuid.FromBytes(payload[:16])
	if err != nil || requestID == uuid.Nil {
		logger.SysWarnCtx(ctx, "Redis.LinkExternalIdentity", "Invalid request id envelope")
		return
	}
	replyChannel := linkExternalIdentityReplyPrefix + requestID.String()
	lockKey := "iam:auth:dispatch:link_external_identity:" + requestID.String()
	acquired, err := h.sharedRedis.SetNX(ctx, lockKey, "1", 30*time.Second).Result()
	if err != nil || !acquired {
		return
	}

	respond := func(response *iamproto.LinkExternalIdentityResponse) {
		data, marshalErr := proto.Marshal(response)
		if marshalErr != nil {
			logger.SysErrorCtx(ctx, "Redis.LinkExternalIdentity", "Failed to marshal response")
			return
		}
		if publishErr := h.sharedRedis.Publish(ctx, replyChannel, data).Err(); publishErr != nil {
			logger.SysErrorCtx(ctx, "Redis.LinkExternalIdentity", "Failed to publish response")
		}
	}

	var request iamproto.LinkExternalIdentityRequest
	if len(payload) > 16+(8<<10) {
		respond(&iamproto.LinkExternalIdentityResponse{ErrorMessage: "INVALID_REQUEST_PAYLOAD"})
		return
	}
	if err := proto.Unmarshal(payload[16:], &request); err != nil {
		respond(&iamproto.LinkExternalIdentityResponse{ErrorMessage: "INVALID_REQUEST_PAYLOAD"})
		return
	}
	operationID, operationErr := uuid.Parse(strings.TrimSpace(request.OperationId))
	userID, userErr := uuid.Parse(strings.TrimSpace(request.UserId))
	provider := iamEntity.ExternalProvider(request.Provider)
	verifiedAt := time.Unix(request.EmailVerifiedAt, 0).UTC()
	avatarURL := strings.TrimSpace(request.AvatarUrl)
	avatarValid := true
	if avatarURL != "" {
		parsed, parseErr := url.Parse(avatarURL)
		avatarValid = parseErr == nil && parsed.Scheme == "https" && parsed.Host != "" &&
			parsed.User == nil && parsed.Fragment == ""
	}
	if request.SchemaVersion != 1 ||
		operationErr != nil || operationID == uuid.Nil ||
		userErr != nil || userID == uuid.Nil ||
		(provider != iamEntity.ExternalProviderGoogle && provider != iamEntity.ExternalProviderGitHub) ||
		request.Provider != strings.ToLower(strings.TrimSpace(request.Provider)) ||
		request.ProviderSubject == "" ||
		request.ProviderSubject != strings.TrimSpace(request.ProviderSubject) ||
		len(request.ProviderSubject) > 255 ||
		request.ProviderEmail == "" ||
		request.ProviderEmail != strings.ToLower(strings.TrimSpace(request.ProviderEmail)) ||
		len(request.ProviderEmail) > 320 ||
		strings.Count(request.ProviderEmail, "@") != 1 ||
		strings.HasPrefix(request.ProviderEmail, "@") ||
		strings.HasSuffix(request.ProviderEmail, "@") ||
		request.DisplayName == "" ||
		request.DisplayName != strings.TrimSpace(request.DisplayName) ||
		len(request.DisplayName) > 120 ||
		len(avatarURL) > 2048 || !avatarValid ||
		request.EmailVerifiedAt <= 0 ||
		verifiedAt.Before(time.Now().Add(-10*time.Minute)) ||
		verifiedAt.After(time.Now().Add(5*time.Minute)) ||
		strings.IndexFunc(request.ProviderSubject, unicode.IsControl) >= 0 ||
		strings.IndexFunc(request.ProviderEmail, unicode.IsControl) >= 0 ||
		strings.IndexFunc(request.DisplayName, unicode.IsControl) >= 0 {
		respond(&iamproto.LinkExternalIdentityResponse{ErrorMessage: "INVALID_EXTERNAL_IDENTITY"})
		return
	}

	var avatar *string
	if avatarURL != "" {
		avatar = &avatarURL
	}
	err = h.userService.LinkExternalIdentity(ctx, iamEntity.LinkExternalIdentity{
		OperationID:     operationID,
		UserID:          userID,
		Provider:        string(provider),
		ProviderSubject: request.ProviderSubject,
		ProviderEmail:   request.ProviderEmail,
		EmailVerifiedAt: verifiedAt,
		DisplayName:     request.DisplayName,
		AvatarURL:       avatar,
	})
	if err != nil {
		switch {
		case errors.Is(err, iamTaxonomy.ErrExternalIdentityConflict):
			respond(&iamproto.LinkExternalIdentityResponse{ErrorMessage: "IDENTITY_ALREADY_LINKED"})
		case errors.Is(err, iamTaxonomy.ErrSocialProviderAlreadyLinked):
			respond(&iamproto.LinkExternalIdentityResponse{ErrorMessage: "PROVIDER_ALREADY_LINKED"})
		case errors.Is(err, iamTaxonomy.ErrInvalidCredentials):
			respond(&iamproto.LinkExternalIdentityResponse{ErrorMessage: "ACCOUNT_UNAVAILABLE"})
		default:
			logger.SysErrorCtx(ctx, "Redis.LinkExternalIdentity", "Social identity link failed")
			respond(&iamproto.LinkExternalIdentityResponse{ErrorMessage: "AUTHENTICATION_UNAVAILABLE"})
		}
		return
	}
	respond(&iamproto.LinkExternalIdentityResponse{Linked: true})
}

func (h *AuthRedisHandler) handleVerifyExternalIdentity(payload []byte) {
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(context.Background(), "iam.auth.verify_external_identity"), 10*time.Second)
	defer cancel()

	if len(payload) <= 16 {
		logger.SysWarnCtx(ctx, "Redis.VerifyExternalIdentity", "Missing request id envelope")
		return
	}
	requestID, err := uuid.FromBytes(payload[:16])
	if err != nil || requestID == uuid.Nil {
		logger.SysWarnCtx(ctx, "Redis.VerifyExternalIdentity", "Invalid request id envelope")
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
			logger.SysErrorCtx(ctx, "Redis.VerifyExternalIdentity", "Failed to marshal response payload")
			return
		}
		if publishErr := h.sharedRedis.Publish(context.Background(), replyChannel, respData).Err(); publishErr != nil {
			logger.SysErrorCtx(ctx, "Redis.VerifyExternalIdentity", "Failed to send Redis reply")
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
			logger.SysErrorCtx(ctx, "Redis.VerifyExternalIdentity", "External identity verification failed")
			respond(&iamproto.VerifyExternalIdentityResponse{
				Valid:        false,
				ErrorMessage: "AUTHENTICATION_UNAVAILABLE",
			})
		}
		return
	}
	respond(&iamproto.VerifyExternalIdentityResponse{
		Valid:                 res.Valid,
		MfaRequired:           res.MFARequired,
		MfaSettingId:          res.MFASettingID,
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
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(context.Background(), "iam.auth.verify_credentials"), 10*time.Second)
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
		logger.SysWarnCtx(ctx, "Redis.VerifyUserCredentials", "Missing request id envelope")
		return
	}
	requestID, err := uuid.FromBytes(payload[:16])
	if err != nil || requestID == uuid.Nil {
		logger.SysWarnCtx(ctx, "Redis.VerifyUserCredentials", "Invalid request id envelope")
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
			logger.SysErrorCtx(ctx, "Redis.VerifyUserCredentials", "Failed to marshal response payload")
			return
		}
		if err := h.sharedRedis.Publish(context.Background(), replyChannel, respData).Err(); err != nil {
			logger.SysErrorCtx(ctx, "Redis.VerifyUserCredentials", "Failed to send Redis reply")
		}
	}

	var req iamproto.VerifyUserCredentialsRequest
	if err := proto.Unmarshal(reqData, &req); err != nil {
		logger.SysErrorCtx(ctx, "Redis.VerifyUserCredentials", "Failed to unmarshal request data")
		respond(&iamproto.VerifyUserCredentialsResponse{
			Valid:        false,
			ErrorMessage: "invalid request payload",
		})
		return
	}

	if req.Username == "" || req.Password == "" {
		logger.SysWarnCtx(ctx, "Redis.VerifyUserCredentials", "Username and password are required")
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
			logger.SysWarnCtx(ctx, "Redis.VerifyUserCredentials", "Login attempt blocked: no active role assigned in target scope")
			respond(&iamproto.VerifyUserCredentialsResponse{
				Valid:        false,
				ErrorMessage: iamTaxonomy.ErrInvalidCredentials.Error(),
			})
			return
		}
		if errors.Is(err, iamTaxonomy.ErrUserNotFound) || errors.Is(err, iamTaxonomy.ErrInvalidCredentials) {
			logger.SysWarnCtx(ctx, "Redis.VerifyUserCredentials", "Login attempt failed: invalid credentials")
			respond(&iamproto.VerifyUserCredentialsResponse{
				Valid:        false,
				ErrorMessage: iamTaxonomy.ErrInvalidCredentials.Error(),
			})
			return
		}
		logger.SysErrorCtx(ctx, "Redis.VerifyUserCredentials", "Failed to verify credentials due to system error")
		respond(&iamproto.VerifyUserCredentialsResponse{
			Valid:        false,
			ErrorMessage: "authentication service temporarily unavailable",
		})
		return
	}

	resp := &iamproto.VerifyUserCredentialsResponse{
		Valid:                 res.Valid,
		MfaRequired:           res.MFARequired,
		MfaSettingId:          res.MFASettingID,
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

func (h *AuthRedisHandler) handleVerifyMfaChallenge(payload []byte) {
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(context.Background(), "iam.auth.verify_mfa_challenge"), 10*time.Second)
	defer cancel()
	if len(payload) <= 16 {
		logger.SysWarnCtx(ctx, "Redis.VerifyMfaChallenge", "Missing request id envelope")
		return
	}
	requestID, err := uuid.FromBytes(payload[:16])
	if err != nil || requestID == uuid.Nil {
		logger.SysWarnCtx(ctx, "Redis.VerifyMfaChallenge", "Invalid request id envelope")
		return
	}
	replyChannel := verifyMfaChallengeReplyPrefix + requestID.String()
	lockKey := "iam:auth:dispatch:verify_mfa_challenge:" + requestID.String()
	acquired, err := h.sharedRedis.SetNX(ctx, lockKey, "1", 30*time.Second).Result()
	if err != nil || !acquired {
		return
	}
	respond := func(resp *iamproto.VerifyMfaChallengeResponse) {
		data, marshalErr := proto.Marshal(resp)
		if marshalErr != nil {
			logger.SysErrorCtx(ctx, "Redis.VerifyMfaChallenge", "Failed to marshal response payload")
			return
		}
		_ = h.sharedRedis.Publish(context.Background(), replyChannel, data).Err()
	}
	var req iamproto.VerifyMfaChallengeRequest
	if err := proto.Unmarshal(payload[16:], &req); err != nil {
		respond(&iamproto.VerifyMfaChallengeResponse{ErrorMessage: "INVALID_MFA_CHALLENGE"})
		return
	}
	userID, err := uuid.Parse(strings.TrimSpace(req.UserId))
	mfaSettingID, mfaSettingErr := uuid.Parse(strings.TrimSpace(req.MfaSettingId))
	operationID, operationIDErr := uuid.Parse(strings.TrimSpace(req.OperationId))
	decodedPublicKey, publicKeyErr := base64.StdEncoding.DecodeString(req.PublicKey)
	method := strings.ToLower(strings.TrimSpace(req.Method))
	if err != nil || userID == uuid.Nil ||
		mfaSettingErr != nil || mfaSettingID == uuid.Nil ||
		operationIDErr != nil || operationID == uuid.Nil ||
		strings.TrimSpace(req.Username) == "" ||
		len(req.Username) > 255 || len(req.TenantDomain) > 255 ||
		!validMFALoginCode(method, req.Code) ||
		publicKeyErr != nil || len(decodedPublicKey) != 32 ||
		base64.StdEncoding.EncodeToString(decodedPublicKey) != req.PublicKey ||
		len(req.PublicKey) > 128 || len(req.DeviceName) > 120 ||
		len(req.DeviceType) > 64 || len(req.ClientIp) > 128 || len(req.UserAgent) > 1024 {
		respond(&iamproto.VerifyMfaChallengeResponse{ErrorMessage: "INVALID_MFA_CHALLENGE"})
		return
	}
	clientDeviceID := uuid.Nil
	if strings.TrimSpace(req.ClientDeviceId) != "" {
		clientDeviceID, err = uuid.Parse(req.ClientDeviceId)
		if err != nil || clientDeviceID == uuid.Nil {
			respond(&iamproto.VerifyMfaChallengeResponse{ErrorMessage: "INVALID_MFA_CHALLENGE"})
			return
		}
	}
	result, err := h.authService.VerifyMfaLogin(ctx, iamEntity.MFALoginRequest{
		UserID:          userID,
		MFASettingID:    mfaSettingID,
		Username:        strings.TrimSpace(req.Username),
		TenantDomain:    strings.ToLower(strings.TrimSpace(req.TenantDomain)),
		Method:          method,
		Code:            strings.TrimSpace(req.Code),
		DevicePublicKey: strings.TrimSpace(req.PublicKey),
		TrustDevice:     req.TrustDevice,
		DeviceName:      strings.TrimSpace(req.DeviceName),
		DeviceType:      strings.TrimSpace(req.DeviceType),
		ClientDeviceID:  clientDeviceID,
		RemoteIP:        strings.TrimSpace(req.ClientIp),
		UserAgent:       strings.TrimSpace(req.UserAgent),
	})
	if err != nil {
		switch {
		case errors.Is(err, iamTaxonomy.ErrMFAInvalidCode),
			errors.Is(err, iamTaxonomy.ErrRecoveryCodeInvalid),
			errors.Is(err, iamTaxonomy.ErrMFAChallengeInvalid):
			respond(&iamproto.VerifyMfaChallengeResponse{ErrorMessage: "MFA_INVALID"})
		case errors.Is(err, iamTaxonomy.ErrAuthenticationUnavailable):
			respond(&iamproto.VerifyMfaChallengeResponse{ErrorMessage: "AUTHENTICATION_UNAVAILABLE"})
		default:
			logger.SysErrorCtx(ctx, "Redis.VerifyMfaChallenge", "MFA login verification failed")
			respond(&iamproto.VerifyMfaChallengeResponse{ErrorMessage: "AUTHENTICATION_UNAVAILABLE"})
		}
		return
	}
	respond(&iamproto.VerifyMfaChallengeResponse{
		Valid:                 result.Valid,
		UserId:                result.UserID,
		RoleId:                result.RoleID,
		Level:                 result.Level,
		TenantId:              result.TenantID,
		ClientDeviceId:        result.ClientDeviceID,
		RefreshToken:          result.RefreshToken,
		RefreshTokenExpiresAt: result.RefreshTokenExpiresAt.Unix(),
		Username:              result.Username,
		ClientProofPublicKey:  result.ClientProofPublicKey,
		TenantCode:            result.TenantCode,
	})
}

func validMFALoginCode(method, code string) bool {
	code = strings.TrimSpace(code)
	switch method {
	case "totp":
		if len(code) != 6 {
			return false
		}
		for _, value := range code {
			if value < '0' || value > '9' {
				return false
			}
		}
		return true
	case "recovery_code":
		if len(code) != 16 {
			return false
		}
		const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
		for _, value := range strings.ToUpper(code) {
			if !strings.ContainsRune(alphabet, value) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// =========================================================================
// 2. LUỒNG XÁC THỰC OPAQUE REFRESH TOKEN (VerifyOpaqueRefreshToken)
// =========================================================================
func (h *AuthRedisHandler) handleVerifyOpaqueToken(payload []byte) {
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(context.Background(), "iam.auth.verify_opaque_token"), 10*time.Second)
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
		logger.SysWarnCtx(ctx, "Redis.VerifyOpaqueRefreshToken", "Missing request id envelope")
		return
	}
	requestID, err := uuid.FromBytes(payload[:16])
	if err != nil || requestID == uuid.Nil {
		logger.SysWarnCtx(ctx, "Redis.VerifyOpaqueRefreshToken", "Invalid request id envelope")
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
			logger.SysErrorCtx(ctx, "Redis.VerifyOpaqueRefreshToken", "Failed to marshal response payload")
			return
		}
		if err := h.sharedRedis.Publish(context.Background(), replyChannel, respData).Err(); err != nil {
			logger.SysErrorCtx(ctx, "Redis.VerifyOpaqueRefreshToken", "Failed to send Redis reply")
		}
	}

	var req iamproto.VerifyOpaqueRefreshTokenRequest
	if err := proto.Unmarshal(reqData, &req); err != nil {
		logger.SysErrorCtx(ctx, "Redis.VerifyOpaqueRefreshToken", "Failed to unmarshal request data")
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
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(context.Background(), "iam.auth.revoke_opaque_token"), 10*time.Second)
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
		logger.SysWarnCtx(ctx, "Redis.RevokeOpaqueRefreshToken", "Missing request id envelope")
		return
	}
	requestID, err := uuid.FromBytes(payload[:16])
	if err != nil || requestID == uuid.Nil {
		logger.SysWarnCtx(ctx, "Redis.RevokeOpaqueRefreshToken", "Invalid request id envelope")
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
		logger.SysErrorCtx(ctx, "Redis.RevokeOpaqueRefreshToken", "Failed to unmarshal request data")
		return
	}

	err = h.sessionRefreshService.RevokeOpaqueRefreshToken(ctx, req.RefreshToken)
	if err != nil {
		logger.SysErrorCtx(ctx, "Redis.RevokeOpaqueRefreshToken", fmt.Sprintf("Failed to revoke refresh token: %s", err.Error()))
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
