// ============================================================================
// 📂 PHÂN HỆ: controlplane/internal/mail/service/endpoint_service_impl.go
//            Đặc Tả Nghiệp Vụ Quản Trị Mail Endpoint Cấp Hạ Tầng (Tier-0)
// ============================================================================

package mailSvcImpl

import (
	"context"
	"controlplane/internal/cacheengine"
	"controlplane/internal/config"
	mailEntity "controlplane/internal/mail/domain/entity"
	mailRepoInterface "controlplane/internal/mail/domain/repo"
	mailSvcInterface "controlplane/internal/mail/domain/service"
	mailMetrics "controlplane/internal/mail/metrics"
	mailTaxonomy "controlplane/internal/mail/taxonomy"
	mailproto "controlplane/internal/mail/transport/rpc/proto"
	"controlplane/pkg/apperr"
	"controlplane/pkg/constant"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

type zoneCodeCtxKey struct{}

func WithZoneCode(ctx context.Context, code string) context.Context {
	return context.WithValue(ctx, zoneCodeCtxKey{}, strings.ToLower(strings.TrimSpace(code)))
}

type endpointServiceImpl struct {
	cfg          *config.Config
	endpointRepo mailRepoInterface.EndpointRepository
	outboxRepo   mailRepoInterface.MailOutboxRepository
	cacheEngine  *cacheengine.CacheRegistry
}

func NewEndpointService(
	cfg *config.Config,
	endpointRepo mailRepoInterface.EndpointRepository,
	outboxRepo mailRepoInterface.MailOutboxRepository,
	cacheEngine *cacheengine.CacheRegistry,
) mailSvcInterface.EndpointService {

	return &endpointServiceImpl{
		cfg:          cfg,
		endpointRepo: endpointRepo,
		outboxRepo:   outboxRepo,
		cacheEngine:  cacheEngine,
	}
}

func (s *endpointServiceImpl) CreateEndpoint(
	ctx context.Context,
	params *mailEntity.CreateEndpointParams,
) error {
	// Trích xuất trực tiếp ZoneID từ context bằng khóa dùng chung
	zoneUUID, ok := ctx.Value(constant.ZoneIDCtxKey).(uuid.UUID)
	if !ok || zoneUUID == uuid.Nil {
		mailMetrics.IncEndpointOperations("create", "global", mailTaxonomy.OutcomeInvalidArgument)
		return apperr.Wrap(mailTaxonomy.ErrInvalidArgument, fmt.Errorf("mail service: zone is required to create endpoint"), mailTaxonomy.OutcomeInvalidArgument)
	}
	params.ZoneID = zoneUUID

	// Xác thực các chứng chỉ TLS dựa vào TLSMode
	switch params.TLSMode {
	case mailEntity.TLSModeTLS:
		if strings.TrimSpace(params.CACertPEM) == "" {
			mailMetrics.IncEndpointOperations("create", params.ZoneID.String(), mailTaxonomy.OutcomeInvalidArgument)
			return apperr.Wrap(mailTaxonomy.ErrInvalidArgument, fmt.Errorf("tls mode 'tls' requires ca_cert_pem"), mailTaxonomy.OutcomeInvalidArgument)
		}
	case mailEntity.TLSModeMTLS:
		if strings.TrimSpace(params.CACertPEM) == "" || strings.TrimSpace(params.ClientCertPEM) == "" || strings.TrimSpace(params.ClientKeyPEM) == "" {
			mailMetrics.IncEndpointOperations("create", params.ZoneID.String(), mailTaxonomy.OutcomeInvalidArgument)
			return apperr.Wrap(mailTaxonomy.ErrInvalidArgument, fmt.Errorf("tls mode 'mtls' requires ca_cert_pem, client_cert_pem, and client_key_pem"), mailTaxonomy.OutcomeInvalidArgument)
		}
	case mailEntity.TLSModeNone, mailEntity.TLSModeStartTLS, "":
		// OK
	default:
		mailMetrics.IncEndpointOperations("create", params.ZoneID.String(), mailTaxonomy.OutcomeInvalidArgument)
		return apperr.Wrap(mailTaxonomy.ErrInvalidArgument, fmt.Errorf("invalid tls_mode: %s", params.TLSMode), mailTaxonomy.OutcomeInvalidArgument)
	}

	// Tạo UUID v7 định danh cho Mail Endpoint mới trực tiếp tại Service layer
	newID, err := uuid.NewV7()
	if err != nil {
		mailMetrics.IncEndpointOperations("create", params.ZoneID.String(), mailTaxonomy.OutcomeDatabaseError)
		return apperr.Wrap(mailTaxonomy.ErrInvalidArgument, err, mailTaxonomy.OutcomeDatabaseError)
	}
	params.ID = newID

	now := time.Now().UTC()
	// Tạo entity Endpoint. Password và client_key_pem lưu trữ dưới dạng text thô theo God View SoT.
	ent := &mailEntity.Endpoint{
		ID:             newID,
		ZoneID:         params.ZoneID,
		Name:           params.Name,
		Host:           params.Host,
		Port:           params.Port,
		Username:       params.Username,
		Password:       params.Password,
		TLSMode:        params.TLSMode,
		Status:         params.Status,
		MaxConnections: params.MaxConnections,
		Priority:       params.Priority,
		Weight:         params.Weight,
		CACertPEM:      params.CACertPEM,
		ClientCertPEM:  params.ClientCertPEM,
		ClientKeyPEM:   params.ClientKeyPEM,
		IsActive:       true,
		CreatedAt:      &now,
		UpdatedAt:      &now,
	}

	// Tạo eventID cho Outbox record
	eventID, err := uuid.NewV7()
	if err != nil {
		mailMetrics.IncEndpointOperations("create", params.ZoneID.String(), mailTaxonomy.OutcomeDatabaseError)
		return apperr.Wrap(mailTaxonomy.ErrInvalidArgument, err, mailTaxonomy.OutcomeDatabaseError)
	}

	// 1. Chuẩn bị thông tin đồng bộ sang protobuf
	syncConfig := &mailproto.SmtpEndpointSync{
		Id:             ent.ID.String(),
		ZoneId:         ent.ZoneID.String(),
		Name:           ent.Name,
		Host:           ent.Host,
		Port:           int32(ent.Port),
		Username:       ent.Username,
		Password:       ent.Password, // lưu thô
		TlsMode:        string(ent.TLSMode),
		Status:         ent.Status,
		MaxConnections: int32(ent.MaxConnections),
		Priority:       int32(ent.Priority),
		Weight:         int32(ent.Weight),
		IsActive:       ent.IsActive,
		UpdatedAt:      ent.UpdatedAt.UnixNano() / int64(time.Millisecond),
	}
	if ent.CACertPEM != "" {
		syncConfig.CaCertPem = &ent.CACertPEM
	}
	if ent.ClientCertPEM != "" {
		syncConfig.ClientCertPem = &ent.ClientCertPEM
	}
	if ent.ClientKeyPEM != "" {
		syncConfig.ClientKeyPem = &ent.ClientKeyPEM
	}

	// 2. Tuần tự hóa cấu hình sang nhị phân bằng Protobuf
	payloadBytes, err := proto.Marshal(syncConfig)
	if err != nil {
		mailMetrics.IncEndpointOperations("create", params.ZoneID.String(), mailTaxonomy.OutcomeDatabaseError)
		return apperr.Wrap(mailTaxonomy.ErrInvalidArgument, err, mailTaxonomy.OutcomeDatabaseError)
	}

	// 3. Trích xuất Trace ID thực tế dạng nhị phân từ context
	var traceID []byte
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		tid := spanCtx.TraceID()
		traceID = tid[:]
	}

	// 4. Trích xuất User ID từ context được middleware inject từ token xác thực
	var userIDStr string
	if ident, ok := ctx.Value(constant.IdentityKey).(*constant.Identity); ok && ident != nil {
		userIDStr = ident.UserID
	}

	// 5. Khởi tạo thực thể MailOutboxRecord
	outboxRecord := &mailEntity.MailOutboxRecord{
		EventID:              eventID,
		ZoneID:               ent.ZoneID,
		JobTopic:             "mail.create_endpoint",
		Payload:              payloadBytes,
		UserID:               userIDStr,
		Status:               mailEntity.OutboxStatusPending,
		JobVersion:           1,
		ResourceID:           ent.ID.String(),
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 90, // Hạn mức timeout 90 giây cho kết nối
	}

	// 6. Ghi đồng thời vào endpoint và outbox record thông qua Repo (Repository layer tự quản lý transaction)
	if err := s.endpointRepo.Create(ctx, ent, outboxRecord); err != nil {
		mailMetrics.IncEndpointOperations("create", params.ZoneID.String(), mailTaxonomy.OutcomeDatabaseError)
		return apperr.Wrap(mailTaxonomy.ErrInvalidArgument, err, mailTaxonomy.OutcomeDatabaseError)
	}

	mailMetrics.IncEndpointOperations("create", params.ZoneID.String(), mailTaxonomy.Success)
	return nil
}

func (s *endpointServiceImpl) GetEndpoint(ctx context.Context, id uuid.UUID) (*mailEntity.Endpoint, error) {
	// Trích xuất trực tiếp ZoneID từ context bằng khóa dùng chung
	zoneID, _ := ctx.Value(constant.ZoneIDCtxKey).(uuid.UUID)

	var ent *mailEntity.Endpoint
	var err error
	if zoneID == uuid.Nil {
		ent, err = s.endpointRepo.GetGlobalByID(ctx, id)
	} else {
		ent, err = s.endpointRepo.GetByID(ctx, zoneID, id)
	}

	if err != nil {
		mailMetrics.IncEndpointOperations("get", zoneID.String(), mailTaxonomy.OutcomeNotFound)
		return nil, apperr.Wrap(mailTaxonomy.ErrEndpointNotFound, err, mailTaxonomy.OutcomeNotFound)
	}

	// Thông tin nhạy cảm password và client_key_pem được lưu trữ plain text thô, không cần giải mã.
	mailMetrics.IncEndpointOperations("get", zoneID.String(), mailTaxonomy.Success)
	return ent, nil
}

func (s *endpointServiceImpl) ListEndpoints(
	ctx context.Context,
	cursor string,
	limit int,
) ([]*mailEntity.Endpoint, string, error) {
	// Trích xuất trực tiếp ZoneID từ context bằng khóa dùng chung
	zoneID, _ := ctx.Value(constant.ZoneIDCtxKey).(uuid.UUID)

	var list []*mailEntity.Endpoint
	var nextCursor string
	var err error

	if zoneID == uuid.Nil {
		list, nextCursor, err = s.endpointRepo.ListGlobal(ctx, cursor, limit)
	} else {
		list, nextCursor, err = s.endpointRepo.ListByZone(ctx, zoneID, cursor, limit)
	}

	if err != nil {
		mailMetrics.IncEndpointOperations("list", zoneID.String(), mailTaxonomy.OutcomeDatabaseError)
		return nil, "", apperr.Wrap(mailTaxonomy.ErrInvalidArgument, err, mailTaxonomy.OutcomeDatabaseError)
	}

	// Không cần thực hiện giải mã credentials vì được lưu trữ dạng plain text thô
	mailMetrics.IncEndpointOperations("list", zoneID.String(), mailTaxonomy.Success)
	return list, nextCursor, nil
}

func (s *endpointServiceImpl) UpdateEndpoint(
	ctx context.Context,
	params mailEntity.UpdateEndpointParams,
) (*mailEntity.Endpoint, error) {
	if params.ZoneID == uuid.Nil {
		resolved, ok := ctx.Value(constant.ZoneIDCtxKey).(uuid.UUID)
		if !ok || resolved == uuid.Nil {
			mailMetrics.IncEndpointOperations("update", "global", mailTaxonomy.OutcomeInvalidArgument)
			return nil, apperr.Wrap(mailTaxonomy.ErrInvalidArgument, fmt.Errorf("mail service: zone is required for update operation"), mailTaxonomy.OutcomeInvalidArgument)
		}
		params.ZoneID = resolved
	}

	existing, err := s.endpointRepo.GetByID(ctx, params.ZoneID, params.ID)
	if err != nil || existing == nil {
		mailMetrics.IncEndpointOperations("update", params.ZoneID.String(), mailTaxonomy.OutcomeNotFound)
		return nil, apperr.Wrap(mailTaxonomy.ErrEndpointNotFound, fmt.Errorf("endpoint not found"), mailTaxonomy.OutcomeNotFound)
	}

	// Xác thực các chứng chỉ TLS dựa vào TLSMode
	switch params.TLSMode {
	case mailEntity.TLSModeTLS:
		if strings.TrimSpace(params.CACertPEM) == "" {
			mailMetrics.IncEndpointOperations("update", params.ZoneID.String(), mailTaxonomy.OutcomeInvalidArgument)
			return nil, apperr.Wrap(mailTaxonomy.ErrInvalidArgument, fmt.Errorf("tls mode 'tls' requires ca_cert_pem"), mailTaxonomy.OutcomeInvalidArgument)
		}
	case mailEntity.TLSModeMTLS:
		if strings.TrimSpace(params.CACertPEM) == "" || strings.TrimSpace(params.ClientCertPEM) == "" || strings.TrimSpace(params.ClientKeyPEM) == "" {
			mailMetrics.IncEndpointOperations("update", params.ZoneID.String(), mailTaxonomy.OutcomeInvalidArgument)
			return nil, apperr.Wrap(mailTaxonomy.ErrInvalidArgument, fmt.Errorf("tls mode 'mtls' requires ca_cert_pem, client_cert_pem, and client_key_pem"), mailTaxonomy.OutcomeInvalidArgument)
		}
	case mailEntity.TLSModeNone, mailEntity.TLSModeStartTLS, "":
		// OK
	default:
		mailMetrics.IncEndpointOperations("update", params.ZoneID.String(), mailTaxonomy.OutcomeInvalidArgument)
		return nil, apperr.Wrap(mailTaxonomy.ErrInvalidArgument, fmt.Errorf("invalid tls_mode: %s", params.TLSMode), mailTaxonomy.OutcomeInvalidArgument)
	}

	// Cập nhật các trường phẳng
	existing.Name = params.Name
	existing.Host = params.Host
	existing.Port = params.Port
	existing.Username = params.Username
	existing.TLSMode = params.TLSMode
	existing.Status = params.Status
	existing.MaxConnections = params.MaxConnections
	existing.Priority = params.Priority
	existing.Weight = params.Weight
	existing.CACertPEM = params.CACertPEM
	existing.ClientCertPEM = params.ClientCertPEM
	existing.IsActive = params.IsActive

	// Password và client_key_pem được cập nhật dạng plain text thô theo God View SoT
	if params.Password != "" {
		existing.Password = params.Password
	}
	if params.ClientKeyPEM != "" {
		existing.ClientKeyPEM = params.ClientKeyPEM
	}

	now := time.Now().UTC()
	existing.UpdatedAt = &now

	if err := s.endpointRepo.Update(ctx, existing); err != nil {
		mailMetrics.IncEndpointOperations("update", params.ZoneID.String(), mailTaxonomy.OutcomeDatabaseError)
		return nil, apperr.Wrap(mailTaxonomy.ErrInvalidArgument, err, mailTaxonomy.OutcomeDatabaseError)
	}

	mailMetrics.IncEndpointOperations("update", params.ZoneID.String(), mailTaxonomy.Success)
	return existing, nil
}

func (s *endpointServiceImpl) DeleteEndpoint(ctx context.Context, id uuid.UUID) error {
	// Trích xuất trực tiếp ZoneID từ context bằng khóa dùng chung
	zoneID, ok := ctx.Value(constant.ZoneIDCtxKey).(uuid.UUID)
	if !ok || zoneID == uuid.Nil {
		mailMetrics.IncEndpointOperations("create", "global", mailTaxonomy.OutcomeInvalidArgument)
		return apperr.Wrap(mailTaxonomy.ErrInvalidArgument, fmt.Errorf("mail service: zone is required to create endpoint"), mailTaxonomy.OutcomeInvalidArgument)
	}

	if err := s.endpointRepo.Delete(ctx, zoneID, id); err != nil {
		mailMetrics.IncEndpointOperations("delete", zoneID.String(), mailTaxonomy.OutcomeDatabaseError)
		return apperr.Wrap(mailTaxonomy.ErrEndpointNotFound, err, mailTaxonomy.OutcomeDatabaseError)
	}

	mailMetrics.IncEndpointOperations("delete", zoneID.String(), mailTaxonomy.Success)
	return nil
}

func (s *endpointServiceImpl) TestConnection(ctx context.Context, id uuid.UUID) error {
	// Trích xuất trực tiếp ZoneID từ context bằng khóa dùng chung
	zoneUUID, ok := ctx.Value(constant.ZoneIDCtxKey).(uuid.UUID)
	if !ok || zoneUUID == uuid.Nil {
		mailMetrics.IncEndpointOperations("test_connection", "global", mailTaxonomy.OutcomeInvalidArgument)
		return apperr.Wrap(mailTaxonomy.ErrInvalidArgument, fmt.Errorf("mail service: zone is required to test endpoint connection"), mailTaxonomy.OutcomeInvalidArgument)
	}

	_, err := s.GetEndpoint(ctx, id)
	if err != nil {
		mailMetrics.IncEndpointOperations("test_connection", zoneUUID.String(), mailTaxonomy.OutcomeNotFound)
		return apperr.Wrap(mailTaxonomy.ErrEndpointNotFound, err, mailTaxonomy.OutcomeNotFound)
	}

	mailMetrics.IncEndpointOperations("test_connection", zoneUUID.String(), mailTaxonomy.OutcomeDatabaseError)
	return apperr.Wrap(mailTaxonomy.ErrEndpointAuthFailed, fmt.Errorf("mail service: connection test is not implemented"), mailTaxonomy.OutcomeDatabaseError)
}

func (s *endpointServiceImpl) TestConnectionRaw(ctx context.Context, req mailEntity.TestConnection) error {
	// Trích xuất trực tiếp ZoneID từ Go standard context bằng khóa dùng chung constant.ZoneIDCtxKey.
	// Điều này giúp tầng Service tự lấy thông tin độc lập mà không cần import hay phụ thuộc vào HTTP middleware.
	zoneID, ok := ctx.Value(constant.ZoneIDCtxKey).(uuid.UUID)
	if !ok || zoneID == uuid.Nil {
		return mailTaxonomy.ErrZoneNotFound
	}

	switch req.TLSMode {
	case mailEntity.TLSModeTLS:
		if req.CACertPEM == nil || *req.CACertPEM == "" {
			return mailTaxonomy.ErrInvalidArgument
		}
	case mailEntity.TLSModeMTLS:
		if req.CACertPEM == nil || *req.CACertPEM == "" ||
			req.ClientCertPEM == nil || *req.ClientCertPEM == "" ||
			req.ClientKeyPEM == nil || *req.ClientKeyPEM == "" {
			return mailTaxonomy.ErrInvalidArgument
		}
	case mailEntity.TLSModeNone, mailEntity.TLSModeStartTLS, "":
		// OK
	default:
		return mailTaxonomy.ErrInvalidArgument
	}

	eventID, err := uuid.NewV7()
	if err != nil {
		return err
	}

	// Trích xuất UserID từ context được middleware inject từ token xác thực
	var userIDStr string
	if ident, ok := ctx.Value(constant.IdentityKey).(*constant.Identity); ok && ident != nil {
		userIDStr = ident.UserID
	}

	// Xây dựng cấu trúc cấu hình SMTP bằng protobuf
	smtpConfig := &mailproto.SmtpTestConfig{
		Host:     req.Host,
		Port:     int32(req.Port),
		Username: req.Username,
		Password: req.Password,
		TlsMode:  string(req.TLSMode),
	}
	if req.TLSMode == mailEntity.TLSModeTLS || req.TLSMode == mailEntity.TLSModeMTLS {
		smtpConfig.CaCertPem = req.CACertPEM
	}
	if req.TLSMode == mailEntity.TLSModeMTLS {
		smtpConfig.ClientCertPem = req.ClientCertPEM
		smtpConfig.ClientKeyPem = req.ClientKeyPEM
	}

	// Tuần tự hóa cấu hình sang nhị phân cực nhanh bằng Protobuf (triệt tiêu dynamic allocation)
	payloadBytes, err := proto.Marshal(smtpConfig)
	if err != nil {
		return err
	}

	// Trích xuất Trace ID thực tế dạng nhị phân 16-byte từ context để tối ưu hóa lưu trữ cột BYTEA trong DB
	var traceID []byte
	if spanCtx := trace.SpanContextFromContext(ctx); spanCtx.IsValid() {
		tid := spanCtx.TraceID()
		traceID = tid[:]
	}

	// Khởi tạo thực thể MailOutboxRecord hoàn chỉnh (lưu UserID riêng biệt ở cột DB)
	record := &mailEntity.MailOutboxRecord{
		EventID:              eventID,
		ZoneID:               zoneID,
		JobTopic:             "mail.test_connection",
		Payload:              payloadBytes,
		UserID:               userIDStr,
		Status:               mailEntity.OutboxStatusPending,
		JobVersion:           1,
		ResourceID:           "transient_test",
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 90, // Hạn mức timeout 90 giây cho kết nối test
	}

	// Lưu bản ghi outbox trực tiếp vào cơ sở dữ liệu Postgres
	if err := s.outboxRepo.Create(ctx, record); err != nil {
		return err
	}

	return nil
}
