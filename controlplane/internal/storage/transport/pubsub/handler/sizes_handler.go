package pubsubHandler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"controlplane/internal/config"
	storageSvcInterface "controlplane/internal/storage/domain/service"
	"controlplane/pkg/logger"

	"github.com/nats-io/nats.go"
)

// [COMMENT]: SyncBucketSizesMsg đại diện cho payload đồng bộ dung lượng các bucket nhận từ NATS.
type SyncBucketSizesMsg struct {
	Sizes map[string]int64 `json:"sizes"`
}

// [COMMENT]: SizesNatsHandler lắng nghe các sự kiện NATS liên quan đến cập nhật dung lượng bucket từ biên.
type SizesNatsHandler struct {
	cfg               *config.Config
	personalBucketSvc storageSvcInterface.PersonalBucketService
	tenantBucketSvc   storageSvcInterface.TenantBucketService
}

// [COMMENT]: NewSizesNatsHandler khởi tạo handler lắng nghe sự kiện đồng bộ dung lượng.
func NewSizesNatsHandler(
	cfg *config.Config,
	personalBucketSvc storageSvcInterface.PersonalBucketService,
	tenantBucketSvc storageSvcInterface.TenantBucketService,
) *SizesNatsHandler {
	return &SizesNatsHandler{
		cfg:               cfg,
		personalBucketSvc: personalBucketSvc,
		tenantBucketSvc:   tenantBucketSvc,
	}
}

// [COMMENT]: Subscribe đăng ký lắng nghe sự kiện đồng bộ dung lượng từ NATS Core.
func (h *SizesNatsHandler) Subscribe(nc *nats.Conn) ([]*nats.Subscription, error) {
	const queueGroup = "storage_sizes_service"
	var subs []*nats.Subscription

	subSync, err := nc.QueueSubscribe("storage.bucket.sizes.sync", queueGroup, func(msg *nats.Msg) {
		ctx := context.Background()

		var req SyncBucketSizesMsg
		if err := json.Unmarshal(msg.Data, &req); err != nil {
			logger.SysError("NATS.SyncBucketSizes", fmt.Sprintf("Failed to unmarshal sync message payload: %v", err))
			return
		}

		for name, size := range req.Sizes {
			var updateErr error
			if strings.HasPrefix(name, "ws-") {
				// [COMMENT]: Đồng bộ dung lượng cho personal bucket
				updateErr = h.personalBucketSvc.UpdateUsedBytes(ctx, name, size)
			} else if strings.HasPrefix(name, "tn-") {
				// [COMMENT]: Đồng bộ dung lượng cho tenant bucket
				updateErr = h.tenantBucketSvc.UpdateUsedBytes(ctx, name, size)
			}

			if updateErr != nil {
				// [COMMENT]: Log cảnh báo nếu không tìm thấy hoặc lỗi hệ thống, không làm crash luồng đồng bộ
				logger.SysWarn("NATS.SyncBucketSizes", fmt.Sprintf("Failed to update used_bytes for bucket '%s': %v", name, updateErr))
			}
		}
	})

	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to storage.bucket.sizes.sync: %w", err)
	}

	subs = append(subs, subSync)
	return subs, nil
}
