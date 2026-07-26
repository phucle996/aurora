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
	"github.com/google/uuid"
)

var referralCodePattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{3,31}$`)

type PersonalAccountHandler struct {
	service      billingSvcInterface.PersonalAccountService
	minimumTopUp int64
}

func NewPersonalAccountHandler(
	service billingSvcInterface.PersonalAccountService,
	minimumTopUp int64,
) *PersonalAccountHandler {
	return &PersonalAccountHandler{
		service:      service,
		minimumTopUp: minimumTopUp,
	}
}

func (h *PersonalAccountHandler) GetOnboarding(c *gin.Context) {
	const op = "handler.personal_account.get_onboarding"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 3*time.Second)
	defer cancel()

	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	snapshot, err := h.service.GetOnboarding(ctx, userID)
	if err != nil {
		switch {
		case errors.Is(err, billingTaxonomy.ErrWalletNotFound):
			apires.RespondNotFound(c, "personal wallet is still being provisioned")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondServiceUnavailable(c, "billing onboarding is temporarily unavailable")
		}
		return
	}

	data := gin.H{
		"wallet":                     walletSummaryResponse(snapshot.Wallet),
		"minimum_top_up_micro_units": strconv.FormatInt(snapshot.MinimumTopUp, 10),
		"referral":                   nil,
		"latest_payment_intent":      nil,
	}
	if snapshot.Referral != nil {
		data["referral"] = referralReservationResponse(*snapshot.Referral)
	}
	if snapshot.LatestPaymentIntent != nil {
		data["latest_payment_intent"] = paymentIntentResponse(*snapshot.LatestPaymentIntent)
	}
	apires.RespondSuccess(c, data, "billing onboarding state")
}

func (h *PersonalAccountHandler) ReserveReferral(c *gin.Context) {
	const op = "handler.personal_account.reserve_referral"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	idempotencyKey := strings.TrimSpace(c.GetHeader("idempotency-key"))
	if idempotencyKey == "" || len(idempotencyKey) > 128 {
		apires.RespondBadRequest(c, "valid idempotency-key header is required")
		return
	}
	// [COMMENT]: Sử dụng DTO struct từ package dto để bind payload mã giới thiệu
	var request dto.ReserveReferralRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		apires.RespondBadRequest(c, "referral code is required")
		return
	}
	code := strings.ToUpper(strings.TrimSpace(request.Code))
	if !referralCodePattern.MatchString(code) {
		apires.RespondBadRequest(c, "referral code must be 4-32 uppercase letters, digits, '-' or '_'")
		return
	}
	reservation, err := h.service.ReserveReferral(ctx, entity.ReserveReferralCommand{
		OwnerID: userID, Code: code, IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		switch {
		case errors.Is(err, billingTaxonomy.ErrReferralNotFound):
			apires.RespondNotFound(c, "referral code is invalid or inactive")
		case errors.Is(err, billingTaxonomy.ErrReferralUnavailable):
			apires.RespondConflict(c, "referral campaign has no remaining capacity")
		case errors.Is(err, billingTaxonomy.ErrReferralAlreadyReserved),
			errors.Is(err, billingTaxonomy.ErrReferralAlreadyRedeemed),
			errors.Is(err, billingTaxonomy.ErrWalletAlreadyActive):
			apires.RespondConflict(c, err.Error())
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "failed to reserve referral")
		}
		return
	}
	apires.RespondCreated(c, referralReservationResponse(*reservation), "referral reserved")
}

func walletSummaryResponse(summary entity.WalletSummary) gin.H {
	return gin.H{
		"wallet_id":                       summary.WalletID.String(),
		"currency":                        summary.Currency,
		"cash_balance_micro_units":        strconv.FormatInt(summary.CashBalanceMicroUnits, 10),
		"promotional_balance_micro_units": strconv.FormatInt(summary.PromotionalBalanceMicroUnits, 10),
		"overdraft_limit_micro_units":     strconv.FormatInt(summary.OverdraftLimitMicroUnits, 10),
		"status":                          summary.Status,
		"version":                         strconv.FormatInt(summary.Version, 10),
		"updated_at":                      summary.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func referralReservationResponse(reservation entity.ReferralReservation) gin.H {
	data := gin.H{
		"id":                         reservation.ID.String(),
		"code":                       reservation.Code,
		"status":                     reservation.Status,
		"grant_amount_micro_units":   strconv.FormatInt(reservation.GrantAmountMicroUnits, 10),
		"minimum_top_up_micro_units": strconv.FormatInt(reservation.MinimumTopUpMicroUnits, 10),
		"currency":                   reservation.Currency,
		"expires_at":                 reservation.ExpiresAt.UTC().Format(time.RFC3339Nano),
		"rejection_reason":           reservation.RejectionReason,
	}
	if reservation.RedeemedAt != nil {
		data["redeemed_at"] = reservation.RedeemedAt.UTC().Format(time.RFC3339Nano)
	}
	return data
}

func paymentIntentResponse(intent entity.PaymentIntent) gin.H {
	data := gin.H{
		"id":                 intent.ID.String(),
		"amount_micro_units": strconv.FormatInt(intent.AmountMicroUnits, 10),
		"currency":           intent.Currency,
		"status":             intent.Status,
		"activates_wallet":   intent.ActivatesWallet,
		"expires_at":         intent.ExpiresAt.UTC().Format(time.RFC3339Nano),
		"created_at":         intent.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	if intent.CheckoutURL != "" {
		data["checkout_url"] = intent.CheckoutURL
	}
	if intent.SettledAt != nil {
		data["settled_at"] = intent.SettledAt.UTC().Format(time.RFC3339Nano)
	}
	return data
}

func (h *PersonalAccountHandler) ListReferralCampaigns(c *gin.Context) {
	const op = "handler.personal_account.referral.list"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 4*time.Second)
	defer cancel()

	campaigns, err := h.service.ListReferralCampaigns(ctx)
	if err != nil {
		logger.HandlerError(c, op, err)
		apires.RespondServiceUnavailable(c, "referral campaigns are temporarily unavailable")
		return
	}

	data := make([]gin.H, 0, len(campaigns))
	for _, campaign := range campaigns {
		item := referralCampaignResponse(campaign)
		data = append(data, item)
	}
	apires.RespondSuccess(c, data, "referral campaigns")
}

func (h *PersonalAccountHandler) CreateReferralCampaign(c *gin.Context) {
	const op = "handler.personal_account.referral.create"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

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
	apires.RespondCreated(c, referralCampaignResponse(*campaign), "referral campaign created in paused state")
}

func (h *PersonalAccountHandler) UpdateReferralCampaignStatus(c *gin.Context) {
	const op = "handler.personal_account.referral.update_status"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	campaignID, err := uuid.Parse(strings.TrimSpace(c.Param("id")))
	if err != nil || campaignID == uuid.Nil {
		apires.RespondBadRequest(c, "valid campaign id is required")
		return
	}

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
	apires.RespondSuccess(c, referralCampaignResponse(*campaign), "referral campaign status updated")
}

func referralCampaignResponse(campaign entity.ReferralCampaign) gin.H {
	data := gin.H{
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
		data["max_redemptions"] = strconv.FormatInt(*campaign.MaxRedemptions, 10)
	}
	if campaign.EndsAt != nil {
		data["ends_at"] = campaign.EndsAt.UTC().Format(time.RFC3339Nano)
	}
	return data
}
