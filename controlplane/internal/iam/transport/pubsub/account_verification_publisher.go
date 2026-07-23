package pubsub

import (
	"context"
	"errors"
	"fmt"
	"strings"

	kafkainfra "controlplane/infra/kafka"
	iamEntity "controlplane/internal/iam/domain/entity"
	mailproto "controlplane/internal/mail/transport/rpc/proto"

	"google.golang.org/protobuf/proto"
)

// AccountVerificationPublisher chuyển outbound port của IAM thành Protobuf binary trên Kafka.
// Đây là biên transport: IAM service và ACR không phụ thuộc Kafka hay mail runtime contract.
type AccountVerificationPublisher struct {
	producer *kafkainfra.Producer
	topic    string
}

func NewAccountVerificationPublisher(
	producer *kafkainfra.Producer,
	topic string,
) (*AccountVerificationPublisher, error) {
	if producer == nil {
		return nil, errors.New("account verification publisher: kafka producer is required")
	}
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return nil, errors.New("account verification publisher: topic is required")
	}
	return &AccountVerificationPublisher{producer: producer, topic: topic}, nil
}

func (p *AccountVerificationPublisher) PublishAccountVerification(
	ctx context.Context,
	dispatch iamEntity.AccountVerificationDispatch,
) error {
	if dispatch.EventID == [16]byte{} || strings.TrimSpace(dispatch.Recipient) == "" ||
		dispatch.ExpiresAt.IsZero() {
		return errors.New("account verification publisher: invalid dispatch")
	}

	// [COMMENT]: Binary envelope chỉ chứa người nhận và JSON-like parameter map;
	// root-owned consumer/template quyết định Zone và cách render {{ }} ở runtime.
	envelope := &mailproto.MailDispatchEnvelopeV1{
		EventId:        dispatch.EventID[:],
		SchemaVersion:  1,
		To:             strings.TrimSpace(dispatch.Recipient),
		Parameter:      dispatch.Parameter,
		NotAfterUnixMs: dispatch.ExpiresAt.UnixMilli(),
	}
	payload, err := proto.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("account verification publisher: marshal envelope: %w", err)
	}

	// [COMMENT]: event_id là Kafka key ổn định để cùng logical dispatch giữ partition ordering.
	if err := p.producer.Publish(ctx, p.topic, dispatch.EventID[:], payload); err != nil {
		return fmt.Errorf("account verification publisher: publish: %w", err)
	}
	return nil
}
