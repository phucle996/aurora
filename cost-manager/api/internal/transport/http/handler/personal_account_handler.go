package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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

// referralCodePattern định nghĩa chuẩn regex cho mã giới thiệu / mã khuyến mãi (4-32 ký tự in hoa, số, '-' hoặc '_')
var referralCodePattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{3,31}$`)

// PersonalAccountHandler là HTTP Handler xử lý các yêu cầu liên quan đến tài khoản thanh toán cá nhân và chiến dịch giới thiệu:
// 1. Nhóm Personal Scope (/api/v1/personal/billing/...): Tra cứu trạng thái onboarding ví cá nhân (`GetOnboarding`) và giữ chỗ mã khuyến mãi (`ReserveReferral`).
// 2. Nhóm Platform Catalog Scope (/api/v1/billing/platform/...): Quản lý vòng đời chiến dịch giới thiệu (`ListReferralCampaigns`, `CreateReferralCampaign`, `UpdateReferralCampaignStatus`).
type PersonalAccountHandler struct {
	service      billingSvcInterface.PersonalAccountService // Service thực thi nghiệp vụ tài khoản cá nhân và referral
	minimumTopUp int64                                      // Hạn mức nạp tiền tối thiểu (micro-units) được quy định trong Payment Policy
}

// NewPersonalAccountHandler khởi tạo instance mới của PersonalAccountHandler.
func NewPersonalAccountHandler(
	service billingSvcInterface.PersonalAccountService,
	minimumTopUp int64,
) *PersonalAccountHandler {
	return &PersonalAccountHandler{
		service:      service,
		minimumTopUp: minimumTopUp,
	}
}

// GetOnboarding (GET /api/v1/personal/billing/wallet/onboarding)
// Tra cứu toàn bộ bức tranh thanh toán ban đầu của tài khoản cá nhân (Personal Scope):
// - Trạng thái ví cá nhân (`billing.wallets` ở PENDING_ACTIVATION hay ACTIVE, số dư tiền mặt, số dư khuyến mãi).
// - Hạn mức nạp tiền tối thiểu để kích hoạt ví.
// - Thông tin mã giới thiệu đang giữ chỗ (nếu có).
// - Phiên thanh toán nạp tiền gần nhất (kèm checkout URL).
func (h *PersonalAccountHandler) GetOnboarding(c *gin.Context) {
	const op = "handler.personal_account.get_onboarding"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 3*time.Second)
	defer cancel()

	// Trích xuất userID từ identity context đã được Gateway/ACR xác thực
	userID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}

	snapshot, err := h.service.GetOnboarding(ctx, userID)
	if err != nil {
		switch {
		case errors.Is(err, billingTaxonomy.ErrWalletNotFound):
			// Ví đang trong quá trình được worker khởi tạo bất đồng bộ từ stream sự kiện
			apires.RespondNotFound(c, "personal wallet is still being provisioned")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondServiceUnavailable(c, "billing onboarding is temporarily unavailable")
		}
		return
	}

	// ============================================================================
	// INLINE RESPONSE MAPPING CHO BILLING ONBOARDING STATE
	// ============================================================================
	walletData := gin.H{
		"wallet_id":                       snapshot.Wallet.WalletID.String(),
		"currency":                        snapshot.Wallet.Currency,
		"cash_balance_micro_units":        strconv.FormatInt(snapshot.Wallet.CashBalanceMicroUnits, 10),
		"promotional_balance_micro_units": strconv.FormatInt(snapshot.Wallet.PromotionalBalanceMicroUnits, 10),
		"overdraft_limit_micro_units":     strconv.FormatInt(snapshot.Wallet.OverdraftLimitMicroUnits, 10),
		"status":                          snapshot.Wallet.Status,
		"version":                         strconv.FormatInt(snapshot.Wallet.Version, 10),
		"updated_at":                      snapshot.Wallet.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}

	data := gin.H{
		"wallet":                     walletData,
		"minimum_top_up_micro_units": strconv.FormatInt(snapshot.MinimumTopUp, 10),
		"referral":                   nil,
		"latest_payment_intent":      nil,
	}

	if snapshot.Referral != nil {
		referralData := gin.H{
			"id":                         snapshot.Referral.ID.String(),
			"code":                       snapshot.Referral.Code,
			"status":                     snapshot.Referral.Status,
			"grant_amount_micro_units":   strconv.FormatInt(snapshot.Referral.GrantAmountMicroUnits, 10),
			"minimum_top_up_micro_units": strconv.FormatInt(snapshot.Referral.MinimumTopUpMicroUnits, 10),
			"currency":                   snapshot.Referral.Currency,
			"expires_at":                 snapshot.Referral.ExpiresAt.UTC().Format(time.RFC3339Nano),
			"rejection_reason":           snapshot.Referral.RejectionReason,
		}
		if snapshot.Referral.RedeemedAt != nil {
			referralData["redeemed_at"] = snapshot.Referral.RedeemedAt.UTC().Format(time.RFC3339Nano)
		}
		data["referral"] = referralData
	}

	if snapshot.LatestPaymentIntent != nil {
		intentData := gin.H{
			"id":                 snapshot.LatestPaymentIntent.ID.String(),
			"amount_micro_units": strconv.FormatInt(snapshot.LatestPaymentIntent.AmountMicroUnits, 10),
			"currency":           snapshot.LatestPaymentIntent.Currency,
			"status":             snapshot.LatestPaymentIntent.Status,
			"activates_wallet":   snapshot.LatestPaymentIntent.ActivatesWallet,
			"expires_at":         snapshot.LatestPaymentIntent.ExpiresAt.UTC().Format(time.RFC3339Nano),
			"created_at":         snapshot.LatestPaymentIntent.CreatedAt.UTC().Format(time.RFC3339Nano),
		}
		if snapshot.LatestPaymentIntent.CheckoutURL != "" {
			intentData["checkout_url"] = snapshot.LatestPaymentIntent.CheckoutURL
		}
		if snapshot.LatestPaymentIntent.SettledAt != nil {
			intentData["settled_at"] = snapshot.LatestPaymentIntent.SettledAt.UTC().Format(time.RFC3339Nano)
		}
		data["latest_payment_intent"] = intentData
	}

	apires.RespondSuccess(c, data, "billing onboarding state")
}

// ReserveReferral (POST /api/v1/billing/personal/referral/reserve)
// Giữ chỗ mã khuyến mãi/giới thiệu cho tài khoản cá nhân mới trước khi nạp tiền:
// - Bắt buộc header `Idempotency-Key` để bảo đảm tính an toàn khi client gửi lại request.
// - Kiểm tra giới hạn kích thước payload (64KB) và nghiêm cấm trường lạ (DisallowUnknownFields).
// - Kiểm tra định dạng Regex của mã giới thiệu.
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

	// ============================================================================
	// DECODE JSON VỚI GIỚI HẠN DUNG LƯỢNG VÀ STRICT CHECK
	// ============================================================================
	var request dto.ReserveReferralRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		apires.RespondBadRequest(c, "invalid request body")
		return
	}

	code := strings.ToUpper(strings.TrimSpace(request.Code))
	if !referralCodePattern.MatchString(code) {
		apires.RespondBadRequest(c, "referral code must be 4-32 uppercase letters, digits, '-' or '_'")
		return
	}

	reservation, err := h.service.ReserveReferral(ctx, entity.ReserveReferralCommand{
		OwnerID:        userID,
		Code:           code,
		IdempotencyKey: idempotencyKey,
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

	// Inline mapping response cho kết quả giữ chỗ referral
	responseData := gin.H{
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
		responseData["redeemed_at"] = reservation.RedeemedAt.UTC().Format(time.RFC3339Nano)
	}

	apires.RespondCreated(c, responseData, "referral reserved")
}

// ListReferralCampaigns (GET /api/v1/billing/platform/referral/campaigns)
// Liệt kê danh sách các chiến dịch giới thiệu/khuyến mãi dành cho Quản trị viên nền tảng (Platform Scope).
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

	// Inline mapping cho danh sách campaign
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

// CreateReferralCampaign (POST /api/v1/billing/platform/referral/campaigns)
// Tạo mới một chiến dịch khuyến mãi (khởi tạo ở trạng thái PAUSED):
// - Đọc body JSON qua `http.MaxBytesReader` và `json.NewDecoder` với `DisallowUnknownFields()`.
// - Xác thực các trường: mã chiến dịch, tên, số tiền thưởng, điều kiện nạp tối thiểu, thời gian hiệu lực.
func (h *PersonalAccountHandler) CreateReferralCampaign(c *gin.Context) {
	const op = "handler.personal_account.referral.create"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// ============================================================================
	// DECODE JSON VỚI GIỚI HẠN DUNG LƯỢNG VÀ STRICT CHECK
	// ============================================================================
	var request dto.CreateReferralCampaignRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
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
		Code:                   code,
		Name:                   name,
		AmountMicroUnits:       amount,
		MinimumTopUpMicroUnits: minimum,
		Currency:               "USD",
		MaxRedemptions:         maxRedemptions,
		StartsAt:               startsAt.UTC(),
		EndsAt:                 endsAt,
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

	// Inline mapping response cho campaign vừa tạo
	responseData := gin.H{
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
		responseData["max_redemptions"] = strconv.FormatInt(*campaign.MaxRedemptions, 10)
	}
	if campaign.EndsAt != nil {
		responseData["ends_at"] = campaign.EndsAt.UTC().Format(time.RFC3339Nano)
	}

	apires.RespondCreated(c, responseData, "referral campaign created in paused state")
}

// UpdateReferralCampaignStatus (PATCH /api/v1/billing/platform/referral/campaigns/:id/status)
// Cập nhật trạng thái của chiến dịch khuyến mãi (ACTIVE, PAUSED, ENDED):
// - Đọc body JSON qua `http.MaxBytesReader` và `json.NewDecoder` với `DisallowUnknownFields()`.
// - Áp dụng Optimistic Concurrency Control thông qua `ExpectedVersion` để bảo vệ tính nhất quán dữ liệu.
func (h *PersonalAccountHandler) UpdateReferralCampaignStatus(c *gin.Context) {
	const op = "handler.personal_account.referral.update_status"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	campaignID, err := uuid.Parse(strings.TrimSpace(c.Param("id")))
	if err != nil || campaignID == uuid.Nil {
		apires.RespondBadRequest(c, "valid campaign id is required")
		return
	}

	// ============================================================================
	// DECODE JSON VỚI GIỚI HẠN DUNG LƯỢNG VÀ STRICT CHECK
	// ============================================================================
	var request dto.UpdateReferralCampaignStatusRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
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
		ID:              campaignID,
		Status:          status,
		ExpectedVersion: version,
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

	// Inline mapping response cho kết quả cập nhật trạng thái campaign
	responseData := gin.H{
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
		responseData["max_redemptions"] = strconv.FormatInt(*campaign.MaxRedemptions, 10)
	}
	if campaign.EndsAt != nil {
		responseData["ends_at"] = campaign.EndsAt.UTC().Format(time.RFC3339Nano)
	}

	apires.RespondSuccess(c, responseData, "referral campaign status updated")
}
