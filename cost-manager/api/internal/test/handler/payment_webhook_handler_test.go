package handler_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"cost-manager/api/internal/domain/entity"
	"cost-manager/api/internal/transport/http/handler"
	"cost-manager/api/pkg/pkgcontext"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type personalPaymentServiceStub struct {
	settlement      entity.PaymentSettlement
	settlementCalls int
	intentCalls     int
}

func (s *personalPaymentServiceStub) GetWallet(context.Context, uuid.UUID) (*entity.WalletSummary, error) {
	return nil, nil
}
func (s *personalPaymentServiceStub) CreateTopUp(context.Context, entity.CreatePersonalPaymentIntentCommand) (*entity.PaymentIntent, error) {
	s.intentCalls++
	return nil, nil
}
func (s *personalPaymentServiceStub) GetTopUp(context.Context, uuid.UUID, uuid.UUID) (*entity.PaymentIntent, error) {
	return nil, nil
}
func (s *personalPaymentServiceStub) ApplyVerifiedSettlement(
	_ context.Context,
	settlement entity.PaymentSettlement,
) (*entity.SettlementResult, error) {
	s.settlementCalls++
	s.settlement = settlement
	return &entity.SettlementResult{
		PaymentIntentID: settlement.PaymentIntentID,
		WalletID:        uuid.New(),
		OwnerID:         uuid.New(),
		OwnerType:       entity.OwnerTypePersonal,
		ActorID:         uuid.New(),
		WalletStatus:    entity.WalletStatusActive,
	}, nil
}

type tenantPaymentServiceStub struct {
	settlement      entity.PaymentSettlement
	settlementCalls int
}

func (s *tenantPaymentServiceStub) GetWallet(context.Context, uuid.UUID) (*entity.WalletSummary, error) {
	return nil, nil
}
func (s *tenantPaymentServiceStub) CreateTopUp(context.Context, entity.CreateTenantPaymentIntentCommand) (*entity.PaymentIntent, error) {
	return nil, nil
}
func (s *tenantPaymentServiceStub) GetTopUp(context.Context, uuid.UUID, uuid.UUID) (*entity.PaymentIntent, error) {
	return nil, nil
}
func (s *tenantPaymentServiceStub) ApplyVerifiedSettlement(
	_ context.Context,
	settlement entity.PaymentSettlement,
) (*entity.SettlementResult, error) {
	s.settlementCalls++
	s.settlement = settlement
	return &entity.SettlementResult{
		PaymentIntentID: settlement.PaymentIntentID,
		WalletID:        uuid.New(),
		OwnerID:         uuid.New(),
		OwnerType:       entity.OwnerTypeTenant,
		ActorID:         uuid.New(),
		WalletStatus:    entity.WalletStatusActive,
	}, nil
}

func webhookPolicy() entity.PaymentPolicy {
	return entity.PaymentPolicy{
		Provider:          "test-gateway",
		WebhookSigningKey: "webhook-signing-secret-at-least-32-bytes",
		WebhookTolerance:  5 * time.Minute,
	}
}

func signedWebhookRequest(
	t *testing.T,
	policy entity.PaymentPolicy,
	path string,
	body []byte,
	eventID string,
) *http.Request {
	t.Helper()
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(policy.WebhookSigningKey))
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	request := httptest.NewRequest(
		http.MethodPost,
		path,
		bytes.NewReader(body),
	)
	request.Header.Set("content-type", "application/json")
	request.Header.Set("x-aurora-payment-timestamp", timestamp)
	request.Header.Set("x-aurora-payment-event-id", eventID)
	request.Header.Set("x-aurora-payment-signature", base64.RawURLEncoding.EncodeToString(mac.Sum(nil)))
	return request
}

func TestPaymentWebhookDispatchesSignedPersonalOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	personal := &personalPaymentServiceStub{}
	policy := webhookPolicy()
	webhookHandler := handler.NewPersonalPaymentHandler(personal, policy)
	router := gin.New()
	router.POST("/api/v1/billing/webhooks/personal/payment-settled", webhookHandler.ApplySettlement)

	intentID := uuid.New()
	body := []byte(`{"payment_intent_id":"` + intentID.String() + `","owner_type":"PERSONAL","provider_payment_id":"provider-payment-1","amount_micro_units":"1000000","currency":"USD","settled_at":"` + time.Now().UTC().Format(time.RFC3339) + `"}`)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, signedWebhookRequest(
		t,
		policy,
		"/api/v1/billing/webhooks/personal/payment-settled",
		body,
		"event-1",
	))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if personal.settlementCalls != 1 ||
		personal.settlement.PaymentIntentID != intentID ||
		personal.settlement.OwnerType != entity.OwnerTypePersonal {
		t.Fatalf("unexpected owner dispatch: %#v", personal.settlement)
	}
}

func TestPaymentWebhookDispatchesSignedTenantOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tenant := &tenantPaymentServiceStub{}
	policy := webhookPolicy()
	webhookHandler := handler.NewTenantPaymentHandler(tenant, policy)
	router := gin.New()
	const path = "/api/v1/billing/webhooks/tenant/payment-settled"
	router.POST(path, webhookHandler.ApplySettlement)

	intentID := uuid.New()
	body := []byte(`{"payment_intent_id":"` + intentID.String() + `","owner_type":"TENANT","provider_payment_id":"provider-payment-tenant-1","amount_micro_units":"1000000","currency":"USD","settled_at":"` + time.Now().UTC().Format(time.RFC3339) + `"}`)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, signedWebhookRequest(t, policy, path, body, "event-tenant-1"))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if tenant.settlementCalls != 1 ||
		tenant.settlement.PaymentIntentID != intentID ||
		tenant.settlement.OwnerType != entity.OwnerTypeTenant {
		t.Fatalf("unexpected owner dispatch: %#v", tenant.settlement)
	}
}

func TestPaymentWebhookRejectsInvalidSignatureBeforeDispatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	personal := &personalPaymentServiceStub{}
	policy := webhookPolicy()
	webhookHandler := handler.NewPersonalPaymentHandler(personal, policy)
	router := gin.New()
	router.POST("/api/v1/billing/webhooks/personal/payment-settled", webhookHandler.ApplySettlement)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/billing/webhooks/personal/payment-settled", bytes.NewBufferString(`{}`))
	request.Header.Set("x-aurora-payment-timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	request.Header.Set("x-aurora-payment-event-id", "event-2")
	request.Header.Set("x-aurora-payment-signature", "invalid")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if personal.settlementCalls != 0 {
		t.Fatal("invalid signature reached a settlement service")
	}
}

func TestPaymentWebhookRejectsSignedTrailingJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	personal := &personalPaymentServiceStub{}
	policy := webhookPolicy()
	webhookHandler := handler.NewPersonalPaymentHandler(personal, policy)
	router := gin.New()
	router.POST("/api/v1/billing/webhooks/personal/payment-settled", webhookHandler.ApplySettlement)

	body := []byte(`{"payment_intent_id":"` + uuid.NewString() + `","owner_type":"PERSONAL","provider_payment_id":"provider-payment-2","amount_micro_units":"1000000","currency":"USD","settled_at":"` + time.Now().UTC().Format(time.RFC3339) + `"} {}`)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, signedWebhookRequest(
		t,
		policy,
		"/api/v1/billing/webhooks/personal/payment-settled",
		body,
		"event-trailing-json",
	))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if personal.settlementCalls != 0 {
		t.Fatal("signed trailing JSON reached a settlement service")
	}
}

func TestCreatePersonalTopUpRejectsSubMinimumBeforeService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &personalPaymentServiceStub{}
	personalHandler := handler.NewPersonalPaymentHandler(service, entity.PaymentPolicy{
		MinimumTopUp: 1_000_000,
	})
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(pkgcontext.CtxUserID, uuid.New())
		c.Next()
	})
	router.POST("/api/v1/personal/billing/wallet/top-ups", personalHandler.CreateTopUp)

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/personal/billing/wallet/top-ups",
		bytes.NewBufferString(`{"amount_micro_units":"999999"}`),
	)
	request.Header.Set("content-type", "application/json")
	request.Header.Set("idempotency-key", "sub-minimum")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if service.intentCalls != 0 {
		t.Fatal("sub-minimum top-up reached personal payment service")
	}
}
