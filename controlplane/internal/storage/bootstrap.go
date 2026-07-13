package storage

import (
	"context"

	"controlplane/pkg/logger"
)

// [COMMENT]: Bootstrap khởi tạo runtime side-effects của Storage module.
func (m *StorageModule) Bootstrap(ctx context.Context) error {
	const op = "storage.bootstrap"
	logger.SysInfo(op, "storage module bootstrap initiated")

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
