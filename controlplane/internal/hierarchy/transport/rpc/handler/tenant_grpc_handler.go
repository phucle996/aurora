// ======================================================================================================
// 📂 MODULE: controlplane/internal/hierarchy/transport/rpc/handler/tenant_grpc_handler.go
//            gRPC Handler cho TenantService - phục vụ phân giải domain, membership và warmup cho ACR
// ======================================================================================================
//
// 🔒 RANH GIỚI BẢO MẬT:
//   - Transport Layer: chịu trách nhiệm map error và unwrap request/response.
//   - Single Flight bảo vệ CP tránh thundering herd khi nhiều ACR node cùng miss L2 cache.
//   - tenant_code đã bỏ hoàn toàn khỏi resolution chain - domain là source of truth.
//
// ======================================================================================================

package coreRpcHandler

import (
	"context"
	"errors"

	coreEntity "controlplane/internal/hierarchy/domain/entity"
	coreSvcInterface "controlplane/internal/hierarchy/domain/service"
	coreProto "controlplane/internal/hierarchy/transport/rpc/proto"
	coreTaxonomy "controlplane/internal/hierarchy/taxonomy"
	"controlplane/pkg/logger"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"
)

// TenantGRPCHandler xử lý các RPC liên quan đến phân giải Tenant từ Edge/ACR.
type TenantGRPCHandler struct {
	coreProto.UnimplementedTenantServiceServer
	service coreSvcInterface.TenantService
	// [COMMENT]: sfGroup gom nhóm request trùng domain tránh thundering herd lên DB
	sfGroup *singleflight.Group
}

// NewTenantGRPCHandler khởi tạo handler với TenantService dependency.
func NewTenantGRPCHandler(service coreSvcInterface.TenantService) *TenantGRPCHandler {
	return &TenantGRPCHandler{
		service: service,
		sfGroup: &singleflight.Group{},
	}
}

// ResolveTenant phân giải Tenant theo domain liên kết.
// Trả về tenant_id (UUID). tenant_code đã bỏ - domain là key resolution duy nhất.
// Single Flight đảm bảo chỉ 1 DB query cho N node ACR cùng hỏi domain giống nhau.
func (h *TenantGRPCHandler) ResolveTenant(ctx context.Context, req *coreProto.ResolveTenantRequest) (*coreProto.ResolveTenantResponse, error) {
	const op = "core.tenant.rpc.resolve_tenant"
	domain := req.TenantDomain

	// [COMMENT]: Single flight key theo domain để dedup concurrent request từ nhiều Gateway node
	val, err, _ := h.sfGroup.Do("resolve:"+domain, func() (interface{}, error) {
		return h.service.ResolveTenantByDomain(ctx, domain)
	})

	if err != nil {
		if errors.Is(err, coreTaxonomy.ErrTenantNotFound) {
			// [COMMENT]: Not found trả found=false để ACR ghi negative cache, không trả error
			return &coreProto.ResolveTenantResponse{Found: false}, nil
		}
		logger.RPCHandlerWarn(ctx, op, err, "failed to resolve tenant by domain")
		return nil, err
	}

	tenant := val.(*coreEntity.Tenant)
	return &coreProto.ResolveTenantResponse{
		Found:    true,
		TenantId: tenant.ID.String(),
		// [COMMENT]: Không trả tenant_code - client (ACR) chỉ cần tenant_id để nhúng vào JWT
	}, nil
}

// CheckMembership kiểm tra user có thuộc tenant không.
// Dùng trong luồng tenant context switch để xác thực trước khi re-issue JWT.
// TTL tự nhiên ở ACR L1 = 5 phút để tránh stale khi user bị revoke.
func (h *TenantGRPCHandler) CheckMembership(ctx context.Context, req *coreProto.CheckMembershipRequest) (*coreProto.CheckMembershipResponse, error) {
	const op = "core.tenant.rpc.check_membership"

	tenantID, err := uuid.Parse(req.TenantId)
	if err != nil {
		return nil, err
	}
	userID, err := uuid.Parse(req.UserId)
	if err != nil {
		return nil, err
	}

	// [COMMENT]: Membership check không áp dụng single flight vì (tenant_id, user_id)
	// là pair unique - xác suất thundering herd rất thấp, không đáng thêm complexity
	isMember, role, err := h.service.CheckMembership(ctx, tenantID, userID)
	if err != nil {
		logger.RPCHandlerWarn(ctx, op, err, "failed to check tenant membership")
		return nil, err
	}

	return &coreProto.CheckMembershipResponse{
		IsMember: isMember,
		Role:     role,
	}, nil
}

// WarmupTenants lấy danh sách tenants theo chunk (offset-based) để ACR warmup L1/L2.
// Mỗi entry chỉ chứa tenant_id + domain, không có tenant_code.
// Chunk tối đa 1000 để tránh spike DB khi nhiều ACR node bootstrap cùng lúc.
func (h *TenantGRPCHandler) WarmupTenants(ctx context.Context, req *coreProto.WarmupTenantsRequest) (*coreProto.WarmupTenantsResponse, error) {
	const op = "core.tenant.rpc.warmup_tenants"

	// [COMMENT]: Cap chunk size bảo vệ DB khỏi bị query quá lớn
	limit := int(req.ChunkSize)
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	offset := int(req.Offset)

	tenants, hasMore, err := h.service.ListTenantsPaged(ctx, limit, offset)
	if err != nil {
		logger.RPCHandlerWarn(ctx, op, err, "failed to list tenants for warmup")
		return nil, err
	}

	pbEntries := make([]*coreProto.TenantWarmupEntry, 0, len(tenants))
	for _, t := range tenants {
		if t.Domain == "" {
			// [COMMENT]: Bỏ qua tenant chưa có domain - không thể warmup theo key domain
			continue
		}
		pbEntries = append(pbEntries, &coreProto.TenantWarmupEntry{
			TenantId: t.ID.String(),
			Domain:   t.Domain,
			// [COMMENT]: tenant_code không còn trong proto - domain là key duy nhất
		})
	}

	return &coreProto.WarmupTenantsResponse{
		Tenants: pbEntries,
		HasMore: hasMore,
	}, nil
}
