package mailSvcImpl

import (
	"bytes"
	"context"
	"crypto/sha256"
	"testing"

	mailEntity "controlplane/internal/mail/domain/entity"
	mailproto "controlplane/internal/mail/transport/rpc/proto"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

type personalConsumerRepoCapture struct {
	created *mailEntity.PersonalConsumer
	outbox  *mailEntity.MailOutboxRecord
}

func (r *personalConsumerRepoCapture) Create(_ context.Context, entity *mailEntity.PersonalConsumer, outbox *mailEntity.MailOutboxRecord) error {
	r.created, r.outbox = entity, outbox
	return nil
}
func (r *personalConsumerRepoCapture) GetByID(_ context.Context, _ *mailEntity.PersonalConsumer) (*mailEntity.PersonalConsumer, error) {
	return r.created, nil
}
func (r *personalConsumerRepoCapture) List(_ context.Context, _ *mailEntity.PersonalConsumer) ([]*mailEntity.PersonalConsumer, error) {
	return nil, nil
}
func (r *personalConsumerRepoCapture) Update(_ context.Context, entity *mailEntity.PersonalConsumer, outbox *mailEntity.MailOutboxRecord) error {
	r.created, r.outbox = entity, outbox
	return nil
}
func (r *personalConsumerRepoCapture) Delete(_ context.Context, _ *mailEntity.PersonalConsumer, outbox *mailEntity.MailOutboxRecord) error {
	r.outbox = outbox
	return nil
}

func validPersonalConsumer() *mailEntity.PersonalConsumer {
	// [COMMENT]: Fixture mô phỏng entity đã được HTTP handler normalize và validate.
	return &mailEntity.PersonalConsumer{ActorUserID: uuid.New(), WorkspaceID: uuid.New(), ZoneID: uuid.New(), Code: "orders", Name: "orders", SourceType: mailEntity.Kafka, BrokerResourceID: uuid.New(), SourceConfigEnvelope: []byte{1, 2, 3}, Topic: "orders.created", ConsumerGroup: "mail-orders", TemplateID: "template-1", TemplateVersion: 2, SenderProfileID: "sender-1", SenderVersion: 1, Parallelism: 3}
}

func TestPersonalUpdateKeepsEncryptedSourceWhenAPILeavesItEmpty(t *testing.T) {
	current := validPersonalConsumer()
	current.ID = uuid.New()
	current.ConfigVersion = 4
	current.SourceConfigEnvelope = []byte{7, 8, 9}
	repo := &personalConsumerRepoCapture{created: current}

	command := *current
	command.ExpectedConfigVersion = 4
	command.ConfigVersion = 0
	command.SourceConfigEnvelope = nil
	command.DesiredState = mailEntity.ConsumerEnabled
	updated, err := NewPersonalConsumerService(repo).UpdateConsumer(context.Background(), &command)
	if err != nil {
		t.Fatalf("UpdateConsumer() error = %v", err)
	}
	if !bytes.Equal(updated.SourceConfigEnvelope, []byte{7, 8, 9}) {
		t.Fatalf("encrypted source was not retained: %v", updated.SourceConfigEnvelope)
	}

	// [COMMENT]: Outbox phải chứa đúng ciphertext đã persist để hash/projection không lệch database.
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
}

func TestPersonalUpdateRequiresFreshEnvelopeWhenAADIdentityChanges(t *testing.T) {
	current := validPersonalConsumer()
	current.ID = uuid.New()
	current.ConfigVersion = 4
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
		if _, err := NewPersonalConsumerService(repo).UpdateConsumer(context.Background(), &command); err == nil {
			t.Fatal("AAD identity changed without a replacement encrypted envelope")
		}
	}
}

func TestPersonalCreateUsesOneEntityAndOutbox(t *testing.T) {
	repo, command := &personalConsumerRepoCapture{}, validPersonalConsumer()
	consumer, err := NewPersonalConsumerService(repo).CreateConsumer(context.Background(), command)
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

func TestPersonalCreateUsesFreshRuntimeIdentity(t *testing.T) {
	command := validPersonalConsumer()
	firstRepo := &personalConsumerRepoCapture{}
	first, err := NewPersonalConsumerService(firstRepo).CreateConsumer(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	retry := *command
	secondRepo := &personalConsumerRepoCapture{}
	second, err := NewPersonalConsumerService(secondRepo).CreateConsumer(context.Background(), &retry)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatal("recreated consumer reused a tombstoned runtime identity")
	}
}

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
			if _, err := NewPersonalConsumerService(repo).CreateConsumer(context.Background(), command); err != nil {
				t.Fatalf("CreateConsumer() error = %v", err)
			}

			var event mailproto.MailConsumerUpsertV1
			if err := proto.Unmarshal(repo.outbox.Payload, &event); err != nil || event.Stream == nil {
				t.Fatalf("invalid event: %v", err)
			}
			if event.Stream.StreamType != test.wireType {
				t.Fatalf("stream type = %v, want %v", event.Stream.StreamType, test.wireType)
			}

			// [COMMENT]: Decode đúng message suite để bắt regression producer gắn discriminator mới nhưng vẫn bọc Kafka bytes.
			switch test.sourceType {
			case mailEntity.Kafka:
				var payload mailproto.KafkaStreamPayloadV1
				if err := proto.Unmarshal(event.Stream.Payload, &payload); err != nil || payload.Topic != command.Topic {
					t.Fatalf("invalid Kafka payload: %+v, %v", payload, err)
				}
			case mailEntity.RedisStream:
				var payload mailproto.RedisStreamPayloadV1
				if err := proto.Unmarshal(event.Stream.Payload, &payload); err != nil || payload.StreamKey != command.Topic {
					t.Fatalf("invalid Redis Stream payload: %+v, %v", payload, err)
				}
			case mailEntity.NATSJetStream:
				var payload mailproto.NatsJetStreamPayloadV1
				if err := proto.Unmarshal(event.Stream.Payload, &payload); err != nil || payload.StreamName != command.Topic {
					t.Fatalf("invalid JetStream payload: %+v, %v", payload, err)
				}
			case mailEntity.RabbitMQ:
				var payload mailproto.RabbitMqPayloadV1
				if err := proto.Unmarshal(event.Stream.Payload, &payload); err != nil || payload.QueueName != command.Topic {
					t.Fatalf("invalid RabbitMQ payload: %+v, %v", payload, err)
				}
			}
		})
	}
}
