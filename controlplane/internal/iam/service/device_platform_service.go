package iamSvcImpl

import (
	"context"
	"sync"
	"time"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/internal/observability"
	"controlplane/pkg/apperr"

	"github.com/google/uuid"
)

// [COMMENT]: DevicePlatformService thực thi các thao tác giám sát thiết bị toàn hệ thống
type DevicePlatformService struct {
	deviceRepo iamRepoInterface.DevicePlatformRepository
	metrics    observability.WorkflowRecorder
}

// [COMMENT]: NewDevicePlatformService khởi tạo thể hiện DevicePlatformService
func NewDevicePlatformService(
	deviceRepo iamRepoInterface.DevicePlatformRepository,
	metrics observability.WorkflowRecorder,
) iamSvcInterface.DevicePlatformService {
	return &DevicePlatformService{
		deviceRepo: deviceRepo,
		metrics:    metrics,
	}
}

// [COMMENT]: ListUserDevicesPlatform lấy danh sách thiết bị của user đích phục vụ platform audit kèm hierarchy check trong 1 RTT CTE
func (s *DevicePlatformService) ListUserDevicesPlatform(ctx context.Context, targetUserID uuid.UUID, callerLevel int32, limit int, offset int) (*iamEntity.DeviceListResult, error) {
	startedAt := time.Now()
	result, reason := observability.ResultFailure, observability.ReasonInternal
	defer func() { s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt)) }()

	// [COMMENT]: 1. Truy vấn danh sách thiết bị từ database kết hợp kiểm tra phân cấp
	var items []iamEntity.DevicePresence
	var listErr error
	presenceByTracked := make(map[string]iamEntity.UserAccessSession, 10)

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		items, listErr = s.deviceRepo.ListDevicesByUserIDWithHierarchy(ctx, targetUserID, callerLevel, limit, offset)
	}()

	wg.Wait()

	if listErr != nil {
		return nil, apperr.Wrap(iamTaxonomy.ErrAuthenticationUnavailable, listErr, "dependency_error")
	}

	// [COMMENT]: Cập nhật trạng thái online và thời gian hoạt động cuối cùng của từng thiết bị trong mảng items
	for i := range items {
		if rt, ok := presenceByTracked[items[i].ID]; ok {
			items[i].IsOnline = true
			if rt.LastSeenAt > 0 {
				ts := time.Unix(rt.LastSeenAt, 0).UTC()
				items[i].LastSeenAt = &ts
			}
		}
	}
	result, reason = observability.ResultSuccess, observability.ReasonNone
	return &iamEntity.DeviceListResult{Devices: items, Total: int64(len(items))}, nil
}
