package storage

import (
	"context"

	pubsubHandler "controlplane/internal/storage/transport/pubsub/handler"
	"controlplane/pkg/logger"
)

// [COMMENT]: Bootstrap khởi tạo runtime side-effects của Storage module.
func (m *StorageModule) Bootstrap(ctx context.Context) error {
	const op = "storage.bootstrap"
	logger.SysInfo(op, "storage module bootstrap initiated")

	// [COMMENT]: Khởi động NATS subscriber để đồng bộ dung lượng các bucket
	if m.natsConn != nil {
		sizesNatsHandler := pubsubHandler.NewSizesNatsHandler(m.cfg, m.PersonalBucketService, m.TenantBucketService)
		subs, err := sizesNatsHandler.Subscribe(m.natsConn)
		if err != nil {
			logger.SysError(op, "failed to subscribe NATS bucket sizes handler: "+err.Error())
			return err
		}
		m.natsSubs = append(m.natsSubs, subs...)
		logger.SysInfo(op, "successfully subscribed to NATS bucket sizes sync channel")
	}

	return nil
}

// [COMMENT]: Stop dừng các background worker do Storage module quản lý (Graceful Shutdown).
func (m *StorageModule) Stop() {
	if m == nil {
		return
	}
	
	// [COMMENT]: Hủy đăng ký NATS subscriptions trước khi tắt ứng dụng
	for _, sub := range m.natsSubs {
		if sub != nil {
			_ = sub.Unsubscribe()
		}
	}
}
