package iam

import (
	"context"

	pubsubHandler "controlplane/internal/iam/transport/pubsub/handler"
)

// Bootstrap khởi tạo runtime side-effects của IAM module.
//
// Trách nhiệm:
//  1. chạy admin bootstrap idempotent ở service layer,
//  2. khởi động scheduler caller cho admin key rotation trigger.
//
// Lưu ý:
// - Business logic rotate nằm ở service/repo.
// - Bootstrap chỉ orchestration, không chứa persistence logic.
func (m *IAMModule) Bootstrap(ctx context.Context) error {
	// [COMMENT]: Relay sở hữu runtime context riêng; bootstrap timeout không được dừng worker sau 20 giây.
	if m.billingOutboxRelay != nil {
		m.billingOutboxRelay.Start()
	}
	// [COMMENT]: Khởi động NATS subscriber để lắng nghe và điều phối luồng Login (Request-Reply) và bulk presence updates
	if m.natsConn != nil {
		authNatsHandler := pubsubHandler.NewAuthNatsHandler(
			m.cfg,
			m.AuthService,
			m.SessionRefreshService,
			m.RbacPlatformRepository,
			m.rds,
			m.otel,
		)
		subs, err := authNatsHandler.Subscribe(m.natsConn)
		if err != nil {
			return err
		}
		m.natsSubs = append(m.natsSubs, subs...)

		deviceNatsHandler := pubsubHandler.NewDeviceNatsHandler(m.cfg, m.deviceSelfSvcImpl, m.otel)
		deviceSubs, err := deviceNatsHandler.Subscribe(m.natsConn)
		if err != nil {
			return err
		}
		m.natsSubs = append(m.natsSubs, deviceSubs...)
	}

	return nil
}

// Stop dừng các background worker do IAM module quản lý.
func (m *IAMModule) Stop() {
	if m == nil {
		return
	}

	// [COMMENT]: Hủy đăng ký NATS subscriptions trước khi tắt ứng dụng
	for _, sub := range m.natsSubs {
		if sub != nil {
			_ = sub.Unsubscribe()
		}
	}
	m.natsSubs = nil
	if m.billingOutboxRelay != nil {
		m.billingOutboxRelay.Stop()
	}

}
