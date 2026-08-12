package billingmigrations

import (
	"strings"
	"testing"
)

func TestPayGPricingCutoverUsesOneScheduleAuthority(t *testing.T) {
	content := readMigration(t, "000011_payg_pricing_schedule_cutover.up.sql")
	for _, legacy := range []string{
		"DROP TABLE IF EXISTS billing.plans CASCADE",
		"DROP TABLE IF EXISTS billing.packs CASCADE",
		"DROP TABLE IF EXISTS billing.subscriptions CASCADE",
		"CREATE TABLE billing.pricing_schedules",
		"CREATE TABLE billing.charge_kind_catalog",
		"CREATE TABLE billing.usage_settlement_runs",
		"CREATE TABLE IF NOT EXISTS billing.storage_pending_activation_reconcile",
		"wallet-admission-cutover",
		"DROP INDEX IF EXISTS billing.idx_wallet_ledger_billing_run",
		"ex_pricing_schedule_version_effective_window",
		"enforce_scalar_bracket_coverage",
		"pricing_schedule_scalar_bracket_coverage",
	} {
		if !strings.Contains(content, legacy) {
			t.Fatalf("PAYG cutover is missing required authority transition: %s", legacy)
		}
	}
	if strings.Contains(content, "FREE_TIER_100_USD')") {
		t.Fatal("PAYG cutover must not seed the legacy free-tier campaign")
	}
}
