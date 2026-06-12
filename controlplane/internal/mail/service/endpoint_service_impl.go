// ============================================================================
// 📂 PHÂN HỆ: controlplane/internal/mail/service/endpoint_service_impl.go
//            Đặc Tả Nghiệp Vụ Quản Trị Mail Endpoint Cấp Hạ Tầng (Tier-0)
// ============================================================================

package mailSvcImpl

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"controlplane/internal/cacheengine"
	"controlplane/internal/config"
	"controlplane/internal/http/middleware"
	mailEntity "controlplane/internal/mail/domain/entity"
	mailRepoInterface "controlplane/internal/mail/domain/repo"
	mailSvcInterface "controlplane/internal/mail/domain/service"
	mailMetrics "controlplane/internal/mail/metrics"
	mailTaxonomy "controlplane/internal/mail/taxonomy"
	"controlplane/internal/security"
	"controlplane/pkg/apperr"
	"controlplane/pkg/logger"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type zoneCodeCtxKey struct{}

func WithZoneCode(ctx context.Context, code string) context.Context {
	return context.WithValue(ctx, zoneCodeCtxKey{}, strings.ToLower(strings.TrimSpace(code)))
}

type endpointServiceImpl struct {
	cfg          *config.Config
	endpointRepo mailRepoInterface.EndpointRepository
	outboxRepo   mailRepoInterface.MailOutboxRepository
	rdsJob       *redis.Client
	l1Registry   *cacheengine.CacheRegistry
}

func NewEndpointService(
	cfg *config.Config,
	endpointRepo mailRepoInterface.EndpointRepository,
	outboxRepo mailRepoInterface.MailOutboxRepository,
	rdsJob *redis.Client,
	l1Registry *cacheengine.CacheRegistry,
) mailSvcInterface.EndpointService {

	return &endpointServiceImpl{
		cfg:          cfg,
		endpointRepo: endpointRepo,
		outboxRepo:   outboxRepo,
		rdsJob:       rdsJob,
		l1Registry:   l1Registry,
	}
}

func (s *endpointServiceImpl) CreateEndpoint(
	ctx context.Context,
	params mailEntity.CreateEndpointParams,
) error {
	zoneUUID, ok := middleware.GetZoneID(ctx)
	if !ok || zoneUUID == uuid.Nil {
		mailMetrics.EndpointOperationsCounter.WithLabelValues("create", "global", mailTaxonomy.OutcomeInvalidArgument).Inc()
		return apperr.Wrap(mailTaxonomy.ErrInvalidArgument, fmt.Errorf("mail service: zone is required to create endpoint"), mailTaxonomy.OutcomeInvalidArgument)
	}
	params.ZoneID = zoneUUID

	// Xác thực các chứng chỉ TLS dựa vào TLSMode
	switch params.TLSMode {
	case "tls":
		if strings.TrimSpace(params.CACertPEM) == "" {
			mailMetrics.EndpointOperationsCounter.WithLabelValues("create", params.ZoneID.String(), mailTaxonomy.OutcomeInvalidArgument).Inc()
			return apperr.Wrap(mailTaxonomy.ErrInvalidArgument, fmt.Errorf("tls mode 'tls' requires ca_cert_pem"), mailTaxonomy.OutcomeInvalidArgument)
		}
	case "mtls":
		if strings.TrimSpace(params.CACertPEM) == "" || strings.TrimSpace(params.ClientCertPEM) == "" || strings.TrimSpace(params.ClientKeyPEM) == "" {
			mailMetrics.EndpointOperationsCounter.WithLabelValues("create", params.ZoneID.String(), mailTaxonomy.OutcomeInvalidArgument).Inc()
			return apperr.Wrap(mailTaxonomy.ErrInvalidArgument, fmt.Errorf("tls mode 'mtls' requires ca_cert_pem, client_cert_pem, and client_key_pem"), mailTaxonomy.OutcomeInvalidArgument)
		}
	case "none", "starttls", "":
		// OK
	default:
		mailMetrics.EndpointOperationsCounter.WithLabelValues("create", params.ZoneID.String(), mailTaxonomy.OutcomeInvalidArgument).Inc()
		return apperr.Wrap(mailTaxonomy.ErrInvalidArgument, fmt.Errorf("invalid tls_mode: %s", params.TLSMode), mailTaxonomy.OutcomeInvalidArgument)
	}

	// Mã hóa password nhạy cảm
	var encryptedPassword string
	if params.Password != "" {
		enc, err := security.EncryptSecret(params.Password)
		if err != nil {
			mailMetrics.EndpointOperationsCounter.WithLabelValues("create", params.ZoneID.String(), mailTaxonomy.OutcomeCryptoError).Inc()
			return apperr.Wrap(mailTaxonomy.ErrEnvelopeDecryptFailed, err, mailTaxonomy.OutcomeCryptoError)
		}
		encryptedPassword = enc
	}

	// Mã hóa client_key_pem nhạy cảm
	var encryptedClientKey string
	if params.ClientKeyPEM != "" {
		enc, err := security.EncryptSecret(params.ClientKeyPEM)
		if err != nil {
			mailMetrics.EndpointOperationsCounter.WithLabelValues("create", params.ZoneID.String(), mailTaxonomy.OutcomeCryptoError).Inc()
			return apperr.Wrap(mailTaxonomy.ErrEnvelopeDecryptFailed, err, mailTaxonomy.OutcomeCryptoError)
		}
		encryptedClientKey = enc
	}

	newID, err := uuid.NewV7()
	if err != nil {
		mailMetrics.EndpointOperationsCounter.WithLabelValues("create", params.ZoneID.String(), mailTaxonomy.OutcomeDatabaseError).Inc()
		return apperr.Wrap(mailTaxonomy.ErrInvalidArgument, err, mailTaxonomy.OutcomeDatabaseError)
	}

	now := time.Now().UTC()
	ent := &mailEntity.Endpoint{
		ID:             newID,
		ZoneID:         params.ZoneID,
		Name:           params.Name,
		Host:           params.Host,
		Port:           params.Port,
		Username:       params.Username,
		Password:       encryptedPassword,
		TLSMode:        params.TLSMode,
		Status:         params.Status,
		MaxConnections: params.MaxConnections,
		Priority:       params.Priority,
		Weight:         params.Weight,
		CACertPEM:      params.CACertPEM,
		ClientCertPEM:  params.ClientCertPEM,
		ClientKeyPEM:   encryptedClientKey,
		IsActive:       true,
		CreatedAt:      &now,
		UpdatedAt:      &now,
	}

	if err := s.endpointRepo.Create(ctx, ent); err != nil {
		mailMetrics.EndpointOperationsCounter.WithLabelValues("create", params.ZoneID.String(), mailTaxonomy.OutcomeDatabaseError).Inc()
		return apperr.Wrap(mailTaxonomy.ErrInvalidArgument, err, mailTaxonomy.OutcomeDatabaseError)
	}

	mailMetrics.EndpointOperationsCounter.WithLabelValues("create", params.ZoneID.String(), mailTaxonomy.Success).Inc()
	return nil
}

func (s *endpointServiceImpl) GetEndpoint(ctx context.Context, id uuid.UUID) (*mailEntity.Endpoint, error) {
	zoneID, _ := middleware.GetZoneID(ctx)

	var ent *mailEntity.Endpoint
	var err error
	if zoneID == uuid.Nil {
		ent, err = s.endpointRepo.GetGlobalByID(ctx, id)
	} else {
		ent, err = s.endpointRepo.GetByID(ctx, zoneID, id)
	}

	if err != nil {
		mailMetrics.EndpointOperationsCounter.WithLabelValues("get", zoneID.String(), mailTaxonomy.OutcomeNotFound).Inc()
		return nil, apperr.Wrap(mailTaxonomy.ErrEndpointNotFound, err, mailTaxonomy.OutcomeNotFound)
	}

	// Giải mã password nhạy cảm
	if ent.Password != "" {
		dec, err := security.DecryptSecret(ent.Password)
		if err != nil {
			mailMetrics.EndpointOperationsCounter.WithLabelValues("get", zoneID.String(), mailTaxonomy.OutcomeCryptoError).Inc()
			return nil, apperr.Wrap(mailTaxonomy.ErrEnvelopeDecryptFailed, err, mailTaxonomy.OutcomeCryptoError)
		}
		ent.Password = dec
	}

	// Giải mã client_key_pem nhạy cảm
	if ent.ClientKeyPEM != "" {
		dec, err := security.DecryptSecret(ent.ClientKeyPEM)
		if err != nil {
			mailMetrics.EndpointOperationsCounter.WithLabelValues("get", zoneID.String(), mailTaxonomy.OutcomeCryptoError).Inc()
			return nil, apperr.Wrap(mailTaxonomy.ErrEnvelopeDecryptFailed, err, mailTaxonomy.OutcomeCryptoError)
		}
		ent.ClientKeyPEM = dec
	}

	mailMetrics.EndpointOperationsCounter.WithLabelValues("get", zoneID.String(), mailTaxonomy.Success).Inc()
	return ent, nil
}

func (s *endpointServiceImpl) ListEndpoints(
	ctx context.Context,
	cursor string,
	limit int,
) ([]*mailEntity.Endpoint, string, error) {
	zoneID, _ := middleware.GetZoneID(ctx)

	var list []*mailEntity.Endpoint
	var nextCursor string
	var err error

	if zoneID == uuid.Nil {
		list, nextCursor, err = s.endpointRepo.ListGlobal(ctx, cursor, limit)
	} else {
		list, nextCursor, err = s.endpointRepo.ListByZone(ctx, zoneID, cursor, limit)
	}

	if err != nil {
		mailMetrics.EndpointOperationsCounter.WithLabelValues("list", zoneID.String(), mailTaxonomy.OutcomeDatabaseError).Inc()
		return nil, "", apperr.Wrap(mailTaxonomy.ErrInvalidArgument, err, mailTaxonomy.OutcomeDatabaseError)
	}

	for _, ent := range list {
		if ent.Password != "" {
			dec, err := security.DecryptSecret(ent.Password)
			if err != nil {
				mailMetrics.EndpointOperationsCounter.WithLabelValues("list", zoneID.String(), mailTaxonomy.OutcomeCryptoError).Inc()
				return nil, "", apperr.Wrap(mailTaxonomy.ErrEnvelopeDecryptFailed, err, mailTaxonomy.OutcomeCryptoError)
			}
			ent.Password = dec
		}
		if ent.ClientKeyPEM != "" {
			dec, err := security.DecryptSecret(ent.ClientKeyPEM)
			if err != nil {
				mailMetrics.EndpointOperationsCounter.WithLabelValues("list", zoneID.String(), mailTaxonomy.OutcomeCryptoError).Inc()
				return nil, "", apperr.Wrap(mailTaxonomy.ErrEnvelopeDecryptFailed, err, mailTaxonomy.OutcomeCryptoError)
			}
			ent.ClientKeyPEM = dec
		}
	}

	mailMetrics.EndpointOperationsCounter.WithLabelValues("list", zoneID.String(), mailTaxonomy.Success).Inc()
	return list, nextCursor, nil
}

func (s *endpointServiceImpl) UpdateEndpoint(
	ctx context.Context,
	params mailEntity.UpdateEndpointParams,
) (*mailEntity.Endpoint, error) {
	if params.ZoneID == uuid.Nil {
		resolved, ok := middleware.GetZoneID(ctx)
		if !ok || resolved == uuid.Nil {
			mailMetrics.EndpointOperationsCounter.WithLabelValues("update", "global", mailTaxonomy.OutcomeInvalidArgument).Inc()
			return nil, apperr.Wrap(mailTaxonomy.ErrInvalidArgument, fmt.Errorf("mail service: zone is required for update operation"), mailTaxonomy.OutcomeInvalidArgument)
		}
		params.ZoneID = resolved
	}

	existing, err := s.endpointRepo.GetByID(ctx, params.ZoneID, params.ID)
	if err != nil || existing == nil {
		mailMetrics.EndpointOperationsCounter.WithLabelValues("update", params.ZoneID.String(), mailTaxonomy.OutcomeNotFound).Inc()
		return nil, apperr.Wrap(mailTaxonomy.ErrEndpointNotFound, fmt.Errorf("endpoint not found"), mailTaxonomy.OutcomeNotFound)
	}

	// Xác thực các chứng chỉ TLS dựa vào TLSMode
	switch params.TLSMode {
	case "tls":
		if strings.TrimSpace(params.CACertPEM) == "" {
			mailMetrics.EndpointOperationsCounter.WithLabelValues("update", params.ZoneID.String(), mailTaxonomy.OutcomeInvalidArgument).Inc()
			return nil, apperr.Wrap(mailTaxonomy.ErrInvalidArgument, fmt.Errorf("tls mode 'tls' requires ca_cert_pem"), mailTaxonomy.OutcomeInvalidArgument)
		}
	case "mtls":
		if strings.TrimSpace(params.CACertPEM) == "" || strings.TrimSpace(params.ClientCertPEM) == "" || strings.TrimSpace(params.ClientKeyPEM) == "" {
			mailMetrics.EndpointOperationsCounter.WithLabelValues("update", params.ZoneID.String(), mailTaxonomy.OutcomeInvalidArgument).Inc()
			return nil, apperr.Wrap(mailTaxonomy.ErrInvalidArgument, fmt.Errorf("tls mode 'mtls' requires ca_cert_pem, client_cert_pem, and client_key_pem"), mailTaxonomy.OutcomeInvalidArgument)
		}
	case "none", "starttls", "":
		// OK
	default:
		mailMetrics.EndpointOperationsCounter.WithLabelValues("update", params.ZoneID.String(), mailTaxonomy.OutcomeInvalidArgument).Inc()
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

	// Xử lý mã hóa password nếu được cung cấp mới (hoặc không trống)
	if params.Password != "" {
		enc, err := security.EncryptSecret(params.Password)
		if err != nil {
			mailMetrics.EndpointOperationsCounter.WithLabelValues("update", params.ZoneID.String(), mailTaxonomy.OutcomeCryptoError).Inc()
			return nil, apperr.Wrap(mailTaxonomy.ErrEnvelopeDecryptFailed, err, mailTaxonomy.OutcomeCryptoError)
		}
		existing.Password = enc
	}

	// Xử lý mã hóa client_key_pem nếu được cung cấp mới (hoặc không trống)
	if params.ClientKeyPEM != "" {
		enc, err := security.EncryptSecret(params.ClientKeyPEM)
		if err != nil {
			mailMetrics.EndpointOperationsCounter.WithLabelValues("update", params.ZoneID.String(), mailTaxonomy.OutcomeCryptoError).Inc()
			return nil, apperr.Wrap(mailTaxonomy.ErrEnvelopeDecryptFailed, err, mailTaxonomy.OutcomeCryptoError)
		}
		existing.ClientKeyPEM = enc
	}

	now := time.Now().UTC()
	existing.UpdatedAt = &now

	if err := s.endpointRepo.Update(ctx, existing); err != nil {
		mailMetrics.EndpointOperationsCounter.WithLabelValues("update", params.ZoneID.String(), mailTaxonomy.OutcomeDatabaseError).Inc()
		return nil, apperr.Wrap(mailTaxonomy.ErrInvalidArgument, err, mailTaxonomy.OutcomeDatabaseError)
	}

	// Giải mã password nhạy cảm để trả về thực thể sạch
	if existing.Password != "" {
		dec, err := security.DecryptSecret(existing.Password)
		if err == nil {
			existing.Password = dec
		}
	}
	if existing.ClientKeyPEM != "" {
		dec, err := security.DecryptSecret(existing.ClientKeyPEM)
		if err == nil {
			existing.ClientKeyPEM = dec
		}
	}

	mailMetrics.EndpointOperationsCounter.WithLabelValues("update", params.ZoneID.String(), mailTaxonomy.Success).Inc()
	return existing, nil
}

func (s *endpointServiceImpl) DeleteEndpoint(ctx context.Context, id uuid.UUID) error {
	zoneID, ok := middleware.GetZoneID(ctx)
	if !ok || zoneID == uuid.Nil {
		mailMetrics.EndpointOperationsCounter.WithLabelValues("create", "global", mailTaxonomy.OutcomeInvalidArgument).Inc()
		return apperr.Wrap(mailTaxonomy.ErrInvalidArgument, fmt.Errorf("mail service: zone is required to create endpoint"), mailTaxonomy.OutcomeInvalidArgument)
	}

	if err := s.endpointRepo.Delete(ctx, zoneID, id); err != nil {
		mailMetrics.EndpointOperationsCounter.WithLabelValues("delete", zoneID.String(), mailTaxonomy.OutcomeDatabaseError).Inc()
		return apperr.Wrap(mailTaxonomy.ErrEndpointNotFound, err, mailTaxonomy.OutcomeDatabaseError)
	}

	mailMetrics.EndpointOperationsCounter.WithLabelValues("delete", zoneID.String(), mailTaxonomy.Success).Inc()
	return nil
}

func (s *endpointServiceImpl) TestConnection(ctx context.Context, id uuid.UUID) error {
	zoneUUID, ok := middleware.GetZoneID(ctx)
	if !ok || zoneUUID == uuid.Nil {
		mailMetrics.EndpointOperationsCounter.WithLabelValues("test_connection", "global", mailTaxonomy.OutcomeInvalidArgument).Inc()
		return apperr.Wrap(mailTaxonomy.ErrInvalidArgument, fmt.Errorf("mail service: zone is required to test endpoint connection"), mailTaxonomy.OutcomeInvalidArgument)
	}

	_, err := s.GetEndpoint(ctx, id)
	if err != nil {
		mailMetrics.EndpointOperationsCounter.WithLabelValues("test_connection", zoneUUID.String(), mailTaxonomy.OutcomeNotFound).Inc()
		return apperr.Wrap(mailTaxonomy.ErrEndpointNotFound, err, mailTaxonomy.OutcomeNotFound)
	}

	mailMetrics.EndpointOperationsCounter.WithLabelValues("test_connection", zoneUUID.String(), mailTaxonomy.OutcomeDatabaseError).Inc()
	return apperr.Wrap(mailTaxonomy.ErrEndpointAuthFailed, fmt.Errorf("mail service: connection test is not implemented"), mailTaxonomy.OutcomeDatabaseError)
}

type SmtpTestConfig struct {
	Host          string  `json:"host"`
	Port          int     `json:"port"`
	Username      string  `json:"username"`
	Password      string  `json:"password"`
	TLSMode       string  `json:"tls_mode"`
	CACertPEM     *string `json:"ca_cert_pem,omitempty"`
	ClientCertPEM *string `json:"client_cert_pem,omitempty"`
	ClientKeyPEM  *string `json:"client_key_pem,omitempty"`
}

func (s *endpointServiceImpl) TestConnectionRaw(
	ctx context.Context,
	params mailEntity.CreateEndpointParams,
) error {
	switch params.TLSMode {
	case "tls":
		if strings.TrimSpace(params.CACertPEM) == "" {
			return apperr.Wrap(mailTaxonomy.ErrInvalidArgument, fmt.Errorf("tls mode 'tls' requires ca_cert_pem"), mailTaxonomy.OutcomeInvalidArgument)
		}
	case "mtls":
		if strings.TrimSpace(params.CACertPEM) == "" || strings.TrimSpace(params.ClientCertPEM) == "" || strings.TrimSpace(params.ClientKeyPEM) == "" {
			return apperr.Wrap(mailTaxonomy.ErrInvalidArgument, fmt.Errorf("tls mode 'mtls' requires ca_cert_pem, client_cert_pem, and client_key_pem"), mailTaxonomy.OutcomeInvalidArgument)
		}
	case "none", "starttls", "":
		// OK
	default:
		return apperr.Wrap(mailTaxonomy.ErrInvalidArgument, fmt.Errorf("invalid tls_mode: %s", params.TLSMode), mailTaxonomy.OutcomeInvalidArgument)
	}

	eventID, err := uuid.NewV7()
	if err != nil {
		return apperr.Wrap(mailTaxonomy.ErrInternal, err, mailTaxonomy.OutcomeDatabaseError)
	}

	// Build payload_json
	var caPtr, certPtr, keyPtr *string
	if params.CACertPEM != "" {
		caPtr = &params.CACertPEM
	}
	if params.ClientCertPEM != "" {
		certPtr = &params.ClientCertPEM
	}
	if params.ClientKeyPEM != "" {
		keyPtr = &params.ClientKeyPEM
	}

	smtpConfig := SmtpTestConfig{
		Host:          params.Host,
		Port:          params.Port,
		Username:      params.Username,
		Password:      params.Password,
		TLSMode:       params.TLSMode,
		CACertPEM:     caPtr,
		ClientCertPEM: certPtr,
		ClientKeyPEM:  keyPtr,
	}

	payloadBytes, err := json.Marshal(smtpConfig)
	if err != nil {
		return apperr.Wrap(mailTaxonomy.ErrInvalidArgument, err, mailTaxonomy.OutcomeInvalidArgument)
	}

	// Write to outbox_records
	record := &mailEntity.MailOutboxRecord{
		EventID:              eventID,
		ZoneID:               params.ZoneID,
		JobTopic:             "mail.test_connection",
		PayloadJSON:          string(payloadBytes),
		Status:               mailEntity.OutboxStatusPending,
		JobVersion:           1,
		ResourceID:           "",
		PayloadSchemaVersion: 1,
		TraceID:              eventID.String(),
		Idle:                 30,
	}

	if err := s.outboxRepo.Save(ctx, record); err != nil {
		return apperr.Wrap(mailTaxonomy.ErrInternal, fmt.Errorf("failed to save outbox event: %w", err), mailTaxonomy.OutcomeDatabaseError)
	}

	// Publish trigger event on rdsJob
	if err := s.rdsJob.Publish(ctx, "mail:outbox:trigger", "1").Err(); err != nil {
		logger.SysWarn("mail.outbox", fmt.Sprintf("Failed to publish outbox trigger notification: %v", err))
	}

	// Subscribe to result on rdsJob
	resultChannel := fmt.Sprintf("job_results:%s", eventID.String())
	pubsub := s.rdsJob.Subscribe(ctx, resultChannel)
	defer pubsub.Close()

	// Wait with timeout
	select {
	case <-ctx.Done():
		return apperr.Wrap(mailTaxonomy.ErrInternal, ctx.Err(), mailTaxonomy.OutcomeTimeout)
	case msg, ok := <-pubsub.Channel():
		if !ok || msg == nil {
			return apperr.Wrap(mailTaxonomy.ErrInternal, fmt.Errorf("result channel closed"), mailTaxonomy.OutcomeInternalError)
		}

		// Decode job execution result
		var result struct {
			Status       string `json:"status"` // SUCCEEDED, FAILED
			ErrorCode    string `json:"error_code"`
			ErrorMessage string `json:"error_message"`
		}
		if err := json.Unmarshal([]byte(msg.Payload), &result); err != nil {
			// fallback check if payload is plain text
			if msg.Payload == "SUCCEEDED" {
				return nil
			}
			return apperr.Wrap(mailTaxonomy.ErrEndpointAuthFailed, fmt.Errorf("connection test failed: %s", msg.Payload), mailTaxonomy.OutcomeDatabaseError)
		}

		if result.Status == "SUCCEEDED" {
			return nil
		}
		return apperr.Wrap(mailTaxonomy.ErrEndpointAuthFailed, fmt.Errorf("connection test failed: %s (code: %s)", result.ErrorMessage, result.ErrorCode), mailTaxonomy.OutcomeDatabaseError)
	case <-time.After(8 * time.Second):
		return apperr.Wrap(mailTaxonomy.ErrInternal, fmt.Errorf("timeout waiting for connection test result from dataplane"), mailTaxonomy.OutcomeTimeout)
	}
}
