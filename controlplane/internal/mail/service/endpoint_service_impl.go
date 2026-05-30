// ============================================================================
// 📂 PHÂN HỆ: controlplane/internal/mail/service/endpoint_service_impl.go
//            Đặc Tả Nghiệp Vụ Quản Trị Mail Endpoint Cấp Hạ Tầng (Tier-0)
// ============================================================================
//
// 📜 HIỆP ĐỒNG THIẾT KẾ & PHÂN CHIA HẠ TẦNG (ARCHITECTURAL & DOMAIN CONTRACT):
//   1. [Zone Isolation Contract]: Mọi hoạt động quản trị vật lý Mail Endpoint bắt buộc phải
//      được cô lập nghiêm ngặt theo ZoneID. Không bao giờ cho phép cấu hình của zone này
//      rò rỉ sang zone khác, đảm bảo tính sẵn sàng cao (HA) và phân vùng lưu lượng vật lý.
//   2. [Strict Type Safety]: Enforce việc sử dụng native uuid.UUID (đặc biệt là UUIDv7)
//      để làm mã định danh duy nhất cho Endpoint, loại bỏ hoàn toàn rủi ro trùng lặp hoặc phân
//      loại lỗi ở mức ranh giới nghiệp vụ.
//   3. [Clean Repository Separation]: Repository layer chỉ chịu trách nhiệm lưu trữ thô, không
//      được phép biết về nghiệp vụ mã hóa. Tầng Service này là ranh giới duy nhất quản lý khóa,
//      thực hiện mã hóa phong bì (Envelope Encryption) trước khi lưu dữ liệu nhạy cảm.
//
// 🔒 RANH GIỚI BẢO MẬT & MÃ HÓA PHONG BÌ (ENVELOPE ENCRYPTION BOUNDARY):
//   - Cấu hình kết nối (`ConnectionConfig`) của SMTP/SaaS chứa thông tin tối mật (Password, API Key).
//   - Tầng Service này chịu trách nhiệm đóng gói cấu hình thành JSON, sau đó mã hóa đối xứng AES-256-GCM
//     qua thư viện `security.EncryptSecret` trước khi chuyển xuống Repository dưới dạng binary opaque block (`[]byte`).
//   - Khi truy vấn dữ liệu lên, Service tự giải mã qua `security.DecryptSecret` và chuyển đổi ngược lại
//     thành cấu hình dạng Map để sử dụng nội bộ, đảm bảo thông tin nhạy cảm không bao giờ lộ ra database dưới dạng văn bản thô.
//
// 📊 GIÁ TRỊ VẬN HÀNH & SRE TELEMETRY (SRE OPERATIONAL TELEMETRY & OBSERVABILITY):
//   - Sử dụng Prometheus metric `EndpointOperationsCounter` để đếm tổng số thao tác vận hành trên Endpoint
//     (create, update, delete, list, get, test_connection) được gán nhãn theo operation, physical zone, và outcome.
//   - Mọi lỗi phát sinh đều được đóng gói qua `apperr.Wrap` để đính kèm correlation outcome chính xác
//     (invalid_argument, crypto_error, database_error, v.v.), giúp SRE dễ dàng đồng bộ giữa Prometheus và Loki log.
//
// 👥 VAI TRÒ VÀ GHI CHÚ VẬN HÀNH TRÊN PRODUCTION:
//
//   📌 ĐỐI VỚI SRE & DEVOPS PLATFORM ENGINEERS:
//     * Giám sát vận hành:
//       - Theo dõi sát sao metric `mail_endpoint_operations_total` trên hệ thống Dashboard Grafana.
//       - Khi có spike về lỗi `crypto_error`, cần kiểm tra ngay lập tức trạng thái Master Secrets Key Provider.
//
//   📌 ĐỐI VỚI APPLICATION DEVELOPERS:
//     * Quy tắc phát triển:
//       - Tuyệt đối không được phép in (log) plaintext password/API key trong ConnectionConfig ra console.
//       - Luôn sử dụng `apperr.Wrap` để giữ nguyên gốc lỗi gốc (cause) phục vụ debug mà vẫn đảm bảo tính an toàn bảo mật.
//       - Ranh giới xác thực dữ liệu thô đầu vào thuộc về Transport Handler; tầng Service này hoàn toàn giả định đầu vào đã hợp lệ.
//
// ============================================================================

package mailSvcImpl

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"controlplane/internal/config"
	mailEntity "controlplane/internal/mail/domain/entity"
	mailRepoInterface "controlplane/internal/mail/domain/repo"
	mailSvcInterface "controlplane/internal/mail/domain/service"
	mailMetrics "controlplane/internal/mail/metrics"
	mailTaxonomy "controlplane/internal/mail/taxonomy"
	"controlplane/internal/security"
	"controlplane/pkg/apperr"

	"github.com/google/uuid"
)

// endpointServiceImpl triển khai interface mailSvcInterface.EndpointService.
// Quản lý logic nghiệp vụ và đóng gói ranh giới mật mã học của Mail Endpoint.
type endpointServiceImpl struct {
	cfg          *config.Config                       // Cấu hình hệ thống
	endpointRepo mailRepoInterface.EndpointRepository // Repository Postgres quản lý Endpoint
}

// NewEndpointService khởi tạo một đối tượng EndpointService mới.
// Áp dụng cơ chế fail-fast an toàn để panic ngay lập tức nếu các dependency bị nil.
func NewEndpointService(cfg *config.Config, endpointRepo mailRepoInterface.EndpointRepository) mailSvcInterface.EndpointService {
	if cfg == nil {
		panic("mail service: cấu hình hệ thống config không được phép nil khi khởi tạo EndpointService")
	}
	if endpointRepo == nil {
		panic("mail service: repo không được phép nil khi khởi tạo EndpointService")
	}
	return &endpointServiceImpl{
		cfg:          cfg,
		endpointRepo: endpointRepo,
	}
}

// CreateEndpoint xử lý nghiệp vụ tạo mới một Mail Endpoint vật lý trong Zone chỉ định.
// Thực hiện sinh UUIDv7 thời gian thực, mã hóa cấu hình kết nối và chuyển Persist xuống storage.
func (s *endpointServiceImpl) CreateEndpoint(
	ctx context.Context,
	params mailEntity.CreateEndpointParams,
) error {
	// 0. Xác thực provider hợp lệ ở tầng Service (đây là nghiệp vụ thuộc về Domain).
	switch params.Provider {
	case mailEntity.SMTP, mailEntity.SendGrid, mailEntity.Mailgun:
		// Hợp lệ
	default:
		mailMetrics.EndpointOperationsCounter.WithLabelValues("create", params.ZoneID.String(), mailTaxonomy.OutcomeInvalidArgument).Inc()
		return apperr.Wrap(mailTaxonomy.ErrInvalidArgument, fmt.Errorf("unsupported provider: %s", params.Provider), mailTaxonomy.OutcomeInvalidArgument)
	}

	// 1. Chuyển cấu hình kết nối dạng Map thành chuỗi JSON bytes để chuẩn bị mã hóa phong bì.
	jsonBytes, err := json.Marshal(params.ConnectionConfig)
	if err != nil {
		mailMetrics.EndpointOperationsCounter.WithLabelValues("create", params.ZoneID.String(), mailTaxonomy.OutcomeSerializationError).Inc()
		return apperr.Wrap(mailTaxonomy.ErrInvalidArgument, err, mailTaxonomy.OutcomeSerializationError)
	}

	// 2. Sử dụng mã hóa phong bì AES-256-GCM để bảo mật toàn vẹn cấu hình nhạy cảm.
	encryptedPayload, err := security.EncryptSecret(string(jsonBytes))
	if err != nil {
		mailMetrics.EndpointOperationsCounter.WithLabelValues("create", params.ZoneID.String(), mailTaxonomy.OutcomeCryptoError).Inc()
		return apperr.Wrap(mailTaxonomy.ErrEnvelopeDecryptFailed, err, mailTaxonomy.OutcomeCryptoError)
	}

	// 3. Tạo định danh duy nhất dựa trên đặc tả UUIDv7 (timestamp sorted) phục vụ HA và optimize index.
	newID, err := uuid.NewV7()
	if err != nil {
		mailMetrics.EndpointOperationsCounter.WithLabelValues("create", params.ZoneID.String(), mailTaxonomy.OutcomeDatabaseError).Inc()
		return apperr.Wrap(mailTaxonomy.ErrInvalidArgument, err, mailTaxonomy.OutcomeDatabaseError)
	}

	// 4. Khởi tạo thực thể domain Entity Endpoint.
	now := time.Now().UTC()
	ent := &mailEntity.Endpoint{
		ID:               newID,
		ZoneID:           params.ZoneID,
		Name:             params.Name,
		Provider:         params.Provider,
		ConnectionConfig: params.ConnectionConfig,
		IsActive:         true, // Kích hoạt hoạt động khi khởi tạo thành công
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	// 5. Thực hiện persist dữ liệu qua repository xuống cơ sở dữ liệu vật lý.
	if err := s.endpointRepo.Create(ctx, ent, []byte(encryptedPayload)); err != nil {
		mailMetrics.EndpointOperationsCounter.WithLabelValues("create", params.ZoneID.String(), mailTaxonomy.OutcomeDatabaseError).Inc()
		return apperr.Wrap(mailTaxonomy.ErrInvalidArgument, err, mailTaxonomy.OutcomeDatabaseError)
	}

	// Ghi nhận thao tác thành công vào hệ thống giám sát.
	mailMetrics.EndpointOperationsCounter.WithLabelValues("create", params.ZoneID.String(), mailTaxonomy.OutcomeSuccess).Inc()
	return nil
}

// GetEndpoint truy vấn chi tiết một Endpoint theo ID và nằm trong phạm vi Zone vật lý được phân vùng.
// Quá trình giải mã cấu hình kết nối nhạy cảm được thực hiện hoàn toàn trong suốt tại tầng Service này.
func (s *endpointServiceImpl) GetEndpoint(ctx context.Context, zoneID uuid.UUID, id uuid.UUID) (*mailEntity.Endpoint, error) {
	ent, encryptedConfig, err := s.endpointRepo.GetByID(ctx, zoneID, id)
	if err != nil {
		mailMetrics.EndpointOperationsCounter.WithLabelValues("get", zoneID.String(), mailTaxonomy.OutcomeNotFound).Inc()
		return nil, apperr.Wrap(mailTaxonomy.ErrEndpointNotFound, err, mailTaxonomy.OutcomeNotFound)
	}

	// Thực hiện giải mã cấu hình phong bì thô lấy ra từ Postgres.
	decryptedPayload, err := security.DecryptSecret(string(encryptedConfig))
	if err != nil {
		mailMetrics.EndpointOperationsCounter.WithLabelValues("get", zoneID.String(), mailTaxonomy.OutcomeCryptoError).Inc()
		return nil, apperr.Wrap(mailTaxonomy.ErrEnvelopeDecryptFailed, err, mailTaxonomy.OutcomeCryptoError)
	}

	// Deserialize cấu hình dạng thô sau khi giải mã ngược lại thành Map.
	var plainConfig map[string]interface{}
	if err := json.Unmarshal([]byte(decryptedPayload), &plainConfig); err != nil {
		mailMetrics.EndpointOperationsCounter.WithLabelValues("get", zoneID.String(), mailTaxonomy.OutcomeSerializationError).Inc()
		return nil, apperr.Wrap(mailTaxonomy.ErrEnvelopeDecryptFailed, err, mailTaxonomy.OutcomeSerializationError)
	}

	ent.ConnectionConfig = plainConfig
	mailMetrics.EndpointOperationsCounter.WithLabelValues("get", zoneID.String(), mailTaxonomy.OutcomeSuccess).Inc()
	return ent, nil
}

// ListEndpoints trả về toàn bộ danh sách Endpoint thuộc về một physical zone.
// Quá trình giải mã cấu hình kết nối nhạy cảm được thực hiện hoàn toàn trong suốt tại tầng Service này.
func (s *endpointServiceImpl) ListEndpoints(ctx context.Context, zoneID uuid.UUID) ([]*mailEntity.Endpoint, error) {
	list, encryptedConfigs, err := s.endpointRepo.List(ctx, zoneID)
	if err != nil {
		mailMetrics.EndpointOperationsCounter.WithLabelValues("list", zoneID.String(), mailTaxonomy.OutcomeDatabaseError).Inc()
		return nil, apperr.Wrap(mailTaxonomy.ErrInvalidArgument, err, mailTaxonomy.OutcomeDatabaseError)
	}

	// Giải mã trong suốt từng phần tử cấu hình trong danh sách trả về.
	for i, ent := range list {
		decryptedPayload, err := security.DecryptSecret(string(encryptedConfigs[i]))
		if err != nil {
			mailMetrics.EndpointOperationsCounter.WithLabelValues("list", zoneID.String(), mailTaxonomy.OutcomeCryptoError).Inc()
			return nil, apperr.Wrap(mailTaxonomy.ErrEnvelopeDecryptFailed, err, mailTaxonomy.OutcomeCryptoError)
		}

		var plainConfig map[string]interface{}
		if err := json.Unmarshal([]byte(decryptedPayload), &plainConfig); err != nil {
			mailMetrics.EndpointOperationsCounter.WithLabelValues("list", zoneID.String(), mailTaxonomy.OutcomeSerializationError).Inc()
			return nil, apperr.Wrap(mailTaxonomy.ErrEnvelopeDecryptFailed, err, mailTaxonomy.OutcomeSerializationError)
		}

		ent.ConnectionConfig = plainConfig
	}

	mailMetrics.EndpointOperationsCounter.WithLabelValues("list", zoneID.String(), mailTaxonomy.OutcomeSuccess).Inc()
	return list, nil
}

// UpdateEndpoint thực hiện cập nhật cấu hình của một Endpoint đang tồn tại.
// Xác thực sự tồn tại của bản ghi trong database trước khi thực hiện đột biến trạng thái.
func (s *endpointServiceImpl) UpdateEndpoint(
	ctx context.Context,
	params mailEntity.UpdateEndpointParams,
) (*mailEntity.Endpoint, error) {
	// 1. Kiểm tra sự tồn tại của bản ghi đích trước khi cập nhật.
	existing, err := s.GetEndpoint(ctx, params.ZoneID, params.ID)
	if err != nil {
		mailMetrics.EndpointOperationsCounter.WithLabelValues("update", params.ZoneID.String(), mailTaxonomy.OutcomeNotFound).Inc()
		return nil, apperr.Wrap(mailTaxonomy.ErrEndpointNotFound, err, mailTaxonomy.OutcomeNotFound)
	}

	// 2. Serialize cấu hình kết nối mới.
	jsonBytes, err := json.Marshal(params.ConnectionConfig)
	if err != nil {
		mailMetrics.EndpointOperationsCounter.WithLabelValues("update", params.ZoneID.String(), mailTaxonomy.OutcomeSerializationError).Inc()
		return nil, apperr.Wrap(mailTaxonomy.ErrInvalidArgument, err, mailTaxonomy.OutcomeSerializationError)
	}

	// 3. Mã hóa lại thông tin kết nối mới bằng master key đối xứng.
	encryptedPayload, err := security.EncryptSecret(string(jsonBytes))
	if err != nil {
		mailMetrics.EndpointOperationsCounter.WithLabelValues("update", params.ZoneID.String(), mailTaxonomy.OutcomeCryptoError).Inc()
		return nil, apperr.Wrap(mailTaxonomy.ErrEnvelopeDecryptFailed, err, mailTaxonomy.OutcomeCryptoError)
	}

	// 4. Đột biến các trường dữ liệu trên thực thể hiện tại.
	existing.Name = params.Name
	existing.ConnectionConfig = params.ConnectionConfig
	existing.IsActive = params.IsActive
	existing.UpdatedAt = time.Now().UTC()

	// 5. Persist các trường cập nhật xuống database.
	if err := s.endpointRepo.Update(ctx, existing, []byte(encryptedPayload)); err != nil {
		mailMetrics.EndpointOperationsCounter.WithLabelValues("update", params.ZoneID.String(), mailTaxonomy.OutcomeDatabaseError).Inc()
		return nil, apperr.Wrap(mailTaxonomy.ErrInvalidArgument, err, mailTaxonomy.OutcomeDatabaseError)
	}

	mailMetrics.EndpointOperationsCounter.WithLabelValues("update", params.ZoneID.String(), mailTaxonomy.OutcomeSuccess).Inc()
	return existing, nil
}

// DeleteEndpoint xóa vĩnh viễn cấu hình Endpoint vật lý ra khỏi cơ sở dữ liệu.
func (s *endpointServiceImpl) DeleteEndpoint(ctx context.Context, zoneID uuid.UUID, id uuid.UUID) error {
	if err := s.endpointRepo.Delete(ctx, zoneID, id); err != nil {
		mailMetrics.EndpointOperationsCounter.WithLabelValues("delete", zoneID.String(), mailTaxonomy.OutcomeDatabaseError).Inc()
		return apperr.Wrap(mailTaxonomy.ErrEndpointNotFound, err, mailTaxonomy.OutcomeDatabaseError)
	}

	mailMetrics.EndpointOperationsCounter.WithLabelValues("delete", zoneID.String(), mailTaxonomy.OutcomeSuccess).Inc()
	return nil
}

// TestConnection thực hiện bắt tay mạng (handshake) đầy đủ và xác thực tài khoản với Endpoint đã lưu.
// LƯU Ý: Theo yêu cầu nghiệp vụ, logic bắt tay chi tiết của từng Provider được cấu hình stub và trì hoãn.
func (s *endpointServiceImpl) TestConnection(ctx context.Context, zoneID uuid.UUID, id uuid.UUID) error {
	// Đảm bảo Endpoint tồn tại trong cơ sở dữ liệu trước khi chạy thử kết nối.
	_, err := s.GetEndpoint(ctx, zoneID, id)
	if err != nil {
		mailMetrics.EndpointOperationsCounter.WithLabelValues("test_connection", zoneID.String(), mailTaxonomy.OutcomeNotFound).Inc()
		return apperr.Wrap(mailTaxonomy.ErrEndpointNotFound, err, mailTaxonomy.OutcomeNotFound)
	}

	mailMetrics.EndpointOperationsCounter.WithLabelValues("test_connection", zoneID.String(), mailTaxonomy.OutcomeDatabaseError).Inc()
	return apperr.Wrap(mailTaxonomy.ErrEndpointAuthFailed, fmt.Errorf("mail service: tính năng chạy thử kết nối trên Endpoint đã lưu chưa được triển khai"), mailTaxonomy.OutcomeDatabaseError)
}

// TestConnectionRaw thực hiện bắt tay (handshake) sử dụng cấu hình thô truyền trực tiếp chưa lưu.
// LƯU Ý: Theo đặc tả, luồng kiểm thử bắt tay thô này hiện được cấu hình stub và trì hoãn triển khai thực tế.
func (s *endpointServiceImpl) TestConnectionRaw(
	ctx context.Context,
	provider mailEntity.ProviderType,
	plainConfig map[string]interface{},
) error {
	return apperr.Wrap(mailTaxonomy.ErrEndpointAuthFailed, fmt.Errorf("mail service: tính năng chạy thử kết nối thô chưa được triển khai"), mailTaxonomy.OutcomeDatabaseError)
}
