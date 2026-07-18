package handler

import (
	"context"
	"time"

	billingSvcInterface "cost-manager/api/internal/domain/service"
	"cost-manager/api/internal/transport/http/dto"
	"cost-manager/api/pkg/apires"
	"cost-manager/api/pkg/logger"
	"cost-manager/api/pkg/pkgcontext"

	"github.com/gin-gonic/gin"
)

// [COMMENT]: TierHandler tiếp nhận và xử lý các HTTP request liên quan tới Tier.
type TierHandler struct {
	tierService billingSvcInterface.TierService
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
			"id":              ft.ID.String(),        // Range ID
			"tier_id":         ft.TierID.String(),    // Tier ID gốc
			"name":            ft.Name,
			"code":            ft.Code,
			"service_type":    ft.ServiceType,
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
