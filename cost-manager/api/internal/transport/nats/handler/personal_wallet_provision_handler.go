package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"time"

	billingSvcInterface "cost-manager/api/internal/domain/service"
	walletv1 "cost-manager/api/internal/genproto/billing/wallet/v1"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"
)

const personalWalletProvisionSubject = "billing.wallet.personal.provision.requested.v1"

type PersonalWalletProvisionSubscriber struct {
	consumer jetstream.Consumer
	js       jetstream.JetStream
	service  billingSvcInterface.AccountService
	stop     chan struct{}
}

func NewPersonalWalletProvisionSubscriber(nc *nats.Conn, service billingSvcInterface.AccountService) (*PersonalWalletProvisionSubscriber, error) {

	js, err := jetstream.New(nc)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name: "BILLING_WALLET_PROVISION", Subjects: []string{personalWalletProvisionSubject},
		Storage: jetstream.FileStorage, Retention: jetstream.LimitsPolicy, MaxAge: 30 * 24 * time.Hour,
	})
	if err != nil {
		return nil, fmt.Errorf("personal wallet provision: ensure stream: %w", err)
	}
	_, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name: "BILLING_WALLET_PROVISION_DLQ", Subjects: []string{personalWalletProvisionSubject + ".dlq"},
		Storage: jetstream.FileStorage, Retention: jetstream.LimitsPolicy, MaxAge: 30 * 24 * time.Hour,
	})
	if err != nil {
		return nil, fmt.Errorf("personal wallet provision: ensure DLQ stream: %w", err)
	}
	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable: "billing-personal-wallet-provision-v1", FilterSubject: personalWalletProvisionSubject,
		AckPolicy: jetstream.AckExplicitPolicy, AckWait: 30 * time.Second, MaxDeliver: 25,
	})
	if err != nil {
		return nil, err
	}
	return &PersonalWalletProvisionSubscriber{consumer: consumer, js: js, service: service, stop: make(chan struct{})}, nil
}

func (s *PersonalWalletProvisionSubscriber) deadLetter(msg jetstream.Msg, reason string) {
	msgID := msg.Headers().Get("Nats-Msg-Id")
	if msgID == "" {
		msgID = uuid.NewString()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.js.Publish(ctx, personalWalletProvisionSubject+".dlq", msg.Data(), jetstream.WithMsgID("dlq:"+msgID)); err != nil {
		log.Printf("[PersonalWalletProvision] DLQ publish failed (%s): %v", reason, err)
		_ = msg.Nak()
		return
	}
	log.Printf("[PersonalWalletProvision] message moved to DLQ: %s", reason)
	_ = msg.Term()
}

func (s *PersonalWalletProvisionSubscriber) Start() error {
	consume, err := s.consumer.Consume(func(msg jetstream.Msg) {
		wire := &walletv1.PersonalWalletProvisionRequestedV1{}
		if err := proto.Unmarshal(msg.Data(), wire); err != nil {
			s.deadLetter(msg, "invalid protobuf")
			return
		}
		eventID, eventErr := uuid.FromBytes(wire.GetEventId())
		ownerID, ownerErr := uuid.FromBytes(wire.GetOwnerId())
		headerID, headerErr := uuid.Parse(msg.Headers().Get("Nats-Msg-Id"))
		_, occurredErr := time.Parse(time.RFC3339Nano, wire.GetOccurredAt())
		if eventErr != nil || ownerErr != nil || headerErr != nil || occurredErr != nil ||
			eventID == uuid.Nil || ownerID == uuid.Nil || headerID != eventID ||
			wire.GetOwnerType() != "PERSONAL" || wire.GetCurrency() != "USD" || wire.GetSchemaVersion() != 1 {
			s.deadLetter(msg, "invalid personal wallet provision contract")
			return
		}
		hash := sha256.Sum256(msg.Data())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.service.ProvisionPersonalWallet(ctx, eventID, ownerID, hex.EncodeToString(hash[:])); err != nil {
			log.Printf("[PersonalWalletProvision] apply event %s failed: %v", eventID, err)
			if metadata, metadataErr := msg.Metadata(); metadataErr == nil && metadata.NumDelivered >= 25 {
				s.deadLetter(msg, "billing apply exhausted retries")
				return
			}
			_ = msg.Nak()
			return
		}
		_ = msg.Ack()
	})
	if err != nil {
		return err
	}
	go func() { <-s.stop; consume.Stop() }()
	return nil
}

func (s *PersonalWalletProvisionSubscriber) Stop() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
}
