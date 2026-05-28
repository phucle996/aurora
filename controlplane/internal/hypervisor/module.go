package hypervisor

import (
	"context"
	"errors"

	"controlplane/internal/config"

	"github.com/jackc/pgx/v5/pgxpool"
)

// HypervisorModule đại diện cho phân hệ tương tác với ảo hóa (KVM, vSphere, v.v.).
// Đây là module Tier-1 (Non-Critical): Lỗi khởi tạo phân hệ này không được phép
// gây sập hệ thống (Crash-Loopback) mà chỉ làm suy giảm tính năng (Graceful Degradation).
type HypervisorModule struct {
	enabled bool
	err     error // Lưu vết lỗi khởi tạo để phục vụ Observability (SRE monitor)
	db      *pgxpool.Pool
	cfg     *config.Config
}

// IsEnabled trả về true nếu module được khởi tạo thành công và sẵn sàng phục vụ.
func (m *HypervisorModule) IsEnabled() bool {
	return m != nil && m.enabled
}

// GetError trả về lỗi khởi tạo (nếu có) phục vụ logging và monitoring.
func (m *HypervisorModule) GetError() error {
	if m == nil {
		return errors.New("hypervisor module is nil")
	}
	return m.err
}

// NewModule khởi tạo HypervisorModule.
// Giả lập: Nếu không cấu hình Hostname ảo hóa hoặc cấu hình bị lỗi, module sẽ trả về lỗi
// để kích hoạt cơ chế Degrade tại App-Layer.
func NewModule(cfg *config.Config, db *pgxpool.Pool) (*HypervisorModule, error) {
	if cfg == nil {
		return nil, errors.New("hypervisor module: config is required")
	}
	if db == nil {
		return nil, errors.New("hypervisor module: database connection pool is required")
	}

	return &HypervisorModule{
		enabled: true,
		db:      db,
		cfg:     cfg,
	}, nil
}

// NewDegradedModule tạo một phân hệ ảo hóa câm (Dummy/Disabled) mang theo thông điệp lỗi.
// Giúp tránh việc gọi các API gây nil pointer panic và lưu vết lỗi chẩn đoán (Diagnostic).
func NewDegradedModule(err error) *HypervisorModule {
	return &HypervisorModule{
		enabled: false,
		err:     err,
	}
}

// Bootstrap khởi chạy các scheduler ngầm của hypervisor module.
func (m *HypervisorModule) Bootstrap(ctx context.Context) error {
	if !m.IsEnabled() {
		// Bỏ qua không khởi chạy các scheduler ngầm nếu module bị degraded
		return nil
	}
	// Khởi động các worker định kỳ đồng bộ VM, v.v.
	return nil
}

func (m *HypervisorModule) Stop() {
	if !m.IsEnabled() {
		return
	}
	// Dừng an toàn các workers
}
