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

type personalPaymentService struct {
	repo        billingRepoInterface.PersonalPaymentRepository
	sharedRedis *goredis.Client
	policy      entity.PaymentPolicy
	checkoutURL url.URL
	returnURL   url.URL
}

func NewPersonalPaymentService(
	repo billingRepoInterface.PersonalPaymentRepository,
	sharedRedis *goredis.Client,
	policy entity.PaymentPolicy,
	checkoutURL url.URL,
	returnURL url.URL,
) *personalPaymentService {
	return &personalPaymentService{
		repo:        repo,
		sharedRedis: sharedRedis,
		policy:      policy,
		checkoutURL: checkoutURL,
		returnURL:   returnURL,
	}
}

func (s *personalPaymentService) GetWallet(
	ctx context.Context,
	ownerID uuid.UUID,
) (*entity.WalletSummary, error) {
	return s.repo.GetPersonalWalletSummary(ctx, ownerID)
}

func (s *personalPaymentService) CreateTopUp(
	ctx context.Context,
	command entity.CreatePersonalPaymentIntentCommand,
) (*entity.PaymentIntent, error) {
	command.Currency = "USD"
	command.Provider = s.policy.Provider
	command.ExpiresAt = time.Now().UTC().Add(s.policy.IntentTTL)
	intent, err := s.repo.CreatePersonalIntent(ctx, command)
	if err != nil {
		return nil, err
	}

	// [COMMENT]: Inline xây dựng checkout URL và ký HMAC signature nếu intent ở trạng thái PENDING
	if intent != nil && intent.Status == "PENDING" {
		checkoutURL := s.checkoutURL
		returnURL := s.returnURL
		returnQuery := returnURL.Query()
		returnQuery.Set("payment_intent_id", intent.ID.String())
		returnURL.RawQuery = returnQuery.Encode()

		// [COMMENT]: Định dạng chuỗi signing payload với đầy đủ các trường owner-bound
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

		// [COMMENT]: Đóng gói query parameters và signature vào Checkout URL
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

	return intent, nil
}

func (s *personalPaymentService) GetTopUp(
	ctx context.Context,
	ownerID uuid.UUID,
	intentID uuid.UUID,
) (*entity.PaymentIntent, error) {
	return s.repo.GetPersonalIntent(ctx, ownerID, intentID)
}

func (s *personalPaymentService) ApplyVerifiedSettlement(
	ctx context.Context,
	settlement entity.PaymentSettlement,
) (*entity.SettlementResult, error) {
	result, err := s.repo.ApplyPersonalSettlement(ctx, settlement)
	if err != nil || result.Replayed {
		return result, err
	}

	activityAction := "billing.wallet.top_up"
	activityTitle := "Personal wallet top-up settled"
	if result.WalletActivated {
		activityAction = "billing.wallet.activate"
		activityTitle = "Personal wallet activated"
	}
	// [COMMENT]: Timeline delivery is UX-only and happens after the money
	// transaction. Shared Redis failure must not make the provider retry a
	// settlement that PostgreSQL already committed.
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
		ResourceType: "personal_wallet",
		ResourceID:   result.WalletID.String(),
		OperationID:  settlement.ProviderEventID,
		Title:        activityTitle,
		Summary:      "A verified payment settlement credited the personal wallet",
		OccurredAt:   settlement.SettledAt,
		Metadata: map[string]any{
			"payment_intent_id":  settlement.PaymentIntentID.String(),
			"amount_micro_units": strconv.FormatInt(settlement.Amount, 10),
			"currency":           settlement.Currency,
			"owner_id":           result.OwnerID.String(),
			"owner_type":         string(result.OwnerType),
			"referral_applied":   result.ReferralApplied,
			"wallet_activated":   result.WalletActivated,
		},
	}); activityErr != nil {
		logger.SysError("billing.user_activity.personal_wallet_settlement", activityErr.Error())
	}
	return result, nil
}
