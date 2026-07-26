package service_test

import (
	"context"
	"testing"
	"time"

	"cost-manager/api/internal/domain/entity"
	billingService "cost-manager/api/internal/service"

	"github.com/google/uuid"
)

type personalAccountRepoStub struct {
	referralCommand entity.ReserveReferralCommand
	err             error
}

func (s *personalAccountRepoStub) ApplyPersonalWalletProvision(context.Context, uuid.UUID, uuid.UUID, string) error {
	return s.err
}
func (s *personalAccountRepoStub) GetOnboarding(context.Context, uuid.UUID, int64) (*entity.OnboardingSnapshot, error) {
	return nil, s.err
}
func (s *personalAccountRepoStub) ReserveReferral(_ context.Context, command entity.ReserveReferralCommand) (*entity.ReferralReservation, error) {
	s.referralCommand = command
	return &entity.ReferralReservation{ID: uuid.New(), ExpiresAt: command.ExpiresAt}, s.err
}
func (s *personalAccountRepoStub) ListReferralCampaigns(context.Context) ([]entity.ReferralCampaign, error) {
	return nil, s.err
}
func (s *personalAccountRepoStub) CreateReferralCampaign(context.Context, entity.CreateReferralCampaignCommand) (*entity.ReferralCampaign, error) {
	return nil, s.err
}
func (s *personalAccountRepoStub) UpdateReferralCampaignStatus(context.Context, entity.UpdateReferralCampaignStatusCommand) (*entity.ReferralCampaign, error) {
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

func TestReserveReferralPinsServerTTL(t *testing.T) {
	repo := &personalAccountRepoStub{}
	policy := testPaymentPolicy()
	service := billingService.NewPersonalAccountService(repo, policy)
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
