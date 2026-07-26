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

var referralCodePattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{3,31}$`)

type AccountHandler struct {
	service      billingSvcInterface.AccountService
	minimumTopUp int64
}

func NewAccountHandler(
	service billingSvcInterface.AccountService,
	minimumTopUp int64,
) *AccountHandler {
	return &AccountHandler{
		service:      service,
		minimumTopUp: minimumTopUp,
	}
}

func (h *AccountHandler) GetOnboarding(c *gin.Context) {
	const op = "handler.account.get_onboarding"
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

func (h *AccountHandler) ReserveReferral(c *gin.Context) {
	const op = "handler.account.reserve_referral"
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
