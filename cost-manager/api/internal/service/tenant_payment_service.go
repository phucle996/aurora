package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strconv"
	"strings"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingRepoInterface "cost-manager/api/internal/domain/repo"
	"cost-manager/api/internal/useractivity"
	"cost-manager/api/pkg/logger"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

type tenantPaymentService struct {
	repo        billingRepoInterface.TenantPaymentRepository
	sharedRedis *goredis.Client
	policy      entity.PaymentPolicy
	checkoutURL url.URL
	returnURL   url.URL
}

func NewTenantPaymentService(
	repo billingRepoInterface.TenantPaymentRepository,
	sharedRedis *goredis.Client,
	policy entity.PaymentPolicy,
	checkoutURL url.URL,
	returnURL url.URL,
) *tenantPaymentService {
	return &tenantPaymentService{
		repo:        repo,
		sharedRedis: sharedRedis,
		policy:      policy,
		checkoutURL: checkoutURL,
		returnURL:   returnURL,
	}
}

func (s *tenantPaymentService) GetWallet(
	ctx context.Context,
	tenantID uuid.UUID,
) (*entity.WalletSummary, error) {
	return s.repo.GetTenantWalletSummary(ctx, tenantID)
}

func (s *tenantPaymentService) CreateTopUp(
	ctx context.Context,
	command entity.CreateTenantPaymentIntentCommand,
) (*entity.PaymentIntent, error) {
	command.Currency = "USD"
	command.Provider = s.policy.Provider
	command.ExpiresAt = time.Now().UTC().Add(s.policy.IntentTTL)
	intent, err := s.repo.CreateTenantIntent(ctx, command)
	if err != nil {
		return nil, err
	}
	s.buildCheckout(intent)
	return intent, nil
}

func (s *tenantPaymentService) GetTopUp(
	ctx context.Context,
	tenantID uuid.UUID,
	intentID uuid.UUID,
) (*entity.PaymentIntent, error) {
	return s.repo.GetTenantIntent(ctx, tenantID, intentID)
}

func (s *tenantPaymentService) ApplyVerifiedSettlement(
	ctx context.Context,
	settlement entity.PaymentSettlement,
) (*entity.SettlementResult, error) {
	result, err := s.repo.ApplyTenantSettlement(ctx, settlement)
	if err != nil || result.Replayed {
		return result, err
	}

	activityAction := "billing.wallet.top_up"
	activityTitle := "Tenant wallet top-up settled"
	if result.WalletActivated {
		activityAction = "billing.wallet.activate"
		activityTitle = "Tenant wallet activated"
	}
	// [COMMENT]: A tenant actor is retained for self-history, while the wallet
	// and ledger remain owned by the tenant aggregate.
	activityCtx, cancelActivity := context.WithTimeout(context.WithoutCancel(ctx), 150*time.Millisecond)
	defer cancelActivity()
	if activityErr := useractivity.Append(activityCtx, s.sharedRedis, useractivity.Event{
		EventID:      uuid.New().String(),
		UserID:       result.ActorID.String(),
		Category:     "billing",
		Action:       activityAction,
		ActorType:    "system",
		Outcome:      "succeeded",
		Source:       "cost-manager",
		ResourceType: "tenant_wallet",
		ResourceID:   result.WalletID.String(),
		OperationID:  settlement.ProviderEventID,
		Title:        activityTitle,
		Summary:      "A verified payment settlement credited the tenant wallet",
		OccurredAt:   settlement.SettledAt,
		Metadata: map[string]any{
			"payment_intent_id":  settlement.PaymentIntentID.String(),
			"amount_micro_units": strconv.FormatInt(settlement.Amount, 10),
			"currency":           settlement.Currency,
			"tenant_id":          result.OwnerID.String(),
			"owner_type":         string(result.OwnerType),
			"wallet_activated":   result.WalletActivated,
		},
	}); activityErr != nil {
		logger.SysError("billing.user_activity.tenant_wallet_settlement", activityErr.Error())
	}
	return result, nil
}

func (s *tenantPaymentService) buildCheckout(intent *entity.PaymentIntent) {
	if intent == nil || intent.Status != "PENDING" {
		return
	}
	checkoutURL := s.checkoutURL
	returnURL := s.returnURL
	returnQuery := returnURL.Query()
	returnQuery.Set("payment_intent_id", intent.ID.String())
	returnURL.RawQuery = returnQuery.Encode()

	signingPayload := strings.Join([]string{
		"aurora.checkout.v1",
		intent.ID.String(),
		string(intent.OwnerType),
		strconv.FormatInt(intent.AmountMicroUnits, 10),
		intent.Currency,
		strconv.FormatInt(intent.ExpiresAt.Unix(), 10),
		returnURL.String(),
	}, "\n")
	mac := hmac.New(sha256.New, []byte(s.policy.CheckoutSigningKey))
	_, _ = mac.Write([]byte(signingPayload))

	query := checkoutURL.Query()
	query.Set("payment_intent_id", intent.ID.String())
	query.Set("owner_type", string(intent.OwnerType))
	query.Set("amount_micro_units", strconv.FormatInt(intent.AmountMicroUnits, 10))
	query.Set("currency", intent.Currency)
	query.Set("expires_at", strconv.FormatInt(intent.ExpiresAt.Unix(), 10))
	query.Set("return_url", returnURL.String())
	query.Set("signature", base64.RawURLEncoding.EncodeToString(mac.Sum(nil)))
	checkoutURL.RawQuery = query.Encode()
	intent.CheckoutURL = checkoutURL.String()
}
