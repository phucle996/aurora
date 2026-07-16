package apperr

import "errors"

var (
	ErrWalletNotFound    = errors.New("wallet_not_found")
	ErrPriceNotFound     = errors.New("price_not_found")
	ErrInvalidWallet     = errors.New("invalid_wallet")
	ErrInsufficientFunds = errors.New("insufficient_funds")
	ErrDatabaseFailed    = errors.New("database_operation_failed")
	ErrInternalServer    = errors.New("internal_server_error")
	ErrBadRequest        = errors.New("bad_request")
)
