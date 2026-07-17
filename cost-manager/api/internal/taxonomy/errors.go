// Package billingTaxonomy định nghĩa danh mục phân loại lỗi (Error Taxonomy) cố định cho module Cost & Billing Control Plane.
package billingTaxonomy

import "errors"

// Sentinel errors chính cho hệ thống Cost & Billing (Packs, Plans, Subscriptions, Wallets)
var (
	// Lỗi hệ thống và cơ sở dữ liệu chung
	ErrInternalError      = errors.New("billing: internal server error")
	ErrDatabaseFailed     = errors.New("billing: database operation failed")
	ErrInvalidArgument    = errors.New("billing: invalid argument")
	ErrPreconditionFailed = errors.New("billing: precondition failed")
	ErrConflict           = errors.New("billing: concurrent conflict or duplicate entry")

	// Lỗi liên quan đến Pack & Resource SKU Plan Catalog
	ErrPackNotFound  = errors.New("billing: pack not found")
	ErrPackNotActive = errors.New("billing: pack is deprecated or inactive")
	ErrPlanNotFound  = errors.New("billing: resource plan not found")
	ErrPlanNotActive = errors.New("billing: resource plan is inactive")

	// Lỗi liên quan đến Thuê bao Subscription
	ErrSubscriptionNotFound = errors.New("billing: active subscription not found")
	ErrAlreadySubscribed    = errors.New("billing: tenant already subscribed to this pack")
	ErrIdempotencyConflict  = errors.New("billing: idempotency request conflict")

	// Lỗi liên quan đến Ví tiền & Thanh toán (Wallet & Billing Ledger)
	ErrWalletNotFound    = errors.New("billing: wallet not found")
	ErrInvalidWallet     = errors.New("billing: invalid wallet state")
	ErrInsufficientFunds = errors.New("billing: insufficient wallet balance")
	ErrPriceNotFound     = errors.New("billing: pay-as-you-go price not found")
)
