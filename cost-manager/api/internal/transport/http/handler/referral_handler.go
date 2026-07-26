package handler

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingTaxonomy "cost-manager/api/internal/taxonomy"
	"cost-manager/api/internal/transport/http/dto"
	"cost-manager/api/pkg/apires"
	"cost-manager/api/pkg/logger"
	"cost-manager/api/pkg/pkgcontext"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (h *AccountHandler) ListReferralCampaigns(c *gin.Context) {
	const op = "handler.referral.list"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 4*time.Second)
	defer cancel()

	campaigns, err := h.service.ListReferralCampaigns(ctx)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondServiceUnavailable(c, "referral campaigns are temporarily unavailable")
		return
	}

	// [COMMENT]: Map danh sách referral campaign sang DTO response gin.H inline
	data := make([]gin.H, 0, len(campaigns))
	for _, campaign := range campaigns {
		item := gin.H{
			"id":                         campaign.ID.String(),
			"code":                       campaign.Code,
			"name":                       campaign.Name,
			"amount_micro_units":         strconv.FormatInt(campaign.AmountMicroUnits, 10),
			"minimum_top_up_micro_units": strconv.FormatInt(campaign.MinimumTopUpMicroUnits, 10),
			"currency":                   campaign.Currency,
			"status":                     campaign.Status,
			"redemptions":                strconv.FormatInt(campaign.Redemptions, 10),
			"active_reservations":        strconv.FormatInt(campaign.ActiveReservations, 10),
			"version":                    strconv.FormatInt(campaign.Version, 10),
			"starts_at":                  campaign.StartsAt.UTC().Format(time.RFC3339Nano),
			"created_at":                 campaign.CreatedAt.UTC().Format(time.RFC3339Nano),
			"updated_at":                 campaign.UpdatedAt.UTC().Format(time.RFC3339Nano),
		}
		if campaign.MaxRedemptions != nil {
			item["max_redemptions"] = strconv.FormatInt(*campaign.MaxRedemptions, 10)
		}
		if campaign.EndsAt != nil {
			item["ends_at"] = campaign.EndsAt.UTC().Format(time.RFC3339Nano)
		}
		data = append(data, item)
	}
	apires.RespondSuccess(c, data, "referral campaigns")
}

func (h *AccountHandler) CreateReferralCampaign(c *gin.Context) {
	const op = "handler.referral.create"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// [COMMENT]: Sử dụng DTO struct từ package dto để bind request payload
	var request dto.CreateReferralCampaignRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		apires.RespondBadRequest(c, "invalid referral campaign payload")
		return
	}
	code := strings.ToUpper(strings.TrimSpace(request.Code))
	name := strings.TrimSpace(request.Name)
	amount, amountErr := strconv.ParseInt(request.AmountMicroUnits, 10, 64)
	minimum, minimumErr := strconv.ParseInt(request.MinimumTopUpMicroUnits, 10, 64)
	startsAt, startsErr := time.Parse(time.RFC3339, request.StartsAt)
	if !referralCodePattern.MatchString(code) ||
		name == "" || len(name) > 128 ||
		amountErr != nil || amount <= 0 ||
		minimumErr != nil || minimum < h.minimumTopUp ||
		strings.ToUpper(strings.TrimSpace(request.Currency)) != "USD" ||
		startsErr != nil {
		apires.RespondBadRequest(c, "invalid referral campaign fields")
		return
	}
	var maxRedemptions *int64
	if request.MaxRedemptions != nil {
		parsed, err := strconv.ParseInt(*request.MaxRedemptions, 10, 64)
		if err != nil || parsed <= 0 {
			apires.RespondBadRequest(c, "max_redemptions must be a positive integer string")
			return
		}
		maxRedemptions = &parsed
	}
	var endsAt *time.Time
	if request.EndsAt != nil {
		parsed, err := time.Parse(time.RFC3339, *request.EndsAt)
		if err != nil || !parsed.After(startsAt) {
			apires.RespondBadRequest(c, "ends_at must be RFC3339 and after starts_at")
			return
		}
		endsAt = &parsed
	}

	campaign, err := h.service.CreateReferralCampaign(ctx, entity.CreateReferralCampaignCommand{
		Code: code, Name: name, AmountMicroUnits: amount,
		MinimumTopUpMicroUnits: minimum, Currency: "USD",
		MaxRedemptions: maxRedemptions, StartsAt: startsAt.UTC(), EndsAt: endsAt,
	})
	if err != nil {
		if errors.Is(err, billingTaxonomy.ErrConflict) {
			apires.RespondConflict(c, "referral code already exists")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondInternalError(c, "failed to create referral campaign")
		return
	}

	// [COMMENT]: Định dạng response DTO cho campaign mới tạo inline
	res := gin.H{
		"id":                         campaign.ID.String(),
		"code":                       campaign.Code,
		"name":                       campaign.Name,
		"amount_micro_units":         strconv.FormatInt(campaign.AmountMicroUnits, 10),
		"minimum_top_up_micro_units": strconv.FormatInt(campaign.MinimumTopUpMicroUnits, 10),
		"currency":                   campaign.Currency,
		"status":                     campaign.Status,
		"redemptions":                strconv.FormatInt(campaign.Redemptions, 10),
		"active_reservations":        strconv.FormatInt(campaign.ActiveReservations, 10),
		"version":                    strconv.FormatInt(campaign.Version, 10),
		"starts_at":                  campaign.StartsAt.UTC().Format(time.RFC3339Nano),
		"created_at":                 campaign.CreatedAt.UTC().Format(time.RFC3339Nano),
		"updated_at":                 campaign.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if campaign.MaxRedemptions != nil {
		res["max_redemptions"] = strconv.FormatInt(*campaign.MaxRedemptions, 10)
	}
	if campaign.EndsAt != nil {
		res["ends_at"] = campaign.EndsAt.UTC().Format(time.RFC3339Nano)
	}
	apires.RespondCreated(c, res, "referral campaign created in paused state")
}

func (h *AccountHandler) UpdateReferralCampaignStatus(c *gin.Context) {
	const op = "handler.referral.update_status"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	campaignID, err := uuid.Parse(strings.TrimSpace(c.Param("id")))
	if err != nil || campaignID == uuid.Nil {
		apires.RespondBadRequest(c, "valid campaign id is required")
		return
	}

	// [COMMENT]: Sử dụng DTO struct từ package dto để bind request payload
	var request dto.UpdateReferralCampaignStatusRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		apires.RespondBadRequest(c, "status and expected_version are required")
		return
	}
	status := strings.ToUpper(strings.TrimSpace(request.Status))
	version, versionErr := strconv.ParseInt(request.ExpectedVersion, 10, 64)
	if (status != "ACTIVE" && status != "PAUSED" && status != "ENDED") ||
		versionErr != nil || version <= 0 {
		apires.RespondBadRequest(c, "invalid campaign status or expected_version")
		return
	}
	campaign, err := h.service.UpdateReferralCampaignStatus(ctx, entity.UpdateReferralCampaignStatusCommand{
		ID: campaignID, Status: status, ExpectedVersion: version,
	})
	if err != nil {
		switch {
		case errors.Is(err, billingTaxonomy.ErrReferralNotFound):
			apires.RespondNotFound(c, "referral campaign not found")
		case errors.Is(err, billingTaxonomy.ErrConflict):
			apires.RespondConflict(c, "referral campaign version conflict")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "failed to update referral campaign")
		}
		return
	}

	// [COMMENT]: Định dạng response DTO cho campaign sau khi cập nhật trạng thái inline
	res := gin.H{
		"id":                         campaign.ID.String(),
		"code":                       campaign.Code,
		"name":                       campaign.Name,
		"amount_micro_units":         strconv.FormatInt(campaign.AmountMicroUnits, 10),
		"minimum_top_up_micro_units": strconv.FormatInt(campaign.MinimumTopUpMicroUnits, 10),
		"currency":                   campaign.Currency,
		"status":                     campaign.Status,
		"redemptions":                strconv.FormatInt(campaign.Redemptions, 10),
		"active_reservations":        strconv.FormatInt(campaign.ActiveReservations, 10),
		"version":                    strconv.FormatInt(campaign.Version, 10),
		"starts_at":                  campaign.StartsAt.UTC().Format(time.RFC3339Nano),
		"created_at":                 campaign.CreatedAt.UTC().Format(time.RFC3339Nano),
		"updated_at":                 campaign.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if campaign.MaxRedemptions != nil {
		res["max_redemptions"] = strconv.FormatInt(*campaign.MaxRedemptions, 10)
	}
	if campaign.EndsAt != nil {
		res["ends_at"] = campaign.EndsAt.UTC().Format(time.RFC3339Nano)
	}
	apires.RespondSuccess(c, res, "referral campaign status updated")
}
