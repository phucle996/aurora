package entity

import (
	"time"

	"github.com/google/uuid"
)

type OwnerType string

const (
	OwnerTypePersonal OwnerType = "PERSONAL"
	OwnerTypeTenant   OwnerType = "TENANT"
)

const (
	WalletStatusPendingActivation = "PENDING_ACTIVATION"
	WalletStatusActive            = "ACTIVE"
	WalletStatusSuspended         = "SUSPENDED"
	WalletStatusClosed            = "CLOSED"
)

type WalletSummary struct {
	WalletID                     uuid.UUID
	Currency                     string
	CashBalanceMicroUnits        int64
	PromotionalBalanceMicroUnits int64
	OverdraftLimitMicroUnits     int64
	Status                       string
	Version                      int64
	UpdatedAt                    time.Time
}

type ReferralReservation struct {
	ID                     uuid.UUID
	CampaignID             uuid.UUID
	Code                   string
	Status                 string
	GrantAmountMicroUnits  int64
	MinimumTopUpMicroUnits int64
	Currency               string
	ExpiresAt              time.Time
	GrantExpiresAt         *time.Time
	RedeemedAt             *time.Time
	RejectionReason        string
}

type OnboardingSnapshot struct {
	Wallet              WalletSummary
	MinimumTopUp        int64
	Referral            *ReferralReservation
	LatestPaymentIntent *PaymentIntent
}

type ReserveReferralCommand struct {
	OwnerID        uuid.UUID
	Code           string
	IdempotencyKey string
	ExpiresAt      time.Time
}

type CreatePersonalPaymentIntentCommand struct {
	OwnerID        uuid.UUID
	Amount         int64
	Currency       string
	Provider       string
	IdempotencyKey string
	ExpiresAt      time.Time
}

type CreateTenantPaymentIntentCommand struct {
	TenantID       uuid.UUID
	ActorID        uuid.UUID
	Amount         int64
	Currency       string
	Provider       string
	IdempotencyKey string
	ExpiresAt      time.Time
}

type PaymentIntent struct {
	ID                    uuid.UUID
	OwnerID               uuid.UUID
	OwnerType             OwnerType
	ActorID               uuid.UUID
	WalletID              uuid.UUID
	AmountMicroUnits      int64
	Currency              string
	Provider              string
	ProviderPaymentID     string
	Status                string
	ActivatesWallet       bool
	ReferralReservationID *uuid.UUID
	ExpiresAt             time.Time
	SettledAt             *time.Time
	CreatedAt             time.Time
	CheckoutURL           string
	Created               bool
}

type PaymentSettlement struct {
	Provider          string
	ProviderEventID   string
	ProviderPaymentID string
	PaymentIntentID   uuid.UUID
	OwnerType         OwnerType
	Amount            int64
	Currency          string
	SettledAt         time.Time
	PayloadHash       string
}

type SettlementResult struct {
	PaymentIntentID      uuid.UUID
	WalletID             uuid.UUID
	OwnerID              uuid.UUID
	OwnerType            OwnerType
	ActorID              uuid.UUID
	WalletStatus         string
	CashBalance          int64
	PromotionalBalance   int64
	ReferralApplied      bool
	ReferralRejectReason string
	WalletActivated      bool
	Replayed             bool
}

type ReferralCampaign struct {
	ID                     uuid.UUID
	Code                   string
	Name                   string
	AmountMicroUnits       int64
	MinimumTopUpMicroUnits int64
	Currency               string
	Status                 string
	MaxRedemptions         *int64
	Redemptions            int64
	ActiveReservations     int64
	Version                int64
	StartsAt               time.Time
	EndsAt                 *time.Time
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

type CreateReferralCampaignCommand struct {
	Code                   string
	Name                   string
	AmountMicroUnits       int64
	MinimumTopUpMicroUnits int64
	Currency               string
	MaxRedemptions         *int64
	StartsAt               time.Time
	EndsAt                 *time.Time
}

type UpdateReferralCampaignStatusCommand struct {
	ID              uuid.UUID
	Status          string
	ExpectedVersion int64
}

type PaymentPolicy struct {
	Provider           string
	CheckoutBaseURL    string
	ReturnBaseURL      string
	CheckoutSigningKey string
	WebhookSigningKey  string
	MinimumTopUp       int64
	IntentTTL          time.Duration
	ReferralTTL        time.Duration
	WebhookTolerance   time.Duration
}
