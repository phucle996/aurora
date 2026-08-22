package handler

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingSvcInterface "cost-manager/api/internal/domain/service"
	billingTaxonomy "cost-manager/api/internal/taxonomy"
	"cost-manager/api/internal/transport/http/dto"
	"cost-manager/api/pkg/apires"
	"cost-manager/api/pkg/logger"
	"cost-manager/api/pkg/pkgcontext"

	"github.com/gin-gonic/gin"
)

// MailPricingHandler là HTTP Handler tiếp nhận và xử lý toàn bộ các REST API requests
// liên quan đến nghiệp vụ định giá dịch vụ Email (Aurora Mail Service):
// 1. GET  /api/v1/billing/mail/pricing/zones/:zone_id/adjustments : Tra cứu lịch sử hệ số giá theo khu vực
// 2. GET  /api/v1/billing/mail/pricing/estimate                   : Dự toán chi phí gửi mail theo số lượng người nhận
// 3. POST /api/v1/billing/mail/pricing/schedules/:code/versions   : Ban hành phiên bản bảng giá gốc (Base Price Publishing)
// 4. POST /api/v1/billing/mail/pricing/zones/:zone_id/adjustments : Ban hành hệ số điều chỉnh giá theo khu vực (Zone Adjustment)
type MailPricingHandler struct {
	service billingSvcInterface.MailPricingService
}

// NewMailPricingHandler khởi tạo MailPricingHandler với MailPricingService domain dependency.
func NewMailPricingHandler(service billingSvcInterface.MailPricingService) *MailPricingHandler {
	return &MailPricingHandler{service: service}
}

// ============================================================================
// 1. ENDPOINT: TRA CỨU HỆ SỐ ĐIỀU CHỈNH GIÁ THEO KHU VỰC (ZONE ADJUSTMENTS)
// ============================================================================

// ListZonePriceAdjustments xử lý GET request trả về danh sách lịch sử các phiên bản hệ số điều chỉnh giá
// tại một trung tâm dữ liệu / Zone cụ thể của dịch vụ Mail.
//
// Quy trình xử lý:
//   - Bước 1: Trích xuất và kiểm tra tham số phân trang `limit` (mặc định 100, giới hạn từ 1 đến 100).
//   - Bước 2: Lấy `zoneID` đã được xác thực từ Header/Token qua tầng bảo mật ACR.
//   - Bước 3: Thiết lập Timeout 5 giây và gọi Domain Service truy vấn PostgreSQL.
//   - Bước 4: Chuyển đổi dữ liệu sang DTO JSON, chuẩn hóa thời gian ISO 8601 (RFC3339Nano) và
//     ép kiểu các số nguyên lớn BIGINT (64-bit) thành chuỗi String để chống mất độ chính xác khi truyền qua JS Frontend.
func (h *MailPricingHandler) ListZonePriceAdjustments(c *gin.Context) {
	const op = "handler.mail_pricing.list_zone_adjustments"

	// 1. Parse tham số phân trang limit
	limit := 100
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			apires.RespondBadRequest(c, "limit must be an integer between 1 and 100")
			return
		}
		limit = parsed
	}

	// 2. Lấy Zone ID từ context được ACR / Envoy truyền vào
	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}

	// 3. Gọi service với context có gắn deadline 5s
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	result, err := h.service.ListMailZonePriceAdjustments(ctx, entity.MailZoneAdjustmentListQuery{ZoneID: zoneID, Limit: limit})
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "failed to retrieve Mail Zone price adjustments")
		return
	}

	// 4. Map kết quả sang JSON response (chuyển số BIGINT thành decimal string)
	items := make([]gin.H, len(result.Items))
	for index, item := range result.Items {
		var effectiveTo *string
		if item.EffectiveTo != nil {
			formatted := item.EffectiveTo.UTC().Format(time.RFC3339Nano)
			effectiveTo = &formatted
		}
		items[index] = gin.H{
			"id":                     item.ID,
			"zone_id":                item.ZoneID,
			"version_number":         item.VersionNumber,
			"status":                 item.Status,
			"effective_from":         item.EffectiveFrom.UTC().Format(time.RFC3339Nano),
			"effective_to":           effectiveTo,
			"multiplier_numerator":   strconv.FormatInt(item.MultiplierNumerator, 10),
			"multiplier_denominator": strconv.FormatInt(item.MultiplierDenominator, 10),
			"checksum":               item.Checksum,
			"change_reason":          item.ChangeReason,
			"created_by":             item.CreatedBy,
			"created_at":             item.CreatedAt.UTC().Format(time.RFC3339Nano),
			"is_latest":              item.IsLatest,
			"is_effective":           item.IsEffective,
		}
	}

	apires.RespondSuccess(c, gin.H{
		"zone_id":     result.ZoneID,
		"adjustments": items,
		"has_more":    result.HasMore,
		"observed_at": result.ObservedAt.UTC().Format(time.RFC3339Nano),
	}, "Mail Zone price adjustments")
}

// ============================================================================
// 2. ENDPOINT: DỰ TOÁN CHI PHÍ GỬI EMAIL (ESTIMATE)
// ============================================================================

// Estimate xử lý GET request tính toán trước số tiền dự kiến khi gửi một số lượng email (recipient_quantity) tại một Zone.
//
// Đơn vị tính:
// - Đơn vị tính giá: `RECIPIENT` (tính trên mỗi người nhận thư được chấp nhận).
// - Đơn vị tiền tệ: `micro-units` ($1\text{ USD} = 1,000,000\text{ micro-units}$).
// - Áp dụng bậc thang giá luỹ tiến (Progressive Brackets) và hệ số khu vực (Zone Multiplier).
//
// Quy trình xử lý:
// - Bước 1: Validate số lượng người nhận: phải là số nguyên dương từ 1 đến 1,000,000,000 recipients.
// - Bước 2: Lấy ZoneID từ context xác thực.
// - Bước 3: Gọi Domain Service (được tăng tốc bởi Cache L1 RAM -> L2 Redis Protobuf -> PostgreSQL).
// - Bước 4: Trả về chi tiết dự toán kèm mã kiểm tra toàn vẹn SHA-256 Checksum của bảng giá.
func (h *MailPricingHandler) Estimate(c *gin.Context) {
	const op = "handler.mail_pricing.estimate"

	// 1. Kiểm tra tham số recipient_quantity trong query string
	quantity, err := strconv.ParseInt(strings.TrimSpace(c.Query("recipient_quantity")), 10, 64)
	if err != nil || quantity < 1 || quantity > 1_000_000_000 {
		apires.RespondBadRequest(c, "recipient_quantity must be a positive decimal integer no larger than 1000000000")
		return
	}

	// 2. Lấy Zone ID từ context
	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}

	// 3. Thiết lập timeout nhanh 2s cho luồng dự toán trực tuyến
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 2*time.Second)
	defer cancel()

	// 4. Tính toán chi phí qua domain service
	estimate, err := h.service.EstimateMail(ctx, quantity, zoneID)
	if err != nil {
		if errors.Is(err, billingTaxonomy.ErrInvalidArgument) || errors.Is(err, billingTaxonomy.ErrInvalidPricingBrackets) {
			apires.RespondBadRequest(c, "invalid Mail estimate request")
		} else {
			logger.HandlerError(c, op, err)
			apires.RespondServiceUnavailable(c, "Mail pricing is not available")
		}
		return
	}

	// 5. Trả về kết quả dự toán chi tiết
	apires.RespondSuccess(c, gin.H{
		"recipient_quantity":          strconv.FormatInt(estimate.RecipientQuantity, 10),
		"estimate_micro_units":        strconv.FormatInt(estimate.EstimateMicroUnits, 10),
		"currency":                    estimate.Currency,
		"pricing_schedule_code":       estimate.PricingScheduleCode,
		"pricing_schedule_id":         estimate.PricingScheduleID,
		"pricing_schedule_version_id": estimate.PricingScheduleVersionID,
		"pricing_version":             estimate.PricingVersion,
		"pricing_checksum":            estimate.PricingChecksum,
		"pricing_effective_from":      estimate.PricingEffectiveFrom.UTC().Format(time.RFC3339Nano),
		"rate_adjustment_id":          estimate.RateAdjustmentID,
		"rate_adjustment_version":     estimate.RateAdjustmentVersion,
		"rate_adjustment_checksum":    estimate.RateAdjustmentChecksum,
		"rate_adjustment_numerator":   strconv.FormatInt(estimate.RateAdjustmentNumerator, 10),
		"rate_adjustment_denominator": strconv.FormatInt(estimate.RateAdjustmentDenominator, 10),
		"estimated_at":                estimate.EstimatedAt.UTC().Format(time.RFC3339Nano),
	}, "Mail accepted-recipient estimate")
}

// ============================================================================
// 3. ENDPOINT: BAN HÀNH PHIÊN BẢN BẢNG GIÁ GỐC (PUBLISH BASE PRICE VERSION)
// ============================================================================

// CreateBasePriceVersion xử lý POST request ban hành một phiên bản bảng giá gốc mới cho dịch vụ Mail.
//
// Quy tắc nghiệp vụ (Business Invariants):
// - Yêu cầu quyền Quản trị viên (Platform Admin).
// - Kiểm soát xung đột phiên bản Optimistic Concurrency Control (OCC) qua `expected_latest_version`.
// - Bảng giá bậc thang (Brackets) phải liên tục từ 0 đến vô cực $[0, \infty)$, không có khoảng trống (Gap) hoặc chồng lấn (Overlap).
// - Tính toán mã niêm phong SHA-256 Checksum ngay khi commit vào DB và phát sóng Outbox Event sang Redis.
func (h *MailPricingHandler) CreateBasePriceVersion(c *gin.Context) {
	const op = "handler.mail_pricing.create_base_version"

	// 1. Xác thực danh tính Admin (Actor User ID)
	actor, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	// 2. Parse và validate cấu trúc Body JSON
	var req dto.CreateMailBasePriceVersionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apires.RespondBadRequest(c, "invalid Mail base price version payload")
		return
	}
	expectedLatestVersion, err := strconv.ParseInt(req.ExpectedLatestVersion.String(), 10, 32)
	if err != nil || expectedLatestVersion < 0 {
		apires.RespondBadRequest(c, "expected_latest_version must be a non-negative integer")
		return
	}

	// 3. Chuyển đổi các trường BIGINT từ chuỗi String sang int64 an toàn
	brackets := make([]entity.MailBasePriceBracketCommand, len(req.Brackets))
	for index, bracket := range req.Brackets {
		start, err := strconv.ParseInt(strings.TrimSpace(bracket.RangeStartQuantity), 10, 64)
		if err != nil {
			apires.RespondBadRequest(c, "Mail price bracket BIGINT fields must be decimal strings within int64 range")
			return
		}
		var end *int64
		if bracket.RangeEndQuantity != nil {
			value, err := strconv.ParseInt(strings.TrimSpace(*bracket.RangeEndQuantity), 10, 64)
			if err != nil {
				apires.RespondBadRequest(c, "Mail price bracket BIGINT fields must be decimal strings within int64 range")
				return
			}
			end = &value
		}
		numerator, numeratorErr := strconv.ParseInt(strings.TrimSpace(bracket.PriceNumeratorMicroUnits), 10, 64)
		denominator, denominatorErr := strconv.ParseInt(strings.TrimSpace(bracket.PriceDenominatorQuantity), 10, 64)
		if numeratorErr != nil || denominatorErr != nil {
			apires.RespondBadRequest(c, "Mail price bracket BIGINT fields must be decimal strings within int64 range")
			return
		}
		brackets[index] = entity.MailBasePriceBracketCommand{
			RangeStartQuantity:       start,
			RangeEndQuantity:         end,
			PriceNumeratorMicroUnits: numerator,
			PriceDenominatorQuantity: denominator,
		}
	}

	// 4. Thiết lập deadline 5s và gọi domain service thực hiện transaction DB
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	published, err := h.service.CreateMailBasePriceVersion(ctx, entity.MailBasePricePublishCommand{
		ScheduleCode:          strings.TrimSpace(c.Param("code")),
		ExpectedLatestVersion: int(expectedLatestVersion),
		EffectiveFrom:         req.EffectiveFrom,
		ChangeReason:          req.ChangeReason,
		CreatedBy:             actor,
	}, brackets)
	if err != nil {
		switch {
		case errors.Is(err, billingTaxonomy.ErrPricingScheduleNotFound):
			apires.RespondNotFound(c, "MAIL_PRICING_SCHEDULE_NOT_FOUND")
		case errors.Is(err, billingTaxonomy.ErrPricingScheduleVersionConflict), errors.Is(err, billingTaxonomy.ErrPricingScheduleEffectiveConflict):
			apires.RespondConflict(c, "MAIL_PRICING_SCHEDULE_VERSION_CONFLICT")
		case errors.Is(err, billingTaxonomy.ErrInvalidArgument), errors.Is(err, billingTaxonomy.ErrInvalidPricingBrackets):
			apires.RespondBadRequest(c, "invalid Mail base price version")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "failed to publish Mail base price version")
		}
		return
	}

	// 5. Trả về thông tin phiên bản mới vừa được tạo thành công (HTTP 201 Created)
	apires.RespondCreated(c, gin.H{
		"id":                  published.ID,
		"pricing_schedule_id": published.PricingScheduleID,
		"charge_kind_code":    published.ChargeKindCode,
		"version_number":      published.VersionNumber,
		"pricing_model":       published.PricingModel,
		"status":              published.Status,
		"effective_from":      published.EffectiveFrom.UTC().Format(time.RFC3339Nano),
		"effective_to":        nil,
		"checksum":            published.Checksum,
	}, "Mail base price version published")
}

// ============================================================================
// 4. ENDPOINT: BAN HÀNH HỆ SỐ ĐIỀU CHỈNH GIÁ THEO KHU VỰC (PUBLISH ZONE ADJUSTMENT)
// ============================================================================

// CreateZonePriceAdjustment xử lý POST request cập nhật hệ số nhân giá theo khu vực cho dịch vụ Mail.
//
// Ví dụ:
// - Khu vực Hà Nội / TP.HCM có chi phí vận hành tiêu chuẩn: Hệ số $1/1$ (Tử số = 1, Mẫu số = 1).
// - Khu vực đặc thù / Quốc tế có chi phí cao hơn 20%: Hệ số $6/5$ (Tử số = 6, Mẫu số = 5).
//
// Quy trình:
// - Kiểm tra định dạng phân số nguyên (numerator > 0, denominator > 0).
// - Ghi nhận nguyên tử vào DB, tính SHA-256 Checksum, đồng thời phát sóng xóa Cache L2 trên toàn bộ cụm Redis.
func (h *MailPricingHandler) CreateZonePriceAdjustment(c *gin.Context) {
	const op = "handler.mail_pricing.create_zone_adjustment"

	// 1. Xác thực Actor Admin và ZoneID
	actor, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}

	// 2. Parse body JSON
	var req dto.CreateMailZonePriceAdjustmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apires.RespondBadRequest(c, "invalid Mail Zone price adjustment payload")
		return
	}
	expectedLatestVersion, err := strconv.ParseInt(req.ExpectedLatestVersion.String(), 10, 32)
	if err != nil || expectedLatestVersion < 0 {
		apires.RespondBadRequest(c, "expected_latest_version must be a non-negative integer")
		return
	}

	// 3. Parse và kiểm tra tử số, mẫu số của phân số hệ số nhân
	numerator, numeratorErr := strconv.ParseInt(strings.TrimSpace(req.MultiplierNumerator), 10, 64)
	denominator, denominatorErr := strconv.ParseInt(strings.TrimSpace(req.MultiplierDenominator), 10, 64)
	if numeratorErr != nil || denominatorErr != nil {
		apires.RespondBadRequest(c, "multiplier BIGINT fields must be decimal strings within int64 range")
		return
	}

	// 4. Gọi domain service với timeout 5s
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	created, err := h.service.CreateMailZonePriceAdjustment(ctx, entity.MailZoneAdjustmentPublishCommand{
		ZoneID:                zoneID,
		ExpectedLatestVersion: int(expectedLatestVersion),
		EffectiveFrom:         req.EffectiveFrom,
		ChangeReason:          req.ChangeReason,
		CreatedBy:             actor,
		MultiplierNumerator:   numerator,
		MultiplierDenominator: denominator,
	})
	if err != nil {
		switch {
		case errors.Is(err, billingTaxonomy.ErrMailZoneAdjustmentConflict):
			apires.RespondConflict(c, "MAIL_ZONE_PRICE_ADJUSTMENT_VERSION_CONFLICT")
		case errors.Is(err, billingTaxonomy.ErrInvalidArgument):
			apires.RespondBadRequest(c, "invalid Mail Zone price adjustment")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "failed to publish Mail Zone price adjustment")
		}
		return
	}

	// 5. Phản hồi thành công HTTP 201 Created
	apires.RespondCreated(c, gin.H{
		"id":                     created.ID,
		"zone_id":                created.ZoneID,
		"version_number":         created.VersionNumber,
		"status":                 created.Status,
		"effective_from":         created.EffectiveFrom.UTC().Format(time.RFC3339Nano),
		"effective_to":           nil,
		"multiplier_numerator":   strconv.FormatInt(created.MultiplierNumerator, 10),
		"multiplier_denominator": strconv.FormatInt(created.MultiplierDenominator, 10),
		"checksum":               created.Checksum,
	}, "Mail Zone price adjustment published")
}
