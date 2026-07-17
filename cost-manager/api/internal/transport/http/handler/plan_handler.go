package handler

import (
	"context"
	"encoding/base64"
	"strings"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingSvcInterface "cost-manager/api/internal/domain/service"
	"cost-manager/api/internal/transport/http/dto"
	"cost-manager/api/pkg/apires"
	"cost-manager/api/pkg/logger"
	"cost-manager/api/pkg/pkgcontext"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// PlanHandler tiếp nhận và xử lý các HTTP request liên quan tới Plan
type PlanHandler struct {
	planService billingSvcInterface.PlanService // Service xử lý nghiệp vụ Plan ở mức Domain
}

// NewPlanHandler khởi tạo đối tượng PlanHandler
func NewPlanHandler(planService billingSvcInterface.PlanService) *PlanHandler {
	return &PlanHandler{
		planService: planService,
	}
}

// ListPlans tiếp nhận yêu cầu lấy danh sách plans từ HTTP Client
func (h *PlanHandler) ListPlans(c *gin.Context) {
	op := "handler.plan.list"

	// Thiết lập context timeout 5 giây cho các truy vấn DB hoặc cache phía dưới
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// Sử dụng struct DTO được định nghĩa riêng biệt
	var req dto.ListPlansRequest

	// Thực hiện bind tham số từ URL Query string
	if err := c.ShouldBindQuery(&req); err != nil {
		logger.HandlerWarn(c, op, err, "Invalid query parameters binding")
		apires.RespondBadRequest(c, "Invalid query parameters")
		return
	}

	// Đảm bảo các tham số phân trang luôn nằm trong khoảng hợp lệ
	if req.Limit <= 0 {
		req.Limit = 10
	} else if req.Limit > 100 {
		// Ngăn chặn việc kéo quá nhiều dữ liệu gây quá tải hệ thống (HA protection)
		req.Limit = 100
	}

	// Thực hiện validate dữ liệu đầu vào theo yêu cầu ở handler
	if req.ServiceType != "" {
		if req.ServiceType != entity.ServiceTypeStorage &&
			req.ServiceType != entity.ServiceTypeVM &&
			req.ServiceType != entity.ServiceTypeMail {
			logger.HandlerWarn(c, op, nil, "Invalid service_type parameter: "+req.ServiceType)
			apires.RespondBadRequest(c, "Invalid service_type. Supported: STORAGE, VM, MAIL")
			return
		}
	}

	if req.Status != "" {
		if req.Status != entity.PlanStatusActive &&
			req.Status != entity.PlanStatusDeprecated {
			logger.HandlerWarn(c, op, nil, "Invalid status parameter: "+req.Status)
			apires.RespondBadRequest(c, "Invalid status. Supported: ACTIVE, DEPRECATED")
			return
		}
	}

	// Thực hiện giải mã và validate cursor tại handler
	var cursorTime time.Time
	var cursorID uuid.UUID
	if req.Cursor != "" {
		decodedBytes, err := base64.StdEncoding.DecodeString(req.Cursor)
		if err != nil {
			logger.HandlerWarn(c, op, err, "Invalid cursor base64 encoding")
			apires.RespondBadRequest(c, "Invalid cursor format")
			return
		}
		parts := strings.Split(string(decodedBytes), ",")
		if len(parts) != 2 {
			logger.HandlerWarn(c, op, nil, "Invalid cursor format parts")
			apires.RespondBadRequest(c, "Invalid cursor format")
			return
		}
		t, err := time.Parse(time.RFC3339Nano, parts[0])
		if err != nil {
			logger.HandlerWarn(c, op, err, "Invalid cursor timestamp format")
			apires.RespondBadRequest(c, "Invalid cursor format")
			return
		}
		id, err := uuid.Parse(parts[1])
		if err != nil {
			logger.HandlerWarn(c, op, err, "Invalid cursor UUID format")
			apires.RespondBadRequest(c, "Invalid cursor format")
			return
		}
		cursorTime = t
		cursorID = id
	}

	// Thực hiện validate và parse zone_id thành UUID tại handler
	var filterZoneID uuid.UUID
	if req.ZoneID != "" {
		parsedZoneID, err := uuid.Parse(req.ZoneID)
		if err != nil {
			logger.HandlerWarn(c, op, err, "Invalid zone_id parameter: "+req.ZoneID)
			apires.RespondBadRequest(c, "Invalid zone_id format (must be UUID)")
			return
		}
		filterZoneID = parsedZoneID
	}

	// Sử dụng entity.Plan làm điều kiện lọc
	filter := entity.Plan{
		ServiceType: req.ServiceType,
		ZoneID:      filterZoneID,
		Status:      req.Status,
	}

	// Gọi xuống tầng Service để lấy danh sách plans (đã qua caching & singleflight bảo vệ DB)
	plans, nextCursor, err := h.planService.ListPlans(ctx, filter, cursorTime, cursorID, req.Limit)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "Failed to retrieve plans")
		return
	}

	// Chuyển đổi danh sách entities sang slice gin.H inline để định dạng JSON CamelCase theo yêu cầu của user (entity không chứa tag json)
	plansData := make([]gin.H, len(plans))
	for i, p := range plans {
		plansData[i] = gin.H{
			"id":            p.ID,
			"name":          p.Name,
			"code":          p.Code,
			"service_type":  p.ServiceType,
			"zone_id":       p.ZoneID,
			"monthly_price": p.MonthlyPrice,
			"currency":      p.Currency,
			"status":        p.Status,
			"description":   p.Description,
			"created_at":    p.CreatedAt,
			"updated_at":    p.UpdatedAt,
		}
	}

	// Trả về kết quả thông qua gin.H inline func theo cấu trúc yêu cầu của user
	apires.RespondSuccess(c, gin.H{
		"plans":       plansData,
		"limit":       req.Limit,
		"next_cursor": nextCursor,
		"count":       len(plans),
	}, "Successfully retrieved plans")
}
