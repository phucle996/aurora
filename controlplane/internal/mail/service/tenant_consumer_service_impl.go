package mailSvcImpl

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	mailEntity "controlplane/internal/mail/domain/entity"
	mailRepoInterface "controlplane/internal/mail/domain/repo"
	mailSvcInterface "controlplane/internal/mail/domain/service"
	mailTaxonomy "controlplane/internal/mail/taxonomy"
	mailproto "controlplane/internal/mail/transport/proto"
	"controlplane/internal/observability"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

// [COMMENT]: Namespace cố định giữ consumer/event ID deterministic qua mọi replica Tenant.
var tenantMailConsumerEventNamespace = uuid.MustParse("43de31a4-0c86-54e9-8384-47b33f541c28")

type tenantConsumerServiceImpl struct {
	repo    mailRepoInterface.TenantConsumerRepository
	metrics observability.WorkflowRecorder
}

// NewTenantConsumerService khoi tao service quản lý consumer o scope Tenant.
func NewTenantConsumerService(repo mailRepoInterface.TenantConsumerRepository, metrics observability.WorkflowRecorder) mailSvcInterface.TenantConsumerService {
	return &tenantConsumerServiceImpl{repo: repo, metrics: metrics}
}

// CreateConsumer thuc hien validate va khoi tao consumer moi cung outbox record cho Tenant.
// [COMMENT]: Handler/DTO layer đã validate va normalize input payload; service tap trung vao domain logic.
func (s *tenantConsumerServiceImpl) CreateConsumer(ctx context.Context, req *mailEntity.CreateTenantConsumer) (out *mailEntity.CreateTenantConsumer, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, mailTaxonomy.ErrAlreadyExists):
			result, reason = observability.ResultRejected, observability.ReasonAlreadyExists
		case errors.Is(err, mailTaxonomy.ErrWorkspaceNotFound), errors.Is(err, mailTaxonomy.ErrTemplateNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, mailTaxonomy.ErrInvalidArgument):
			result, reason = observability.ResultRejected, observability.ReasonInvalidArgument
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	// [COMMENT]: UUID là runtime identity mới cho mỗi lần create; hard-delete giải phóng code nhưng không cho tái sử dụng identity cũ.
	consumerID := uuid.New()
	now := time.Now().UTC()
	actor := req.ActorUserID

	consumer := &mailEntity.CreateTenantConsumer{
		ActorUserID:          req.ActorUserID,
		TenantID:             req.TenantID,
		ZoneID:               req.ZoneID,
		ID:                   consumerID,
		WorkspaceID:          req.WorkspaceID,
		Code:                 req.Code,
		Name:                 req.Name,
		SourceType:           req.SourceType,
		BrokerResourceID:     req.BrokerResourceID,
		SourceConfigEnvelope: append([]byte(nil), req.SourceConfigEnvelope...),
		Topic:                req.Topic,
		ConsumerGroup:        req.ConsumerGroup,
		TemplateID:           req.TemplateID,
		TemplateVersion:      req.TemplateVersion,
		SenderProfileID:      req.SenderProfileID,
		SenderVersion:        req.SenderVersion,
		DesiredState:         mailEntity.ConsumerPaused,
		Parallelism:          req.Parallelism,
		ConfigVersion:        1,
		NextConfigVersion:    2,
		CreatedBy:            actor,
		UpdatedBy:            actor,
		CreatedAt:            now,
		UpdatedAt:            now,
	}

	// [COMMENT]: Match một lần tại producer rồi đóng gói adapter protobuf riêng; JO/outbox không hiểu field của broker.
	var streamType mailproto.MailStreamType
	var streamPayload []byte
	switch consumer.SourceType {
	case mailEntity.Kafka:
		streamType = mailproto.MailStreamType_MAIL_STREAM_TYPE_KAFKA
		streamPayload, err = proto.MarshalOptions{Deterministic: true}.Marshal(&mailproto.KafkaStreamPayloadV1{SourceConfigEnvelope: consumer.SourceConfigEnvelope, Topic: consumer.Topic, ConsumerGroup: consumer.ConsumerGroup})
	case mailEntity.RedisStream:
		streamType = mailproto.MailStreamType_MAIL_STREAM_TYPE_REDIS_STREAM
		streamPayload, err = proto.MarshalOptions{Deterministic: true}.Marshal(&mailproto.RedisStreamPayloadV1{SourceConfigEnvelope: consumer.SourceConfigEnvelope, StreamKey: consumer.Topic, ConsumerGroup: consumer.ConsumerGroup})
	case mailEntity.NATSJetStream:
		streamType = mailproto.MailStreamType_MAIL_STREAM_TYPE_NATS_JETSTREAM
		streamPayload, err = proto.MarshalOptions{Deterministic: true}.Marshal(&mailproto.NatsJetStreamPayloadV1{SourceConfigEnvelope: consumer.SourceConfigEnvelope, StreamName: consumer.Topic, DurableName: consumer.ConsumerGroup})
	case mailEntity.RabbitMQ:
		streamType = mailproto.MailStreamType_MAIL_STREAM_TYPE_RABBITMQ
		streamPayload, err = proto.MarshalOptions{Deterministic: true}.Marshal(&mailproto.RabbitMqPayloadV1{SourceConfigEnvelope: consumer.SourceConfigEnvelope, QueueName: consumer.Topic, ConsumerTagPrefix: consumer.ConsumerGroup})
	}
	if err != nil {
		return nil, fmt.Errorf("mail tenant consumer service: marshal stream payload: %w", err)
	}

	upsert := &mailproto.MailConsumerUpsertV1{
		ConsumerId:    consumer.ID[:],
		ConfigVersion: 1,
		Stream: &mailproto.MailStreamSourceV1{
			StreamType:           streamType,
			PayloadSchemaVersion: 1,
			BrokerResourceId:     consumer.BrokerResourceID[:],
			Payload:              streamPayload,
		},
		TemplateId:      consumer.TemplateID,
		TemplateVersion: consumer.TemplateVersion,
		SenderProfileId: consumer.SenderProfileID,
		SenderVersion:   consumer.SenderVersion,
		DesiredState:    mailproto.MailConsumerDesiredState_MAIL_CONSUMER_DESIRED_STATE_PAUSED,
		Parallelism:     consumer.Parallelism,
		OwnerId:         consumer.TenantID[:],
		OwnerType:       "TENANT",
		WorkspaceId:     consumer.WorkspaceID[:],
		ZoneId:          consumer.ZoneID[:],
	}

	canonicalConfig, err := proto.MarshalOptions{Deterministic: true}.Marshal(upsert)
	if err != nil {
		return nil, fmt.Errorf("mail tenant consumer service: marshal canonical config: %w", err)
	}

	configHash := sha256.Sum256(canonicalConfig)
	consumer.ConfigSHA256 = append([]byte(nil), configHash[:]...)

	upsert.ConfigSha256 = consumer.ConfigSHA256

	eventID := uuid.NewSHA1(tenantMailConsumerEventNamespace, []byte("consumer:"+consumer.ID.String()+":1:upsert:"+req.ZoneID.String()))
	upsert.Metadata = &mailproto.MailEventMetadataV1{
		EventId:          eventID[:],
		SchemaVersion:    1,
		OccurredAtUnixMs: now.UnixMilli(),
		Producer:         "controlplane-mail",
	}

	var traceID []byte
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		upsert.Metadata.Traceparent = "00-" + spanContext.TraceID().String() + "-" + spanContext.SpanID().String() + "-" + fmt.Sprintf("%02x", byte(spanContext.TraceFlags()))
		id := spanContext.TraceID()
		traceID = append([]byte(nil), id[:]...)
	}

	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(upsert)
	if err != nil {
		return nil, fmt.Errorf("mail tenant consumer service: marshal create event: %w", err)
	}

	outbox := &mailEntity.MailOutboxRecord{
		EventID:              eventID,
		ZoneID:               req.ZoneID,
		JobTopic:             "mail.consumer.upsert",
		Payload:              payload,
		ActorUserID:          actor,
		Status:               mailEntity.OutboxStatusPending,
		JobVersion:           1,
		ResourceID:           consumer.ID.String(),
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 60,
	}

	// [COMMENT]: Repository commit consumer và outbox trong cùng một PostgreSQL transaction.
	if err = s.repo.Create(ctx, consumer, outbox); err != nil {
		return nil, err
	}
	consumer.OperationID = outbox.EventID
	return consumer, nil
}

// GetConsumer lay thong tin consumer theo ID va Tenant request.
func (s *tenantConsumerServiceImpl) GetConsumer(ctx context.Context, req *mailEntity.GetTenantConsumer) (out *mailEntity.GetTenantConsumer, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		} else if errors.Is(err, mailTaxonomy.ErrConsumerNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.GetByID(ctx, req)
}

// ListConsumers danh sach consumer theo dieu kien loc va pagination.
func (s *tenantConsumerServiceImpl) ListConsumers(ctx context.Context, req *mailEntity.ListTenantConsumer) (out []*mailEntity.ListTenantConsumer, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if err == nil {
			result, reason = observability.ResultSuccess, observability.ReasonNone
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()
	return s.repo.List(ctx, req)
}

// UpdateConsumer cap nhat thong tin consumer voi optimistic version check va tao outbox event.
func (s *tenantConsumerServiceImpl) UpdateConsumer(ctx context.Context, req *mailEntity.UpdateTenantConsumer) (out *mailEntity.UpdateTenantConsumer, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, mailTaxonomy.ErrConsumerNotFound), errors.Is(err, mailTaxonomy.ErrTemplateNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, mailTaxonomy.ErrVersionConflict):
			result, reason = observability.ResultRejected, observability.ReasonConflict
		case errors.Is(err, mailTaxonomy.ErrOperationInProgress):
			result, reason = observability.ResultRejected, observability.ReasonBusy
		case errors.Is(err, mailTaxonomy.ErrInvalidArgument):
			result, reason = observability.ResultRejected, observability.ReasonInvalidArgument
		case errors.Is(err, mailTaxonomy.ErrCommercialAdmissionUnavailable):
			result, reason = observability.ResultRejected, observability.ReasonUnavailable
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	// [COMMENT]: API không bao giờ đọc trả ciphertext; envelope rỗng trong full update giữ nguyên cấu hình broker hiện tại.
	current, err := s.repo.GetByID(ctx, &mailEntity.GetTenantConsumer{
		ActorUserID: req.ActorUserID,
		TenantID:    req.TenantID,
		ZoneID:      req.ZoneID,
		ID:          req.ID,
		WorkspaceID: req.WorkspaceID,
	})
	if err != nil {
		return nil, err
	}
	sourceConfigEnvelope := append([]byte(nil), req.SourceConfigEnvelope...)
	if len(sourceConfigEnvelope) == 0 {
		// [COMMENT]: Envelope AAD bind cả stream type và broker resource; giữ ciphertext cũ sau khi đổi một trong hai sẽ tạo config chắc chắn không decrypt được.
		if req.SourceType != current.SourceType || req.BrokerResourceID != current.BrokerResourceID {
			return nil, mailTaxonomy.ErrInvalidArgument
		}
		sourceConfigEnvelope = append([]byte(nil), current.SourceConfigEnvelope...)
	}
	if len(sourceConfigEnvelope) > 16<<10 || (req.DesiredState == mailEntity.ConsumerEnabled && len(sourceConfigEnvelope) == 0) {
		return nil, mailTaxonomy.ErrInvalidArgument
	}

	// [COMMENT]: Optimistic config_version ở repository đóng race giữa lần đọc giữ envelope và UPDATE.
	now := time.Now().UTC()
	actor := req.ActorUserID

	consumer := &mailEntity.UpdateTenantConsumer{
		ActorUserID:          req.ActorUserID,
		TenantID:             req.TenantID,
		ZoneID:               req.ZoneID,
		ID:                   req.ID,
		WorkspaceID:          req.WorkspaceID,
		Code:                 current.Code,
		Name:                 req.Name,
		SourceType:           req.SourceType,
		BrokerResourceID:     req.BrokerResourceID,
		SourceConfigEnvelope: sourceConfigEnvelope,
		Topic:                req.Topic,
		ConsumerGroup:        req.ConsumerGroup,
		TemplateID:           req.TemplateID,
		TemplateVersion:      req.TemplateVersion,
		SenderProfileID:      req.SenderProfileID,
		SenderVersion:        req.SenderVersion,
		DesiredState:         req.DesiredState,
		Parallelism:          req.Parallelism,
		// [COMMENT]: Candidate lấy monotonic sequence của aggregate; version FAILED không được tái sử dụng.
		ConfigVersion:         current.NextConfigVersion,
		NextConfigVersion:     current.NextConfigVersion + 1,
		ExpectedConfigVersion: req.ExpectedConfigVersion,
		CreatedBy:             current.CreatedBy,
		UpdatedBy:             actor,
		CreatedAt:             current.CreatedAt,
		UpdatedAt:             now,
	}

	desiredState := mailproto.MailConsumerDesiredState_MAIL_CONSUMER_DESIRED_STATE_PAUSED
	if consumer.DesiredState == mailEntity.ConsumerEnabled {
		desiredState = mailproto.MailConsumerDesiredState_MAIL_CONSUMER_DESIRED_STATE_ENABLED
	}

	// [COMMENT]: Update giữ nguyên nguyên tắc mỗi suite một payload; không có generic map hoặc JSON mapper ở giữa.
	var streamType mailproto.MailStreamType
	var streamPayload []byte
	switch consumer.SourceType {
	case mailEntity.Kafka:
		streamType = mailproto.MailStreamType_MAIL_STREAM_TYPE_KAFKA
		streamPayload, err = proto.MarshalOptions{Deterministic: true}.Marshal(&mailproto.KafkaStreamPayloadV1{SourceConfigEnvelope: consumer.SourceConfigEnvelope, Topic: consumer.Topic, ConsumerGroup: consumer.ConsumerGroup})
	case mailEntity.RedisStream:
		streamType = mailproto.MailStreamType_MAIL_STREAM_TYPE_REDIS_STREAM
		streamPayload, err = proto.MarshalOptions{Deterministic: true}.Marshal(&mailproto.RedisStreamPayloadV1{SourceConfigEnvelope: consumer.SourceConfigEnvelope, StreamKey: consumer.Topic, ConsumerGroup: consumer.ConsumerGroup})
	case mailEntity.NATSJetStream:
		streamType = mailproto.MailStreamType_MAIL_STREAM_TYPE_NATS_JETSTREAM
		streamPayload, err = proto.MarshalOptions{Deterministic: true}.Marshal(&mailproto.NatsJetStreamPayloadV1{SourceConfigEnvelope: consumer.SourceConfigEnvelope, StreamName: consumer.Topic, DurableName: consumer.ConsumerGroup})
	case mailEntity.RabbitMQ:
		streamType = mailproto.MailStreamType_MAIL_STREAM_TYPE_RABBITMQ
		streamPayload, err = proto.MarshalOptions{Deterministic: true}.Marshal(&mailproto.RabbitMqPayloadV1{SourceConfigEnvelope: consumer.SourceConfigEnvelope, QueueName: consumer.Topic, ConsumerTagPrefix: consumer.ConsumerGroup})
	}
	if err != nil {
		return nil, fmt.Errorf("mail tenant consumer service: marshal stream payload: %w", err)
	}

	upsert := &mailproto.MailConsumerUpsertV1{
		ConsumerId:    consumer.ID[:],
		ConfigVersion: consumer.ConfigVersion,
		Stream: &mailproto.MailStreamSourceV1{
			StreamType:           streamType,
			PayloadSchemaVersion: 1,
			BrokerResourceId:     consumer.BrokerResourceID[:],
			Payload:              streamPayload,
		},
		TemplateId:      consumer.TemplateID,
		TemplateVersion: consumer.TemplateVersion,
		SenderProfileId: consumer.SenderProfileID,
		SenderVersion:   consumer.SenderVersion,
		DesiredState:    desiredState,
		Parallelism:     consumer.Parallelism,
		OwnerId:         consumer.TenantID[:],
		OwnerType:       "TENANT",
		WorkspaceId:     consumer.WorkspaceID[:],
		ZoneId:          consumer.ZoneID[:],
	}

	canonicalConfig, err := proto.MarshalOptions{Deterministic: true}.Marshal(upsert)
	if err != nil {
		return nil, fmt.Errorf("mail tenant consumer service: marshal update config: %w", err)
	}

	configHash := sha256.Sum256(canonicalConfig)
	consumer.ConfigSHA256 = append([]byte(nil), configHash[:]...)
	upsert.ConfigSha256 = consumer.ConfigSHA256

	eventID := uuid.NewSHA1(tenantMailConsumerEventNamespace, fmt.Appendf(nil, "consumer:%s:%d:upsert:%s", consumer.ID, consumer.ConfigVersion, req.ZoneID))
	upsert.Metadata = &mailproto.MailEventMetadataV1{
		EventId:          eventID[:],
		SchemaVersion:    1,
		OccurredAtUnixMs: now.UnixMilli(),
		Producer:         "controlplane-mail",
	}

	var traceID []byte
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		upsert.Metadata.Traceparent = "00-" + spanContext.TraceID().String() + "-" + spanContext.SpanID().String() + "-" + fmt.Sprintf("%02x", byte(spanContext.TraceFlags()))
		id := spanContext.TraceID()
		traceID = append([]byte(nil), id[:]...)
	}

	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(upsert)
	if err != nil {
		return nil, fmt.Errorf("mail tenant consumer service: marshal update event: %w", err)
	}

	outbox := &mailEntity.MailOutboxRecord{
		EventID:              eventID,
		ZoneID:               req.ZoneID,
		JobTopic:             "mail.consumer.upsert",
		Payload:              payload,
		ActorUserID:          actor,
		Status:               mailEntity.OutboxStatusPending,
		JobVersion:           1,
		ResourceID:           consumer.ID.String(),
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 60,
	}

	// [COMMENT]: Repository chỉ lưu immutable candidate + outbox; active row chờ JO promote sau Zone ACK.
	if err = s.repo.Update(ctx, consumer, outbox); err != nil {
		return nil, err
	}
	consumer.OperationID = outbox.EventID
	return consumer, nil
}

func (s *tenantConsumerServiceImpl) ChangeConsumerState(ctx context.Context, req *mailEntity.ChangeTenantConsumerState) (*mailEntity.ChangeTenantConsumerState, error) {
	startedAt := time.Now()
	// [COMMENT]: Lay consumer hien tai tu repository
	consumer, err := s.repo.GetByID(ctx, &mailEntity.GetTenantConsumer{
		ActorUserID: req.ActorUserID,
		TenantID:    req.TenantID,
		ZoneID:      req.ZoneID,
		ID:          req.ID,
		WorkspaceID: req.WorkspaceID,
	})
	if err != nil {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		if errors.Is(err, mailTaxonomy.ErrConsumerNotFound) {
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
		return nil, err
	}

	// [COMMENT]: Kiem tra phien ban cau hinh hien tai so voi phien ban ky vọng
	if consumer.ConfigVersion != req.ExpectedConfigVersion {
		s.metrics.ObserveWorkflow(ctx, observability.ResultRejected, observability.ReasonConflict, time.Since(startedAt))
		return nil, mailTaxonomy.ErrVersionConflict
	}

	// [COMMENT]: Goi UpdateConsumer de thuc hien update trang thai va phat outbox event
	updated, err := s.UpdateConsumer(ctx, &mailEntity.UpdateTenantConsumer{
		ActorUserID:           req.ActorUserID,
		TenantID:              req.TenantID,
		ZoneID:                req.ZoneID,
		WorkspaceID:           req.WorkspaceID,
		ID:                    consumer.ID,
		ExpectedConfigVersion: req.ExpectedConfigVersion,
		Name:                  consumer.Name,
		SourceType:            consumer.SourceType,
		BrokerResourceID:      consumer.BrokerResourceID,
		Topic:                 consumer.Topic,
		ConsumerGroup:         consumer.ConsumerGroup,
		TemplateID:            consumer.TemplateID,
		TemplateVersion:       consumer.TemplateVersion,
		SenderProfileID:       consumer.SenderProfileID,
		SenderVersion:         consumer.SenderVersion,
		DesiredState:          req.DesiredState,
		Parallelism:           consumer.Parallelism,
	})
	if err != nil {
		return nil, err
	}
	req.OperationID = updated.OperationID
	req.ConfigVersion = updated.ConfigVersion
	req.UpdatedAt = updated.UpdatedAt
	req.Code = updated.Code
	req.Name = updated.Name
	req.SourceType = updated.SourceType
	req.BrokerResourceID = updated.BrokerResourceID
	req.SourceConfigEnvelope = append([]byte(nil), updated.SourceConfigEnvelope...)
	req.Topic = updated.Topic
	req.ConsumerGroup = updated.ConsumerGroup
	req.TemplateID = updated.TemplateID
	req.TemplateVersion = updated.TemplateVersion
	req.SenderProfileID = updated.SenderProfileID
	req.SenderVersion = updated.SenderVersion
	req.Parallelism = updated.Parallelism
	req.ConfigSHA256 = append([]byte(nil), updated.ConfigSHA256...)
	req.CreatedAt = updated.CreatedAt
	return req, nil
}

// DeleteConsumer xoa consumer va phat tombstone delete event vao outbox.
func (s *tenantConsumerServiceImpl) DeleteConsumer(ctx context.Context, req *mailEntity.DeleteTenantConsumer) (err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, mailTaxonomy.ErrConsumerNotFound):
			result, reason = observability.ResultRejected, observability.ReasonNotFound
		case errors.Is(err, mailTaxonomy.ErrVersionConflict):
			result, reason = observability.ResultRejected, observability.ReasonConflict
		case errors.Is(err, mailTaxonomy.ErrOperationInProgress):
			result, reason = observability.ResultRejected, observability.ReasonBusy
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	// Delete yêu cầu drained; repository chuyển cùng cột state sang deleting và append outbox nguyên tử.
	current, err := s.repo.GetByID(ctx, &mailEntity.GetTenantConsumer{
		ActorUserID: req.ActorUserID,
		TenantID:    req.TenantID,
		ZoneID:      req.ZoneID,
		ID:          req.ID,
		WorkspaceID: req.WorkspaceID,
	})
	if err != nil {
		return err
	}
	if current.ConfigVersion != req.ExpectedConfigVersion {
		return mailTaxonomy.ErrVersionConflict
	}
	// [COMMENT]: Fence lấy monotonic allocator để cao hơn cả candidate FAILED có thể từng chạm Zone.
	deleteFence := current.NextConfigVersion
	updatedAt := time.Now().UTC()
	// [COMMENT]: Delete không advance business version, nên mỗi retry cần operation ID mới; DP vẫn dedupe bằng version fence.
	eventID := uuid.New()
	tombstone := &mailproto.MailConsumerDeleteV1{
		Metadata: &mailproto.MailEventMetadataV1{
			EventId:          eventID[:],
			SchemaVersion:    1,
			OccurredAtUnixMs: updatedAt.UnixMilli(),
			Producer:         "controlplane-mail",
		},
		ConsumerId:    req.ID[:],
		ConfigVersion: deleteFence,
		Reason:        req.Reason,
	}

	var traceID []byte
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		tombstone.Metadata.Traceparent = "00-" + spanContext.TraceID().String() + "-" + spanContext.SpanID().String() + "-" + fmt.Sprintf("%02x", byte(spanContext.TraceFlags()))
		id := spanContext.TraceID()
		traceID = append([]byte(nil), id[:]...)
	}

	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(tombstone)
	if err != nil {
		return fmt.Errorf("mail tenant consumer service: marshal delete event: %w", err)
	}

	actor := req.ActorUserID
	outbox := &mailEntity.MailOutboxRecord{
		EventID:              eventID,
		ZoneID:               req.ZoneID,
		JobTopic:             "mail.consumer.delete",
		Payload:              payload,
		ActorUserID:          actor,
		Status:               mailEntity.OutboxStatusPending,
		JobVersion:           1,
		ResourceID:           req.ID.String(),
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 60,
	}

	// [COMMENT]: Repository khóa aggregate và chỉ insert outbox; JO hard-delete record sau Zone ACK.
	req.UpdatedAt = updatedAt
	if err = s.repo.Delete(ctx, req, outbox); err != nil {
		return err
	}
	req.OperationID = outbox.EventID
	return nil
}

func (s *tenantConsumerServiceImpl) Drain(ctx context.Context, cmd mailEntity.TenantConsumerDrainCommand) (uuid.UUID, error) {
	target, err := s.repo.LoadDrainTarget(ctx, cmd)
	if err != nil {
		return uuid.Nil, err
	}
	if target.ConfigVersion != cmd.ExpectedConfigVersion {
		return uuid.Nil, mailTaxonomy.ErrVersionConflict
	}
	if target.State != mailEntity.ConsumerEnabled && target.State != mailEntity.ConsumerPaused {
		return uuid.Nil, mailTaxonomy.ErrOperationInProgress
	}
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(&mailproto.MailConsumerDrainV1{
		SchemaVersion: 1, ConsumerId: cmd.ConsumerID[:], ConfigVersion: target.ConfigVersion,
		Parallelism: target.Parallelism, TimeoutSeconds: cmd.TimeoutSeconds,
	})
	if err != nil {
		return uuid.Nil, err
	}
	id := uuid.New()
	outbox := mailEntity.MailOutboxRecord{EventID: id, ZoneID: cmd.ZoneID, JobTopic: "mail.consumer.drain",
		Payload: payload, ActorUserID: cmd.ActorUserID, Status: mailEntity.OutboxStatusPending, JobVersion: 1,
		ResourceID: cmd.ConsumerID.String(), PayloadSchemaVersion: 1, Idle: min(cmd.TimeoutSeconds+30, 3600)}
	if err = s.repo.RequestDrain(ctx, cmd, target.Parallelism, outbox); err != nil {
		return uuid.Nil, err
	}
	return id, nil
}
