package iamSvcImpl

import (
	"context"
	"errors"
	"sync"
	"time"

	iamEntity "controlplane/internal/iam/domain/entity"
	iamRepoInterface "controlplane/internal/iam/domain/repo"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	"controlplane/internal/observability"

	"github.com/google/uuid"
)

// PersonalDeviceService implements platform-authorized device audit in the
// `/personal` branch.
type PersonalDeviceService struct {
	deviceRepo iamRepoInterface.PersonalDeviceRepository
	metrics    observability.WorkflowRecorder
}

func NewPersonalDeviceService(
	deviceRepo iamRepoInterface.PersonalDeviceRepository,
	metrics observability.WorkflowRecorder,
) iamSvcInterface.PersonalDeviceService {
	return &PersonalDeviceService{
		deviceRepo: deviceRepo,
		metrics:    metrics,
	}
}

// [COMMENT]: ListUserDevicesPlatform lấy danh sách thiết bị của user đích phục vụ platform audit kèm hierarchy check trong 1 RTT CTE
func (s *PersonalDeviceService) ListUserDevicesPlatform(ctx context.Context, targetUserID uuid.UUID, callerLevel int32, limit int, offset int) (output *iamEntity.PersonalDeviceListResult, err error) {
	startedAt := time.Now()
	defer func() {
		result, reason := observability.ResultFailure, observability.ReasonInternal
		switch {
		case err == nil:
			result, reason = observability.ResultSuccess, observability.ReasonNone
		case errors.Is(err, iamTaxonomy.ErrActionNotAllowed):
			result, reason = observability.ResultRejected, observability.ReasonForbidden
		case errors.Is(err, context.DeadlineExceeded):
			reason = observability.ReasonTimeout
		case errors.Is(err, context.Canceled):
			reason = observability.ReasonCanceled
		}
		s.metrics.ObserveWorkflow(ctx, result, reason, time.Since(startedAt))
	}()

	// [COMMENT]: 1. Truy vấn danh sách thiết bị từ database kết hợp kiểm tra phân cấp
	var items []iamEntity.PersonalDeviceListItem
	var listErr error
	presenceByTracked := make(map[uuid.UUID]iamEntity.UserAccessSession, 10)

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		items, listErr = s.deviceRepo.ListDevicesByUserID(ctx, targetUserID, callerLevel, limit, offset)
	}()

	wg.Wait()

	if listErr != nil {
		return nil, listErr
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
	return &iamEntity.PersonalDeviceListResult{Devices: items, Total: int64(len(items))}, nil
}
