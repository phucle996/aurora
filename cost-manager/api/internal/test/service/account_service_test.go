package service_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingService "cost-manager/api/internal/service"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
)

type accountRepoStub struct {
	referralCommand entity.ReserveReferralCommand
	err             error
}

func (s *accountRepoStub) ApplyPersonalWalletProvision(context.Context, uuid.UUID, uuid.UUID, string) error {
	return s.err
}
func (s *accountRepoStub) ApplyTenantWalletProvision(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string) error {
	return s.err
}
func (s *accountRepoStub) GetPersonalWalletSummary(context.Context, uuid.UUID) (*entity.WalletSummary, error) {
	return nil, s.err
}
func (s *accountRepoStub) GetOnboarding(context.Context, uuid.UUID, int64) (*entity.OnboardingSnapshot, error) {
	return nil, s.err
}
func (s *accountRepoStub) ReserveReferral(_ context.Context, command entity.ReserveReferralCommand) (*entity.ReferralReservation, error) {
	s.referralCommand = command
	return &entity.ReferralReservation{ID: uuid.New(), ExpiresAt: command.ExpiresAt}, s.err
}
func (s *accountRepoStub) ListReferralCampaigns(context.Context) ([]entity.ReferralCampaign, error) {
	return nil, s.err
}
func (s *accountRepoStub) CreateReferralCampaign(context.Context, entity.CreateReferralCampaignCommand) (*entity.ReferralCampaign, error) {
	return nil, s.err
}
func (s *accountRepoStub) UpdateReferralCampaignStatus(context.Context, entity.UpdateReferralCampaignStatusCommand) (*entity.ReferralCampaign, error) {
	return nil, s.err
}

type personalPaymentRepoStub struct {
	command entity.CreatePersonalPaymentIntentCommand
	intent  *entity.PaymentIntent
	err     error
}

func (s *personalPaymentRepoStub) GetPersonalWalletSummary(context.Context, uuid.UUID) (*entity.WalletSummary, error) {
	return nil, s.err
}
func (s *personalPaymentRepoStub) CreatePersonalIntent(
	_ context.Context,
	command entity.CreatePersonalPaymentIntentCommand,
) (*entity.PaymentIntent, error) {
	s.command = command
	if s.intent != nil {
		return s.intent, s.err
	}
	return &entity.PaymentIntent{
		ID:               uuid.New(),
		OwnerID:          command.OwnerID,
		OwnerType:        entity.OwnerTypePersonal,
		ActorID:          command.OwnerID,
		AmountMicroUnits: command.Amount,
		Currency:         command.Currency,
		Provider:         command.Provider,
		Status:           "PENDING",
		ExpiresAt:        command.ExpiresAt,
		Created:          true,
	}, s.err
}
func (s *personalPaymentRepoStub) GetPersonalIntent(context.Context, uuid.UUID, uuid.UUID) (*entity.PaymentIntent, error) {
	return s.intent, s.err
}
func (s *personalPaymentRepoStub) ApplyPersonalSettlement(context.Context, entity.PaymentSettlement) (*entity.SettlementResult, error) {
	return nil, s.err
}

func testPaymentPolicy() entity.PaymentPolicy {
	return entity.PaymentPolicy{
		Provider:           "test-gateway",
		CheckoutBaseURL:    "https://payments.aurora.test/checkout",
		ReturnBaseURL:      "https://cost.aurora.test/",
		CheckoutSigningKey: "checkout-signing-secret-at-least-32-bytes",
		WebhookSigningKey:  "webhook-signing-secret-at-least-32-bytes",
		MinimumTopUp:       1_000_000,
		IntentTTL:          30 * time.Minute,
		ReferralTTL:        24 * time.Hour,
		WebhookTolerance:   5 * time.Minute,
	}
}

func TestPersonalPaymentServiceSignsOwnerBoundDurableIntent(t *testing.T) {
	repo := &personalPaymentRepoStub{}
	policy := testPaymentPolicy()
	checkoutURL, _ := url.Parse(policy.CheckoutBaseURL)
	returnURL, _ := url.Parse(policy.ReturnBaseURL)
	service := billingService.NewPersonalPaymentService(
		repo,
		goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"}),
		policy,
		*checkoutURL,
		*returnURL,
	)
	intent, err := service.CreateTopUp(context.Background(), entity.CreatePersonalPaymentIntentCommand{
		OwnerID: uuid.New(), Amount: 2_500_000, IdempotencyKey: "top-up-1",
	})
	if err != nil {
		t.Fatalf("CreateTopUp() error = %v", err)
	}
	if repo.command.Provider != policy.Provider ||
		repo.command.Currency != "USD" ||
		repo.command.ExpiresAt.IsZero() {
		t.Fatalf("repository command did not receive server-owned payment fields: %#v", repo.command)
	}

	checkout, err := url.Parse(intent.CheckoutURL)
	if err != nil {
		t.Fatalf("parse checkout URL: %v", err)
	}
	payload := strings.Join([]string{
		"aurora.checkout.v1",
		intent.ID.String(),
		string(entity.OwnerTypePersonal),
		strconv.FormatInt(intent.AmountMicroUnits, 10),
		intent.Currency,
		strconv.FormatInt(intent.ExpiresAt.Unix(), 10),
		checkout.Query().Get("return_url"),
	}, "\n")
	mac := hmac.New(sha256.New, []byte(policy.CheckoutSigningKey))
	_, _ = mac.Write([]byte(payload))
	wantSignature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if checkout.Query().Get("owner_type") != string(entity.OwnerTypePersonal) ||
		checkout.Query().Get("signature") != wantSignature {
		t.Fatal("checkout owner binding or signature mismatch")
	}
}

func TestReserveReferralPinsServerTTL(t *testing.T) {
	repo := &accountRepoStub{}
	policy := testPaymentPolicy()
	service := billingService.NewAccountService(repo, policy)
	before := time.Now().UTC()
	_, err := service.ReserveReferral(context.Background(), entity.ReserveReferralCommand{
		OwnerID: uuid.New(), Code: "AURORA", IdempotencyKey: "reserve-1",
	})
	if err != nil {
		t.Fatalf("ReserveReferral() error = %v", err)
	}
	want := before.Add(policy.ReferralTTL)
	if repo.referralCommand.ExpiresAt.Before(want) ||
		repo.referralCommand.ExpiresAt.After(want.Add(time.Second)) {
		t.Fatalf("reservation expiry %v is outside server TTL window", repo.referralCommand.ExpiresAt)
	}
}
