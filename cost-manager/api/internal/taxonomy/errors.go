// Package billingTaxonomy định nghĩa danh mục phân loại lỗi (Error Taxonomy) cố định cho module Cost & Billing Control Plane.
package billingTaxonomy

import "errors"

// Sentinel errors chính cho hệ thống Cost & Billing (pricing schedules, wallets and payments).
var (
	// Lỗi hệ thống và cơ sở dữ liệu chung
	ErrInternalError      = errors.New("billing: internal server error")
	ErrDatabaseFailed     = errors.New("billing: database operation failed")
	ErrInvalidArgument    = errors.New("billing: invalid argument")
	ErrPreconditionFailed = errors.New("billing: precondition failed")
	ErrConflict           = errors.New("billing: concurrent conflict or duplicate entry")

	ErrNotFound = errors.New("billing: not found")

	// Pricing schedule catalog errors.
	ErrPricingScheduleNotFound          = errors.New("billing: pricing schedule not found")
	ErrPricingScheduleVersionConflict   = errors.New("billing: pricing schedule version conflict")
	ErrPricingScheduleMetadataConflict  = errors.New("billing: pricing schedule metadata version conflict")
	ErrPricingScheduleEffectiveConflict = errors.New("billing: pricing schedule effective time conflict")
	ErrInvalidPricingBrackets           = errors.New("billing: invalid pricing schedule brackets")
	ErrStorageZoneAdjustmentConflict    = errors.New("billing: Storage Zone adjustment version conflict")
	ErrHypervisorZoneAdjustmentConflict = errors.New("billing: Hypervisor Zone adjustment version conflict")
	ErrMailZoneAdjustmentConflict       = errors.New("billing: Mail Zone adjustment version conflict")
	ErrHypervisorResourcePlanNotFound   = errors.New("billing: Hypervisor resource plan not found")
	ErrHypervisorResourcePlanConflict   = errors.New("billing: Hypervisor resource plan revision conflict")

	// Lỗi liên quan đến Thuê bao Subscription
	ErrSubscriptionNotFound = errors.New("billing: active subscription not found")
	ErrAlreadySubscribed    = errors.New("billing: tenant already subscribed to this pack")
	ErrIdempotencyConflict  = errors.New("billing: idempotency request conflict")

	// Lỗi liên quan đến Ví tiền & Thanh toán (Wallet & Billing Ledger)
	ErrWalletNotFound    = errors.New("billing: wallet not found")
	ErrInvalidWallet     = errors.New("billing: invalid wallet state")
	ErrInsufficientFunds = errors.New("billing: insufficient wallet balance")
	ErrPriceNotFound     = errors.New("billing: pay-as-you-go price not found")

	ErrWalletAlreadyActive        = errors.New("billing: wallet is already active")
	ErrReferralNotFound           = errors.New("billing: referral code not found")
	ErrReferralUnavailable        = errors.New("billing: referral campaign unavailable")
	ErrReferralAlreadyReserved    = errors.New("billing: onboarding referral already reserved")
	ErrReferralAlreadyRedeemed    = errors.New("billing: onboarding referral already redeemed")
	ErrPaymentIntentNotFound      = errors.New("billing: payment intent not found")
	ErrPaymentIntentExpired       = errors.New("billing: payment intent expired")
	ErrSettlementMismatch         = errors.New("billing: settlement does not match payment intent")
	ErrWebhookReplayConflict      = errors.New("billing: webhook event replayed with different payload")
	ErrPaymentProviderUnavailable = errors.New("billing: payment provider unavailable")

	// Lỗi liên quan đến Quyền sở hữu tài nguyên (Resource Ownership Projection)
	ErrResourceOwnershipIntegrity = errors.New("billing: resource ownership integrity violation")
)
