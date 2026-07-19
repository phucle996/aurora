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
	return &mailEntity.PersonalConsumer{ActorUserID: uuid.New(), WorkspaceID: uuid.New(), ZoneID: uuid.New(), IdempotencyKey: "request-0001", Name: "orders", SourceType: mailEntity.Kafka, BrokerResourceID: uuid.New(), Topic: "orders.created", ConsumerGroup: "mail-orders", Mapping: mailEntity.MessageMapping{RecipientJSONPath: "$.recipient", VariableJSONPaths: map[string]string{"name": "$.data.name"}}, TemplateID: "template-1", TemplateVersion: 2, SenderProfileID: "sender-1", SenderVersion: 1, Parallelism: 3}
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
	if repo.outbox == nil || repo.outbox.RoutingScope != "zone:"+command.ZoneID.String() {
		t.Fatalf("unexpected outbox: %+v", repo.outbox)
	}
	var event mailproto.MailConsumerUpsertV1
	if err = proto.Unmarshal(repo.outbox.Payload, &event); err != nil || event.Kafka == nil {
		t.Fatalf("invalid payload: %v", err)
	}
}

func TestPersonalCreateConvergesByBodyIdempotencyKey(t *testing.T) {
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
	if first.ID != second.ID || !bytes.Equal(first.CreateRequestSHA256, second.CreateRequestSHA256) {
		t.Fatal("retry did not converge")
	}
}
