package billingmigrations

import (
	"strings"
	"testing"
)

func TestPayGBaselineUsesOneScheduleAuthority(t *testing.T) {
	pricing := readMigration(t, "000003_tables_pricing.up.sql")
	settlement := readMigration(t, "000004_tables_settlement.up.sql")
	enforcement := readMigration(t, "000005_indexes_and_triggers.up.sql")
	seeds := readMigration(t, "000006_seeds.up.sql")
	for _, required := range []struct {
		name    string
		content string
		needle  string
	}{
		{"pricing tables", pricing, "CREATE TABLE billing.pricing_schedules"},
		{"charge kind catalog", pricing, "CREATE TABLE billing.charge_kind_catalog"},
		{"settlement runs", pricing, "CREATE TABLE billing.usage_settlement_runs"},
		{"storage admission queue", readMigration(t, "000002_tables_core.up.sql"), "CREATE TABLE billing.storage_pending_activation_reconcile"},
		{"version overlap guard", pricing, "ex_pricing_schedule_version_effective_window"},
		{"bracket coverage guard", enforcement, "enforce_scalar_bracket_coverage"},
		{"bracket deferred trigger", enforcement, "pricing_schedule_scalar_bracket_coverage"},
		{"storage settlement evidence", settlement, "CREATE TABLE billing.storage_usage_line_inbox"},
	} {
		if !strings.Contains(required.content, required.needle) {
			t.Fatalf("%s is missing required PAYG authority: %s", required.name, required.needle)
		}
	}
	if strings.Contains(seeds, "FREE_TIER") || strings.Contains(seeds, "monthly_price") {
		t.Fatal("PAYG baseline must not seed legacy free/pro commercial plans")
	}
}
