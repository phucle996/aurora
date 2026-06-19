// ======================================================================================================
// 📂 MODULE: controlplane/internal/iam/service/refresh_token_service.go
//            Đặc Tả Nghiệp Vụ Làm Mới Phiên Truy Cập Người Dùng (User Refresh Token Service)
// ======================================================================================================
//
// 📜 HIỆP ĐỒNG THIẾT KẾ & PHÂN CHIA QUYỀN LỰC (CONTRACT & ARCHITECTURAL PLANES):
//   - Phân hệ này thuộc MẶT PHẲNG NGHIỆP VỤ NGƯỜI DÙNG VÀ NỀN TẢNG (USER & PLATFORM PLANE)
//     chịu trách nhiệm xoay vòng và tái cấp phát các thông tin định danh cho tài khoản người dùng
//     và thiết bị trong hệ thống khi JWT Access Token hết hạn mà không cần bắt người dùng đăng nhập lại.
//   - Tiến trình này liên kết chặt chẽ với cơ chế bảo mật chống tấn công replay/hijacking thông qua
//     hoạt động xoay vòng Refresh Token (Token Rotation) một lần và cơ chế so khớp runtime session
//     sử dụng Redis Cache Engine L2.
//
// 🎯 SOURCE OF TRUTH (SoT):
//   - Postgres Database lưu giữ trạng thái Refresh Token thực tế (Hash SHA256) thông qua các
//     bản ghi Session, User, và Device.
//   - Redis L2 Cache lưu trữ thông tin phiên truy cập (`UserAccessSession`) làm cơ sở
//     cho Middleware đối sánh credentials (`access_key`/`access_secret`) trong các request nghiệp vụ tiếp theo.
//
// 🔒 RANH GIỚI BẢO MẬT NGHIÊM NGẶT (CRITICAL SECURITY BOUNDARY):
//   - **Token Rotation**: Mỗi Refresh Token chỉ có giá trị sử dụng DUY NHẤT một lần (One-time use).
//     Khi request `Refresh` được gửi lên, hệ thống sẽ sinh ra cặp Access & Refresh Token mới,
//     đồng thời vô hiệu hóa Refresh Token cũ ngay lập tức bằng phương thức `RotateRefreshToken` của repository.
//   - **Phân tách lỗi & Telemetry**: Mọi lỗi xảy ra đều được đóng gói dạng `apperr.Wrap` cùng taxonomy
//     tương ứng (ví dụ: `iamMetrics.OutcomeInvalidCredential` hoặc `iamMetrics.OutcomeFailureUnknown`) để đảm bảo tính nhất quán
//     cho hệ thống giám sát và không để lộ thông tin chi tiết về cơ sở dữ liệu cho Client.
//
// ======================================================================================================

package iamSvcImpl

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"controlplane/internal/cacheengine"
	"controlplane/internal/config"
	coreEntity "controlplane/internal/core/domain/entity"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamMetrics "controlplane/internal/iam/metrics"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/internal/security"
	"controlplane/pkg/apperr"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// RefreshTokenService chịu trách nhiệm thực thi logic nghiệp vụ cho việc
// xoay vòng Refresh Token và đồng bộ runtime session xuống Redis.
type RefreshTokenService struct {
	repo        iamRepoInterface.RefreshTokenRepository
	cacheEngine *cacheengine.CacheRegistry
	cfg         *config.Config
}

// NewRefreshTokenService khởi tạo một instance mới của RefreshTokenService.
func NewRefreshTokenService(
	cfg *config.Config,
	repo iamRepoInterface.RefreshTokenRepository,
	cacheEngine *cacheengine.CacheRegistry,
) iamSvcInterface.RefreshTokenService {
	return &RefreshTokenService{
		repo:        repo,
		cacheEngine: cacheEngine,
		cfg:         cfg,
	}
}

// Refresh thực hiện xác thực Refresh Token hiện tại, xoay vòng tạo ra Refresh Token mới,
// cấp mới Access Token và Access Key/Secret, đồng thời cập nhật runtime session vào Redis cache.
func (s *RefreshTokenService) Refresh(ctx context.Context, rawRefreshToken string) (result *iamEntity.RefreshTokenResult, err error) {
	const workflow = "refresh_token"

	refreshOutcome := iamMetrics.OutcomeSuccess
	defer func() {
		// Ghi nhận metrics cuộc gọi dịch vụ refresh token phục vụ đo lường hệ thống
		iamMetrics.ServiceCall(ctx, refreshOutcome)
	}()

	// ==========================================================================
	// BƯỚC 1: TẢI THÔNG TIN PHIÊN LÀM VIỆC TỪ DATABASE (POSTGRESQL)
	// ==========================================================================
	// Thực hiện băm SHA256 Refresh Token thô nhận được từ phía Client để so khớp với DB.
	startLoad := time.Now()
	refreshContext, ctxErr := s.repo.LoadRefreshContextByHash(ctx, security.HashTokenSHA256(rawRefreshToken))
	if ctxErr != nil {
		if errors.Is(ctxErr, iamTaxonomy.ErrNotFound) {
			iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "LoadRefreshContextByHash", iamMetrics.OutcomeInvalidCredential, time.Since(startLoad), ctxErr)
			return nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, ctxErr, iamMetrics.OutcomeInvalidCredential)
		}
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "LoadRefreshContextByHash", iamMetrics.OutcomeFailureUnknown, time.Since(startLoad), ctxErr)
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, ctxErr, iamMetrics.OutcomeFailureUnknown)
	}
	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "LoadRefreshContextByHash", iamMetrics.OutcomeSuccess, time.Since(startLoad), nil)

	// ==========================================================================
	// BƯỚC 2: KIỂM TRA TRẠNG THÁI HIỆU LỰC CỦA PHIÊN, USER VÀ THIẾT BỊ
	// ==========================================================================
	session := &refreshContext.Session
	// Kiểm tra xem token đã quá hạn sử dụng hay chưa
	if time.Now().UTC().After(session.ExpiresAt) {
		refreshOutcome = iamMetrics.OutcomeInvalidCredential
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, nil, iamMetrics.OutcomeInvalidCredential)
	}
	trackedDeviceID := *session.DeviceID

	user := &refreshContext.User
	// Xác nhận tài khoản người dùng có tồn tại hợp lệ
	if user.ID == (uuid.UUID{}) {
		refreshOutcome = iamMetrics.OutcomeInvalidCredential
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, nil, iamMetrics.OutcomeInvalidCredential)
	}
	// Kiểm tra trạng thái tài khoản để ngăn chặn các phiên của user bị treo/vô hiệu hóa/đang chờ kích hoạt
	if user.Status == iamEntity.UserStatusPendingActive || user.Status == iamEntity.UserStatusSuspended || user.Status == iamEntity.UserStatusDisabled {
		refreshOutcome = iamMetrics.OutcomeInvalidCredential
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, nil, iamMetrics.OutcomeInvalidCredential)
	}
	// Kiểm tra trạng thái thiết bị có bị thu hồi quyền truy cập (Revoked) hay không
	if refreshContext.Device == nil || refreshContext.Device.Status == iamEntity.DeviceStatusRevoked {
		refreshOutcome = iamMetrics.OutcomeInvalidCredential
		return nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, nil, iamMetrics.OutcomeInvalidCredential)
	}

	now := time.Now().UTC()

	// ==========================================================================
	// BƯỚC 3: SINH KHÓA VÀ CẶP BẢN TIN CREDENTIALS MỚI (ACCESS KEY & SECRET)
	// ==========================================================================
	// Tạo mới Token ID sử dụng UUIDv7 đóng vai trò JTI Claim
	accessJTI, idErr := uuid.NewV7()
	if idErr != nil {
		refreshOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, idErr, iamMetrics.OutcomeFailureUnknown)
	}
	// Sinh ngẫu nhiên access key để làm định danh phiên làm việc mới
	accessKey := uuid.NewString()
	// Sinh ngẫu nhiên access secret dài 32 bytes có tính mật mã bảo mật cao
	accessSecret, secretErr := security.GenerateToken(32)
	if secretErr != nil {
		refreshOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, secretErr, iamMetrics.OutcomeFailureUnknown)
	}
	accessExpiresAt := now.Add(s.cfg.Security.AccessSecretTTL)

	// ==========================================================================
	// BƯỚC 4: TẢI KHÓA KÝ MẬT JWT TỪ L1/L2 CACHE REGISTRY
	// ==========================================================================
	startCache := time.Now()
	val, err := s.cacheEngine.GetOrLoad(ctx, "access_secret", "")
	if err != nil {
		iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineL1, "GetOrLoad", iamMetrics.OutcomeFailureUnknown, time.Since(startCache), err)
		refreshOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, err, iamMetrics.OutcomeFailureUnknown)
	}
	secrets, ok := val.(*coreEntity.RuntimeSecrets)
	if !ok || secrets == nil {
		iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineL1, "GetOrLoad", iamMetrics.OutcomeFailureUnknown, time.Since(startCache), errors.New("invalid runtime secrets type"))
		refreshOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, errors.New("invalid runtime secrets type"), refreshOutcome)
	}
	iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineL1, "GetOrLoad", iamMetrics.OutcomeSuccess, time.Since(startCache), nil)

	// ==========================================================================
	// BƯỚC 5: KÝ JWT ACCESS TOKEN MỚI
	// ==========================================================================
	accessToken, accessErr := security.SignWithSecret(security.Claims{
		Subject:   user.ID.String(),
		Role:      "",
		Level:     0,
		AccessKey: accessKey,
		TokenID:   accessJTI.String(),
		TokenUse:  "access",
		IssuedAt:  now.Unix(),
		ExpiresAt: accessExpiresAt.Unix(),
	}, secrets.Active.Secret)
	if accessErr != nil {
		refreshOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, accessErr, iamMetrics.OutcomeFailureUnknown)
	}

	// ==========================================================================
	// BƯỚC 6: TẠO REFRESH TOKEN TIẾP THEO (ROTATION TOKEN)
	// ==========================================================================
	rawNextRefreshToken, refreshErr := security.GenerateToken(43)
	if refreshErr != nil {
		refreshOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, refreshErr, iamMetrics.OutcomeFailureUnknown)
	}
	nextRefreshID, refreshIDErr := uuid.NewV7()
	if refreshIDErr != nil {
		refreshOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, apperr.Wrap(iamTaxonomy.ErrUuidGenerateFailed, refreshIDErr, iamMetrics.OutcomeFailureUnknown)
	}
	nextRefreshExpiresAt := now.Add(s.cfg.Security.RefreshTokenTTL)
	nextRefreshToken := iamEntity.RefreshToken{
		ID:            nextRefreshID,
		UserID:        session.UserID,
		DeviceID:      &trackedDeviceID,
		TokenHash:     security.HashTokenSHA256(rawNextRefreshToken),
		TokenFamilyID: session.TokenFamilyID,
		TenantID:      nil,
		IssuedAt:      now,
		ExpiresAt:     nextRefreshExpiresAt,
	}

	// ==========================================================================
	// BƯỚC 7: THỰC THI XOAY VÒNG REFRESH TOKEN ĐỒNG BỘ Ở DATABASE (CAS/ROTATE)
	// ==========================================================================
	// Hàm RotateRefreshToken sẽ thực hiện thu hồi token cũ và chèn token mới trong một Transaction
	startRotate := time.Now()
	if rotateErr := s.repo.RotateRefreshToken(ctx, *session, nextRefreshToken); rotateErr != nil {
		if errors.Is(rotateErr, iamTaxonomy.ErrInvalidSession) || errors.Is(rotateErr, pgx.ErrNoRows) {
			iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "RotateRefreshToken", iamMetrics.OutcomeInvalidCredential, time.Since(startRotate), rotateErr)
			refreshOutcome = iamMetrics.OutcomeInvalidCredential
			return nil, apperr.Wrap(iamTaxonomy.ErrInvalidSession, rotateErr, iamMetrics.OutcomeInvalidCredential)
		}
		iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "RotateRefreshToken", iamMetrics.OutcomeFailureUnknown, time.Since(startRotate), rotateErr)
		refreshOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, rotateErr, iamMetrics.OutcomeFailureUnknown)
	}
	iamMetrics.Downstream(ctx, iamMetrics.KindRepo, "RotateRefreshToken", iamMetrics.OutcomeSuccess, time.Since(startRotate), nil)

	// ==========================================================================
	// BƯỚC 8: ĐỒNG BỘ TRẠNG THÁI RUNTIME CỦA THIẾT BỊ VÀO REDIS CACHE ENGINE L2
	// ==========================================================================
	// Băm hash Access Secret mới trước khi lưu xuống cache để tránh lộ plaintext bí mật
	newAccessSecretHash := security.HashTokenSHA256(rawNextRefreshToken[:0] + accessSecret)
	sessionRecord := iamEntity.UserAccessSession{
		AccessSecretHash: newAccessSecretHash,
		TrackedDeviceID:  trackedDeviceID.String(),
		LastSeenAt:       now.Unix(),
	}
	payload, marshalErr := json.Marshal(sessionRecord)
	if marshalErr != nil {
		refreshOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, marshalErr, iamMetrics.OutcomeFailureUnknown)
	}

	sessionKey := "iam:user_access_session:" + user.ID.String() + ":" + accessKey
	indexKey := "iam:user_access_index:" + user.ID.String()
	runtimeTTL := s.cfg.Security.AccessSecretTTL

	startSetRuntime := time.Now()
	rdb := s.cacheEngine.L2.Client()
	pipe := rdb.Pipeline()
	pipe.Set(ctx, sessionKey, payload, runtimeTTL)
	pipe.SAdd(ctx, indexKey, accessKey)
	pipe.Expire(ctx, indexKey, runtimeTTL*3)
	_, setErr := pipe.Exec(ctx)

	if setErr != nil {
		iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineL1, "setTrinityToken", iamMetrics.OutcomeFailureUnknown, time.Since(startSetRuntime), setErr)
		refreshOutcome = iamMetrics.OutcomeFailureUnknown
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, setErr, iamMetrics.OutcomeFailureUnknown)
	}
	iamMetrics.Downstream(ctx, iamMetrics.KindCacheEngineL1, "setTrinityToken", iamMetrics.OutcomeSuccess, time.Since(startSetRuntime), nil)

	// Trả về bộ kết quả làm mới phiên thành công cho Client
	return &iamEntity.RefreshTokenResult{
		AccessToken:      accessToken,
		RefreshToken:     rawNextRefreshToken,
		AccessKey:        accessKey,
		AccessSecret:     accessSecret,
		TrackedDeviceID:  trackedDeviceID.String(),
		AccessExpiresAt:  accessExpiresAt,
		RefreshExpiresAt: nextRefreshExpiresAt,
	}, nil
}

// RevokeRefreshTokensByDeviceIDAndUserID thu hồi tất cả refresh token thuộc về một thiết bị và một user xác định.
func (s *RefreshTokenService) RevokeRefreshTokensByDeviceIDAndUserID(ctx context.Context, userID uuid.UUID, deviceID uuid.UUID) error {
	_, err := s.repo.RevokeRefreshTokensByDeviceIDAndUserID(ctx, userID, deviceID)
	return err
}

// RevokeRefreshTokensByUserID thu hồi tất cả refresh token của một user xác định.
func (s *RefreshTokenService) RevokeRefreshTokensByUserID(ctx context.Context, userID uuid.UUID, exceptDeviceID *uuid.UUID) error {
	_, err := s.repo.RevokeRefreshTokensByUserID(ctx, userID, exceptDeviceID)
	return err
}
