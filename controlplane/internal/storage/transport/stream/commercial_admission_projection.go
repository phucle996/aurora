package storageStream

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	storageEntity "controlplane/internal/storage/domain/entity"
	storageSvcInterface "controlplane/internal/storage/domain/service"
	storageTaxonomy "controlplane/internal/storage/taxonomy"
	storageproto "controlplane/internal/storage/transport/proto"
	"controlplane/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"
)

const (
	commercialAdmissionStream = "billing.commercial.admission.storage.changed.v1"
	commercialAdmissionGroup  = "controlplane-storage-commercial-admission-v1"
)

type CommercialAdmissionProjectionConsumer struct {
	rds     *goredis.Client
	service storageSvcInterface.CommercialAdmissionProjectionService
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func NewCommercialAdmissionProjectionConsumer(
	rds *goredis.Client,
	service storageSvcInterface.CommercialAdmissionProjectionService,
) *CommercialAdmissionProjectionConsumer {
	return &CommercialAdmissionProjectionConsumer{
		rds: rds, service: service, cancel: func() {},
	}
}

func (c *CommercialAdmissionProjectionConsumer) Start() error {
	ctx, cancel := context.WithCancel(context.Background())
	if err := c.rds.XGroupCreateMkStream(ctx, commercialAdmissionStream, commercialAdmissionGroup, "0").Err(); err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		cancel()
		return fmt.Errorf("create Storage commercial admission consumer group: %w", err)
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
	consumer := "storage-admission-" + uuid.NewString()
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
			var event storageproto.CommercialAdmissionChangedV1
			if len(payload) == 0 || len(payload) > 64*1024 || proto.Unmarshal(payload, &event) != nil {
				logger.SysWarn("storage.commercial_admission.invalid", "commercial admission event rejected")
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
				logger.SysWarn("storage.commercial_admission.invalid", "commercial admission transport rejected")
				c.ack(ctx, message.ID)
				continue
			}
			err := c.service.Apply(ctx, &storageEntity.CommercialAdmissionProjectionCommand{
				EventID: eventID, OwnerID: ownerID, OwnerType: event.OwnerType,
				PolicyVersion: event.PolicyVersion, Decision: event.Decision,
				RestrictionReason: event.RestrictionReason, EffectiveAt: effectiveAt,
				ValidUntil: validUntil,
			})
			if err != nil {
				if errors.Is(err, storageTaxonomy.ErrInvalidCommercialAdmissionProjection) {
					logger.SysWarn("storage.commercial_admission.invalid", "commercial admission event rejected")
					c.ack(ctx, message.ID)
				}
				continue
			}
			c.ack(ctx, message.ID)
		}
	}
	claimStart := "0-0"
	for ctx.Err() == nil {
		claimed, nextClaimStart, claimErr := c.rds.XAutoClaim(ctx, &goredis.XAutoClaimArgs{
			Stream: commercialAdmissionStream, Group: commercialAdmissionGroup,
			Consumer: consumer, MinIdle: 30 * time.Second, Start: claimStart, Count: 64,
		}).Result()
		if claimErr == nil {
			claimStart = nextClaimStart
			if claimStart == "" {
				claimStart = "0-0"
			}
			process(claimed)
		} else if !errors.Is(claimErr, goredis.Nil) && ctx.Err() == nil {
			logger.SysWarn("storage.commercial_admission.reclaim", claimErr.Error())
		}

		streams, err := c.rds.XReadGroup(ctx, &goredis.XReadGroupArgs{
			Group: commercialAdmissionGroup, Consumer: consumer,
			Streams: []string{commercialAdmissionStream, ">"}, Count: 64, Block: 5 * time.Second,
		}).Result()
		if err != nil {
			if !errors.Is(err, goredis.Nil) && ctx.Err() == nil {
				logger.SysWarn("storage.commercial_admission.read", err.Error())
			}
		} else {
			for _, stream := range streams {
				process(stream.Messages)
			}
		}
	}
}

func (c *CommercialAdmissionProjectionConsumer) ack(ctx context.Context, messageID string) {
	_, _ = c.rds.TxPipelined(ctx, func(pipe goredis.Pipeliner) error {
		pipe.XAck(ctx, commercialAdmissionStream, commercialAdmissionGroup, messageID)
		pipe.XDel(ctx, commercialAdmissionStream, messageID)
		return nil
	})
}
