package coreSvcImpl

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"time"

	"controlplane/internal/cacheengine"
	coreEntity "controlplane/internal/core/domain/entity"
	coreRepoInterface "controlplane/internal/core/domain/repo"
	coreSvcInterface "controlplane/internal/core/domain/service"
	"controlplane/internal/security"
	"controlplane/pkg/logger"
)

const (
	// Kích thước của secret thô (entropy) trước khi encode Base64.
	bootstrapSecretBytes = 32
)

// SecretRotationService chịu trách nhiệm khởi tạo và xoay vòng các loại secret dùng cho hệ thống
// theo cơ chế Active-Standby hai luồng song song, tránh downtime khi verify token.
type SecretRotationService struct {
	repo         coreRepoInterface.SecretRepository
	l1Registry   *cacheengine.CacheRegistry // Tích hợp CacheRegistry từ cache-engine để xử lý cache L1 cục bộ
	l1Fanout     *cacheengine.RedisFanout   // Tích hợp RedisFanout để phát tin nhắn xóa cache trên toàn cụm (Pub/Sub)
	Now          func() time.Time
	isRotatingMu sync.Mutex
	isRotating   map[string]time.Time // Quản lý thời gian xoay vòng tránh spam yêu cầu xoay vòng liên tục
}

// NewSecretRotationService tạo mới một instance điều phối xoay vòng secret
func NewSecretRotationService(
	repo coreRepoInterface.SecretRepository,
	l1Registry *cacheengine.CacheRegistry,
	l1Fanout *cacheengine.RedisFanout,
) coreSvcInterface.SecretRotationService {
	return &SecretRotationService{
		repo:       repo,
		l1Registry: l1Registry,
		l1Fanout:   l1Fanout,
		Now:        time.Now,
		isRotating: make(map[string]time.Time),
	}
}

// EnsureInitialSecrets đảm bảo rằng loại secret được yêu cầu đã tồn tại trong DB khi khởi tạo hệ thống.
// Hàm này sử dụng PostgreSQL Advisory Lock để đảm bảo chỉ có duy nhất một instance trong cụm thực thi việc khởi tạo.
func (s *SecretRotationService) EnsureInitialSecrets(ctx context.Context, secretType string) (*coreEntity.RuntimeSecrets, error) {
	secretType = strings.TrimSpace(secretType)
	if secretType == "" {
		return nil, errors.New("empty secret type")
	}

	// 1. Acquire khóa PostgreSQL Advisory Lock độc quyền theo loại secret để tránh race condition giữa các replica
	lock, err := s.repo.AcquireSecretTypeBootstrapLock(ctx, secretType)
	if err != nil {
		return nil, err
	}
	defer lock.Release(context.Background())

	// 2. Kiểm tra xem secret đã được khởi tạo bởi replica khác trước đó chưa
	secrets, err := s.repo.GetSecretsByType(ctx, secretType)
	if err != nil {
		return nil, err
	}
	if secrets != nil {
		return secrets, nil
	}

	// 3. Nếu chưa có, tiến hành sinh ngẫu nhiên 2 cặp secret độc lập cho cả Active và Standby
	plainActive, cipherActive, fingerActive, err := generateSecretMaterial()
	if err != nil {
		return nil, err
	}
	plainStandby, cipherStandby, fingerStandby, err := generateSecretMaterial()
	if err != nil {
		return nil, err
	}

	now := s.Now().UTC()
	row := coreEntity.CoreSecretRow{
		SecretType:         secretType,
		ActiveSecret:       cipherActive,
		ActiveFingerprint:  fingerActive,
		ActiveCreatedAt:    now,
		StandbySecret:      cipherStandby,
		StandbyFingerprint: fingerStandby,
		StandbyCreatedAt:   now,
		UpdatedAt:          now,
	}

	// 4. Lưu cặp secret vừa tạo vào Database
	if err := s.repo.SaveSecrets(ctx, row); err != nil {
		return nil, err
	}

	// 5. Đồng bộ hóa Cache L1: Invalidate bản ghi local trong registry
	s.l1Registry.InvalidateLocal(ctx, secretType)

	// 6. Phát tín hiệu xóa cache tới tất cả các replica khác trong cụm thông qua Redis Fanout Bus
	if _, err := s.l1Fanout.Publish(ctx, secretType, nil); err != nil {
		logger.SysWarnFields("core.secret.invalidate", "failed to publish fanout invalidation", err, logger.Fields{"secret_type": secretType})
	}

	// 7. Trả về thông tin Plaintext cho runtime sử dụng ngay lập tức
	return &coreEntity.RuntimeSecrets{
		SecretType: secretType,
		Active: coreEntity.RuntimeSecret{
			Secret:      plainActive,
			Fingerprint: fingerActive,
			CreatedAt:   now,
		},
		Standby: coreEntity.RuntimeSecret{
			Secret:      plainStandby,
			Fingerprint: fingerStandby,
			CreatedAt:   now,
		},
		LoadedAt: now,
	}, nil
}

// RotateSecret thực hiện xoay vòng khóa nguyên tử theo cơ chế Active-Standby:
// - Khóa Active hiện tại sẽ được đẩy xuống làm khóa Standby.
// - Một khóa Active hoàn toàn mới sẽ được sinh ra để ký mới các token tiếp theo.
// Quá trình này giúp hệ thống luôn chấp nhận cả token cũ ký bằng Standby và token mới ký bằng Active, loại bỏ downtime.
func (s *SecretRotationService) RotateSecret(ctx context.Context, secretType string) (*coreEntity.RuntimeSecrets, error) {
	secretType = strings.TrimSpace(secretType)
	if secretType == "" {
		return nil, errors.New("empty secret type")
	}

	// 1. Rate Limiting ở RAM local (Khóa 30 giây): Ngăn chặn việc spam API xoay vòng liên tục gây quá tải Redis/Database
	s.isRotatingMu.Lock()
	lastRotateTime, rotating := s.isRotating[secretType]
	if rotating && s.Now().Sub(lastRotateTime) < 30*time.Second {
		s.isRotatingMu.Unlock()
		return s.repo.GetSecretsByType(ctx, secretType)
	}
	s.isRotating[secretType] = s.Now()
	s.isRotatingMu.Unlock()

	// 2. Acquire khóa PostgreSQL Advisory Lock độc quyền phục vụ việc xoay vòng khóa
	lock, err := s.repo.AcquireSecretTypeRotationLock(ctx, secretType)
	if err != nil {
		return nil, err
	}
	defer lock.Release(context.Background())

	// 3. Lấy thông tin secret hiện tại đang lưu trong DB
	current, err := s.repo.GetSecretsByType(ctx, secretType)
	if err != nil {
		return nil, err
	}
	// Nếu chưa từng có, tiến hành bootstrap
	if current == nil {
		return s.EnsureInitialSecrets(ctx, secretType)
	}

	// Chống xoay vòng kép (Double Rotation) từ các replica khác nhau trong cụm
	if s.Now().Sub(current.Active.CreatedAt) < 30*time.Second {
		return current, nil
	}

	// 4. Tạo secret mới làm Active mới
	plainNew, cipherNew, fingerNew, err := generateSecretMaterial()
	if err != nil {
		return nil, err
	}

	// 5. Mã hóa lại khóa Active hiện tại bằng Master Key để lưu trữ an toàn dưới vai trò Standby mới
	cipherActiveCurrent, err := security.EncryptSecretBytes(current.Active.Secret)
	if err != nil {
		return nil, err
	}

	// 6. Thực hiện update nguyên tử xuống DB: Active mới ghi đè Active cũ; Active cũ chuyển thành Standby mới
	err = s.repo.UpdateSecrets(ctx,
		secretType,
		cipherNew,
		fingerNew,
		cipherActiveCurrent,
		current.Active.Fingerprint,
	)
	if err != nil {
		return nil, err
	}

	// 7. Đồng bộ hóa xóa cache local
	s.l1Registry.InvalidateLocal(ctx, secretType)

	// 8. Phát tin nhắn xóa cache cho các replica khác thông qua Redis Pub/Sub
	if _, err := s.l1Fanout.Publish(ctx, secretType, nil); err != nil {
		logger.SysWarnFields("core.secret.invalidate", "failed to publish fanout invalidation", err, logger.Fields{"secret_type": secretType})
	}

	now := s.Now().UTC()
	return &coreEntity.RuntimeSecrets{
		SecretType: secretType,
		Active: coreEntity.RuntimeSecret{
			Secret:      plainNew,
			Fingerprint: fingerNew,
			CreatedAt:   now,
		},
		Standby: coreEntity.RuntimeSecret{
			Secret:      current.Active.Secret,
			Fingerprint: current.Active.Fingerprint,
			CreatedAt:   current.Active.CreatedAt,
		},
		LoadedAt: now,
	}, nil
}

// generateSecretMaterial sinh ngẫu nhiên entropy thô từ crypto/rand, mã hóa AES-GCM qua security package
// và tính toán SHA-256 fingerprint phục vụ định danh và check trùng lặp secret.
func generateSecretMaterial() (plain []byte, cipherText string, fingerprint string, err error) {
	raw := make([]byte, bootstrapSecretBytes)
	if _, err = cryptorand.Read(raw); err != nil {
		return nil, "", "", err
	}
	// Dùng Base64 Raw URL Encoding để đảm bảo an toàn khi truyền tải/sử dụng
	plain = []byte(base64.RawURLEncoding.EncodeToString(raw))

	// Mã hóa đối xứng AES-GCM thông qua Master Key cấu hình của runtime
	cipherText, err = security.EncryptSecretBytes(plain)
	if err != nil {
		return nil, "", "", err
	}

	// Sinh fingerprint (SHA-256 hash của plaintext) để hỗ trợ so sánh và truy vết vận hành không nhạy cảm
	sum := sha256.Sum256(plain)
	fingerprint = base64.RawURLEncoding.EncodeToString(sum[:])
	return plain, cipherText, fingerprint, nil
}
