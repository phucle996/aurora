package iamSvcImpl

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"controlplane/internal/cacheengine"
	iamEntity "controlplane/internal/iam/domain/entity"
	iamSvcInterface "controlplane/internal/iam/domain/service"
	iamTaxonomy "controlplane/internal/iam/taxonomy"
	iamproto "controlplane/internal/iam/transport/proto"
	"controlplane/internal/observability"
)

// [COMMENT]: TenantRenderContextService thực thi việc nạp phân quyền tenant từ CacheEngine L1/L2 và xây dựng cấu trúc capabilities/menu cho tenant.
type TenantRenderContextService struct {
	cacheEngine *cacheengine.CacheRegistry
	metrics     observability.WorkflowRecorder
}

// [COMMENT]: NewTenantRenderContextService khởi tạo thể hiện mới của TenantRenderContextService
func NewTenantRenderContextService(
	cacheEngine *cacheengine.CacheRegistry,
	metrics observability.WorkflowRecorder,
) iamSvcInterface.TenantRenderContextService {
	return &TenantRenderContextService{
		cacheEngine: cacheEngine,
		metrics:     metrics,
	}
}

// [COMMENT]: GetTenantRenderContext đọc quyền membership_role từ L1/L2 theo key {userID}:{tenantID}, validate tính toàn vẹn 5 cấp và chuẩn hóa dữ liệu menu.
func (s *TenantRenderContextService) GetTenantRenderContext(ctx context.Context, workflow *iamEntity.TenantRenderContext) (err error) {
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

	// 1. Đọc membership role từ L1/L2 theo composite key (userID:tenantID)
	value, err := s.cacheEngine.GetOrLoad(
		ctx,
		"membership_role",
		workflow.UserID.String()+":"+workflow.TenantID.String(),
	)
	if err != nil {
		return fmt.Errorf("tenant render context service: load tenant assignment: %w", err)
	}
	entry, ok := value.(*iamproto.RoleEntry)
	if !ok || entry == nil {
		return fmt.Errorf("tenant render context service: tenant cache value has unexpected type")
	}
	if len(entry.Permissions) == 0 {
		return iamTaxonomy.ErrActionNotAllowed
	}

	// 2. Validate định dạng quyền 5 cấp: <tenant>:<workspace>:<module>:<object>:<behavior>
	workflow.Permissions = make([]string, len(entry.Permissions))
	tenantID := workflow.TenantID.String()
	for index, permission := range entry.Permissions {
		parts := strings.Split(permission, ":")
		if len(parts) != 5 || parts[0] != tenantID || parts[1] == "" || parts[2] == "" || parts[3] == "" || parts[4] == "" {
			return fmt.Errorf("tenant render context service: invalid compiled tenant permission")
		}
		workflow.Permissions[index] = permission
	}

	// 3. Deduplicate và sắp xếp danh sách capabilities và navigation keys
	capabilitySet := make(map[string]struct{}, len(workflow.Permissions))
	navigationSet := make(map[string]struct{}, len(workflow.Permissions))
	for _, permission := range workflow.Permissions {
		parts := strings.Split(permission, ":")
		capabilitySet[permission] = struct{}{}
		navigationSet[parts[2]+":"+parts[3]+"\x00"+parts[4]] = struct{}{}
	}
	workflow.Capabilities = make([]string, 0, len(capabilitySet))
	for permission := range capabilitySet {
		workflow.Capabilities = append(workflow.Capabilities, permission)
	}
	sort.Strings(workflow.Capabilities)

	navigation := make([]string, 0, len(navigationSet))
	for entry := range navigationSet {
		navigation = append(navigation, entry)
	}
	sort.Strings(navigation)
	workflow.NavigationKeys = make([]string, 0, len(navigation))
	workflow.NavigationActions = make([]string, 0, len(navigation))
	for _, entry := range navigation {
		parts := strings.SplitN(entry, "\x00", 2)
		workflow.NavigationKeys = append(workflow.NavigationKeys, parts[0])
		workflow.NavigationActions = append(workflow.NavigationActions, parts[1])
	}
	return nil
}
