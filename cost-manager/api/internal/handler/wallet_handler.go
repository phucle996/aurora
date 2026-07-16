package handler

import (
	"errors"
	"net/http"

	"cost-manager/api/internal/domain/service"
	"cost-manager/api/pkg/apperr"
	"cost-manager/api/pkg/apires"
	"cost-manager/api/pkg/logger"
	"cost-manager/api/internal/transport/dto"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type WalletHandler struct {
	billingSvc service.BillingService
}

func NewWalletHandler(billingSvc service.BillingService) *WalletHandler {
	return &WalletHandler{billingSvc: billingSvc}
}

func (h *WalletHandler) GetWallet(c *gin.Context) {
	const op = "handler.wallet.get_wallet"
	ownerIDStr := c.Query("owner_id")
	ownerType := c.Query("owner_type")

	if ownerIDStr == "" || ownerType == "" {
		apires.RespondBadRequest(c, "missing owner_id or owner_type")
		return
	}

	ownerID, err := uuid.Parse(ownerIDStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid owner_id format")
		return
	}

	wallet, err := h.billingSvc.GetOrCreateWallet(c.Request.Context(), ownerID, ownerType)
	if err != nil {
		h.handleError(c, op, err)
		return
	}

	logger.HandlerInfo(c, op, "Successfully fetched/created wallet")
	apires.RespondSuccess(c, dto.ToWalletResponse(wallet), "ok")
}

type DepositRequest struct {
	OwnerID   string  `json:"owner_id" binding:"required"`
	OwnerType string  `json:"owner_type" binding:"required"`
	Amount    float64 `json:"amount" binding:"required,gt=0"`
	Desc      string  `json:"description"`
}

func (h *WalletHandler) Deposit(c *gin.Context) {
	const op = "handler.wallet.deposit"
	var req DepositRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apires.RespondBadRequest(c, err.Error())
		return
	}

	ownerID, err := uuid.Parse(req.OwnerID)
	if err != nil {
		apires.RespondBadRequest(c, "invalid owner_id format")
		return
	}

	err = h.billingSvc.Deposit(c.Request.Context(), ownerID, req.OwnerType, req.Amount, req.Desc)
	if err != nil {
		h.handleError(c, op, err)
		return
	}

	logger.HandlerInfo(c, op, "Successfully processed wallet deposit")
	apires.RespondSuccess(c, nil, "deposit successful")
}

func (h *WalletHandler) GetTransactions(c *gin.Context) {
	const op = "handler.wallet.get_transactions"
	walletIDStr := c.Param("id")
	walletID, err := uuid.Parse(walletIDStr)
	if err != nil {
		apires.RespondBadRequest(c, "invalid wallet_id format")
		return
	}

	list, err := h.billingSvc.GetTransactions(c.Request.Context(), walletID)
	if err != nil {
		h.handleError(c, op, err)
		return
	}

	logger.HandlerInfo(c, op, "Successfully fetched wallet transactions history")
	apires.RespondSuccess(c, dto.ToTransactionListResponse(list), "ok")
}

func (h *WalletHandler) handleError(c *gin.Context, op string, err error) {
	logger.HandlerError(c, op, err)

	appErr, ok := apperr.As(err)
	if !ok {
		apires.RespondInternalError(c, "internal_server_error")
		return
	}

	if errors.Is(appErr.Kind, apperr.ErrWalletNotFound) {
		apires.RespondNotFound(c, appErr.Outcome)
	} else if errors.Is(appErr.Kind, apperr.ErrInsufficientFunds) || errors.Is(appErr.Kind, apperr.ErrBadRequest) {
		apires.RespondBadRequest(c, appErr.Outcome)
	} else {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   appErr.Kind.Error(),
			"outcome": appErr.Outcome,
		})
	}
}
