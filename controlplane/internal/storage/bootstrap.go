package storage

import (
	"context"

	"controlplane/pkg/logger"
)

// [COMMENT]: Bootstrap khởi tạo runtime side-effects của Storage module.
func (m *StorageModule) Bootstrap(ctx context.Context) error {
	const op = "storage.bootstrap"
	logger.SysInfo(op, "storage module bootstrap initiated")

	if m.CommercialAdmissionProjection != nil {
		if err := m.CommercialAdmissionProjection.Start(); err != nil {
			return err
		}
	}
	if m.CommercialAdmissionReconcile != nil {
		if err := m.CommercialAdmissionReconcile.Start(); err != nil {
			if m.CommercialAdmissionProjection != nil {
				m.CommercialAdmissionProjection.Stop()
			}
			return err
		}
	}
	if m.CommercialAdmissionZoneRelay != nil {
		if err := m.CommercialAdmissionZoneRelay.Start(); err != nil {
			if m.CommercialAdmissionReconcile != nil {
				m.CommercialAdmissionReconcile.Stop()
			}
			if m.CommercialAdmissionProjection != nil {
				m.CommercialAdmissionProjection.Stop()
			}
			return err
		}
	}
	return nil
}

// [COMMENT]: Stop dừng các background worker do Storage module quản lý (Graceful Shutdown).
func (m *StorageModule) Stop() {
	if m == nil {
		return
	}
	if m.CommercialAdmissionProjection != nil {
		m.CommercialAdmissionProjection.Stop()
	}
	if m.CommercialAdmissionReconcile != nil {
		m.CommercialAdmissionReconcile.Stop()
	}
	if m.CommercialAdmissionZoneRelay != nil {
		m.CommercialAdmissionZoneRelay.Stop()
	}
}
