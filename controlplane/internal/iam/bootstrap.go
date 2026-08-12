package iam

import (
	"context"
	"fmt"
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
	// [COMMENT]: Cost authz miss stays inside Central through Shared Redis; the resolved projection is fenced in Auth Redis.
	if m.billingAuthorizationRedisHandler != nil {
		if err := m.billingAuthorizationRedisHandler.Start(); err != nil {
			return err
		}
	}
	if m.tenantAccessRedisHandler != nil {
		if err := m.tenantAccessRedisHandler.Start(); err != nil {
			return err
		}
	}
	if m.personalAccessRedisHandler != nil {
		if err := m.personalAccessRedisHandler.Start(); err != nil {
			return fmt.Errorf("iam bootstrap: start personal access Redis handler: %w", err)
		}
	}
	// [COMMENT]: Khởi động Shared Redis PubSub subscriber để lắng nghe và điều phối luồng Auth
	if m.authRedisHandler != nil {
		if err := m.authRedisHandler.Start(); err != nil {
			return err
		}
	}
	// [COMMENT]: Khởi động Shared Redis PubSub subscriber cho Device domain (bulk presence & evicted)
	if m.deviceRedisHandler != nil {
		if err := m.deviceRedisHandler.Start(); err != nil {
			return err
		}
	}
	return nil
}

// Stop dừng các background worker do IAM module quản lý.
func (m *IAMModule) Stop() {
	if m == nil {
		return
	}

	if m.deviceRedisHandler != nil {
		m.deviceRedisHandler.Stop()
	}
	if m.authRedisHandler != nil {
		m.authRedisHandler.Stop()
	}
	if m.billingAuthorizationRedisHandler != nil {
		m.billingAuthorizationRedisHandler.Stop()
	}
	if m.tenantAccessRedisHandler != nil {
		m.tenantAccessRedisHandler.Stop()
	}
	if m.personalAccessRedisHandler != nil {
		m.personalAccessRedisHandler.Stop()
	}
	if m.billingOutboxRelay != nil {
		m.billingOutboxRelay.Stop()
	}
}
