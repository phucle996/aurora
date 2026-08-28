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
	iamproto "controlplane/internal/iam/transport/proto"
	"controlplane/internal/observability"
	pkgcontext "controlplane/pkg/context"
	"controlplane/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

// [COMMENT]: Danh sách các kênh Redis PubSub và tiền tố kênh phản hồi phục vụ cho các luồng xác thực IAM
const (
	// [COMMENT]: Kênh tiếp nhận và tiền tố phản hồi cho luồng xác thực Username/Password
	verifyCredentialsChannel          = "iam.auth.verify_credentials"
	verifyCredentialsReplyPrefix      = "iam.auth.verify_credentials.reply."

	// [COMMENT]: Kênh tiếp nhận và tiền tố phản hồi cho luồng xác thực danh tính mạng xã hội (Google, GitHub)
	verifyExternalIdentityChannel     = "iam.auth.verify_external_identity"
	verifyExternalIdentityReplyPrefix = "iam.auth.verify_external_identity.reply."

	// [COMMENT]: Kênh tiếp nhận và tiền tố phản hồi cho luồng liên kết tài khoản bên thứ 3 vào user hiện tại
	linkExternalIdentityChannel       = "iam.auth.link_external_identity"
	linkExternalIdentityReplyPrefix   = "iam.auth.link_external_identity.reply."

	// [COMMENT]: Kênh tiếp nhận và tiền tố phản hồi cho luồng xác thực thử thách MFA (TOTP / recovery code)
	verifyMfaChallengeChannel         = "iam.auth.verify_mfa_challenge"
	verifyMfaChallengeReplyPrefix     = "iam.auth.verify_mfa_challenge.reply."

	// [COMMENT]: Kênh tiếp nhận và tiền tố phản hồi cho luồng phục hồi session từ opaque refresh token
	recoverUserSessionChannel     = "iam.auth.recover_user_session"
	recoverUserSessionReplyPrefix = "iam.auth.recover_user_session.reply."

	// [COMMENT]: Kênh tiếp nhận và tiền tố phản hồi cho luồng thu hồi opaque refresh token (Logout)
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
	// [COMMENT]: Kiểm tra các dependency bắt buộc trước khi khởi tạo handler
	if sharedRedis == nil || authService == nil || userService == nil || sessionRefreshService == nil {
		return nil, errors.New("auth Redis handler requires Shared Redis, AuthService, UserService and SessionRefreshService")
	}
	// [COMMENT]: Khởi tạo handler với pool 64 worker slots giới hạn concurrency cục bộ
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
	// [COMMENT]: Tạo context có gắn Operation ID cho luồng subscription
	ctx, cancel := context.WithCancel(pkgcontext.WithOperation(context.Background(), "iam.auth.pubsub.subscribe"))
	// [COMMENT]: Đăng ký lắng nghe đồng thời tất cả các channel xác thực của IAM Auth
	pubsub := h.sharedRedis.Subscribe(ctx,
		verifyCredentialsChannel,
		verifyExternalIdentityChannel,
		linkExternalIdentityChannel,
		verifyMfaChallengeChannel,
		recoverUserSessionChannel,
		revokeOpaqueTokenChannel,
	)
	// [COMMENT]: Chờ phản hồi subscribe ban đầu từ Redis để đảm bảo kết nối thành công
	if _, err := pubsub.Receive(ctx); err != nil {
		cancel()
		_ = pubsub.Close()
		return fmt.Errorf("subscribe Auth Redis channels: %w", err)
	}
	h.cancel = cancel
	h.pubsub = pubsub

	// [COMMENT]: Khởi chạy background worker loop xử lý message từ PubSub channel
	h.loopWG.Add(1)
	go func() {
		defer h.loopWG.Done()
		// [COMMENT]: Cấu hình channel buffer kích thước 256 messages từ Redis client
		channel := pubsub.Channel(goredis.WithChannelSize(256))
		for {
			select {
			case <-ctx.Done():
				return
			case message, ok := <-channel:
				if !ok {
					return
				}
				// [COMMENT]: Điều phối công việc thông qua bounded worker slots
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

// [COMMENT]: dispatch định tuyến message nhận được từ Redis PubSub tới handler tương ứng dựa trên Channel
func (h *AuthRedisHandler) dispatch(msg *goredis.Message) {
	if msg == nil {
		return
	}
	// [COMMENT]: Chuyển đổi payload sang dạng byte slice để xử lý Protobuf
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
	case recoverUserSessionChannel:
		h.handleRecoverUserSession(payload)
	case revokeOpaqueTokenChannel:
		h.handleRevokeOpaqueToken(payload)
	}
}

// [COMMENT]: =========================================================================
// [COMMENT]: LUỒNG LIÊN KẾT TÀI KHOẢN MẠNG XÃ HỘI (LinkExternalIdentity)
// [COMMENT]: =========================================================================
func (h *AuthRedisHandler) handleLinkExternalIdentity(payload []byte) {
	// [COMMENT]: Thiết lập context với timeout 10 giây và định danh operation
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(context.Background(), "iam.auth.link_external_identity"), 10*time.Second)
	defer cancel()

	// [COMMENT]: Kiểm tra kích thước envelope: 16 byte đầu là Request ID dạng UUID binary
	if len(payload) <= 16 {
		logger.SysWarnCtx(ctx, "Redis.LinkExternalIdentity", "Missing request id envelope")
		return
	}
	// [COMMENT]: Parse Request ID từ 16 byte đầu envelope
	requestID, err := uuid.FromBytes(payload[:16])
	if err != nil || requestID == uuid.Nil {
		logger.SysWarnCtx(ctx, "Redis.LinkExternalIdentity", "Invalid request id envelope")
		return
	}
	// [COMMENT]: Cấu hình kênh phản hồi và khóa phân tán để đảm bảo tính idempotent giữa các replica
	replyChannel := linkExternalIdentityReplyPrefix + requestID.String()
	lockKey := "iam:auth:dispatch:link_external_identity:" + requestID.String()
	// [COMMENT]: SetNX 30s đảm bảo chỉ 1 replica xử lý request; replica thua lock sẽ drop request an toàn
	acquired, err := h.sharedRedis.SetNX(ctx, lockKey, "1", 30*time.Second).Result()
	if err != nil || !acquired {
		return
	}

	// [COMMENT]: Closure respond đóng gói và gửi phản hồi Protobuf về reply channel tương ứng
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

	// [COMMENT]: Giới hạn kích thước payload tối đa (16 bytes envelope + 8KB Protobuf) chống tấn công DoS/OOM
	var request iamproto.LinkExternalIdentityRequest
	if len(payload) > 16+(8<<10) {
		respond(&iamproto.LinkExternalIdentityResponse{ErrorMessage: "INVALID_REQUEST_PAYLOAD"})
		return
	}
	// [COMMENT]: Unmarshal Protobuf payload từ byte 16 trở đi
	if err := proto.Unmarshal(payload[16:], &request); err != nil {
		respond(&iamproto.LinkExternalIdentityResponse{ErrorMessage: "INVALID_REQUEST_PAYLOAD"})
		return
	}
	// [COMMENT]: Parse và chuẩn hóa các trường định danh UUID, Provider, Timestamp và Avatar URL
	operationID, operationErr := uuid.Parse(strings.TrimSpace(request.OperationId))
	userID, userErr := uuid.Parse(strings.TrimSpace(request.UserId))
	provider := iamEntity.ExternalProvider(request.Provider)
	verifiedAt := time.Unix(request.EmailVerifiedAt, 0).UTC()
	avatarURL := strings.TrimSpace(request.AvatarUrl)
	avatarValid := true
	// [COMMENT]: Validate URL avatar phải dùng giao thức https và không chứa thông tin user/fragment
	if avatarURL != "" {
		parsed, parseErr := url.Parse(avatarURL)
		avatarValid = parseErr == nil && parsed.Scheme == "https" && parsed.Host != "" &&
			parsed.User == nil && parsed.Fragment == ""
	}
	// [COMMENT]: Rào chắn wire boundary: kiểm tra nghiêm ngặt schema version, UUID hợp lệ, provider được hỗ trợ,
	// [COMMENT]: format email/subject/display_name, độ lệch thời gian xác thực (-10m đến +5m), và ký tự điều khiển
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
	// [COMMENT]: Gọi userService để thực hiện liên kết định danh bên ngoài với user
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
		// [COMMENT]: Ánh xạ lỗi nghiệp vụ từ domain sang mã lỗi chuẩn của giao thức phản hồi
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
	// [COMMENT]: Trả về kết quả liên kết thành công
	respond(&iamproto.LinkExternalIdentityResponse{Linked: true})
}

// [COMMENT]: =========================================================================
// [COMMENT]: LUỒNG XÁC THỰC DANH TÍNH MẠNG XÃ HỘI (VerifyExternalIdentity)
// [COMMENT]: =========================================================================
func (h *AuthRedisHandler) handleVerifyExternalIdentity(payload []byte) {
	// [COMMENT]: Thiết lập context với timeout 10 giây và định danh operation
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(context.Background(), "iam.auth.verify_external_identity"), 10*time.Second)
	defer cancel()

	// [COMMENT]: Kiểm tra kích thước envelope: 16 byte đầu là Request ID dạng UUID binary
	if len(payload) <= 16 {
		logger.SysWarnCtx(ctx, "Redis.VerifyExternalIdentity", "Missing request id envelope")
		return
	}
	// [COMMENT]: Parse Request ID từ envelope 16 byte
	requestID, err := uuid.FromBytes(payload[:16])
	if err != nil || requestID == uuid.Nil {
		logger.SysWarnCtx(ctx, "Redis.VerifyExternalIdentity", "Invalid request id envelope")
		return
	}
	// [COMMENT]: Thiết lập reply channel và distributed lock key
	replyChannel := verifyExternalIdentityReplyPrefix + requestID.String()
	lockKey := "iam:auth:dispatch:verify_external_identity:" + requestID.String()
	// [COMMENT]: Chỉ replica giành được lock mới được chạm DB để xác thực social login
	acquired, err := h.sharedRedis.SetNX(ctx, lockKey, "1", 30*time.Second).Result()
	if err != nil || !acquired {
		return
	}

	// [COMMENT]: Closure respond đóng gói và xuất bản VerifyExternalIdentityResponse về Redis reply channel
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
	// [COMMENT]: Unmarshal Protobuf payload sau prefix Request ID
	var req iamproto.VerifyExternalIdentityRequest
	if err := proto.Unmarshal(payload[16:], &req); err != nil {
		respond(&iamproto.VerifyExternalIdentityResponse{
			Valid:        false,
			ErrorMessage: "INVALID_REQUEST_PAYLOAD",
		})
		return
	}
	// [COMMENT]: Kiểm tra các trường bắt buộc và từ chối ZoneCode 'global'
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
	// [COMMENT]: Giải mã public key Base64 Ed25519 (phải đúng 32 bytes)
	decodedPublicKey, publicKeyErr := base64.StdEncoding.DecodeString(req.PublicKey)
	// [COMMENT]: Rào chắn wire boundary: kiểm tra nghiêm ngặt định dạng chuỗi, độ dài tối đa, độ lệch thời gian, avatar HTTPS
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
	// [COMMENT]: Parse Operation ID dạng UUID
	operationID, err := uuid.Parse(req.OperationId)
	if err != nil {
		respond(&iamproto.VerifyExternalIdentityResponse{
			Valid:        false,
			ErrorMessage: "INVALID_OPERATION_ID",
		})
		return
	}
	// [COMMENT]: Parse Client Device ID dạng UUID nếu có
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
	// [COMMENT]: Xác định provider hỗ trợ (Google / GitHub)
	provider := iamEntity.ExternalProvider(req.Provider)
	if provider != iamEntity.ExternalProviderGoogle && provider != iamEntity.ExternalProviderGitHub {
		respond(&iamproto.VerifyExternalIdentityResponse{
			Valid:        false,
			ErrorMessage: "INVALID_PROVIDER",
		})
		return
	}

	deviceName := strings.TrimSpace(req.DeviceName)
	if deviceName == "" {
		deviceName = "OAuth browser"
	}
	deviceType := strings.TrimSpace(req.DeviceType)
	if deviceType == "" {
		deviceType = "unknown"
	}

	// [COMMENT]: Xây dựng ExternalLoginRequest chuẩn hóa từ thông tin xác thực danh tính
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
		DevicePublicKey: strings.TrimSpace(req.PublicKey),
		TrustDevice:     req.TrustDevice,
		DeviceName:      deviceName,
		DeviceType:      deviceType,
		ClientDeviceID:  clientDeviceID,
		ZoneCode:        req.ZoneCode,
		RemoteIP:        strings.TrimSpace(req.ClientIp),
		UserAgent:       strings.TrimSpace(req.UserAgent),
	}
	// [COMMENT]: Gọi AuthService để xác thực danh tính bên ngoài và thiết lập phiên đăng nhập
	res, err := h.authService.VerifyExternalIdentity(ctx, loginReq)
	if err != nil {
		// [COMMENT]: Ánh xạ lỗi taxonomy từ tầng domain sang lỗi giao thức tương ứng
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
	// [COMMENT]: Xuất bản phản hồi kết quả xác thực thành công chứa thông tin phiên, MFA và token
	respond(&iamproto.VerifyExternalIdentityResponse{
		Valid:                 res.Valid,
		MfaRequired:           res.MFARequired,
		MfaSettingId:          res.MFASettingID,
		UserId:                res.UserID,
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

// [COMMENT]: optionalString chuyển đổi chuỗi rỗng thành con trỏ nil, tránh lưu trữ chuỗi rỗng không cần thiết
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
	// [COMMENT]: Thiết lập context với timeout 10 giây và định danh operation
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(context.Background(), "iam.auth.verify_credentials"), 10*time.Second)
	defer cancel()

	// [COMMENT]: Khởi tạo OpenTelemetry server span ghi nhận trace phân tán cho luồng xác thực credentials
	var span trace.Span
	if h.otel != nil {
		ctx, span = h.otel.StartServerSpan(ctx, "Redis iam.auth.verify_credentials")
		defer span.End()
		// [COMMENT]: Gắn các thuộc tính trace cho hệ thống messaging Redis
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
	// [COMMENT]: Trích xuất Request ID từ 16 byte đầu của envelope
	requestID, err := uuid.FromBytes(payload[:16])
	if err != nil || requestID == uuid.Nil {
		logger.SysWarnCtx(ctx, "Redis.VerifyUserCredentials", "Invalid request id envelope")
		return
	}
	reqData := payload[16:]
	// [COMMENT]: Cấu hình kênh phản hồi và khóa phân tán để đảm bảo tính idempotent giữa các replica
	replyChannel := verifyCredentialsReplyPrefix + requestID.String()
	lockKey := "iam:auth:dispatch:verify_credentials:" + requestID.String()
	// [COMMENT]: Tranh chấp distributed lock qua SetNX; replica thua lock sẽ im lặng để winner xử lý
	acquired, err := h.sharedRedis.SetNX(ctx, lockKey, "1", 30*time.Second).Result()
	if err != nil || !acquired {
		// [COMMENT]: Redis lỗi thì fail-close; replica thua lock im lặng để winner trả lời ACR.
		return
	}

	// [COMMENT]: Closure respond đóng gói và gửi phản hồi Protobuf về reply channel
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

	// [COMMENT]: Unmarshal Protobuf payload từ sau phần envelope
	var req iamproto.VerifyUserCredentialsRequest
	if err := proto.Unmarshal(reqData, &req); err != nil {
		logger.SysErrorCtx(ctx, "Redis.VerifyUserCredentials", "Failed to unmarshal request data")
		respond(&iamproto.VerifyUserCredentialsResponse{
			Valid:        false,
			ErrorMessage: "invalid request payload",
		})
		return
	}

	// [COMMENT]: Kiểm tra tính hợp lệ của username và password
	if req.Username == "" || req.Password == "" {
		logger.SysWarnCtx(ctx, "Redis.VerifyUserCredentials", "Username and password are required")
		respond(&iamproto.VerifyUserCredentialsResponse{
			Valid:        false,
			ErrorMessage: "Username and password are required",
		})
		return
	}

	// [COMMENT]: Parse client device ID nếu client có gửi kèm
	var clientDeviceID uuid.UUID
	if req.ClientDeviceId != "" {
		if parsed, err := uuid.Parse(req.ClientDeviceId); err == nil {
			clientDeviceID = parsed
		}
	}

	deviceName := strings.TrimSpace(req.DeviceName)
	if deviceName == "" {
		deviceName = "unknown device"
	}
	deviceType := strings.TrimSpace(req.DeviceType)
	if deviceType == "" {
		deviceType = "unknown"
	}

	// [COMMENT]: Xây dựng LoginRequest chuẩn hóa gửi xuống domain layer
	loginReq := iamEntity.LoginRequest{
		Username:        strings.TrimSpace(req.Username),
		Password:        req.Password,
		DevicePublicKey: strings.TrimSpace(req.PublicKey),
		TrustDevice:     req.TrustDevice,
		DeviceName:      deviceName,
		DeviceType:      deviceType,
		ClientDeviceID:  clientDeviceID,
		TenantDomain:    strings.ToLower(strings.TrimSpace(req.TenantDomain)),
		RemoteIP:        strings.TrimSpace(req.ClientIp),
		UserAgent:       strings.TrimSpace(req.UserAgent),
	}

	// [COMMENT]: Gọi AuthService để xác thực thông tin đăng nhập người dùng
	res, err := h.authService.VerifyUserCredentials(ctx, loginReq)
	if err != nil {
		// [COMMENT]: Xử lý các trường hợp lỗi từ domain: yêu cầu xác minh tài khoản, thiếu role, sai thông tin đăng nhập hoặc lỗi hệ thống
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

	// [COMMENT]: Tạo payload VerifyUserCredentialsResponse chứa kết quả xác thực, token và thông tin phiên
	resp := &iamproto.VerifyUserCredentialsResponse{
		Valid:                 res.Valid,
		MfaRequired:           res.MFARequired,
		MfaSettingId:          res.MFASettingID,
		UserId:                res.UserID,
		Level:                 res.Level,
		TenantId:              res.TenantID,
		ClientDeviceId:        res.ClientDeviceID,
		RefreshToken:          res.RefreshToken,
		RefreshTokenExpiresAt: res.RefreshTokenExpiresAt.Unix(),
		Username:              res.Username,
		ClientProofPublicKey:  res.ClientProofPublicKey,
	}
	// [COMMENT]: Gửi phản hồi thành công qua reply channel
	respond(resp)
}

// [COMMENT]: =========================================================================
// [COMMENT]: LUỒNG XÁC THỰC THỬ THÁCH MFA (VerifyMfaChallenge)
// [COMMENT]: =========================================================================
func (h *AuthRedisHandler) handleVerifyMfaChallenge(payload []byte) {
	// [COMMENT]: Thiết lập context với timeout 10 giây và định danh operation
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(context.Background(), "iam.auth.verify_mfa_challenge"), 10*time.Second)
	defer cancel()
	// [COMMENT]: Kiểm tra kích thước envelope chứa Request ID 16 bytes
	if len(payload) <= 16 {
		logger.SysWarnCtx(ctx, "Redis.VerifyMfaChallenge", "Missing request id envelope")
		return
	}
	// [COMMENT]: Parse Request ID từ envelope
	requestID, err := uuid.FromBytes(payload[:16])
	if err != nil || requestID == uuid.Nil {
		logger.SysWarnCtx(ctx, "Redis.VerifyMfaChallenge", "Invalid request id envelope")
		return
	}
	// [COMMENT]: Thiết lập reply channel và distributed lock key
	replyChannel := verifyMfaChallengeReplyPrefix + requestID.String()
	lockKey := "iam:auth:dispatch:verify_mfa_challenge:" + requestID.String()
	// [COMMENT]: Giành quyền xử lý challenge bằng SetNX lock 30s
	acquired, err := h.sharedRedis.SetNX(ctx, lockKey, "1", 30*time.Second).Result()
	if err != nil || !acquired {
		return
	}
	// [COMMENT]: Closure respond tuần tự hóa và gửi phản hồi VerifyMfaChallengeResponse
	respond := func(resp *iamproto.VerifyMfaChallengeResponse) {
		data, marshalErr := proto.Marshal(resp)
		if marshalErr != nil {
			logger.SysErrorCtx(ctx, "Redis.VerifyMfaChallenge", "Failed to marshal response payload")
			return
		}
		_ = h.sharedRedis.Publish(context.Background(), replyChannel, data).Err()
	}
	// [COMMENT]: Unmarshal Protobuf payload của yêu cầu xác thực MFA challenge
	var req iamproto.VerifyMfaChallengeRequest
	if err := proto.Unmarshal(payload[16:], &req); err != nil {
		respond(&iamproto.VerifyMfaChallengeResponse{ErrorMessage: "INVALID_MFA_CHALLENGE"})
		return
	}
	// [COMMENT]: Parse các UUID: UserID, MFASettingID, OperationID
	userID, err := uuid.Parse(strings.TrimSpace(req.UserId))
	mfaSettingID, mfaSettingErr := uuid.Parse(strings.TrimSpace(req.MfaSettingId))
	operationID, operationIDErr := uuid.Parse(strings.TrimSpace(req.OperationId))
	// [COMMENT]: Giải mã Ed25519 Public Key từ Base64 và chuẩn hóa method (totp / recovery_code)
	decodedPublicKey, publicKeyErr := base64.StdEncoding.DecodeString(req.PublicKey)
	method := strings.ToLower(strings.TrimSpace(req.Method))
	// [COMMENT]: Rào chắn wire boundary: kiểm tra tính hợp lệ của UUID, độ dài chuỗi, mã OTP/Recovery code và public key
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
	// [COMMENT]: Parse Client Device ID nếu có
	clientDeviceID := uuid.Nil
	if strings.TrimSpace(req.ClientDeviceId) != "" {
		clientDeviceID, err = uuid.Parse(req.ClientDeviceId)
		if err != nil || clientDeviceID == uuid.Nil {
			respond(&iamproto.VerifyMfaChallengeResponse{ErrorMessage: "INVALID_MFA_CHALLENGE"})
			return
		}
	}
	deviceName := strings.TrimSpace(req.DeviceName)
	if deviceName == "" {
		deviceName = "unknown device"
	}
	deviceType := strings.TrimSpace(req.DeviceType)
	if deviceType == "" {
		deviceType = "unknown"
	}

	// [COMMENT]: Gọi AuthService thực hiện xác minh mã MFA (TOTP / Recovery Code) và thiết lập session
	result, err := h.authService.VerifyMfaLogin(ctx, iamEntity.MFALoginRequest{
		UserID:          userID,
		MFASettingID:    mfaSettingID,
		Username:        strings.TrimSpace(req.Username),
		TenantDomain:    strings.ToLower(strings.TrimSpace(req.TenantDomain)),
		Method:          method,
		Code:            strings.TrimSpace(req.Code),
		DevicePublicKey: strings.TrimSpace(req.PublicKey),
		TrustDevice:     req.TrustDevice,
		DeviceName:      deviceName,
		DeviceType:      deviceType,
		ClientDeviceID:  clientDeviceID,
		RemoteIP:        strings.TrimSpace(req.ClientIp),
		UserAgent:       strings.TrimSpace(req.UserAgent),
	})
	if err != nil {
		// [COMMENT]: Ánh xạ lỗi MFA không hợp lệ hoặc lỗi hệ thống sang mã lỗi giao thức
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
	// [COMMENT]: Trả về thông tin phiên đã xác thực MFA thành công kèm refresh token và tenant scope
	respond(&iamproto.VerifyMfaChallengeResponse{
		Valid:                 result.Valid,
		UserId:                result.UserID,
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

// [COMMENT]: validMFALoginCode kiểm tra định dạng cú pháp mã MFA tương ứng với từng phương thức xác thực
func validMFALoginCode(method, code string) bool {
	code = strings.TrimSpace(code)
	switch method {
	case "totp":
		// [COMMENT]: Đối với TOTP: mã phải gồm chính xác 6 chữ số (0-9)
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
		// [COMMENT]: Đối với Recovery Code: mã phải gồm chính xác 16 ký tự thuộc bảng Base32/Crockford (loại trừ 0, 1, I, O)
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
		// [COMMENT]: Từ chối các phương thức MFA không được hỗ trợ
		return false
	}
}

// [COMMENT]: =========================================================================
// [COMMENT]: LUỒNG PHỤC HỒI PHIÊN NGƯỜI DÙNG (RecoverUserSession)
// [COMMENT]: =========================================================================
// handleRecoverUserSession authenticates the opaque user/device credential and
// resolves the requested runtime context in one database snapshot. It does not
// mutate the token and is deliberately isolated from tenant switching.
func (h *AuthRedisHandler) handleRecoverUserSession(payload []byte) {
	// [COMMENT]: Thiết lập context với timeout ngắn (650ms) đáp ứng yêu cầu độ trễ cực thấp cho hot path session recovery
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(context.Background(), "iam.auth.recover_user_session"), 650*time.Millisecond)
	defer cancel()

	// [COMMENT]: Khởi tạo OpenTelemetry server span ghi nhận trace phân tán
	var span trace.Span
	if h.otel != nil {
		ctx, span = h.otel.StartServerSpan(ctx, "Redis iam.auth.recover_user_session")
		defer span.End()
		span.SetAttributes(
			attribute.String("messaging.system", "redis"),
			attribute.String("messaging.destination", recoverUserSessionChannel),
		)
	}

	// [COMMENT]: Kiểm tra kích thước envelope tối thiểu 16 byte cho Request ID
	if len(payload) <= 16 {
		logger.SysWarnCtx(ctx, "Redis.RecoverUserSession", "Missing request id envelope")
		return
	}
	// [COMMENT]: Trích xuất Request ID từ 16 byte đầu envelope
	requestID, err := uuid.FromBytes(payload[:16])
	if err != nil || requestID == uuid.Nil {
		logger.SysWarnCtx(ctx, "Redis.RecoverUserSession", "Invalid request id envelope")
		return
	}
	reqData := payload[16:]
	// [COMMENT]: Cấu hình kênh phản hồi và khóa phân tán
	replyChannel := recoverUserSessionReplyPrefix + requestID.String()
	lockKey := "iam:auth:dispatch:recover_user_session:" + requestID.String()
	// [COMMENT]: Redis PubSub fan-out tới mọi replica. SetNX lock 5s đảm bảo đúng 1 replica chạm DB
	// Redis PubSub fans the request to every CP replica. The bounded lock makes
	// exactly one replica touch PostgreSQL; callers retry safely after timeout.
	acquired, err := h.sharedRedis.SetNX(ctx, lockKey, "1", 5*time.Second).Result()
	if err != nil || !acquired {
		return
	}

	// [COMMENT]: Closure respond đóng gói và gửi RecoverUserSessionResponse về Redis reply channel
	respond := func(resp *iamproto.RecoverUserSessionResponse) {
		respData, err := proto.Marshal(resp)
		if err != nil {
			logger.SysErrorCtx(ctx, "Redis.RecoverUserSession", "Failed to marshal response payload")
			return
		}
		if err := h.sharedRedis.Publish(ctx, replyChannel, respData).Err(); err != nil {
			logger.SysErrorCtx(ctx, "Redis.RecoverUserSession", "Failed to send Redis reply")
		}
	}

	// [COMMENT]: Giới hạn kích thước payload tối đa 4KB để chống payload phình to bất thường
	if len(reqData) > 4<<10 {
		respond(&iamproto.RecoverUserSessionResponse{})
		return
	}
	// [COMMENT]: Unmarshal Protobuf payload của RecoverUserSessionRequest
	var req iamproto.RecoverUserSessionRequest
	if err := proto.Unmarshal(reqData, &req); err != nil {
		logger.SysWarnCtx(ctx, "Redis.RecoverUserSession", "Invalid request payload")
		respond(&iamproto.RecoverUserSessionResponse{})
		return
	}
	// [COMMENT]: Kiểm tra độ dài hợp lệ của opaque refresh token (64 - 512 ký tự)
	rawRefreshToken := strings.TrimSpace(req.RefreshToken)
	if len(rawRefreshToken) < 64 || len(rawRefreshToken) > 512 {
		respond(&iamproto.RecoverUserSessionResponse{})
		return
	}

	// [COMMENT]: Parse requested Tenant ID nếu client yêu cầu chuyển đổi hoặc chỉ định tenant context
	var requestedTenantID *uuid.UUID
	if req.RequestedTenantId != nil {
		parsedTenantID, parseErr := uuid.Parse(strings.TrimSpace(*req.RequestedTenantId))
		if parseErr != nil || parsedTenantID == uuid.Nil {
			respond(&iamproto.RecoverUserSessionResponse{})
			return
		}
		requestedTenantID = &parsedTenantID
	}

	// [COMMENT]: Gọi SessionRefreshService để giải mã token và xác định quyền runtime trong một database snapshot duy nhất
	recovered, err := h.sessionRefreshService.RecoverUserSession(ctx, rawRefreshToken, requestedTenantID)
	if err != nil {
		// [COMMENT]: Lỗi hạ tầng fail-closed không trả response để ACR timeout và bảo vệ tính an toàn của phiên
		// Infrastructure failures have no domain response. The ACR timeout is the
		// fail-closed availability boundary and never becomes invalid credentials.
		logger.SysErrorCtx(ctx, "Redis.RecoverUserSession", "Session recovery unavailable")
		return
	}
	if recovered == nil {
		logger.SysErrorCtx(ctx, "Redis.RecoverUserSession", "Session recovery returned no outcome")
		return
	}

	// [COMMENT]: Xây dựng RecoverUserSessionResponse chứa trạng thái hợp lệ của credential và quyền context
	response := &iamproto.RecoverUserSessionResponse{
		CredentialValid:            recovered.CredentialValid,
		ContextAuthorized:          recovered.ContextAuthorized,
		PersonalFallbackAuthorized: recovered.PersonalFallbackAuthorized,
	}
	// [COMMENT]: Gán thông tin danh tính người dùng nếu credential hợp lệ
	if recovered.CredentialValid {
		response.UserId = recovered.UserID.String()
		response.ClientDeviceId = recovered.ClientDeviceID
		response.Username = recovered.Username
	}
	// [COMMENT]: Gán quyền Role Level và Tenant ID đã giải quyết nếu context hoặc personal fallback được cấp quyền
	if recovered.ContextAuthorized || recovered.PersonalFallbackAuthorized {
		response.RoleLevel = recovered.RoleLevel
		if recovered.ResolvedTenantID != nil {
			response.ResolvedTenantId = recovered.ResolvedTenantID.String()
		}
	}
	// [COMMENT]: Xuất bản phản hồi về reply channel cho ACR
	respond(response)
}

// =========================================================================
// 3. LUỒNG THU HỒI REFRESH TOKEN (RevokeOpaqueRefreshToken)
// =========================================================================
func (h *AuthRedisHandler) handleRevokeOpaqueToken(payload []byte) {
	// [COMMENT]: Thiết lập context với timeout 650ms và định danh operation
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(context.Background(), "iam.auth.revoke_opaque_token"), 650*time.Millisecond)
	defer cancel()

	// [COMMENT]: Tạo OpenTelemetry span ghi nhận vết phân tán cho thao tác thu hồi token
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
	// [COMMENT]: Parse Request ID từ 16 byte đầu envelope
	requestID, err := uuid.FromBytes(payload[:16])
	if err != nil || requestID == uuid.Nil {
		logger.SysWarnCtx(ctx, "Redis.RevokeOpaqueRefreshToken", "Invalid request id envelope")
		return
	}
	reqData := payload[16:]
	// [COMMENT]: Thiết lập reply channel và distributed lock key 30s tránh khuếch đại write load trên nhiều replica
	replyChannel := revokeOpaqueTokenReplyPrefix + requestID.String()
	lockKey := "iam:auth:dispatch:revoke_opaque_token:" + requestID.String()
	// [COMMENT]: Tranh chấp distributed lock qua SetNX
	acquired, err := h.sharedRedis.SetNX(ctx, lockKey, "1", 30*time.Second).Result()
	if err != nil || !acquired {
		return
	}

	// [COMMENT]: Giới hạn kích thước payload tối đa 4KB
	var req iamproto.RevokeOpaqueRefreshTokenRequest
	if len(reqData) > 4<<10 {
		logger.SysWarnCtx(ctx, "Redis.RevokeOpaqueRefreshToken", "Request payload exceeds limit")
		return
	}
	// [COMMENT]: Unmarshal Protobuf payload của RevokeOpaqueRefreshTokenRequest
	if err := proto.Unmarshal(reqData, &req); err != nil {
		logger.SysWarnCtx(ctx, "Redis.RevokeOpaqueRefreshToken", "Invalid request payload")
		return
	}
	// [COMMENT]: Kiểm tra độ dài hợp lệ của opaque refresh token
	rawRefreshToken := strings.TrimSpace(req.RefreshToken)
	if len(rawRefreshToken) < 64 || len(rawRefreshToken) > 512 {
		logger.SysWarnCtx(ctx, "Redis.RevokeOpaqueRefreshToken", "Invalid refresh credential shape")
		return
	}

	// [COMMENT]: Gọi SessionRefreshService để thu hồi token bền vững trong database
	err = h.sessionRefreshService.RevokeOpaqueRefreshToken(ctx, rawRefreshToken)
	if err != nil {
		// [COMMENT]: Nếu thu hồi thất bại, không trả response để ACR timeout giữ cookie/trạng thái fail-safe
		// ACR must time out and keep cookies when durable revocation is not proven.
		logger.SysErrorCtx(ctx, "Redis.RevokeOpaqueRefreshToken", "Failed to revoke refresh credential")
		return
	}

	// [COMMENT]: Đóng gói và gửi phản hồi thu hồi token thành công về reply channel
	respData, marshalErr := proto.Marshal(&iamproto.RevokeOpaqueRefreshTokenResponse{})
	if marshalErr != nil {
		logger.SysErrorCtx(ctx, "Redis.RevokeOpaqueRefreshToken", "Failed to marshal revoke response")
		return
	}
	if publishErr := h.sharedRedis.Publish(ctx, replyChannel, respData).Err(); publishErr != nil {
		logger.SysErrorCtx(ctx, "Redis.RevokeOpaqueRefreshToken", "Failed to publish revoke response")
	}
}

// [COMMENT]: Stop dừng tất cả các Redis PubSub subscription và chờ các worker goroutine hoàn tất công việc
func (h *AuthRedisHandler) Stop() {
	if h == nil {
		return
	}
	// [COMMENT]: Hủy context subscription
	if h.cancel != nil {
		h.cancel()
	}
	// [COMMENT]: Đóng kết nối PubSub Redis
	if h.pubsub != nil {
		_ = h.pubsub.Close()
	}
	// [COMMENT]: Đợi vòng lặp nhận message và các worker đang chạy hoàn thành trước khi thoát
	h.loopWG.Wait()
	h.workWG.Wait()
}
