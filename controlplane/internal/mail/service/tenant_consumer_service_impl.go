package mailSvcImpl

import (
	"context"
	"crypto/sha256"
	"encoding/json"
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

// [COMMENT]: Namespace cố định giữ consumer/event ID deterministic qua mọi replica Tenant.
var tenantMailConsumerEventNamespace = uuid.MustParse("43de31a4-0c86-54e9-8384-47b33f541c28")

type tenantConsumerServiceImpl struct {
	repo mailRepoInterface.TenantConsumerRepository
}

// NewTenantConsumerService khoi tao service quản lý consumer o scope Tenant.
func NewTenantConsumerService(repo mailRepoInterface.TenantConsumerRepository) mailSvcInterface.TenantConsumerService {
	return &tenantConsumerServiceImpl{repo: repo}
}

// CreateConsumer thuc hien validate va khoi tao consumer moi cung outbox record cho Tenant command.
func (s *tenantConsumerServiceImpl) CreateConsumer(ctx context.Context, command *mailEntity.TenantConsumer) (*mailEntity.TenantConsumer, error) {
	// [COMMENT]: Handler đã normalize/validate; service bắt đầu trực tiếp từ business payload.
	mappingJSON, err := json.Marshal(command.Mapping)
	if err != nil {
		return nil, fmt.Errorf("mail tenant consumer service: marshal mapping: %w", err)
	}

	// [COMMENT]: UUID là runtime identity mới cho mỗi lần create; code có thể được dùng lại sau soft-delete mà không va PK cũ.
	consumerID := uuid.New()
	now := time.Now().UTC()
	actor := command.ActorUserID

	consumer := &mailEntity.TenantConsumer{
		ActorUserID:      command.ActorUserID,
		TenantID:         command.TenantID,
		ZoneID:           command.ZoneID,
		ID:               consumerID,
		WorkspaceID:      command.WorkspaceID,
		Code:             command.Code,
		Name:             command.Name,
		SourceType:       command.SourceType,
		BrokerResourceID: command.BrokerResourceID,
		SourceConfigRef:  "vault://zones/" + command.ZoneID.String() + "/workspaces/" + command.WorkspaceID.String() + "/mail/brokers/" + command.BrokerResourceID.String(),
		Topic:            command.Topic,
		ConsumerGroup:    command.ConsumerGroup,
		MappingJSON:      mappingJSON,
		TemplateID:       command.TemplateID,
		TemplateVersion:  command.TemplateVersion,
		SenderProfileID:  command.SenderProfileID,
		SenderVersion:    command.SenderVersion,
		DesiredState:     mailEntity.ConsumerPaused,
		Parallelism:      command.Parallelism,
		ConfigVersion:    1,
		CreatedBy:        &actor,
		UpdatedBy:        &actor,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	upsert := &mailproto.MailConsumerUpsertV1{
		ConsumerId:    consumer.ID[:],
		ConfigVersion: 1,
		Kafka: &mailproto.KafkaSourceV1{
			BrokerResourceId: consumer.BrokerResourceID[:],
			SourceConfigRef:  consumer.SourceConfigRef,
			Topic:            consumer.Topic,
			ConsumerGroup:    consumer.ConsumerGroup,
		},
		Mapping: &mailproto.MailMessageMappingV1{
			ExternalMessageIdJsonPath: command.Mapping.ExternalMessageIDJSONPath,
			RecipientJsonPath:         command.Mapping.RecipientJSONPath,
			VariableJsonPaths:         command.Mapping.VariableJSONPaths,
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
		return nil, fmt.Errorf("mail tenant consumer service: marshal canonical config: %w", err)
	}

	configHash := sha256.Sum256(canonicalConfig)
	consumer.ConfigSHA256 = append([]byte(nil), configHash[:]...)

	upsert.ConfigSha256 = consumer.ConfigSHA256

	eventID := uuid.NewSHA1(tenantMailConsumerEventNamespace, []byte("consumer:"+consumer.ID.String()+":1:upsert:"+command.ZoneID.String()))
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
		RoutingScope:         "zone:" + command.ZoneID.String(),
		JobTopic:             "mail.consumer.upsert",
		Payload:              payload,
		ActorUserID:          &actor,
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
	return s.repo.GetByID(ctx, consumer)
}

// GetConsumer lay thong tin consumer theo ID va Tenant command.
func (s *tenantConsumerServiceImpl) GetConsumer(ctx context.Context, command *mailEntity.TenantConsumer) (*mailEntity.TenantConsumer, error) {
	return s.repo.GetByID(ctx, command)
}

// ListConsumers danh sach consumer theo dieu kien loc va pagination.
func (s *tenantConsumerServiceImpl) ListConsumers(ctx context.Context, command *mailEntity.TenantConsumer) ([]*mailEntity.TenantConsumer, error) {
	return s.repo.List(ctx, command)
}

// UpdateConsumer cap nhat thong tin consumer voi optimistic version check va tao outbox event.
func (s *tenantConsumerServiceImpl) UpdateConsumer(ctx context.Context, command *mailEntity.TenantConsumer) (*mailEntity.TenantConsumer, error) {
	// [COMMENT]: Handler đã validate full-replacement command; service chỉ tạo version/event mới.
	mappingJSON, err := json.Marshal(command.Mapping)
	if err != nil {
		return nil, fmt.Errorf("mail tenant consumer service: marshal update mapping: %w", err)
	}

	now := time.Now().UTC()
	actor := command.ActorUserID

	consumer := &mailEntity.TenantConsumer{
		ActorUserID:           command.ActorUserID,
		TenantID:              command.TenantID,
		ZoneID:                command.ZoneID,
		ID:                    command.ID,
		WorkspaceID:           command.WorkspaceID,
		Name:                  command.Name,
		SourceType:            command.SourceType,
		BrokerResourceID:      command.BrokerResourceID,
		SourceConfigRef:       "vault://zones/" + command.ZoneID.String() + "/workspaces/" + command.WorkspaceID.String() + "/mail/brokers/" + command.BrokerResourceID.String(),
		Topic:                 command.Topic,
		ConsumerGroup:         command.ConsumerGroup,
		MappingJSON:           mappingJSON,
		TemplateID:            command.TemplateID,
		TemplateVersion:       command.TemplateVersion,
		SenderProfileID:       command.SenderProfileID,
		SenderVersion:         command.SenderVersion,
		DesiredState:          command.DesiredState,
		Parallelism:           command.Parallelism,
		ConfigVersion:         command.ExpectedConfigVersion + 1,
		ExpectedConfigVersion: command.ExpectedConfigVersion,
		UpdatedBy:             &actor,
		UpdatedAt:             now,
	}

	desiredState := mailproto.MailConsumerDesiredState_MAIL_CONSUMER_DESIRED_STATE_PAUSED
	if consumer.DesiredState == mailEntity.ConsumerEnabled {
		desiredState = mailproto.MailConsumerDesiredState_MAIL_CONSUMER_DESIRED_STATE_ENABLED
	}

	upsert := &mailproto.MailConsumerUpsertV1{
		ConsumerId:    consumer.ID[:],
		ConfigVersion: consumer.ConfigVersion,
		Kafka: &mailproto.KafkaSourceV1{
			BrokerResourceId: consumer.BrokerResourceID[:],
			SourceConfigRef:  consumer.SourceConfigRef,
			Topic:            consumer.Topic,
			ConsumerGroup:    consumer.ConsumerGroup,
		},
		Mapping: &mailproto.MailMessageMappingV1{
			ExternalMessageIdJsonPath: command.Mapping.ExternalMessageIDJSONPath,
			RecipientJsonPath:         command.Mapping.RecipientJSONPath,
			VariableJsonPaths:         command.Mapping.VariableJSONPaths,
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
		return nil, fmt.Errorf("mail tenant consumer service: marshal update config: %w", err)
	}

	configHash := sha256.Sum256(canonicalConfig)
	consumer.ConfigSHA256 = append([]byte(nil), configHash[:]...)
	upsert.ConfigSha256 = consumer.ConfigSHA256

	eventID := uuid.NewSHA1(tenantMailConsumerEventNamespace, fmt.Appendf(nil, "consumer:%s:%d:upsert:%s", consumer.ID, consumer.ConfigVersion, command.ZoneID))
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
		RoutingScope:         "zone:" + command.ZoneID.String(),
		JobTopic:             "mail.consumer.upsert",
		Payload:              payload,
		ActorUserID:          &actor,
		Status:               mailEntity.OutboxStatusPending,
		JobVersion:           1,
		ResourceID:           consumer.ID.String(),
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 60,
	}

	// [COMMENT]: Optimistic version check và outbox insert cùng nằm trong CTE của Tenant repository.
	if err = s.repo.Update(ctx, consumer, outbox); err != nil {
		return nil, err
	}
	return s.repo.GetByID(ctx, consumer)
}

// ChangeConsumerState thay doi trang thai pause/resume cua consumer.
func (s *tenantConsumerServiceImpl) ChangeConsumerState(ctx context.Context, command *mailEntity.TenantConsumer) (*mailEntity.TenantConsumer, error) {
	// [COMMENT]: Lay consumer hien tai tu repository
	consumer, err := s.repo.GetByID(ctx, command)
	if err != nil {
		return nil, err
	}

	// [COMMENT]: Kiem tra phien ban cau hinh hien tai so voi phien ban ky vọng
	if consumer.ConfigVersion != command.ExpectedConfigVersion {
		return nil, mailTaxonomy.ErrVersionConflict
	}

	// [COMMENT]: Decode JSON string payload thanh structure MessageMapping
	var mapping mailEntity.MessageMapping
	if err = json.Unmarshal(consumer.MappingJSON, &mapping); err != nil {
		return nil, err
	}

	// [COMMENT]: Goi UpdateConsumer de thuc hien update trang thai va phat outbox event
	return s.UpdateConsumer(ctx, &mailEntity.TenantConsumer{
		ActorUserID:           command.ActorUserID,
		TenantID:              command.TenantID,
		ZoneID:                command.ZoneID,
		WorkspaceID:           command.WorkspaceID,
		ID:                    consumer.ID,
		ExpectedConfigVersion: command.ExpectedConfigVersion,
		Name:                  consumer.Name,
		SourceType:            consumer.SourceType,
		BrokerResourceID:      consumer.BrokerResourceID,
		Topic:                 consumer.Topic,
		ConsumerGroup:         consumer.ConsumerGroup,
		Mapping:               mapping,
		TemplateID:            consumer.TemplateID,
		TemplateVersion:       consumer.TemplateVersion,
		SenderProfileID:       consumer.SenderProfileID,
		SenderVersion:         consumer.SenderVersion,
		DesiredState:          command.DesiredState,
		Parallelism:           consumer.Parallelism,
	})
}

// DeleteConsumer xoa consumer va phat tombstone delete event vao outbox.
func (s *tenantConsumerServiceImpl) DeleteConsumer(ctx context.Context, command *mailEntity.TenantConsumer) error {
	// [COMMENT]: Tenant delete tạo tombstone version kế tiếp trước khi CTE đổi desired_state sang deleting.
	updatedAt := time.Now().UTC()
	eventID := uuid.NewSHA1(tenantMailConsumerEventNamespace, fmt.Appendf(nil, "consumer:%s:%d:delete:%s", command.ID, command.ExpectedConfigVersion+1, command.ZoneID))
	tombstone := &mailproto.MailConsumerDeleteV1{
		Metadata: &mailproto.MailEventMetadataV1{
			EventId:          eventID[:],
			SchemaVersion:    1,
			OccurredAtUnixMs: updatedAt.UnixMilli(),
			Producer:         "controlplane-mail",
		},
		ConsumerId:          command.ID[:],
		ConfigVersion:       command.ExpectedConfigVersion + 1,
		DrainTimeoutSeconds: command.DrainTimeoutSeconds,
		Reason:              command.Reason,
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

	actor := command.ActorUserID
	outbox := &mailEntity.MailOutboxRecord{
		EventID:              eventID,
		RoutingScope:         "zone:" + command.ZoneID.String(),
		JobTopic:             "mail.consumer.delete",
		Payload:              payload,
		ActorUserID:          &actor,
		Status:               mailEntity.OutboxStatusPending,
		JobVersion:           1,
		ResourceID:           command.ID.String(),
		PayloadSchemaVersion: 1,
		TraceID:              traceID,
		Idle:                 command.DrainTimeoutSeconds + 30,
	}

	// [COMMENT]: Tenant repository chỉ phát tombstone nếu cùng CTE update đúng expected version.
	command.UpdatedAt = updatedAt
	return s.repo.Delete(ctx, command, outbox)
}
