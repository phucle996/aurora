package dto

// [COMMENT]: CreateTopUpRequest DTO cho payload nạp tiền vào ví
type CreateTopUpRequest struct {
	AmountMicroUnits string `json:"amount_micro_units" binding:"required"`
}

// [COMMENT]: PaymentSettlementWebhookRequest DTO cho webhook thanh toán từ payment gateway
type PaymentSettlementWebhookRequest struct {
	PaymentIntentID   string `json:"payment_intent_id"`
	OwnerType         string `json:"owner_type"`
	ProviderPaymentID string `json:"provider_payment_id"`
	AmountMicroUnits  string `json:"amount_micro_units"`
	Currency          string `json:"currency"`
	SettledAt         string `json:"settled_at"`
}
