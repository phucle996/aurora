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

const maxTenantPaymentWebhookBytes = 64 << 10

type TenantPaymentHandler struct {
	service billingSvcInterface.TenantPaymentService
	policy  entity.PaymentPolicy
}

func NewTenantPaymentHandler(
	service billingSvcInterface.TenantPaymentService,
	policy entity.PaymentPolicy,
) *TenantPaymentHandler {
	return &TenantPaymentHandler{service: service, policy: policy}
}

func (h *TenantPaymentHandler) GetWallet(c *gin.Context) {
	const op = "handler.payment.tenant.get_wallet"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 3*time.Second)
	defer cancel()

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
	data := walletSummaryResponse(*summary)
	data["minimum_top_up_micro_units"] = strconv.FormatInt(h.policy.MinimumTopUp, 10)
	apires.RespondSuccess(c, data, "tenant wallet summary")
}

func (h *TenantPaymentHandler) CreateTopUp(c *gin.Context) {
	const op = "handler.payment.tenant.create_top_up"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 5*time.Second)
	defer cancel()

	actorID, ok := pkgcontext.GetUserID(c, op)
	if !ok {
		return
	}
	tenantID, ok := pkgcontext.GetTenantID(c, op)
	if !ok {
		apires.RespondForbidden(c, "verified tenant context is required")
		return
	}
	idempotencyKey := strings.TrimSpace(c.GetHeader("idempotency-key"))
	if idempotencyKey == "" || len(idempotencyKey) > 128 {
		apires.RespondBadRequest(c, "valid idempotency-key header is required")
		return
	}
	// [COMMENT]: Sử dụng DTO struct từ package dto để bind nạp tiền ví tenant
	var request dto.CreateTopUpRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		apires.RespondBadRequest(c, "amount_micro_units is required as an integer string")
		return
	}
	amount, err := strconv.ParseInt(request.AmountMicroUnits, 10, 64)
	if err != nil || amount < h.policy.MinimumTopUp {
		apires.RespondBadRequest(c, "top-up amount must be at least the configured USD minimum")
		return
	}
	intent, err := h.service.CreateTopUp(ctx, entity.CreateTenantPaymentIntentCommand{
		TenantID: tenantID, ActorID: actorID, Amount: amount, IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		respondTopUpError(c, op, err, "tenant wallet is not provisioned")
		return
	}
	if intent.Created {
		apires.RespondCreated(c, paymentIntentResponse(*intent), "tenant payment intent created")
		return
	}
	apires.RespondSuccess(c, paymentIntentResponse(*intent), "tenant payment intent replayed")
}

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
	apires.RespondSuccess(c, paymentIntentResponse(*intent), "tenant payment intent status")
}

func (h *TenantPaymentHandler) ApplySettlement(c *gin.Context) {
	const op = "handler.payment.tenant.apply_settlement"
	ctx, cancel := context.WithTimeout(pkgcontext.WithOperation(c.Request.Context(), op), 8*time.Second)
	defer cancel()

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxTenantPaymentWebhookBytes)
	rawBody, err := io.ReadAll(c.Request.Body)
	if err != nil || len(rawBody) == 0 {
		apires.RespondBadRequest(c, "invalid or oversized tenant payment webhook body")
		return
	}
	timestampRaw := strings.TrimSpace(c.GetHeader("x-aurora-payment-timestamp"))
	signatureRaw := strings.TrimSpace(c.GetHeader("x-aurora-payment-signature"))
	eventID := strings.TrimSpace(c.GetHeader("x-aurora-payment-event-id"))
	timestamp, timestampErr := strconv.ParseInt(timestampRaw, 10, 64)
	now := time.Now().UTC()
	delta := now.Sub(time.Unix(timestamp, 0).UTC())
	if delta < 0 {
		delta = -delta
	}
	if timestampErr != nil || delta > h.policy.WebhookTolerance ||
		eventID == "" || len(eventID) > 128 || signatureRaw == "" {
		apires.RespondUnauthorized(c, "invalid tenant payment webhook authentication")
		return
	}
	signature, signatureErr := base64.RawURLEncoding.DecodeString(signatureRaw)
	mac := hmac.New(sha256.New, []byte(h.policy.WebhookSigningKey))
	_, _ = mac.Write([]byte(timestampRaw))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(rawBody)
	if signatureErr != nil || !hmac.Equal(signature, mac.Sum(nil)) {
		apires.RespondUnauthorized(c, "invalid tenant payment webhook signature")
		return
	}

	var request struct {
		PaymentIntentID   string `json:"payment_intent_id"`
		OwnerType         string `json:"owner_type"`
		ProviderPaymentID string `json:"provider_payment_id"`
		AmountMicroUnits  string `json:"amount_micro_units"`
		Currency          string `json:"currency"`
		SettledAt         string `json:"settled_at"`
	}
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

	payloadDigest := sha256.Sum256(rawBody)
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
