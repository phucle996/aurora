package mailSvcImpl

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	mailEntity "controlplane/internal/mail/domain/entity"
	mailRepoInterface "controlplane/internal/mail/domain/repo"
	mailSvcInterface "controlplane/internal/mail/domain/service"
	mailTaxonomy "controlplane/internal/mail/taxonomy"
	mailproto "controlplane/internal/mail/transport/rpc/proto"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
)

// [COMMENT]: Namespace cố định giữ consumer/event ID deterministic qua mọi replica Personal.
var personalMailConsumerEventNamespace = uuid.MustParse("43de31a4-0c86-54e9-8384-47b33f541c28")

type personalConsumerServiceImpl struct {
	repo mailRepoInterface.PersonalConsumerRepository
}

// NewPersonalConsumerService khoi tao service quản lý consumer o scope Personal.
func NewPersonalConsumerService(repo mailRepoInterface.PersonalConsumerRepository) mailSvcInterface.PersonalConsumerService {
	return &personalConsumerServiceImpl{repo: repo}
}

// CreateConsumer thuc hien validate va khoi tao consumer moi cung outbox record cho Personal.
// [COMMENT]: Handler/DTO layer đã validate va normalize input payload; service tap trung vao domain logic.
func (s *personalConsumerServiceImpl) CreateConsumer(ctx context.Context, req *mailEntity.CreatePersonalConsumer) (*mailEntity.CreatePersonalConsumer, error) {
	// [COMMENT]: UUID là runtime identity mới cho mỗi lần create; hard-delete giải phóng code nhưng không cho tái sử dụng identity cũ.
	consumerID := uuid.New()
	now := time.Now().UTC()
	actor := req.ActorUserID

	consumer := &mailEntity.CreatePersonalConsumer{
		ActorUserID:          req.ActorUserID,
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
		CreatedAt:            now,
		UpdatedAt:            now,
	}

	// [COMMENT]: Match một lần tại producer rồi đóng gói adapter protobuf riêng; JO/outbox không hiểu field của broker.
	var streamType mailproto.MailStreamType
	var streamPayload []byte
	var err error
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
		return nil, fmt.Errorf("mail personal consumer service: marshal stream payload: %w", err)
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
	}

	canonicalConfig, err := proto.MarshalOptions{Deterministic: true}.Marshal(upsert)
	if err != nil {
		return nil, fmt.Errorf("mail personal consumer service: marshal canonical config: %w", err)
	}

	configHash := sha256.Sum256(canonicalConfig)
	consumer.ConfigSHA256 = append([]byte(nil), configHash[:]...)
	upsert.ConfigSha256 = consumer.ConfigSHA256

	eventID := uuid.NewSHA1(personalMailConsumerEventNamespace, []byte("consumer:"+consumer.ID.String()+":1:upsert:"+req.ZoneID.String()))
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
		return nil, fmt.Errorf("mail personal consumer service: marshal create event: %w", err)
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

// GetConsumer lay thong tin consumer theo ID va Personal request.
func (s *personalConsumerServiceImpl) GetConsumer(ctx context.Context, req *mailEntity.GetPersonalConsumer) (*mailEntity.GetPersonalConsumer, error) {
	return s.repo.GetByID(ctx, req)
}

// ListConsumers danh sach consumer theo dieu kien loc va pagination.
func (s *personalConsumerServiceImpl) ListConsumers(ctx context.Context, req *mailEntity.ListPersonalConsumer) ([]*mailEntity.ListPersonalConsumer, error) {
	return s.repo.List(ctx, req)
}

// UpdateConsumer cap nhat thong tin consumer voi optimistic version check va tao outbox event.
func (s *personalConsumerServiceImpl) UpdateConsumer(ctx context.Context, req *mailEntity.UpdatePersonalConsumer) (*mailEntity.UpdatePersonalConsumer, error) {
	// [COMMENT]: API không bao giờ đọc trả ciphertext; envelope rỗng trong full update giữ nguyên cấu hình broker hiện tại.
	current, err := s.repo.GetByID(ctx, &mailEntity.GetPersonalConsumer{
		ActorUserID: req.ActorUserID,
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

	consumer := &mailEntity.UpdatePersonalConsumer{
		ActorUserID:          req.ActorUserID,
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
		return nil, fmt.Errorf("mail personal consumer service: marshal stream payload: %w", err)
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
	}

	canonicalConfig, err := proto.MarshalOptions{Deterministic: true}.Marshal(upsert)
	if err != nil {
		return nil, fmt.Errorf("mail personal consumer service: marshal update config: %w", err)
	}

	configHash := sha256.Sum256(canonicalConfig)
	consumer.ConfigSHA256 = append([]byte(nil), configHash[:]...)
	upsert.ConfigSha256 = consumer.ConfigSHA256

	eventID := uuid.NewSHA1(personalMailConsumerEventNamespace, fmt.Appendf(nil, "consumer:%s:%d:upsert:%s", consumer.ID, consumer.ConfigVersion, req.ZoneID))
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
		return nil, fmt.Errorf("mail personal consumer service: marshal update event: %w", err)
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

// ChangeConsumerState thay doi trang thai pause/resume cua consumer.
func (s *personalConsumerServiceImpl) ChangeConsumerState(ctx context.Context, req *mailEntity.ChangePersonalConsumerState) (*mailEntity.ChangePersonalConsumerState, error) {
	// [COMMENT]: Lay consumer hien tai tu repository
	consumer, err := s.repo.GetByID(ctx, &mailEntity.GetPersonalConsumer{
		ActorUserID: req.ActorUserID,
		ZoneID:      req.ZoneID,
		ID:          req.ID,
		WorkspaceID: req.WorkspaceID,
	})
	if err != nil {
		return nil, err
	}

	// [COMMENT]: Kiem tra phien ban cau hinh hien tai so voi phien ban ky vọng
	if consumer.ConfigVersion != req.ExpectedConfigVersion {
		return nil, mailTaxonomy.ErrVersionConflict
	}

	// [COMMENT]: Goi UpdateConsumer de thuc hien update trang thai va phat outbox event
	updated, err := s.UpdateConsumer(ctx, &mailEntity.UpdatePersonalConsumer{
		ActorUserID:           req.ActorUserID,
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
func (s *personalConsumerServiceImpl) DeleteConsumer(ctx context.Context, req *mailEntity.DeletePersonalConsumer) error {
	// [COMMENT]: Delete chỉ phát command với fence lớn hơn active version; CP không đổi aggregate trước Zone.
	current, err := s.repo.GetByID(ctx, &mailEntity.GetPersonalConsumer{
		ActorUserID: req.ActorUserID,
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
		ConsumerId:          req.ID[:],
		ConfigVersion:       deleteFence,
		DrainTimeoutSeconds: req.DrainTimeoutSeconds,
		Reason:              req.Reason,
	}

	var traceID []byte
	if spanContext := trace.SpanContextFromContext(ctx); spanContext.IsValid() {
		tombstone.Metadata.Traceparent = "00-" + spanContext.TraceID().String() + "-" + spanContext.SpanID().String() + "-" + fmt.Sprintf("%02x", byte(spanContext.TraceFlags()))
		id := spanContext.TraceID()
		traceID = append([]byte(nil), id[:]...)
	}

	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(tombstone)
	if err != nil {
		return fmt.Errorf("mail personal consumer service: marshal delete event: %w", err)
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
		Idle:                 req.DrainTimeoutSeconds + 30,
	}

	// [COMMENT]: Repository khóa aggregate và chỉ insert outbox; JO hard-delete record sau Zone ACK.
	req.UpdatedAt = updatedAt
	if err = s.repo.Delete(ctx, req, outbox); err != nil {
		return err
	}
	req.OperationID = outbox.EventID
	return nil
}
