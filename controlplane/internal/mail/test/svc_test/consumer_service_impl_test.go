package svc_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"

	mailEntity "controlplane/internal/mail/domain/entity"
	mailSvcImpl "controlplane/internal/mail/service"
	mailTaxonomy "controlplane/internal/mail/taxonomy"
	mailproto "controlplane/internal/mail/transport/proto"
	"controlplane/internal/observability"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

// [COMMENT]: Mock repository capture hỗ trợ kiểm tra thao tác tạo/sửa/xóa Personal Consumer và lưu vệt outbox
type personalConsumerRepoCapture struct {
	drainTarget mailEntity.PersonalConsumerDrainTarget
	created     *mailEntity.CreatePersonalConsumer
	current     *mailEntity.GetPersonalConsumer
	updated     *mailEntity.UpdatePersonalConsumer
	outbox      *mailEntity.MailOutboxRecord
	updateErr   error
}

type tenantConsumerRepoCapture struct {
	drainTarget mailEntity.TenantConsumerDrainTarget
	updated     *mailEntity.UpdateTenantConsumer
	outbox      *mailEntity.MailOutboxRecord
	current     *mailEntity.GetTenantConsumer
	updateErr   error
}

func (r *personalConsumerRepoCapture) LoadDrainTarget(context.Context, mailEntity.PersonalConsumerDrainCommand) (mailEntity.PersonalConsumerDrainTarget, error) {
	return r.drainTarget, nil
}
func (r *personalConsumerRepoCapture) RequestDrain(_ context.Context, _ mailEntity.PersonalConsumerDrainCommand, _ uint32, outbox mailEntity.MailOutboxRecord) error {
	r.outbox = &outbox
	return r.updateErr
}
func (r *tenantConsumerRepoCapture) LoadDrainTarget(context.Context, mailEntity.TenantConsumerDrainCommand) (mailEntity.TenantConsumerDrainTarget, error) {
	return r.drainTarget, nil
}
func (r *tenantConsumerRepoCapture) RequestDrain(_ context.Context, _ mailEntity.TenantConsumerDrainCommand, _ uint32, outbox mailEntity.MailOutboxRecord) error {
	r.outbox = &outbox
	return r.updateErr
}

func TestConsumerDrainUsesCurrentVersionAndScopedOutbox(t *testing.T) {
	for _, scope := range []string{"personal", "tenant"} {
		t.Run(scope, func(t *testing.T) {
			id, actor, workspace, zone := uuid.New(), uuid.New(), uuid.New(), uuid.New()
			var operation uuid.UUID
			var err error
			var outbox *mailEntity.MailOutboxRecord
			if scope == "personal" {
				repo := &personalConsumerRepoCapture{drainTarget: mailEntity.PersonalConsumerDrainTarget{ConfigVersion: 7, Parallelism: 4, State: mailEntity.ConsumerEnabled}}
				service := mailSvcImpl.NewPersonalConsumerService(repo, observability.NewNoopWorkflowRecorder())
				operation, err = service.Drain(context.Background(), mailEntity.PersonalConsumerDrainCommand{ActorUserID: actor, WorkspaceID: workspace, ZoneID: zone, ConsumerID: id, ExpectedConfigVersion: 7, TimeoutSeconds: 30})
				outbox = repo.outbox
			} else {
				repo := &tenantConsumerRepoCapture{drainTarget: mailEntity.TenantConsumerDrainTarget{ConfigVersion: 7, Parallelism: 4, State: mailEntity.ConsumerPaused}}
				service := mailSvcImpl.NewTenantConsumerService(repo, observability.NewNoopWorkflowRecorder())
				operation, err = service.Drain(context.Background(), mailEntity.TenantConsumerDrainCommand{ActorUserID: actor, WorkspaceID: workspace, ZoneID: zone, TenantID: uuid.New(), ConsumerID: id, ExpectedConfigVersion: 7, TimeoutSeconds: 30})
				outbox = repo.outbox
			}
			if err != nil || operation == uuid.Nil || outbox == nil {
				t.Fatalf("drain: operation=%v err=%v", operation, err)
			}
			if outbox.EventID != operation || outbox.JobTopic != "mail.consumer.drain" || outbox.ZoneID != zone || outbox.ResourceID != id.String() || outbox.ActorUserID != actor {
				t.Fatalf("wrong outbox: %#v", outbox)
			}
			var command mailproto.MailConsumerDrainV1
			if err = proto.Unmarshal(outbox.Payload, &command); err != nil {
				t.Fatal(err)
			}
			if command.SchemaVersion != 1 || command.ConfigVersion != 7 || command.Parallelism != 4 || command.TimeoutSeconds != 30 || !bytes.Equal(command.ConsumerId, id[:]) {
				t.Fatalf("wrong command: %v", &command)
			}
		})
	}
}

func TestConsumerDrainRejectsStaleVersionAndBusyState(t *testing.T) {
	for _, state := range []mailEntity.ConsumerDesiredState{mailEntity.ConsumerEnabled, mailEntity.ConsumerDraining, mailEntity.ConsumerDrained, mailEntity.ConsumerDeleting} {
		for _, scope := range []string{"personal", "tenant"} {
			expected := uint64(7)
			want := mailTaxonomy.ErrOperationInProgress
			if state == mailEntity.ConsumerEnabled {
				expected = 6
				want = mailTaxonomy.ErrVersionConflict
			}
			var err error
			var outbox *mailEntity.MailOutboxRecord
			if scope == "personal" {
				repo := &personalConsumerRepoCapture{drainTarget: mailEntity.PersonalConsumerDrainTarget{ConfigVersion: 7, Parallelism: 1, State: state}}
				_, err = mailSvcImpl.NewPersonalConsumerService(repo, observability.NewNoopWorkflowRecorder()).Drain(context.Background(), mailEntity.PersonalConsumerDrainCommand{ExpectedConfigVersion: expected})
				outbox = repo.outbox
			} else {
				repo := &tenantConsumerRepoCapture{drainTarget: mailEntity.TenantConsumerDrainTarget{ConfigVersion: 7, Parallelism: 1, State: state}}
				_, err = mailSvcImpl.NewTenantConsumerService(repo, observability.NewNoopWorkflowRecorder()).Drain(context.Background(), mailEntity.TenantConsumerDrainCommand{ExpectedConfigVersion: expected})
				outbox = repo.outbox
			}
			if !errors.Is(err, want) || outbox != nil {
				t.Fatalf("%s/%s: err=%v outbox=%v", scope, state, err, outbox)
			}
		}
	}
}

func (r *tenantConsumerRepoCapture) Create(context.Context, *mailEntity.CreateTenantConsumer, *mailEntity.MailOutboxRecord) error {
	return nil
}
func (r *tenantConsumerRepoCapture) GetByID(context.Context, *mailEntity.GetTenantConsumer) (*mailEntity.GetTenantConsumer, error) {
	return r.current, nil
}
func (r *tenantConsumerRepoCapture) List(context.Context, *mailEntity.ListTenantConsumer) ([]*mailEntity.ListTenantConsumer, error) {
	return nil, nil
}
func (r *tenantConsumerRepoCapture) Update(_ context.Context, update *mailEntity.UpdateTenantConsumer, outbox *mailEntity.MailOutboxRecord) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	r.updated, r.outbox = update, outbox
	return nil
}
func (r *tenantConsumerRepoCapture) Delete(context.Context, *mailEntity.DeleteTenantConsumer, *mailEntity.MailOutboxRecord) error {
	return nil
}

func TestPersonalResumeFailsWhenCommercialAdmissionIsUnavailable(t *testing.T) {
	repo := &personalConsumerRepoCapture{
		current: &mailEntity.GetPersonalConsumer{
			ID:                   uuid.New(),
			ConfigVersion:        1,
			SourceConfigEnvelope: []byte{1, 2, 3},
		},
		updateErr: mailTaxonomy.ErrCommercialAdmissionUnavailable,
	}
	service := mailSvcImpl.NewPersonalConsumerService(repo, observability.NewNoopWorkflowRecorder())
	_, err := service.ChangeConsumerState(context.Background(), &mailEntity.ChangePersonalConsumerState{
		ActorUserID:           uuid.New(),
		ExpectedConfigVersion: 1,
		DesiredState:          mailEntity.ConsumerEnabled,
	})
	if !errors.Is(err, mailTaxonomy.ErrCommercialAdmissionUnavailable) {
		t.Fatalf("expected commercial admission failure, got %v", err)
	}
}

func TestTenantResumeFailsWhenTenantCommercialAdmissionIsUnavailable(t *testing.T) {
	repo := &tenantConsumerRepoCapture{
		current: &mailEntity.GetTenantConsumer{
			ID:                   uuid.New(),
			ConfigVersion:        1,
			SourceConfigEnvelope: []byte{1, 2, 3},
		},
		updateErr: mailTaxonomy.ErrCommercialAdmissionUnavailable,
	}
	service := mailSvcImpl.NewTenantConsumerService(repo, observability.NewNoopWorkflowRecorder())
	_, err := service.ChangeConsumerState(context.Background(), &mailEntity.ChangeTenantConsumerState{
		TenantID:              uuid.New(),
		ExpectedConfigVersion: 1,
		DesiredState:          mailEntity.ConsumerEnabled,
	})
	if !errors.Is(err, mailTaxonomy.ErrCommercialAdmissionUnavailable) {
		t.Fatalf("expected Tenant commercial admission failure, got %v", err)
	}
}

// [COMMENT]: Giả lập lưu PersonalConsumer entity và record outbox tương ứng khi tạo mới
func (r *personalConsumerRepoCapture) Create(_ context.Context, entity *mailEntity.CreatePersonalConsumer, outbox *mailEntity.MailOutboxRecord) error {
	r.created, r.outbox = entity, outbox
	return nil
}

// [COMMENT]: Trả về entity đã lưu để phục vụ kiểm tra truy vấn theo ID
func (r *personalConsumerRepoCapture) GetByID(_ context.Context, _ *mailEntity.GetPersonalConsumer) (*mailEntity.GetPersonalConsumer, error) {
	return r.current, nil
}

// [COMMENT]: Trả về danh sách trống cho hàm List trong mock repo
func (r *personalConsumerRepoCapture) List(_ context.Context, _ *mailEntity.ListPersonalConsumer) ([]*mailEntity.ListPersonalConsumer, error) {
	return nil, nil
}

// [COMMENT]: Cập nhật thông tin PersonalConsumer entity và ghi nhận record outbox mới
func (r *personalConsumerRepoCapture) Update(_ context.Context, entity *mailEntity.UpdatePersonalConsumer, outbox *mailEntity.MailOutboxRecord) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	r.updated, r.outbox = entity, outbox
	return nil
}

// [COMMENT]: Ghi nhận record outbox đánh dấu thao tác xóa Consumer
func (r *personalConsumerRepoCapture) Delete(_ context.Context, _ *mailEntity.DeletePersonalConsumer, outbox *mailEntity.MailOutboxRecord) error {
	r.outbox = outbox
	return nil
}

// [COMMENT]: Helper khởi tạo fixture PersonalConsumer đã qua bước normalize và validate
func validPersonalConsumer() *mailEntity.CreatePersonalConsumer {
	return &mailEntity.CreatePersonalConsumer{
		ActorUserID:          uuid.New(),
		WorkspaceID:          uuid.New(),
		ZoneID:               uuid.New(),
		Code:                 "orders",
		Name:                 "orders",
		SourceType:           mailEntity.Kafka,
		BrokerResourceID:     uuid.New(),
		SourceConfigEnvelope: []byte{1, 2, 3},
		Topic:                "orders.created",
		ConsumerGroup:        "mail-orders",
		TemplateID:           "template-1",
		TemplateVersion:      2,
		SenderProfileID:      "sender-1",
		SenderVersion:        1,
		Parallelism:          3,
	}
}

// [COMMENT]: Update fixture được khai báo phẳng, không nhúng Create entity vào command.
func validPersonalConsumerUpdate(current *mailEntity.GetPersonalConsumer) *mailEntity.UpdatePersonalConsumer {
	return &mailEntity.UpdatePersonalConsumer{
		ActorUserID: current.ActorUserID, WorkspaceID: current.WorkspaceID, ZoneID: current.ZoneID,
		ID: current.ID, ExpectedConfigVersion: current.ConfigVersion,
		Name: current.Name, SourceType: current.SourceType, BrokerResourceID: current.BrokerResourceID,
		Topic: current.Topic, ConsumerGroup: current.ConsumerGroup,
		TemplateID: current.TemplateID, TemplateVersion: current.TemplateVersion,
		SenderProfileID: current.SenderProfileID, SenderVersion: current.SenderVersion,
		DesiredState: mailEntity.ConsumerEnabled, Parallelism: current.Parallelism,
	}
}

// [COMMENT]: Kiểm tra việc giữ nguyên ciphertext cấu hình khi bản ghi update không truyền lại envelope mới
func TestPersonalUpdateKeepsEncryptedSourceWhenAPILeavesItEmpty(t *testing.T) {
	fixture := validPersonalConsumer()
	current := &mailEntity.GetPersonalConsumer{
		ActorUserID: fixture.ActorUserID, WorkspaceID: fixture.WorkspaceID, ZoneID: fixture.ZoneID,
		ID: uuid.New(), Code: fixture.Code, Name: fixture.Name, SourceType: fixture.SourceType,
		BrokerResourceID: fixture.BrokerResourceID, SourceConfigEnvelope: []byte{7, 8, 9},
		Topic: fixture.Topic, ConsumerGroup: fixture.ConsumerGroup, TemplateID: fixture.TemplateID,
		TemplateVersion: fixture.TemplateVersion, SenderProfileID: fixture.SenderProfileID,
		SenderVersion: fixture.SenderVersion, DesiredState: mailEntity.ConsumerPaused,
		Parallelism: fixture.Parallelism, ConfigVersion: 4, NextConfigVersion: 5,
	}
	repo := &personalConsumerRepoCapture{current: current}
	command := validPersonalConsumerUpdate(current)

	// [COMMENT]: Gọi service thực thi lệnh cập nhật consumer
	updated, err := mailSvcImpl.NewPersonalConsumerService(repo, observability.NewNoopWorkflowRecorder()).UpdateConsumer(context.Background(), command)
	if err != nil {
		t.Fatalf("UpdateConsumer() error = %v", err)
	}

	// [COMMENT]: Đảm bảo cấu hình mã hóa trước đó được giữ nguyên
	if !bytes.Equal(updated.SourceConfigEnvelope, []byte{7, 8, 9}) {
		t.Fatalf("encrypted source was not retained: %v", updated.SourceConfigEnvelope)
	}

	// [COMMENT]: Outbox phải chứa đúng ciphertext đã persist để hash/projection không lệch database
	var event mailproto.MailConsumerUpsertV1
	if err = proto.Unmarshal(repo.outbox.Payload, &event); err != nil || event.Stream == nil {
		t.Fatalf("invalid payload: %v", err)
	}
	var kafka mailproto.KafkaStreamPayloadV1
	if err = proto.Unmarshal(event.Stream.Payload, &kafka); err != nil {
		t.Fatalf("invalid Kafka stream payload: %v", err)
	}
	if event.Stream.StreamType != mailproto.MailStreamType_MAIL_STREAM_TYPE_KAFKA ||
		!bytes.Equal(kafka.SourceConfigEnvelope, updated.SourceConfigEnvelope) {
		t.Fatalf("outbox stream differs from aggregate: %+v", event.Stream)
	}
	if updated.ConfigVersion != 5 || event.ConfigVersion != 5 || updated.OperationID != repo.outbox.EventID {
		// [COMMENT]: Truyền con trỏ &event vào format string để tránh copy lock (sync.Mutex trong proto message)
		t.Fatalf("update did not allocate candidate operation: entity=%+v event=%+v", updated, &event)
	}
}

// [COMMENT]: Kiểm tra yêu cầu bắt buộc phải có envelope mới khi thông tin danh tính AAD thay đổi
func TestPersonalUpdateRequiresFreshEnvelopeWhenAADIdentityChanges(t *testing.T) {
	fixture := validPersonalConsumer()
	current := &mailEntity.GetPersonalConsumer{
		ActorUserID: fixture.ActorUserID, WorkspaceID: fixture.WorkspaceID, ZoneID: fixture.ZoneID,
		ID: uuid.New(), SourceType: fixture.SourceType, BrokerResourceID: fixture.BrokerResourceID,
		SourceConfigEnvelope: []byte{7, 8, 9}, ConfigVersion: 4, NextConfigVersion: 5,
		Name: fixture.Name, Topic: fixture.Topic, ConsumerGroup: fixture.ConsumerGroup,
		TemplateID: fixture.TemplateID, TemplateVersion: fixture.TemplateVersion,
		SenderProfileID: fixture.SenderProfileID, SenderVersion: fixture.SenderVersion,
		Parallelism: fixture.Parallelism,
	}
	repo := &personalConsumerRepoCapture{current: current}

	for _, mutate := range []func(*mailEntity.UpdatePersonalConsumer){
		func(command *mailEntity.UpdatePersonalConsumer) { command.SourceType = mailEntity.RedisStream },
		func(command *mailEntity.UpdatePersonalConsumer) { command.BrokerResourceID = uuid.New() },
	} {
		command := validPersonalConsumerUpdate(current)
		command.DesiredState = mailEntity.ConsumerPaused
		mutate(command)
		// [COMMENT]: Kiểm tra service trả về lỗi do thay đổi AAD identity mà không có ciphertext mới
		if _, err := mailSvcImpl.NewPersonalConsumerService(repo, observability.NewNoopWorkflowRecorder()).UpdateConsumer(context.Background(), command); err == nil {
			t.Fatal("AAD identity changed without a replacement encrypted envelope")
		}
	}
}

// [COMMENT]: Kiểm tra quá trình khởi tạo Personal Consumer tạo đúng 1 entity và 1 bản ghi outbox
func TestPersonalCreateUsesOneEntityAndOutbox(t *testing.T) {
	repo, command := &personalConsumerRepoCapture{}, validPersonalConsumer()
	// [COMMENT]: Thực thi khởi tạo consumer qua mail service implementation
	consumer, err := mailSvcImpl.NewPersonalConsumerService(repo, observability.NewNoopWorkflowRecorder()).CreateConsumer(context.Background(), command)
	if err != nil {
		t.Fatalf("CreateConsumer() error = %v", err)
	}
	if consumer.Name != "orders" || consumer.ConfigVersion != 1 || len(consumer.ConfigSHA256) != sha256.Size {
		t.Fatalf("unexpected aggregate: %+v", consumer)
	}
	if repo.outbox == nil || repo.outbox.ZoneID != command.ZoneID {
		t.Fatalf("unexpected outbox: %+v", repo.outbox)
	}
	var event mailproto.MailConsumerUpsertV1
	if err = proto.Unmarshal(repo.outbox.Payload, &event); err != nil || event.Stream == nil {
		t.Fatalf("invalid payload: %v", err)
	}
}

// [COMMENT]: Kiểm tra mỗi consumer được khởi tạo lại đều nhận được runtime identity độc lập
func TestPersonalCreateUsesFreshRuntimeIdentity(t *testing.T) {
	command := validPersonalConsumer()
	firstRepo := &personalConsumerRepoCapture{}
	first, err := mailSvcImpl.NewPersonalConsumerService(firstRepo, observability.NewNoopWorkflowRecorder()).CreateConsumer(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	retry := *command
	secondRepo := &personalConsumerRepoCapture{}
	second, err := mailSvcImpl.NewPersonalConsumerService(secondRepo, observability.NewNoopWorkflowRecorder()).CreateConsumer(context.Background(), &retry)
	if err != nil {
		t.Fatal(err)
	}
	// [COMMENT]: Khẳng định hai consumer không bị trùng runtime ID
	if first.ID == second.ID {
		t.Fatal("recreated consumer reused a tombstoned runtime identity")
	}
}

// [COMMENT]: Kiểm tra việc xóa Consumer sử dụng monotonic fence để tránh race condition
func TestPersonalDeleteUsesNextAllocatorAsTombstoneFence(t *testing.T) {
	fixture := validPersonalConsumer()
	current := &mailEntity.GetPersonalConsumer{
		ActorUserID: fixture.ActorUserID, WorkspaceID: fixture.WorkspaceID, ZoneID: fixture.ZoneID,
		ID: uuid.New(), ConfigVersion: 4, NextConfigVersion: 9,
	}
	repo := &personalConsumerRepoCapture{current: current}
	command := &mailEntity.DeletePersonalConsumer{
		ActorUserID:           current.ActorUserID,
		WorkspaceID:           current.WorkspaceID,
		ZoneID:                current.ZoneID,
		ID:                    current.ID,
		ExpectedConfigVersion: 4,
	}
	// [COMMENT]: Thực thi yêu cầu xóa consumer qua service
	if err := mailSvcImpl.NewPersonalConsumerService(repo, observability.NewNoopWorkflowRecorder()).DeleteConsumer(context.Background(), command); err != nil {
		t.Fatalf("DeleteConsumer() error = %v", err)
	}
	var event mailproto.MailConsumerDeleteV1
	if err := proto.Unmarshal(repo.outbox.Payload, &event); err != nil {
		t.Fatalf("invalid delete payload: %v", err)
	}
	if event.ConfigVersion != 9 || command.OperationID != repo.outbox.EventID {
		// [COMMENT]: Truyền con trỏ &event vào format string để tránh copylocks cảnh báo từ linter
		t.Fatalf("delete did not use monotonic fence: event=%+v command=%+v", &event, command)
	}
}

// [COMMENT]: Kiểm tra quá trình encode các loại stream suite hỗ trợ trong Mail Consumer
func TestPersonalCreateEncodesTheSelectedStreamSuite(t *testing.T) {
	tests := []struct {
		name       string
		sourceType mailEntity.SourceType
		wireType   mailproto.MailStreamType
	}{
		{name: "kafka", sourceType: mailEntity.Kafka, wireType: mailproto.MailStreamType_MAIL_STREAM_TYPE_KAFKA},
		{name: "redis stream", sourceType: mailEntity.RedisStream, wireType: mailproto.MailStreamType_MAIL_STREAM_TYPE_REDIS_STREAM},
		{name: "nats jetstream", sourceType: mailEntity.NATSJetStream, wireType: mailproto.MailStreamType_MAIL_STREAM_TYPE_NATS_JETSTREAM},
		{name: "rabbitmq", sourceType: mailEntity.RabbitMQ, wireType: mailproto.MailStreamType_MAIL_STREAM_TYPE_RABBITMQ},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := validPersonalConsumer()
			command.SourceType = test.sourceType
			repo := &personalConsumerRepoCapture{}
			// [COMMENT]: Tạo consumer với từng loại Stream Source cụ thể
			if _, err := mailSvcImpl.NewPersonalConsumerService(repo, observability.NewNoopWorkflowRecorder()).CreateConsumer(context.Background(), command); err != nil {
				t.Fatalf("CreateConsumer() error = %v", err)
			}

			var event mailproto.MailConsumerUpsertV1
			if err := proto.Unmarshal(repo.outbox.Payload, &event); err != nil || event.Stream == nil {
				t.Fatalf("invalid event: %v", err)
			}
			if event.Stream.StreamType != test.wireType {
				t.Fatalf("stream type = %v, want %v", event.Stream.StreamType, test.wireType)
			}

			// [COMMENT]: Decode đúng message suite để bắt regression producer gắn discriminator mới nhưng vẫn bọc Kafka bytes
			switch test.sourceType {
			case mailEntity.Kafka:
				var payload mailproto.KafkaStreamPayloadV1
				if err := proto.Unmarshal(event.Stream.Payload, &payload); err != nil || payload.Topic != command.Topic {
					// [COMMENT]: Truyền &payload dạng con trỏ để tránh copy sync.Mutex trong protobuf struct
					t.Fatalf("invalid Kafka payload: %+v, %v", &payload, err)
				}
			case mailEntity.RedisStream:
				var payload mailproto.RedisStreamPayloadV1
				if err := proto.Unmarshal(event.Stream.Payload, &payload); err != nil || payload.StreamKey != command.Topic {
					// [COMMENT]: Truyền &payload dạng con trỏ để tránh copy sync.Mutex trong protobuf struct
					t.Fatalf("invalid Redis Stream payload: %+v, %v", &payload, err)
				}
			case mailEntity.NATSJetStream:
				var payload mailproto.NatsJetStreamPayloadV1
				if err := proto.Unmarshal(event.Stream.Payload, &payload); err != nil || payload.StreamName != command.Topic {
					// [COMMENT]: Truyền &payload dạng con trỏ để tránh copy sync.Mutex trong protobuf struct
					t.Fatalf("invalid JetStream payload: %+v, %v", &payload, err)
				}
			case mailEntity.RabbitMQ:
				var payload mailproto.RabbitMqPayloadV1
				if err := proto.Unmarshal(event.Stream.Payload, &payload); err != nil || payload.QueueName != command.Topic {
					// [COMMENT]: Truyền &payload dạng con trỏ để tránh copy sync.Mutex trong protobuf struct
					t.Fatalf("invalid RabbitMQ payload: %+v, %v", &payload, err)
				}
			}
		})
	}
}
