package handler

import (
	"context"
	"errors"
	"regexp"
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

// [COMMENT]: validCodeRegex đại diện cho canonical Regex format của Tier Code: bắt đầu bằng chữ hoa, tiếp theo là chữ hoa, số hoặc dấu gạch dưới, độ dài tối đa 64 ký tự.
var validCodeRegex = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

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

// GetTierDetail trả full latest snapshot để Edit không dựa trên flat pagination.
func (h *TierHandler) GetTierDetail(c *gin.Context) {
	op := "handler.tier.get_detail"

	// 1. Parse service_type từ path param trực tiếp
	stRaw := strings.TrimSpace(c.Param("service_type"))
	var parsedServiceType entity.ServiceType
	switch entity.ServiceType(stRaw) {
	case entity.ServiceTypeStorage, entity.ServiceTypeNetworkIn, entity.ServiceTypeNetworkOut, entity.ServiceTypeVM:
		parsedServiceType = entity.ServiceType(stRaw)
	default:
		logger.HandlerWarn(c, op, nil, "Invalid service_type path parameter: "+stRaw)
		apires.RespondBadRequest(c, "INVALID_SERVICE_TYPE")
		return
	}

	// 2. Trim & validate code từ path param
	code := strings.TrimSpace(c.Param("code"))
	if !validCodeRegex.MatchString(code) {
		logger.HandlerWarn(c, op, nil, "Invalid tier code path parameter format: "+code)
		apires.RespondBadRequest(c, "INVALID_TIER_CODE")
		return
	}

	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	detail, err := h.tierService.GetTierDetail(ctx, code, parsedServiceType)
	if err != nil {
		// [COMMENT]: Xử lý lỗi trả về cho client
		switch {
		case errors.Is(err, billingTaxonomy.ErrTierNotFound):
			apires.RespondNotFound(c, "TIER_NOT_FOUND")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "Failed to retrieve tier")
		}
		return
	}

	// [COMMENT]: Viết inline response chuyển đổi từ struct entity.TierVersion sang gin.H
	latestVersionRanges := make([]gin.H, len(detail.LatestVersion.Ranges))
	for i, tierRange := range detail.LatestVersion.Ranges {
		latestVersionRanges[i] = gin.H{
			"id":              tierRange.ID.String(),
			"range_start":     tierRange.RangeStart,
			"range_end":       tierRange.RangeEnd,
			"base_unit_price": tierRange.BaseUnitPrice,
		}
	}
	latestVersionObj := gin.H{
		"id":             detail.LatestVersion.ID.String(),
		"tier_id":        detail.LatestVersion.TierID.String(),
		"version_number": detail.LatestVersion.VersionNumber,
		"status":         detail.LatestVersion.Status,
		"effective_from": detail.LatestVersion.EffectiveFrom,
		"effective_to":   detail.LatestVersion.EffectiveTo,
		"checksum":       detail.LatestVersion.Checksum,
		"ranges":         latestVersionRanges,
	}

	apires.RespondSuccess(c, gin.H{
		"id":               detail.ID.String(),
		"code":             detail.Code,
		"service_type":     detail.ServiceType,
		"name":             detail.Name,
		"metadata_version": detail.MetadataVersion,
		"latest_version":   latestVersionObj,
	}, "Successfully retrieved tier detail")
}

// EstimateStorage calculates a read-only capacity estimate from Cost's effective pricing snapshot.
func (h *TierHandler) EstimateStorage(c *gin.Context) {
	const op = "handler.tier.estimate_storage"
	rawCapacity := strings.TrimSpace(c.Query("capacity_bytes"))
	capacityBytes, err := strconv.ParseInt(rawCapacity, 10, 64)
	if err != nil || capacityBytes <= 0 || capacityBytes > 1<<60 {
		apires.RespondBadRequest(c, "capacity_bytes must be a positive integer no larger than 1<<60")
		return
	}

	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 2*time.Second)
	defer cancel()
	estimate, err := h.tierService.EstimateStorage(ctx, capacityBytes)
	if err != nil {
		switch {
		case errors.Is(err, billingTaxonomy.ErrInvalidArgument), errors.Is(err, billingTaxonomy.ErrInvalidTierRanges):
			apires.RespondBadRequest(c, "invalid storage estimate request")
		case errors.Is(err, billingTaxonomy.ErrTierNotFound):
			apires.RespondServiceUnavailable(c, "storage pricing is not available")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondServiceUnavailable(c, "storage estimate is temporarily unavailable")
		}
		return
	}

	apires.RespondSuccess(c, gin.H{
		"capacity_bytes":              strconv.FormatInt(estimate.CapacityBytes, 10),
		"hourly_estimate_micro_units": strconv.FormatInt(estimate.HourlyMicroUnits, 10),
		"currency":                    estimate.Currency,
		"tier_code":                   estimate.TierCode,
		"tier_id":                     estimate.TierID.String(),
		"tier_version_id":             estimate.TierVersionID.String(),
		"pricing_version":             estimate.PricingVersion,
		"pricing_checksum":            estimate.PricingChecksum,
		"pricing_effective_from":      estimate.PricingEffectiveFrom,
		"estimated_at":                estimate.EstimatedAt,
	}, "storage estimate")
}

// UpdateTierMetadata chỉ sửa display name và không phát pricing event.
func (h *TierHandler) UpdateTierMetadata(c *gin.Context) {
	op := "handler.tier.update_metadata"

	// 1. Parse service_type từ path param trực tiếp
	stRaw := strings.TrimSpace(c.Param("service_type"))
	var parsedServiceType entity.ServiceType
	switch entity.ServiceType(stRaw) {
	case entity.ServiceTypeStorage, entity.ServiceTypeNetworkIn, entity.ServiceTypeNetworkOut, entity.ServiceTypeVM:
		parsedServiceType = entity.ServiceType(stRaw)
	default:
		logger.HandlerWarn(c, op, nil, "Invalid service_type path parameter: "+stRaw)
		apires.RespondBadRequest(c, "INVALID_SERVICE_TYPE")
		return
	}

	// 2. Trim & validate code từ path param
	code := strings.TrimSpace(c.Param("code"))
	if !validCodeRegex.MatchString(code) {
		logger.HandlerWarn(c, op, nil, "Invalid tier code path parameter format: "+code)
		apires.RespondBadRequest(c, "INVALID_TIER_CODE")
		return
	}

	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// 4. Bind JSON body
	var req dto.UpdateTierMetadataRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.HandlerWarn(c, op, err, "Invalid tier metadata payload")
		apires.RespondBadRequest(c, "Invalid tier metadata payload")
		return
	}

	// 5. Gọi service/repository
	updated, err := h.tierService.UpdateTierMetadata(ctx, entity.TierMetadataUpdate{
		Code: code, ServiceType: parsedServiceType, MetadataVersion: req.MetadataVersion, Name: req.Name,
	})
	if err != nil {
		// [COMMENT]: Viết inline logic xử lý lỗi mutation
		switch {
		case errors.Is(err, billingTaxonomy.ErrTierNotFound):
			apires.RespondNotFound(c, "TIER_NOT_FOUND")
		case errors.Is(err, billingTaxonomy.ErrTierMetadataConflict):
			apires.RespondConflict(c, "TIER_VERSION_CONFLICT")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "Failed to mutate tier")
		}
		return
	}
	apires.RespondSuccess(c, gin.H{
		"id": updated.ID.String(), "code": updated.Code, "service_type": updated.ServiceType,
		"metadata_version": updated.MetadataVersion, "name": updated.Name, "updated_at": updated.UpdatedAt,
	}, "Successfully updated tier metadata")
}

// CreateTierVersion append immutable pricing snapshot và transactional outbox.
func (h *TierHandler) CreateTierVersion(c *gin.Context) {
	op := "handler.tier.create_version"

	// 1. Parse service_type từ path param trực tiếp
	stRaw := strings.TrimSpace(c.Param("service_type"))
	var parsedServiceType entity.ServiceType
	switch entity.ServiceType(stRaw) {
	case entity.ServiceTypeStorage, entity.ServiceTypeNetworkIn, entity.ServiceTypeNetworkOut, entity.ServiceTypeVM:
		parsedServiceType = entity.ServiceType(stRaw)
	default:
		logger.HandlerWarn(c, op, nil, "Invalid service_type path parameter: "+stRaw)
		apires.RespondBadRequest(c, "INVALID_SERVICE_TYPE")
		return
	}

	// 2. Trim & validate code từ path param
	code := strings.TrimSpace(c.Param("code"))
	if !validCodeRegex.MatchString(code) {
		logger.HandlerWarn(c, op, nil, "Invalid tier code path parameter format: "+code)
		apires.RespondBadRequest(c, "INVALID_TIER_CODE")
		return
	}

	// [COMMENT]: Actor UUID lấy từ context đã parse bởi middleware, không parse lại header client.
	actorID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// 4. Bind JSON body
	var req dto.CreateTierVersionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		logger.HandlerWarn(c, op, err, "Invalid tier pricing version payload")
		apires.RespondBadRequest(c, "Invalid tier pricing version payload")
		return
	}
	ranges := make([]entity.TierRangeInput, len(req.Ranges))
	for i, input := range req.Ranges {
		ranges[i] = entity.TierRangeInput{
			RangeStart: input.RangeStart, RangeEnd: input.RangeEnd, BaseUnitPrice: input.BaseUnitPrice,
		}
	}

	// 5. Gọi service/repository
	created, err := h.tierService.CreateTierVersion(ctx, entity.TierVersionCreate{
		Code: code, ServiceType: parsedServiceType, ExpectedLatestVersion: req.ExpectedLatestVersion,
		EffectiveFrom: req.EffectiveFrom, ChangeReason: req.ChangeReason, CreatedBy: actorID, Ranges: ranges,
	})
	if err != nil {
		// [COMMENT]: Viết inline logic xử lý lỗi mutation
		switch {
		case errors.Is(err, billingTaxonomy.ErrTierNotFound):
			apires.RespondNotFound(c, "TIER_NOT_FOUND")
		case errors.Is(err, billingTaxonomy.ErrTierVersionConflict):
			apires.RespondConflict(c, "TIER_VERSION_CONFLICT")
		case errors.Is(err, billingTaxonomy.ErrTierEffectiveConflict):
			apires.RespondConflict(c, "Tier effective time conflicts with pricing history")
		case errors.Is(err, billingTaxonomy.ErrInvalidArgument), errors.Is(err, billingTaxonomy.ErrInvalidTierRanges):
			apires.RespondBadRequest(c, "Invalid tier or range configuration")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "Failed to mutate tier")
		}
		return
	}

	// [COMMENT]: Viết inline response chuyển đổi từ struct entity.TierVersion sang gin.H
	createdRanges := make([]gin.H, len(created.Ranges))
	for i, tierRange := range created.Ranges {
		createdRanges[i] = gin.H{
			"id":              tierRange.ID.String(),
			"range_start":     tierRange.RangeStart,
			"range_end":       tierRange.RangeEnd,
			"base_unit_price": tierRange.BaseUnitPrice,
		}
	}
	createdVersionObj := gin.H{
		"id":             created.ID.String(),
		"tier_id":        created.TierID.String(),
		"version_number": created.VersionNumber,
		"status":         created.Status,
		"effective_from": created.EffectiveFrom,
		"effective_to":   created.EffectiveTo,
		"checksum":       created.Checksum,
		"ranges":         createdRanges,
	}

	apires.RespondCreated(c, createdVersionObj, "Successfully published tier pricing version")
}

// [COMMENT]: ListTiers tiếp nhận kết quả Flat Entity từ service, map trực tiếp sang gin.H phẳng để trả về cho client.
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

	// Parse service_type từ query string trực tiếp
	var parsedServiceType entity.ServiceType
	if req.ServiceType != "" {
		stRaw := strings.TrimSpace(req.ServiceType)
		switch entity.ServiceType(stRaw) {
		case entity.ServiceTypeStorage, entity.ServiceTypeNetworkIn, entity.ServiceTypeNetworkOut, entity.ServiceTypeVM:
			parsedServiceType = entity.ServiceType(stRaw)
		default:
			logger.HandlerWarn(c, op, nil, "Invalid service_type filter: "+req.ServiceType)
			apires.RespondBadRequest(c, "INVALID_SERVICE_TYPE")
			return
		}
	}

	// Lấy danh sách flat tiers từ service
	flatTiers, total, err := h.tierService.GetTiersList(ctx, req.Page, req.Limit, parsedServiceType, req.Search)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "Failed to retrieve tiers")
		return
	}

	// [COMMENT]: Map trực tiếp Flat Entity sang mảng gin.H phẳng 1-1 cực kỳ tối giản
	tiersData := make([]gin.H, len(flatTiers))
	for i, ft := range flatTiers {
		tiersData[i] = gin.H{
			"id":               ft.ID.String(),     // Range ID
			"tier_id":          ft.TierID.String(), // Tier ID gốc
			"name":             ft.Name,
			"code":             ft.Code,
			"service_type":     string(ft.ServiceType),
			"metadata_version": ft.MetadataVersion,
			"pricing_version":  ft.PricingVersion,
			"range_start":      ft.RangeStart,
			"range_end":        ft.RangeEnd,
			"base_unit_price":  ft.BaseUnitPrice,
			"created_at":       ft.CreatedAt,
			"updated_at":       ft.UpdatedAt,
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
