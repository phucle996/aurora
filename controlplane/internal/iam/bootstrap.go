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
	if m.lifecycleFactRelay != nil {
		m.lifecycleFactRelay.Start()
	}
	if m.deviceRuntimeRevokeRelay != nil {
		m.deviceRuntimeRevokeRelay.Start()
	}
	// [COMMENT]: Cost authz miss stays inside Central through Shared Redis; the resolved projection is fenced in Auth Redis.
	if m.billingAuthorizationRedisHandler != nil {
		if err := m.billingAuthorizationRedisHandler.Start(); err != nil {
			return err
		}
	}
	if m.runtimeReadAuthorizationRedisHandler != nil {
		if err := m.runtimeReadAuthorizationRedisHandler.Start(); err != nil {
			return fmt.Errorf("iam bootstrap: start runtime-read authorization Redis handler: %w", err)
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
	if m.devicePresenceProjectionHandler != nil {
		if err := m.devicePresenceProjectionHandler.Start(); err != nil {
			return fmt.Errorf("iam bootstrap: start device presence projection handler: %w", err)
		}
	}
	if m.deviceSessionCapacityEvictionHandler != nil {
		if err := m.deviceSessionCapacityEvictionHandler.Start(); err != nil {
			return fmt.Errorf("iam bootstrap: start device session-capacity eviction handler: %w", err)
		}
	}
	return nil
}

// Stop dừng các background worker do IAM module quản lý.
func (m *IAMModule) Stop() {
	if m == nil {
		return
	}

	if m.deviceSessionCapacityEvictionHandler != nil {
		m.deviceSessionCapacityEvictionHandler.Stop()
	}
	if m.devicePresenceProjectionHandler != nil {
		m.devicePresenceProjectionHandler.Stop()
	}
	if m.authRedisHandler != nil {
		m.authRedisHandler.Stop()
	}
	if m.billingAuthorizationRedisHandler != nil {
		m.billingAuthorizationRedisHandler.Stop()
	}
	if m.runtimeReadAuthorizationRedisHandler != nil {
		m.runtimeReadAuthorizationRedisHandler.Stop()
	}
	if m.tenantAccessRedisHandler != nil {
		m.tenantAccessRedisHandler.Stop()
	}
	if m.personalAccessRedisHandler != nil {
		m.personalAccessRedisHandler.Stop()
	}
	if m.deviceRuntimeRevokeRelay != nil {
		m.deviceRuntimeRevokeRelay.Stop()
	}
	if m.lifecycleFactRelay != nil {
		m.lifecycleFactRelay.Stop()
	}
}
