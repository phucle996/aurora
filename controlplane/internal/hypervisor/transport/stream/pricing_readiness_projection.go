package hypervisorStream

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	hypervisorEntity "controlplane/internal/hypervisor/domain/entity"
	hypervisorSvcInterface "controlplane/internal/hypervisor/domain/service"
	hypervisorTaxonomy "controlplane/internal/hypervisor/taxonomy"
	"controlplane/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

const (
	hypervisorPricingReadinessStream = "billing.pricing.hypervisor.rateability.changed.v1"
	hypervisorPricingReadinessGroup  = "controlplane-hypervisor-pricing-readiness-v1"
)

type hypervisorPricingReadinessPayload struct {
	SchemaVersion int      `json:"schema_version"`
	Ready         bool     `json:"ready"`
	Missing       []string `json:"missing"`
	ObservedAt    string   `json:"observed_at"`
	ValidUntil    string   `json:"valid_until"`
	Fingerprint   string   `json:"fingerprint"`
}

type PricingReadinessProjectionConsumer struct {
	rds     *goredis.Client
	service hypervisorSvcInterface.PricingReadinessProjectionService
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func NewPricingReadinessProjectionConsumer(rds *goredis.Client, service hypervisorSvcInterface.PricingReadinessProjectionService) *PricingReadinessProjectionConsumer {
	return &PricingReadinessProjectionConsumer{rds: rds, service: service, cancel: func() {}}
}

func (c *PricingReadinessProjectionConsumer) Start() error {
	ctx, cancel := context.WithCancel(context.Background())
	if err := c.rds.XGroupCreateMkStream(ctx, hypervisorPricingReadinessStream, hypervisorPricingReadinessGroup, "0").Err(); err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		cancel()
		return fmt.Errorf("create Hypervisor pricing readiness consumer group: %w", err)
	}
	c.cancel = cancel
	c.wg.Add(1)
	go func() { defer c.wg.Done(); c.run(ctx) }()
	return nil
}

func (c *PricingReadinessProjectionConsumer) Stop() { c.cancel(); c.wg.Wait() }

func (c *PricingReadinessProjectionConsumer) run(ctx context.Context) {
	consumer := "hypervisor-pricing-readiness-" + uuid.NewString()
	process := func(messages []goredis.XMessage) {
		for _, message := range messages {
			var raw []byte
			switch value := message.Values["payload"].(type) {
			case string:
				raw = []byte(value)
			case []byte:
				raw = value
			}
			var payload hypervisorPricingReadinessPayload
			if len(raw) == 0 || len(raw) > 16*1024 || json.Unmarshal(raw, &payload) != nil || len(payload.Missing) > 32 {
				c.ack(ctx, message.ID)
				continue
			}
			observedAt, observedErr := time.Parse(time.RFC3339Nano, payload.ObservedAt)
			validUntil, validErr := time.Parse(time.RFC3339Nano, payload.ValidUntil)
			fingerprint, fingerprintErr := hex.DecodeString(payload.Fingerprint)
			if observedErr != nil || validErr != nil || fingerprintErr != nil || len(fingerprint) != 32 {
				c.ack(ctx, message.ID)
				continue
			}
			err := c.service.ApplyPricingReadiness(ctx, &hypervisorEntity.PricingReadinessProjectionCommand{
				SchemaVersion: payload.SchemaVersion, Ready: payload.Ready, Missing: payload.Missing,
				ObservedAt: observedAt.UTC(), ValidUntil: validUntil.UTC(), Fingerprint: payload.Fingerprint,
			})
			if err != nil {
				logger.SysWarn("hypervisor.pricing_readiness.apply", err.Error())
				if errors.Is(err, hypervisorTaxonomy.ErrPricingUnavailable) {
					c.ack(ctx, message.ID)
				}
				continue
			}
			c.ack(ctx, message.ID)
		}
	}
	for ctx.Err() == nil {
		claimed, _, err := c.rds.XAutoClaim(ctx, &goredis.XAutoClaimArgs{Stream: hypervisorPricingReadinessStream, Group: hypervisorPricingReadinessGroup, Consumer: consumer, MinIdle: 30 * time.Second, Start: "0-0", Count: 64}).Result()
		if err == nil && len(claimed) > 0 {
			process(claimed)
			continue
		}
		streams, err := c.rds.XReadGroup(ctx, &goredis.XReadGroupArgs{Group: hypervisorPricingReadinessGroup, Consumer: consumer, Streams: []string{hypervisorPricingReadinessStream, ">"}, Count: 64, Block: 5 * time.Second}).Result()
		if err != nil {
			if !errors.Is(err, goredis.Nil) && ctx.Err() == nil {
				logger.SysWarn("hypervisor.pricing_readiness.read", err.Error())
			}
			continue
		}
		for _, stream := range streams {
			process(stream.Messages)
		}
	}
}

func (c *PricingReadinessProjectionConsumer) ack(ctx context.Context, messageID string) {
	_, _ = c.rds.TxPipelined(ctx, func(pipe goredis.Pipeliner) error {
		pipe.XAck(ctx, hypervisorPricingReadinessStream, hypervisorPricingReadinessGroup, messageID)
		pipe.XDel(ctx, hypervisorPricingReadinessStream, messageID)
		return nil
	})
}
