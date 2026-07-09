package zoneSvcImpl

import (
	"context"
	"controlplane/pkg/logger"
	"fmt"

	"github.com/redis/go-redis/v9"
)

type GatewaySyncPublisher struct {
	rdb *redis.Client
}

func NewGatewaySyncPublisher(rdb *redis.Client) *GatewaySyncPublisher {
	return &GatewaySyncPublisher{
		rdb: rdb,
	}
}

// [COMMENT]: PublishGatewaySync gửi tin nhắn Pub/Sub thông qua Redis client sang kênh "gateway:sync"
// của Edge/ACR để báo hiệu việc evict cache.
func (p *GatewaySyncPublisher) PublishGatewaySync(ctx context.Context, actionType string, code string) {
	if p == nil || p.rdb == nil {
		logger.SysWarn("hierarchy.sync", "Redis client is not configured, skipping PublishGatewaySync")
		return
	}
	payload := fmt.Sprintf(`{"type": "%s", "code": "%s"}`, actionType, code)
	err := p.rdb.Publish(ctx, "gateway:sync", payload).Err()
	if err != nil {
		logger.SysError("hierarchy.sync.publish", "Failed to publish invalidation event to gateway:sync")
	} else {
		logger.SysInfo("hierarchy.sync.publish", "Successfully published invalidation event to gateway:sync")
	}
}
