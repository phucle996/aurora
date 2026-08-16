package storageKafka

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	kafkainfra "controlplane/infra/kafka"
	storageEntity "controlplane/internal/storage/domain/entity"
	storageSvcInterface "controlplane/internal/storage/domain/service"
)

type CommercialAdmissionZonePublisher struct {
	producer    *kafkainfra.Producer
	topicPrefix string
	readyTopics sync.Map
}

func NewCommercialAdmissionZonePublisher(
	producer *kafkainfra.Producer,
	topicPrefix string,
) storageSvcInterface.CommercialAdmissionZonePublisher {
	return &CommercialAdmissionZonePublisher{
		producer: producer, topicPrefix: strings.TrimSuffix(topicPrefix, "."),
	}
}

func (p *CommercialAdmissionZonePublisher) Publish(
	ctx context.Context,
	delivery storageEntity.CommercialAdmissionZoneDelivery,
) error {
	topic := fmt.Sprintf("%s.storage.commercial.admission.%s.v1", p.topicPrefix, delivery.ZoneID)
	if _, ready := p.readyTopics.Load(topic); !ready {
		if err := p.producer.EnsureTopic(ctx, topic, 6, 30*24*time.Hour); err != nil {
			return err
		}
		p.readyTopics.Store(topic, struct{}{})
	}
	key := []byte(delivery.SourceEventID.String() + ":" + delivery.ResourceID.String())
	return p.producer.Publish(ctx, topic, key, delivery.Payload)
}
