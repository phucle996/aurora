package handler

import (
	"context"
	"errors"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingSvcInterface "cost-manager/api/internal/domain/service"
	billingTaxonomy "cost-manager/api/internal/taxonomy"
	"cost-manager/api/internal/transport/http/dto"
	"cost-manager/api/pkg/apires"
	"cost-manager/api/pkg/logger"
	"cost-manager/api/pkg/pkgcontext"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// [COMMENT]: TierHandler tiếp nhận và xử lý các HTTP request liên quan tới Tier.
type TierHandler struct {
	tierService billingSvcInterface.TierService
}

// UpdateTier nhận full-state aggregate từ màn Edit và trả snapshot sau commit.
func (h *TierHandler) UpdateTier(c *gin.Context) {
	op := "handler.tier.update"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	var req dto.UpdateTierRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.HandlerWarn(c, op, err, "Invalid tier update payload")
		apires.RespondBadRequest(c, "Invalid tier update payload")
		return
	}

	// Parse optional range IDs tại transport boundary; uuid.Nil được giữ cho range mới.
	ranges := make([]entity.TierRangeInput, len(req.Ranges))
	for i, input := range req.Ranges {
		var rangeID uuid.UUID
		var err error
		if input.ID != "" {
			rangeID, err = uuid.Parse(input.ID)
			if err != nil {
				apires.RespondBadRequest(c, "Invalid tier range id")
				return
			}
		}
		ranges[i] = entity.TierRangeInput{
			ID: rangeID, RangeStart: input.RangeStart, RangeEnd: input.RangeEnd, BaseUnitPrice: input.BaseUnitPrice,
		}
	}

	updated, err := h.tierService.UpdateTier(ctx, entity.TierUpdate{
		Code: req.Code, ServiceType: req.ServiceType, Version: req.Version, Name: req.Name, Ranges: ranges,
	})
	if err != nil {
		h.respondTierUpdateError(c, op, err)
		return
	}

	responseRanges := make([]gin.H, len(updated.Ranges))
	for i, tierRange := range updated.Ranges {
		responseRanges[i] = gin.H{
			"id": tierRange.ID.String(), "range_start": tierRange.RangeStart,
			"range_end": tierRange.RangeEnd, "base_unit_price": tierRange.BaseUnitPrice,
		}
	}
	apires.RespondSuccess(c, gin.H{
		"id": updated.ID.String(), "code": updated.Code, "service_type": updated.ServiceType,
		"version": updated.Version, "name": updated.Name, "ranges": responseRanges, "updated_at": updated.UpdatedAt,
	}, "Successfully updated tier")
}

// respondTierUpdateError giữ taxonomy mapping ổn định và không làm lộ lỗi database ra client.
func (h *TierHandler) respondTierUpdateError(c *gin.Context, op string, err error) {
	switch {
	case errors.Is(err, billingTaxonomy.ErrTierNotFound):
		apires.RespondNotFound(c, "Tier not found")
	case errors.Is(err, billingTaxonomy.ErrTierVersionConflict):
		apires.RespondConflict(c, "Tier was modified by another request")
	case errors.Is(err, billingTaxonomy.ErrInvalidArgument), errors.Is(err, billingTaxonomy.ErrInvalidTierRanges):
		apires.RespondBadRequest(c, "Invalid tier or range configuration")
	default:
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "Failed to update tier")
	}
}

// [COMMENT]: NewTierHandler khởi tạo đối tượng TierHandler.
func NewTierHandler(tierService billingSvcInterface.TierService) *TierHandler {
	return &TierHandler{
		tierService: tierService,
	}
}

// [COMMENT]: ListTiers tiếp nhận kết quả Flat Entity từ service, map trực tiếp sang gin.H phẳng để trả về cho client.
// Loại bỏ hoàn toàn logic gom nhóm lồng nhau phức tạp ở handler để đảm bảo API phản ánh trung thực cấu trúc dữ liệu phẳng tối ưu.
func (h *TierHandler) ListTiers(c *gin.Context) {
	op := "handler.tier.list"

	// Thiết lập context timeout 5 giây cho các truy vấn DB
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	var req dto.ListTiersRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		logger.HandlerWarn(c, op, err, "Invalid query parameters binding for list tiers")
		apires.RespondBadRequest(c, "Invalid query parameters")
		return
	}

	// Đảm bảo limit hợp lý
	if req.Limit <= 0 {
		req.Limit = 10
	} else if req.Limit > 100 {
		req.Limit = 100
	}
	if req.Page <= 0 {
		req.Page = 1
	}

	// Lấy danh sách flat tiers từ service
	flatTiers, total, err := h.tierService.GetTiersList(ctx, req.Page, req.Limit, req.ServiceType, req.Search)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "Failed to retrieve tiers")
		return
	}

	// [COMMENT]: Map trực tiếp Flat Entity sang mảng gin.H phẳng 1-1 cực kỳ tối giản
	tiersData := make([]gin.H, len(flatTiers))
	for i, ft := range flatTiers {
		tiersData[i] = gin.H{
			"id":              ft.ID.String(),     // Range ID
			"tier_id":         ft.TierID.String(), // Tier ID gốc
			"name":            ft.Name,
			"code":            ft.Code,
			"service_type":    ft.ServiceType,
			"version":         ft.Version,
			"range_start":     ft.RangeStart,
			"range_end":       ft.RangeEnd,
			"base_unit_price": ft.BaseUnitPrice,
			"created_at":      ft.CreatedAt,
			"updated_at":      ft.UpdatedAt,
		}
	}

	// Trả về kết quả JSON theo định dạng phân trang phẳng chuẩn
	apires.RespondSuccess(c, gin.H{
		"tiers": tiersData,
		"pagination": gin.H{
			"page":  req.Page,
			"limit": req.Limit,
			"total": total,
		},
	}, "Successfully retrieved tiers")
}
