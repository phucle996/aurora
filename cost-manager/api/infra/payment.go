package infra

import (
	"context"
	"fmt"
	"strings"

	"cost-manager/api/internal/config"
)

const PaymentSecretPath = "secret/data/integrations/payment/cost-manager-api"

type paymentSecretRecord struct {
	SchemaVersion         int    `json:"schema_version"`
	CheckoutSigningSecret string `json:"checkout_signing_secret"`
	WebhookSigningSecret  string `json:"webhook_signing_secret"`
}

// ReadPaymentSecrets loads the provider HMAC material through the Cost Manager
// policy. The values stay in bounded process memory because the payment SDK
// needs the raw signing key; they never enter Config's environment loader.
func ReadPaymentSecrets(ctx context.Context, client *VaultClient, payment *config.PaymentCfg) error {
	if client == nil || payment == nil {
		return fmt.Errorf("payment Vault connector requires client and config")
	}
	var record paymentSecretRecord
	if err := client.ReadJSON(ctx, PaymentSecretPath, &record); err != nil {
		return fmt.Errorf("read payment signing secrets: %w", err)
	}
	checkout := strings.TrimSpace(record.CheckoutSigningSecret)
	webhook := strings.TrimSpace(record.WebhookSigningSecret)
	if record.SchemaVersion != 1 ||
		len(checkout) < 32 ||
		len(webhook) < 32 ||
		checkout == webhook {
		return fmt.Errorf("payment signing secret record is invalid")
	}
	payment.CheckoutSigningSecret = checkout
	payment.WebhookSigningSecret = webhook
	return nil
}
