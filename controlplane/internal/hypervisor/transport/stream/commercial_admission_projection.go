package hypervisorStream

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
	hypervisorSvcInterface "controlplane/internal/hypervisor/domain/service"
	hypervisorTaxonomy "controlplane/internal/hypervisor/taxonomy"
	hypervisorProto "controlplane/internal/hypervisor/transport/proto"
	"controlplane/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

const (
	hypervisorCommercialAdmissionStream = "billing.commercial.admission.hypervisor.changed.v1"
	hypervisorCommercialAdmissionGroup  = "controlplane-hypervisor-commercial-admission-v1"
)

type CommercialAdmissionProjectionConsumer struct {
	rds     *goredis.Client
	service hypervisorSvcInterface.CommercialAdmissionProjectionService
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func NewCommercialAdmissionProjectionConsumer(
	rds *goredis.Client,
	service hypervisorSvcInterface.CommercialAdmissionProjectionService,
) *CommercialAdmissionProjectionConsumer {
	return &CommercialAdmissionProjectionConsumer{
		rds: rds, service: service, cancel: func() {},
	}
}

func (c *CommercialAdmissionProjectionConsumer) Start() error {
	ctx, cancel := context.WithCancel(context.Background())
	if err := c.rds.XGroupCreateMkStream(ctx, hypervisorCommercialAdmissionStream, hypervisorCommercialAdmissionGroup, "0").Err(); err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		cancel()
		return fmt.Errorf("create Hypervisor commercial admission consumer group: %w", err)
	}
	c.cancel = cancel
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.run(ctx)
	}()
	return nil
}

func (c *CommercialAdmissionProjectionConsumer) Stop() {
	c.cancel()
	c.wg.Wait()
}

func (c *CommercialAdmissionProjectionConsumer) run(ctx context.Context) {
	consumer := "hypervisor-admission-" + uuid.NewString()
	process := func(messages []goredis.XMessage) {
		for _, message := range messages {
			var payload []byte
			var envelopeEventID string
			switch value := message.Values["event_id"].(type) {
			case string:
				envelopeEventID = value
			case []byte:
				envelopeEventID = string(value)
			}
			switch value := message.Values["payload"].(type) {
			case string:
				payload = []byte(value)
			case []byte:
				payload = value
			}
			var event hypervisorProto.CommercialAdmissionChangedV1
			if len(payload) == 0 || len(payload) > 64*1024 || proto.Unmarshal(payload, &event) != nil {
				logger.SysWarn("hypervisor.commercial_admission.invalid", "commercial admission event rejected")
				c.ack(ctx, message.ID)
				continue
			}
			envelopeID, envelopeErr := uuid.Parse(envelopeEventID)
			eventID, eventErr := uuid.Parse(event.EventId)
			ownerID, ownerErr := uuid.Parse(event.OwnerId)
			effectiveAt, effectiveErr := time.Parse(time.RFC3339Nano, event.EffectiveAt)
			effectiveAt = effectiveAt.UTC()
			var validUntil *time.Time
			var validUntilErr error
			if event.ValidUntil != "" {
				parsed, err := time.Parse(time.RFC3339Nano, event.ValidUntil)
				parsed = parsed.UTC()
				validUntil = &parsed
				validUntilErr = err
			}
			if envelopeErr != nil || eventErr != nil || ownerErr != nil ||
				envelopeID == uuid.Nil || envelopeID != eventID ||
				ownerID == uuid.Nil || effectiveErr != nil || validUntilErr != nil {
				logger.SysWarn("hypervisor.commercial_admission.invalid", "commercial admission transport rejected")
				c.ack(ctx, message.ID)
				continue
			}
			err := c.service.Apply(ctx, &hypervisorEntity.CommercialAdmissionProjectionCommand{
				EventID: eventID, OwnerID: ownerID, OwnerType: event.OwnerType,
				PolicyVersion: event.PolicyVersion, Decision: event.Decision,
				RestrictionReason: event.RestrictionReason, EffectiveAt: effectiveAt,
				ValidUntil: validUntil,
			})
			if err != nil {
				if errors.Is(err, hypervisorTaxonomy.ErrInvalidCommercialAdmissionProjection) {
					logger.SysWarn("hypervisor.commercial_admission.invalid", "commercial admission event rejected")
					c.ack(ctx, message.ID)
				}
				continue
			}
			c.ack(ctx, message.ID)
		}
	}
	for ctx.Err() == nil {
		claimed, _, claimErr := c.rds.XAutoClaim(ctx, &goredis.XAutoClaimArgs{
			Stream: hypervisorCommercialAdmissionStream, Group: hypervisorCommercialAdmissionGroup,
			Consumer: consumer, MinIdle: 30 * time.Second, Start: "0-0", Count: 64,
		}).Result()
		if claimErr == nil && len(claimed) > 0 {
			process(claimed)
			continue
		}
		streams, err := c.rds.XReadGroup(ctx, &goredis.XReadGroupArgs{
			Group: hypervisorCommercialAdmissionGroup, Consumer: consumer,
			Streams: []string{hypervisorCommercialAdmissionStream, ">"}, Count: 64, Block: 5 * time.Second,
		}).Result()
		if err != nil {
			if errors.Is(err, goredis.Nil) || ctx.Err() != nil {
				continue
			}
			logger.SysWarn("hypervisor.commercial_admission.read", err.Error())
			continue
		}
		for _, stream := range streams {
			process(stream.Messages)
		}
	}
}

func (c *CommercialAdmissionProjectionConsumer) ack(ctx context.Context, messageID string) {
	_, _ = c.rds.TxPipelined(ctx, func(pipe goredis.Pipeliner) error {
		pipe.XAck(ctx, hypervisorCommercialAdmissionStream, hypervisorCommercialAdmissionGroup, messageID)
		pipe.XDel(ctx, hypervisorCommercialAdmissionStream, messageID)
		return nil
	})
}
