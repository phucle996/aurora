package handler

import (
	"net/http"

	"cost-manager/api/repository"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type WalletHandler struct {
	repo repository.WalletRepository
}

func NewWalletHandler(repo repository.WalletRepository) *WalletHandler {
	return &WalletHandler{repo: repo}
}

// [COMMENT]: GetWallet trả về ví tiền của User/Workspace. Tự tạo ví mới nếu chưa tồn tại
func (h *WalletHandler) GetWallet(c *gin.Context) {
	ownerIDStr := c.Query("owner_id")
	ownerType := c.Query("owner_type")

	if ownerIDStr == "" || ownerType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing owner_id or owner_type"})
		return
	}

	ownerID, err := uuid.Parse(ownerIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid owner_id format"})
		return
	}

	wallet, err := h.repo.GetOrCreateWallet(c.Request.Context(), ownerID, ownerType)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, wallet)
}

type DepositRequest struct {
	OwnerID   string  `json:"owner_id" binding:"required"`
	OwnerType string  `json:"owner_type" binding:"required"`
	Amount    float64 `json:"amount" binding:"required,gt=0"`
	Desc      string  `json:"description"`
}

// [COMMENT]: Deposit thực hiện nạp tiền giả lập vào ví tiền
func (h *WalletHandler) Deposit(c *gin.Context) {
	var req DepositRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ownerID, err := uuid.Parse(req.OwnerID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid owner_id format"})
		return
	}

	err = h.repo.Deposit(c.Request.Context(), ownerID, req.OwnerType, req.Amount, req.Desc)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "deposit successful"})
}

// [COMMENT]: GetTransactions trả về danh sách lịch sử giao dịch ví
func (h *WalletHandler) GetTransactions(c *gin.Context) {
	walletIDStr := c.Param("id")
	walletID, err := uuid.Parse(walletIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid wallet_id format"})
		return
	}

	list, err := h.repo.GetTransactions(c.Request.Context(), walletID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, list)
}
