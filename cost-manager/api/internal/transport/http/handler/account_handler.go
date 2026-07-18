package handler

import (
	"context"
	"errors"
	"time"

	billingSvcInterface "cost-manager/api/internal/domain/service"
	billingTaxonomy "cost-manager/api/internal/taxonomy"
	"cost-manager/api/pkg/apires"
	"cost-manager/api/pkg/logger"
	"cost-manager/api/pkg/pkgcontext"

	"github.com/gin-gonic/gin"
)

type AccountHandler struct {
	service billingSvcInterface.AccountService
}

// [COMMENT]: NewAccountHandler khởi tạo HTTP adapter cho subscription/wallet activation.
func NewAccountHandler(service billingSvcInterface.AccountService) *AccountHandler {
	return &AccountHandler{service: service}
}

// ActivatePersonalFreeTier lấy identity từ trusted edge headers và yêu cầu idempotency key rõ ràng.
func (h *AccountHandler) ActivatePersonalFreeTier(c *gin.Context) {
	const op = "handler.account.activate_personal_free_tier"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	account, err := h.service.ActivatePersonalFreeTier(ctx, c.GetHeader("x-user-id"), c.GetHeader("idempotency-key"))
	if err != nil {
		switch {
		case errors.Is(err, billingTaxonomy.ErrInvalidArgument):
			apires.RespondBadRequest(c, "valid x-user-id and idempotency-key headers are required")
		case errors.Is(err, billingTaxonomy.ErrAlreadySubscribed), errors.Is(err, billingTaxonomy.ErrConflict):
			apires.RespondConflict(c, "free tier activation conflicts with an existing subscription")
		case errors.Is(err, billingTaxonomy.ErrPackNotActive):
			apires.RespondServiceUnavailable(c, "free tier campaign is not active")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "failed to activate free tier")
		}
		return
	}

	data := gin.H{
		"subscription_id":                 account.SubscriptionID,
		"wallet_id":                       account.WalletID,
		"credit_grant_id":                 account.CreditGrantID,
		"owner_id":                        account.OwnerID,
		"owner_type":                      account.OwnerType,
		"currency":                        account.Currency,
		"promotional_balance_micro_units": account.PromotionalBalance,
		"granted_micro_units":             account.GrantedMicroUnits,
		"subscription_started_at":         account.SubscriptionStarted,
	}
	if account.Created {
		apires.RespondCreated(c, data, "free tier activated")
		return
	}
	apires.RespondSuccess(c, data, "free tier activation replayed idempotently")
}
