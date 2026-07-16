// Package pubsubHandler chứa các NATS request-reply client handlers của cost-manager.
// Mỗi handler chịu trách nhiệm:
//   - Serialize request → protobuf
//   - Gửi qua nats.Conn.Request()
//   - Deserialize response ← protobuf
//   - Log tại đây (transport boundary)
//   - Trả về domain entity cho tầng service
package pubsubhandler

import (
	"context"
	"fmt"
	"time"

	"cost-manager/api/internal/domain/entity"
	"cost-manager/api/internal/transport/proto/zoneproto"
	"cost-manager/api/pkg/logger"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
)

const (
	subjectGetZoneList = "hierarchy.zone.get_zone_list"
	natsTimeout        = 2 * time.Second
)

// ZoneNatsClientImpl là implementation của ZoneNatsClient — thực hiện NATS request-reply
// tới Controlplane để lấy danh sách Zone catalog.
type ZoneNatsClientImpl struct {
	nc *nats.Conn
}

// NewZoneNatsClient khởi tạo client với nats.Conn đã kết nối
func NewZoneNatsClient(nc *nats.Conn) *ZoneNatsClientImpl {
	return &ZoneNatsClientImpl{nc: nc}
}

// GetZoneList gửi request tới Controlplane qua NATS và trả về danh sách Zone entities.
// Log được thực hiện tại đây — tầng transport — không phải service layer.
func (h *ZoneNatsClientImpl) GetZoneList(ctx context.Context) ([]entity.Zone, error) {
	const op = "pubsub.zone.get_zone_list"

	if h.nc == nil {
		return nil, fmt.Errorf("nats connection is nil")
	}

	// Serialize request protobuf
	req := &zoneproto.GetZoneListRequest{}
	reqData, err := proto.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request failed: %w", err)
	}

	logger.HandlerInfoCtx(ctx, op, "Dispatching NATS request to Controlplane: "+subjectGetZoneList)

	// Gửi request và chờ reply từ Controlplane
	msg, err := h.nc.Request(subjectGetZoneList, reqData, natsTimeout)
	if err != nil {
		logger.HandlerErrorCtx(ctx, op, fmt.Errorf("nats request failed: %w", err))
		return nil, fmt.Errorf("nats request to %s failed: %w", subjectGetZoneList, err)
	}

	// Deserialize response protobuf
	var resp zoneproto.GetZoneListResponse
	if err := proto.Unmarshal(msg.Data, &resp); err != nil {
		logger.HandlerErrorCtx(ctx, op, fmt.Errorf("unmarshal response failed: %w", err))
		return nil, fmt.Errorf("unmarshal zone list response failed: %w", err)
	}

	// Map protobuf → domain entity
	zones := make([]entity.Zone, 0, len(resp.Zones))
	for _, z := range resp.Zones {
		zones = append(zones, entity.Zone{
			ID:     z.ZoneId,
			Code:   z.ZoneCode,
			Name:   z.Name,
			Status: z.Status,
		})
	}

	logger.HandlerInfoCtx(ctx, op, fmt.Sprintf("Received %d zones from Controlplane", len(zones)))
	return zones, nil
}
