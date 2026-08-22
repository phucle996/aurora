package handler

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
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

// maxTenantPaymentWebhookBytes giới hạn kích thước tối đa của body webhook quyết toán (64KB) để chống DoS.
const maxTenantPaymentWebhookBytes = 64 << 10

// TenantPaymentHandler điều phối toàn bộ các tương tác HTTP liên quan đến thanh toán và ví của Tổ chức / Doanh nghiệp (Tenant Scope):
// 1. Tra cứu số dư và trạng thái ví tổ chức (`GetWallet`).
// 2. Khởi tạo phiên nạp tiền (Payment Intent) cho tổ chức (`CreateTopUp`).
// 3. Tra cứu trạng thái phiên nạp tiền theo ID (`GetTopUp`).
// 4. Tiếp nhận và xác thực webhook quyết toán thanh toán từ Cổng thanh toán (`ApplySettlement`).
type TenantPaymentHandler struct {
	service billingSvcInterface.TenantPaymentService // Service xử lý nghiệp vụ thanh toán tổ chức
	policy  entity.PaymentPolicy                     // Chính sách thanh toán (Hạn mức nạp tối thiểu, khóa ký webhook, dung sai thời gian)
}

// NewTenantPaymentHandler khởi tạo một instance mới của TenantPaymentHandler.
func NewTenantPaymentHandler(
	service billingSvcInterface.TenantPaymentService,
	policy entity.PaymentPolicy,
) *TenantPaymentHandler {
	return &TenantPaymentHandler{service: service, policy: policy}
}

// GetWallet (GET /api/v1/tenant/billing/wallet/summary)
// Tra cứu thông tin tóm tắt ví tiền của tổ chức (Tenant Scope):
// - Số dư tiền mặt (`cash_balance_micro_units`), số dư khuyến mãi (`promotional_balance_micro_units`), hạn mức thấu chi (`overdraft_limit_micro_units`).
// - Trạng thái hoạt động của ví (`PENDING_ACTIVATION` hoặc `ACTIVE`).
// - Hạn mức nạp tiền tối thiểu quy định trong Payment Policy.
func (h *TenantPaymentHandler) GetWallet(c *gin.Context) {
	const op = "handler.payment.tenant.get_wallet"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 3*time.Second)
	defer cancel()

	// Trích xuất TenantID từ context đã được Gateway/ACR xác thực quyền thành viên
	tenantID, ok := pkgcontext.GetTenantID(c, op)
	if !ok {
		apires.RespondForbidden(c, "verified tenant context is required")
		return
	}

	summary, err := h.service.GetWallet(ctx, tenantID)
	if err != nil {
		if errors.Is(err, billingTaxonomy.ErrWalletNotFound) {
			apires.RespondNotFound(c, "tenant wallet is not provisioned")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondServiceUnavailable(c, "tenant wallet balance is temporarily unavailable")
		return
	}

	// [WORKFLOW ISOLATION]: Ánh xạ kết quả DTO trực tiếp tại call site
	data := gin.H{
		"wallet_id":                       summary.WalletID.String(),
		"currency":                        summary.Currency,
		"cash_balance_micro_units":        strconv.FormatInt(summary.CashBalanceMicroUnits, 10),
		"promotional_balance_micro_units": strconv.FormatInt(summary.PromotionalBalanceMicroUnits, 10),
		"overdraft_limit_micro_units":     strconv.FormatInt(summary.OverdraftLimitMicroUnits, 10),
		"status":                          summary.Status,
		"version":                         strconv.FormatInt(summary.Version, 10),
		"updated_at":                      summary.UpdatedAt.UTC().Format(time.RFC3339Nano),
		"minimum_top_up_micro_units":      strconv.FormatInt(h.policy.MinimumTopUp, 10),
	}
	apires.RespondSuccess(c, data, "tenant wallet summary")
}

// CreateTopUp (POST /api/v1/tenant/billing/wallet/top-ups)
// Khởi tạo phiên nạp tiền (Payment Intent) cho tổ chức:
// - Yêu cầu xác thực danh tính Người thực hiện (`actorID`) và Ngữ cảnh Tổ chức (`tenantID`).
// - Bắt buộc header `idempotency-key` (tối đa 128 ký tự) để chống nạp tiền trùng lặp.
// - Giới hạn kích thước payload 64KB và từ chối các trường không xác định (`DisallowUnknownFields`).
// - Kiểm tra số tiền nạp phải $\ge$ hạn mức tối thiểu cấu hình trong hệ thống ($5.00 USD).
// - Trả về `201 Created` nếu phiên thanh toán mới được tạo, hoặc `200 OK` nếu là Idempotent Replay.
func (h *TenantPaymentHandler) CreateTopUp(c *gin.Context) {
	const op = "handler.payment.tenant.create_top_up"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	// 1. Xác thực thông tin Actor và Tenant
	actorID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	tenantID, ok := pkgcontext.GetTenantID(c, op)
	if !ok {
		apires.RespondForbidden(c, "verified tenant context is required")
		return
	}

	// 2. Kiểm tra Idempotency Key Header
	idempotencyKey := strings.TrimSpace(c.GetHeader("idempotency-key"))
	if idempotencyKey == "" || len(idempotencyKey) > 128 {
		apires.RespondBadRequest(c, "valid idempotency-key header is required")
		return
	}

	// 3. Đọc và giải mã JSON an toàn với giới hạn 64KB và chống thuộc tính lạ
	var request dto.CreateTopUpRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		apires.RespondBadRequest(c, "amount_micro_units is required as an integer string")
		return
	}

	// 4. Kiểm tra hạn mức nạp tiền tối thiểu
	amount, err := strconv.ParseInt(request.AmountMicroUnits, 10, 64)
	if err != nil || amount < h.policy.MinimumTopUp {
		apires.RespondBadRequest(c, "top-up amount must be at least the configured USD minimum")
		return
	}

	// 5. Gọi Domain Service khởi tạo Payment Intent
	intent, err := h.service.CreateTopUp(ctx, entity.CreateTenantPaymentIntentCommand{
		TenantID: tenantID, ActorID: actorID, Amount: amount, IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		switch {
		case errors.Is(err, billingTaxonomy.ErrWalletNotFound):
			apires.RespondNotFound(c, "tenant wallet is not provisioned")
		case errors.Is(err, billingTaxonomy.ErrPreconditionFailed):
			apires.RespondConflict(c, "top-up does not satisfy the reserved referral terms")
		case errors.Is(err, billingTaxonomy.ErrIdempotencyConflict):
			apires.RespondConflict(c, "idempotency key was already used for another top-up")
		case errors.Is(err, billingTaxonomy.ErrInvalidWallet):
			apires.RespondConflict(c, "wallet cannot accept a top-up in its current state")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondServiceUnavailable(c, "payment checkout is temporarily unavailable")
		}
		return
	}

	// 6. Chuẩn bị dữ liệu phản hồi
	intentData := gin.H{
		"id":                 intent.ID.String(),
		"amount_micro_units": strconv.FormatInt(intent.AmountMicroUnits, 10),
		"currency":           intent.Currency,
		"status":             intent.Status,
		"activates_wallet":   intent.ActivatesWallet,
		"expires_at":         intent.ExpiresAt.UTC().Format(time.RFC3339Nano),
		"created_at":         intent.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	if intent.CheckoutURL != "" {
		intentData["checkout_url"] = intent.CheckoutURL
	}
	if intent.SettledAt != nil {
		intentData["settled_at"] = intent.SettledAt.UTC().Format(time.RFC3339Nano)
	}

	if intent.Created {
		apires.RespondCreated(c, intentData, "tenant payment intent created")
		return
	}
	apires.RespondSuccess(c, intentData, "tenant payment intent replayed")
}

// GetTopUp (GET /api/v1/tenant/billing/wallet/top-ups/:id)
// Tra cứu trạng thái phiên nạp tiền của tổ chức theo UUID của Intent.
func (h *TenantPaymentHandler) GetTopUp(c *gin.Context) {
	const op = "handler.payment.tenant.get_top_up"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 3*time.Second)
	defer cancel()

	tenantID, ok := pkgcontext.GetTenantID(c, op)
	if !ok {
		apires.RespondForbidden(c, "verified tenant context is required")
		return
	}
	intentID, err := uuid.Parse(strings.TrimSpace(c.Param("id")))
	if err != nil || intentID == uuid.Nil {
		apires.RespondBadRequest(c, "valid payment intent id is required")
		return
	}

	intent, err := h.service.GetTopUp(ctx, tenantID, intentID)
	if err != nil {
		if errors.Is(err, billingTaxonomy.ErrPaymentIntentNotFound) {
			apires.RespondNotFound(c, "payment intent not found")
			return
		}
		logger.HandlerError(c, op, err)
		apires.RespondServiceUnavailable(c, "tenant payment status is temporarily unavailable")
		return
	}

	intentData := gin.H{
		"id":                 intent.ID.String(),
		"amount_micro_units": strconv.FormatInt(intent.AmountMicroUnits, 10),
		"currency":           intent.Currency,
		"status":             intent.Status,
		"activates_wallet":   intent.ActivatesWallet,
		"expires_at":         intent.ExpiresAt.UTC().Format(time.RFC3339Nano),
		"created_at":         intent.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	if intent.CheckoutURL != "" {
		intentData["checkout_url"] = intent.CheckoutURL
	}
	if intent.SettledAt != nil {
		intentData["settled_at"] = intent.SettledAt.UTC().Format(time.RFC3339Nano)
	}
	apires.RespondSuccess(c, intentData, "tenant payment intent status")
}

// ApplySettlement (POST /api/v1/tenant/billing/wallet/settlement / webhook)
// Tiếp nhận và quyết toán webhook từ Cổng thanh toán (Payment Provider):
// 1. Giới hạn body tối đa 64KB (`maxTenantPaymentWebhookBytes`).
// 2. Xác thực tính toàn vẹn và chống giả mạo bằng chữ ký HMAC-SHA256 (`x-aurora-payment-signature`).
// 3. Chống tấn công phát lại (Replay Attack) bằng cách so khớp timestamp (`x-aurora-payment-timestamp` sai lệch $\le$ WebhookTolerance).
// 4. Giải mã payload DTO và tính toán SHA-256 Digest để lưu trữ bằng chứng bất biến (Immutable Audit Hash).
// 5. Gọi Service quyết toán nguyên tử: cộng tiền vào ví tổ chức, kích hoạt ví nếu đang chờ và phát Outbox cho phép sử dụng dịch vụ.
func (h *TenantPaymentHandler) ApplySettlement(c *gin.Context) {
	const op = "handler.payment.tenant.apply_settlement"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 8*time.Second)
	defer cancel()

	// 1. Đọc Raw Body an toàn
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxTenantPaymentWebhookBytes)
	rawBody, err := io.ReadAll(c.Request.Body)
	if err != nil || len(rawBody) == 0 {
		apires.RespondBadRequest(c, "invalid or oversized tenant payment webhook body")
		return
	}

	// 2. Đọc Headers xác thực Webhook
	timestampRaw := strings.TrimSpace(c.GetHeader("x-aurora-payment-timestamp"))
	signatureRaw := strings.TrimSpace(c.GetHeader("x-aurora-payment-signature"))
	eventID := strings.TrimSpace(c.GetHeader("x-aurora-payment-event-id"))
	timestamp, timestampErr := strconv.ParseInt(timestampRaw, 10, 64)
	now := time.Now().UTC()
	delta := now.Sub(time.Unix(timestamp, 0).UTC())
	if delta < 0 {
		delta = -delta
	}

	// 3. Kiểm tra tính hợp lệ của Header và dung sai thời gian
	if timestampErr != nil || delta > h.policy.WebhookTolerance ||
		eventID == "" || len(eventID) > 128 || signatureRaw == "" {
		apires.RespondUnauthorized(c, "invalid tenant payment webhook authentication")
		return
	}

	// 4. Xác thực chữ ký HMAC-SHA256
	signature, signatureErr := base64.RawURLEncoding.DecodeString(signatureRaw)
	mac := hmac.New(sha256.New, []byte(h.policy.WebhookSigningKey))
	_, _ = mac.Write([]byte(timestampRaw))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(rawBody)
	if signatureErr != nil || !hmac.Equal(signature, mac.Sum(nil)) {
		apires.RespondUnauthorized(c, "invalid tenant payment webhook signature")
		return
	}

	// 5. Giải mã Payload DTO
	var request dto.PaymentSettlementWebhookRequest
	decoder := json.NewDecoder(bytes.NewReader(rawBody))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&request); err != nil {
		apires.RespondBadRequest(c, "invalid tenant payment settlement payload")
		return
	}
	if err = decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		apires.RespondBadRequest(c, "tenant payment payload must contain one JSON object")
		return
	}

	// 6. Kiểm tra tính hợp lệ của các trường dữ liệu
	intentID, intentErr := uuid.Parse(strings.TrimSpace(request.PaymentIntentID))
	amount, amountErr := strconv.ParseInt(request.AmountMicroUnits, 10, 64)
	settledAt, settledErr := time.Parse(time.RFC3339, request.SettledAt)
	providerPaymentID := strings.TrimSpace(request.ProviderPaymentID)
	currency := strings.ToUpper(strings.TrimSpace(request.Currency))
	if intentErr != nil || intentID == uuid.Nil ||
		strings.ToUpper(strings.TrimSpace(request.OwnerType)) != string(entity.OwnerTypeTenant) ||
		amountErr != nil || amount <= 0 ||
		providerPaymentID == "" || len(providerPaymentID) > 128 ||
		currency != "USD" ||
		settledErr != nil || settledAt.After(now.Add(h.policy.WebhookTolerance)) {
		apires.RespondBadRequest(c, "invalid tenant payment settlement fields")
		return
	}

	// 7. Tính toán SHA-256 Digest của Payload
	payloadDigest := sha256.Sum256(rawBody)

	// 8. Thực thi quyết toán thanh toán trong Domain Service
	result, err := h.service.ApplyVerifiedSettlement(ctx, entity.PaymentSettlement{
		Provider:          h.policy.Provider,
		ProviderEventID:   eventID,
		ProviderPaymentID: providerPaymentID,
		PaymentIntentID:   intentID,
		OwnerType:         entity.OwnerTypeTenant,
		Amount:            amount,
		Currency:          currency,
		SettledAt:         settledAt.UTC(),
		PayloadHash:       hex.EncodeToString(payloadDigest[:]),
	})
	if err != nil {
		switch {
		case errors.Is(err, billingTaxonomy.ErrPaymentIntentNotFound):
			apires.RespondNotFound(c, "tenant payment intent not found")
		case errors.Is(err, billingTaxonomy.ErrSettlementMismatch),
			errors.Is(err, billingTaxonomy.ErrWebhookReplayConflict),
			errors.Is(err, billingTaxonomy.ErrInvalidWallet):
			apires.RespondConflict(c, "tenant settlement conflicts with durable intent state")
		case errors.Is(err, billingTaxonomy.ErrConflict):
			apires.RespondServiceUnavailable(c, "tenant settlement is being retried")
		default:
			logger.HandlerError(c, op, err)
			apires.RespondInternalError(c, "failed to apply tenant payment settlement")
		}
		return
	}

	// 9. Trả về kết quả quyết toán ví tổ chức
	apires.RespondSuccess(c, gin.H{
		"payment_intent_id":               result.PaymentIntentID.String(),
		"wallet_id":                       result.WalletID.String(),
		"wallet_status":                   result.WalletStatus,
		"cash_balance_micro_units":        strconv.FormatInt(result.CashBalance, 10),
		"promotional_balance_micro_units": strconv.FormatInt(result.PromotionalBalance, 10),
		"wallet_activated":                result.WalletActivated,
		"replayed":                        result.Replayed,
	}, "tenant payment settlement applied")
}
