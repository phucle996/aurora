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

// HypervisorPricingHandler xử lý các HTTP endpoints liên quan đến ước tính giá VM và publish hệ số điều chỉnh giá theo Zone.
// Source of Truth: god_view/billing/billing_hypervisor_zone_price_adjustment_publish_god_view.md
type HypervisorPricingHandler struct {
	service billingSvcInterface.HypervisorPricingService
}

// NewHypervisorPricingHandler khởi tạo handler với estimate service và zone adjustment service.
func NewHypervisorPricingHandler(service billingSvcInterface.HypervisorPricingService) *HypervisorPricingHandler {
	return &HypervisorPricingHandler{service: service}
}

func (h *HypervisorPricingHandler) ListZonePriceAdjustments(c *gin.Context) {
	const op = "handler.hypervisor_pricing.list_zone_adjustments"
	limit := 100
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			apires.RespondBadRequest(c, "limit must be an integer between 1 and 100")
			return
		}
		limit = parsed
	}
	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()
	result, err := h.service.ListHypervisorZonePriceAdjustments(ctx, entity.HypervisorZoneAdjustmentListQuery{ZoneID: zoneID, Limit: limit})
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "failed to retrieve Hypervisor Zone price adjustments")
		return
	}
	items := make([]gin.H, len(result.Items))
	for index, item := range result.Items {
		var effectiveTo *string
		if item.EffectiveTo != nil {
			formatted := item.EffectiveTo.UTC().Format(time.RFC3339Nano)
			effectiveTo = &formatted
		}
		items[index] = gin.H{
			"id": item.ID, "zone_id": item.ZoneID, "version_number": item.VersionNumber, "status": item.Status,
			"effective_from": item.EffectiveFrom.UTC().Format(time.RFC3339Nano), "effective_to": effectiveTo,
			"multiplier_numerator": strconv.FormatInt(item.MultiplierNumerator, 10), "multiplier_denominator": strconv.FormatInt(item.MultiplierDenominator, 10),
			"checksum": item.Checksum, "change_reason": item.ChangeReason, "created_by": item.CreatedBy,
			"created_at": item.CreatedAt.UTC().Format(time.RFC3339Nano), "is_latest": item.IsLatest, "is_effective": item.IsEffective,
		}
	}
	apires.RespondSuccess(c, gin.H{"zone_id": result.ZoneID, "adjustments": items, "has_more": result.HasMore, "observed_at": result.ObservedAt.UTC().Format(time.RFC3339Nano)}, "Hypervisor Zone price adjustments")
}

// Estimate xử lý GET request tính toán trước chi phí theo giờ và theo tháng cho cấu hình VM (cpu_cores, memory_mib, disk_gib).
func (h *HypervisorPricingHandler) Estimate(c *gin.Context) {
	const op = "handler.hypervisor_pricing.estimate"

	// 1. Parse và validate các tham số query bắt buộc
	cpu, cpuErr := strconv.ParseInt(strings.TrimSpace(c.Query("cpu_cores")), 10, 64)
	memory, memoryErr := strconv.ParseInt(strings.TrimSpace(c.Query("memory_mib")), 10, 64)
	disk, diskErr := strconv.ParseInt(strings.TrimSpace(c.Query("disk_gib")), 10, 64)

	if cpuErr != nil || memoryErr != nil || diskErr != nil ||
		cpu < 1 || cpu > 1024 ||
		memory < 1 || memory > 4_194_304 ||
		disk < 1 || disk > 1_048_576 {
		apires.RespondBadRequest(c, "cpu_cores, memory_mib and disk_gib must be bounded positive decimal integers")
		return
	}

	// 2. Lấy Zone ID từ context đã được ACR/middleware xác thực
	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 2*time.Second)
	defer cancel()

	// 3. Gọi domain service để tính toán chi phí
	estimate, err := h.service.EstimateHypervisor(ctx, cpu, memory, disk, zoneID)
	if err != nil {
		if errors.Is(err, billingTaxonomy.ErrInvalidArgument) || errors.Is(err, billingTaxonomy.ErrInvalidPricingBrackets) {
			apires.RespondBadRequest(c, "invalid Hypervisor estimate request")
		} else {
			logger.HandlerError(c, op, err)
			apires.RespondServiceUnavailable(c, "Hypervisor pricing is not available")
		}
		return
	}

	// 4. Trả về kết quả ước tính chi phí kèm thông tin lineage phiên bản bảng giá
	apires.RespondSuccess(c, gin.H{
		// Cấu hình phần cứng
		"cpu_cores":  strconv.FormatInt(estimate.CPUCores, 10),
		"memory_mib": strconv.FormatInt(estimate.MemoryMIB, 10),
		"disk_gib":   strconv.FormatInt(estimate.DiskGIB, 10),

		// Chi phí ước tính
		"vcpu_hourly_micro_units":               strconv.FormatInt(estimate.VCPUHourlyMicroUnits, 10),
		"memory_hourly_micro_units":             strconv.FormatInt(estimate.MemoryHourlyMicroUnits, 10),
		"disk_hourly_micro_units":               strconv.FormatInt(estimate.DiskHourlyMicroUnits, 10),
		"hourly_estimate_micro_units":           strconv.FormatInt(estimate.HourlyMicroUnits, 10),
		"monthly_730_hour_estimate_micro_units": strconv.FormatInt(estimate.Monthly730HourMicroUnits, 10),
		"currency":                              estimate.Currency,

		// Thông tin schedule vCPU
		"vcpu_schedule_code":       estimate.VCPUScheduleCode,
		"vcpu_schedule_id":         estimate.VCPUScheduleID,
		"vcpu_schedule_version_id": estimate.VCPUScheduleVersionID,
		"vcpu_version":             estimate.VCPUVersion,
		"vcpu_checksum":            estimate.VCPUChecksum,

		// Thông tin schedule Memory
		"memory_schedule_code":       estimate.MemoryScheduleCode,
		"memory_schedule_id":         estimate.MemoryScheduleID,
		"memory_schedule_version_id": estimate.MemoryScheduleVersionID,
		"memory_version":             estimate.MemoryVersion,
		"memory_checksum":            estimate.MemoryChecksum,

		// Thông tin schedule Disk
		"disk_schedule_code":       estimate.DiskScheduleCode,
		"disk_schedule_id":         estimate.DiskScheduleID,
		"disk_schedule_version_id": estimate.DiskScheduleVersionID,
		"disk_version":             estimate.DiskVersion,
		"disk_checksum":            estimate.DiskChecksum,

		// Hệ số điều chỉnh giá theo Zone
		"rate_adjustment_id":          estimate.RateAdjustmentID,
		"rate_adjustment_version":     estimate.RateAdjustmentVersion,
		"rate_adjustment_checksum":    estimate.RateAdjustmentChecksum,
		"rate_adjustment_numerator":   strconv.FormatInt(estimate.RateAdjustmentNumerator, 10),
		"rate_adjustment_denominator": strconv.FormatInt(estimate.RateAdjustmentDenominator, 10),
		"estimated_at":                estimate.EstimatedAt.UTC().Format(time.RFC3339Nano),
	}, "Hypervisor estimate")
}

func (h *HypervisorPricingHandler) CreateBasePriceVersion(c *gin.Context) {
	const op = "handler.hypervisor_pricing.create_base_version"
	actor, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	var req dto.CreateHypervisorBasePriceVersionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apires.RespondBadRequest(c, "invalid Hypervisor base price version payload")
		return
	}
	expectedLatestVersion, err := strconv.ParseInt(req.ExpectedLatestVersion.String(), 10, 32)
	if err != nil || expectedLatestVersion < 0 {
		apires.RespondBadRequest(c, "expected_latest_version must be a non-negative integer")
		return
	}
	brackets := make([]entity.HypervisorBasePriceBracketCommand, len(req.Brackets))
	for index, bracket := range req.Brackets {
		start, err := strconv.ParseInt(strings.TrimSpace(bracket.RangeStartQuantity), 10, 64)
		if err != nil {
			apires.RespondBadRequest(c, "Hypervisor price bracket BIGINT fields must be decimal strings within int64 range")
			return
		}
		var end *int64
		if bracket.RangeEndQuantity != nil {
			value, err := strconv.ParseInt(strings.TrimSpace(*bracket.RangeEndQuantity), 10, 64)
			if err != nil {
				apires.RespondBadRequest(c, "Hypervisor price bracket BIGINT fields must be decimal strings within int64 range")
				return
			}
			end = &value
		}
		numerator, numeratorErr := strconv.ParseInt(strings.TrimSpace(bracket.PriceNumeratorMicroUnits), 10, 64)
		denominator, denominatorErr := strconv.ParseInt(strings.TrimSpace(bracket.PriceDenominatorQuantity), 10, 64)
		if numeratorErr != nil || denominatorErr != nil {
			apires.RespondBadRequest(c, "Hypervisor price bracket BIGINT fields must be decimal strings within int64 range")
			return
		}
		brackets[index] = entity.HypervisorBasePriceBracketCommand{RangeStartQuantity: start, RangeEndQuantity: end, PriceNumeratorMicroUnits: numerator, PriceDenominatorQuantity: denominator}
	}
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()
	published, err := h.service.CreateHypervisorBasePriceVersion(ctx, entity.HypervisorBasePricePublishCommand{ScheduleCode: strings.TrimSpace(c.Param("code")), ExpectedLatestVersion: int(expectedLatestVersion), EffectiveFrom: req.EffectiveFrom, ChangeReason: req.ChangeReason, CreatedBy: actor}, brackets)
	if err != nil {
		switch {
		case errors.Is(err, billingTaxonomy.ErrPricingScheduleNotFound):
			apires.RespondNotFound(c, "HYPERVISOR_PRICING_SCHEDULE_NOT_FOUND")
		case errors.Is(err, billingTaxonomy.ErrPricingScheduleVersionConflict), errors.Is(err, billingTaxonomy.ErrPricingScheduleEffectiveConflict):
			apires.RespondConflict(c, "HYPERVISOR_PRICING_SCHEDULE_VERSION_CONFLICT")
		case errors.Is(err, billingTaxonomy.ErrInvalidArgument), errors.Is(err, billingTaxonomy.ErrInvalidPricingBrackets):
			apires.RespondBadRequest(c, "invalid Hypervisor base price version")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "failed to publish Hypervisor base price version")
		}
		return
	}
	apires.RespondCreated(c, gin.H{"id": published.ID, "pricing_schedule_id": published.PricingScheduleID, "charge_kind_code": published.ChargeKindCode, "version_number": published.VersionNumber, "pricing_model": published.PricingModel, "status": published.Status, "effective_from": published.EffectiveFrom.UTC().Format(time.RFC3339Nano), "effective_to": nil, "checksum": published.Checksum}, "Hypervisor base price version published")
}

// CreateZonePriceAdjustment xử lý POST request từ Operator để publish phiên bản hệ số điều chỉnh giá mới cho một Zone.
// Source of Truth: god_view/billing/billing_hypervisor_zone_price_adjustment_publish_god_view.md
func (h *HypervisorPricingHandler) CreateZonePriceAdjustment(c *gin.Context) {
	const op = "handler.hypervisor_pricing.create_zone_adjustment"

	// 1. Trích xuất Actor ID và Zone ID từ trusted headers do ACR inject
	actor, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	zoneID, ok := pkgcontext.GetZoneID(c, op)
	if !ok {
		return
	}

	// 2. Parse và validate JSON request body
	var req dto.CreateHypervisorZonePriceAdjustmentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apires.RespondBadRequest(c, "invalid Hypervisor Zone price adjustment payload")
		return
	}
	expectedLatestVersion, err := strconv.ParseInt(req.ExpectedLatestVersion.String(), 10, 32)
	if err != nil || expectedLatestVersion < 0 {
		apires.RespondBadRequest(c, "expected_latest_version must be a non-negative integer")
		return
	}

	numerator, numeratorErr := strconv.ParseInt(strings.TrimSpace(req.MultiplierNumerator), 10, 64)
	denominator, denominatorErr := strconv.ParseInt(strings.TrimSpace(req.MultiplierDenominator), 10, 64)
	if numeratorErr != nil || denominatorErr != nil {
		apires.RespondBadRequest(c, "multiplier BIGINT fields must be decimal strings within int64 range")
		return
	}

	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// 3. Thực thi nghiệp vụ publish hệ số điều chỉnh giá bất biến
	created, err := h.service.CreateHypervisorZonePriceAdjustment(ctx, entity.HypervisorZoneAdjustmentPublishCommand{
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
		case errors.Is(err, billingTaxonomy.ErrHypervisorZoneAdjustmentConflict):
			apires.RespondConflict(c, "HYPERVISOR_ZONE_PRICE_ADJUSTMENT_VERSION_CONFLICT")
		case errors.Is(err, billingTaxonomy.ErrInvalidArgument):
			apires.RespondBadRequest(c, "invalid Hypervisor Zone price adjustment")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "failed to publish Hypervisor Zone price adjustment")
		}
		return
	}

	// 4. Phản hồi HTTP 201 Created kèm dữ liệu bản ghi vừa tạo
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
	}, "Hypervisor Zone price adjustment published")
}
