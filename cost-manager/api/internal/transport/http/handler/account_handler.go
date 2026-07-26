package handler

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	billingSvcInterface "cost-manager/api/internal/domain/service"
	billingTaxonomy "cost-manager/api/internal/taxonomy"
	"cost-manager/api/pkg/apires"
	"cost-manager/api/pkg/logger"
	"cost-manager/api/pkg/pkgcontext"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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

	// [COMMENT]: Trích xuất và validate userID từ context (do middleware identity xử lý)
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok || userID == uuid.Nil {
		// [COMMENT]: Phản hồi BadRequest nếu userID không hợp lệ hoặc rỗng
		apires.RespondBadRequest(c, "valid x-user-id header is required")
		return
	}

	// [COMMENT]: Validate idempotency-key header trực tiếp tại HTTP handler layer
	idempotencyKey := strings.TrimSpace(c.GetHeader("idempotency-key"))
	if idempotencyKey == "" || len(idempotencyKey) > 128 {
		// [COMMENT]: Yêu cầu idempotency-key không rỗng và độ dài tối đa 128 ký tự
		apires.RespondBadRequest(c, "valid idempotency-key header is required (1-128 chars)")
		return
	}

	// [COMMENT]: Truyền userID đã chuẩn hóa kiểu uuid.UUID và idempotencyKey sạch xuống service layer
	account, err := h.service.ActivatePersonalFreeTier(ctx, userID, idempotencyKey)
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

// GetPersonalWalletSummary trả về snapshot wallet của actor đã được Envoy xác minh.
// Các micro-unit serialize thành string để browser không làm tròn số tiền qua Number.
func (h *AccountHandler) GetPersonalWalletSummary(c *gin.Context) {
	const op = "handler.account.get_personal_wallet_summary"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 3*time.Second)
	defer cancel()

	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	summary, err := h.service.GetPersonalWalletSummary(ctx, userID)
	if err != nil {
		switch {
		case errors.Is(err, billingTaxonomy.ErrWalletNotFound):
			apires.RespondNotFound(c, "personal wallet is not provisioned")
		case errors.Is(err, billingTaxonomy.ErrInvalidArgument):
			apires.RespondBadRequest(c, "valid trusted billing identity is required")
		default:
			// A stale/unavailable balance must never be presented as zero to the user.
			logger.HandlerError(c, op, err)
			apires.RespondServiceUnavailable(c, "wallet balance is temporarily unavailable")
		}
		return
	}

	apires.RespondSuccess(c, gin.H{
		"wallet_id":                       summary.WalletID.String(),
		"currency":                        summary.Currency,
		"cash_balance_micro_units":        strconv.FormatInt(summary.CashBalanceMicroUnits, 10),
		"promotional_balance_micro_units": strconv.FormatInt(summary.PromotionalBalanceMicroUnits, 10),
		"overdraft_limit_micro_units":     strconv.FormatInt(summary.OverdraftLimitMicroUnits, 10),
		"status":                          summary.Status,
		"version":                         strconv.FormatInt(summary.Version, 10),
		"updated_at":                      summary.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}, "personal wallet summary")
}
