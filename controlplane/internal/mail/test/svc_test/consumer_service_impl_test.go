package svc_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"testing"

	mailEntity "controlplane/internal/mail/domain/entity"
	mailSvcImpl "controlplane/internal/mail/service"
	mailproto "controlplane/internal/mail/transport/rpc/proto"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

// [COMMENT]: Mock repository capture hỗ trợ kiểm tra thao tác tạo/sửa/xóa Personal Consumer và lưu vệt outbox
type personalConsumerRepoCapture struct {
	created *mailEntity.PersonalConsumer
	outbox  *mailEntity.MailOutboxRecord
}

// [COMMENT]: Giả lập lưu PersonalConsumer entity và record outbox tương ứng khi tạo mới
func (r *personalConsumerRepoCapture) Create(_ context.Context, entity *mailEntity.PersonalConsumer, outbox *mailEntity.MailOutboxRecord) error {
	r.created, r.outbox = entity, outbox
	return nil
}

// [COMMENT]: Trả về entity đã lưu để phục vụ kiểm tra truy vấn theo ID
func (r *personalConsumerRepoCapture) GetByID(_ context.Context, _ *mailEntity.PersonalConsumer) (*mailEntity.PersonalConsumer, error) {
	return r.created, nil
}

// [COMMENT]: Trả về danh sách trống cho hàm List trong mock repo
func (r *personalConsumerRepoCapture) List(_ context.Context, _ *mailEntity.PersonalConsumer) ([]*mailEntity.PersonalConsumer, error) {
	return nil, nil
}

// [COMMENT]: Cập nhật thông tin PersonalConsumer entity và ghi nhận record outbox mới
func (r *personalConsumerRepoCapture) Update(_ context.Context, entity *mailEntity.PersonalConsumer, outbox *mailEntity.MailOutboxRecord) error {
	r.created, r.outbox = entity, outbox
	return nil
}

// [COMMENT]: Ghi nhận record outbox đánh dấu thao tác xóa Consumer
func (r *personalConsumerRepoCapture) Delete(_ context.Context, _ *mailEntity.PersonalConsumer, outbox *mailEntity.MailOutboxRecord) error {
	r.outbox = outbox
	return nil
}

// [COMMENT]: Helper khởi tạo fixture PersonalConsumer đã qua bước normalize và validate
func validPersonalConsumer() *mailEntity.PersonalConsumer {
	return &mailEntity.PersonalConsumer{
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

// [COMMENT]: Kiểm tra việc giữ nguyên ciphertext cấu hình khi bản ghi update không truyền lại envelope mới
func TestPersonalUpdateKeepsEncryptedSourceWhenAPILeavesItEmpty(t *testing.T) {
	current := validPersonalConsumer()
	current.ID = uuid.New()
	current.ConfigVersion = 4
	current.NextConfigVersion = 5
	current.SourceConfigEnvelope = []byte{7, 8, 9}
	repo := &personalConsumerRepoCapture{created: current}

	command := *current
	command.ExpectedConfigVersion = 4
	command.ConfigVersion = 0
	command.SourceConfigEnvelope = nil
	command.DesiredState = mailEntity.ConsumerEnabled

	// [COMMENT]: Gọi service thực thi lệnh cập nhật consumer
	updated, err := mailSvcImpl.NewPersonalConsumerService(repo).UpdateConsumer(context.Background(), &command)
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
	current := validPersonalConsumer()
	current.ID = uuid.New()
	current.ConfigVersion = 4
	current.NextConfigVersion = 5
	current.SourceConfigEnvelope = []byte{7, 8, 9}
	repo := &personalConsumerRepoCapture{created: current}

	for _, mutate := range []func(*mailEntity.PersonalConsumer){
		func(command *mailEntity.PersonalConsumer) { command.SourceType = mailEntity.RedisStream },
		func(command *mailEntity.PersonalConsumer) { command.BrokerResourceID = uuid.New() },
	} {
		command := *current
		command.ExpectedConfigVersion = current.ConfigVersion
		command.SourceConfigEnvelope = nil
		command.DesiredState = mailEntity.ConsumerPaused
		mutate(&command)
		// [COMMENT]: Kiểm tra service trả về lỗi do thay đổi AAD identity mà không có ciphertext mới
		if _, err := mailSvcImpl.NewPersonalConsumerService(repo).UpdateConsumer(context.Background(), &command); err == nil {
			t.Fatal("AAD identity changed without a replacement encrypted envelope")
		}
	}
}

// [COMMENT]: Kiểm tra quá trình khởi tạo Personal Consumer tạo đúng 1 entity và 1 bản ghi outbox
func TestPersonalCreateUsesOneEntityAndOutbox(t *testing.T) {
	repo, command := &personalConsumerRepoCapture{}, validPersonalConsumer()
	// [COMMENT]: Thực thi khởi tạo consumer qua mail service implementation
	consumer, err := mailSvcImpl.NewPersonalConsumerService(repo).CreateConsumer(context.Background(), command)
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
	first, err := mailSvcImpl.NewPersonalConsumerService(firstRepo).CreateConsumer(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	retry := *command
	secondRepo := &personalConsumerRepoCapture{}
	second, err := mailSvcImpl.NewPersonalConsumerService(secondRepo).CreateConsumer(context.Background(), &retry)
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
	current := validPersonalConsumer()
	current.ID = uuid.New()
	current.ConfigVersion = 4
	current.NextConfigVersion = 9
	repo := &personalConsumerRepoCapture{created: current}
	command := &mailEntity.PersonalConsumer{
		ActorUserID:           current.ActorUserID,
		WorkspaceID:           current.WorkspaceID,
		ZoneID:                current.ZoneID,
		ID:                    current.ID,
		ExpectedConfigVersion: 4,
		DrainTimeoutSeconds:   30,
	}
	// [COMMENT]: Thực thi yêu cầu xóa consumer qua service
	if err := mailSvcImpl.NewPersonalConsumerService(repo).DeleteConsumer(context.Background(), command); err != nil {
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
			if _, err := mailSvcImpl.NewPersonalConsumerService(repo).CreateConsumer(context.Background(), command); err != nil {
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
