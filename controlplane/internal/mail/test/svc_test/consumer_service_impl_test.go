package svc_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"
	"time"

	mailEntity "controlplane/internal/mail/domain/entity"
	mailSvcImpl "controlplane/internal/mail/service"
	mailproto "controlplane/internal/mail/transport/rpc/proto"
	"controlplane/internal/observability"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

// [COMMENT]: Mock repository capture hỗ trợ kiểm tra thao tác tạo/sửa/xóa Personal Consumer và lưu vệt outbox
type personalConsumerRepoCapture struct {
	created *mailEntity.CreatePersonalConsumer
	current *mailEntity.GetPersonalConsumer
	updated *mailEntity.UpdatePersonalConsumer
	outbox  *mailEntity.MailOutboxRecord
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
	updated, err := mailSvcImpl.NewPersonalConsumerService(repo, nil, observability.NewNoopWorkflowRecorder()).UpdateConsumer(context.Background(), command)
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
		if _, err := mailSvcImpl.NewPersonalConsumerService(repo, nil, observability.NewNoopWorkflowRecorder()).UpdateConsumer(context.Background(), command); err == nil {
			t.Fatal("AAD identity changed without a replacement encrypted envelope")
		}
	}
}

// [COMMENT]: Kiểm tra quá trình khởi tạo Personal Consumer tạo đúng 1 entity và 1 bản ghi outbox
func TestPersonalCreateUsesOneEntityAndOutbox(t *testing.T) {
	repo, command := &personalConsumerRepoCapture{}, validPersonalConsumer()
	// [COMMENT]: Thực thi khởi tạo consumer qua mail service implementation
	consumer, err := mailSvcImpl.NewPersonalConsumerService(repo, nil, observability.NewNoopWorkflowRecorder()).CreateConsumer(context.Background(), command)
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
	first, err := mailSvcImpl.NewPersonalConsumerService(firstRepo, nil, observability.NewNoopWorkflowRecorder()).CreateConsumer(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	retry := *command
	secondRepo := &personalConsumerRepoCapture{}
	second, err := mailSvcImpl.NewPersonalConsumerService(secondRepo, nil, observability.NewNoopWorkflowRecorder()).CreateConsumer(context.Background(), &retry)
	if err != nil {
		t.Fatal(err)
	}
	// [COMMENT]: Khẳng định hai consumer không bị trùng runtime ID
	if first.ID == second.ID {
		t.Fatal("recreated consumer reused a tombstoned runtime identity")
	}
}

func TestPersonalRuntimeWatchUsesShortLeaseAndRejectsPreviousEpoch(t *testing.T) {
	redisServer, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer redisServer.Close()
	redisClient := goredis.NewClient(&goredis.Options{Addr: redisServer.Addr()})
	defer redisClient.Close()

	current := &mailEntity.GetPersonalConsumer{
		ActorUserID:   uuid.New(),
		WorkspaceID:   uuid.New(),
		ZoneID:        uuid.New(),
		ID:            uuid.New(),
		ConfigVersion: 4,
		Parallelism:   3,
	}
	repo := &personalConsumerRepoCapture{current: current}
	service := mailSvcImpl.NewPersonalConsumerService(repo, redisClient, observability.NewNoopWorkflowRecorder())
	request := &mailEntity.WatchPersonalConsumerRuntime{
		ActorUserID: current.ActorUserID,
		WorkspaceID: current.WorkspaceID,
		ZoneID:      current.ZoneID,
		ID:          current.ID,
	}

	first, err := service.WatchConsumerRuntime(context.Background(), request)
	if err != nil {
		t.Fatalf("first WatchConsumerRuntime() error = %v", err)
	}
	if first.WatchTTLSeconds != 30 || first.RuntimeObserved || !strings.HasPrefix(first.WatchLeaseID, "4:") {
		t.Fatalf("unexpected first watch: %+v", first)
	}
	_, epoch, ok := strings.Cut(first.WatchLeaseID, ":")
	if !ok {
		t.Fatalf("invalid watch lease ID: %q", first.WatchLeaseID)
	}
	watchRequests, err := redisClient.XRangeN(
		context.Background(),
		"mail:runtime:watch-requests",
		"-",
		"+",
		1,
	).Result()
	if err != nil || len(watchRequests) != 1 {
		t.Fatalf("runtime watch was not enqueued for JO bridge: entries=%v err=%v", watchRequests, err)
	}
	rawWatch, ok := watchRequests[0].Values["payload"].(string)
	if !ok {
		t.Fatalf("runtime watch payload has unexpected type: %T", watchRequests[0].Values["payload"])
	}
	var watch mailproto.MailConsumerRuntimeWatchRequestedV1
	if err := proto.Unmarshal([]byte(rawWatch), &watch); err != nil {
		t.Fatalf("decode runtime watch request: %v", err)
	}
	if !bytes.Equal(watch.ZoneId, current.ZoneID[:]) ||
		!bytes.Equal(watch.ConsumerId, current.ID[:]) ||
		watch.ConfigVersion != current.ConfigVersion ||
		watch.RuntimeEpoch != epoch {
		t.Fatalf("unexpected runtime watch request: %+v", &watch)
	}
	snapshot, err := json.Marshal(map[string]any{
		"config_version":   4,
		"runtime_epoch":    epoch,
		"runtime_revision": 7,
		"state":            mailEntity.ConsumerRuntimeRunning,
		"active_instances": 2,
		"consumer_lag":     3,
		"error_code":       "",
		"error_message":    "",
		"observed_at":      time.Now().UTC(),
		"expires_at":       time.Now().UTC().Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshotKey := "mail:runtime:{" + current.ID.String() + "}:snapshot:personal"
	redisServer.Set(snapshotKey, string(snapshot))

	second, err := service.WatchConsumerRuntime(context.Background(), &mailEntity.WatchPersonalConsumerRuntime{
		ActorUserID: current.ActorUserID,
		WorkspaceID: current.WorkspaceID,
		ZoneID:      current.ZoneID,
		ID:          current.ID,
	})
	if err != nil || !second.RuntimeObserved || second.RuntimeRevision != 7 {
		t.Fatalf("matching epoch snapshot was not returned: result=%+v err=%v", second, err)
	}

	// [COMMENT]: Lease expiry tạo epoch mới; snapshot cũ dù còn TTL/future expires_at cũng phải là cache miss.
	redisServer.FastForward(31 * time.Second)
	third, err := service.WatchConsumerRuntime(context.Background(), &mailEntity.WatchPersonalConsumerRuntime{
		ActorUserID: current.ActorUserID,
		WorkspaceID: current.WorkspaceID,
		ZoneID:      current.ZoneID,
		ID:          current.ID,
	})
	if err != nil {
		t.Fatalf("third WatchConsumerRuntime() error = %v", err)
	}
	if third.RuntimeObserved || third.WatchLeaseID == first.WatchLeaseID {
		t.Fatalf("previous epoch leaked into new watch: first=%q third=%+v", first.WatchLeaseID, third)
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
		DrainTimeoutSeconds:   30,
	}
	// [COMMENT]: Thực thi yêu cầu xóa consumer qua service
	if err := mailSvcImpl.NewPersonalConsumerService(repo, nil, observability.NewNoopWorkflowRecorder()).DeleteConsumer(context.Background(), command); err != nil {
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
			if _, err := mailSvcImpl.NewPersonalConsumerService(repo, nil, observability.NewNoopWorkflowRecorder()).CreateConsumer(context.Background(), command); err != nil {
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
