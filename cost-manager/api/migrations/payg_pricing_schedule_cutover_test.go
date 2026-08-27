package billingmigrations

import (
	"strings"
	"testing"
)

func TestPayGBaselineUsesOneScheduleAuthority(t *testing.T) {
	pricing := readMigration(t, "000003_tables_pricing.up.sql")
	enums := readMigration(t, "000001_enums.up.sql")
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
		{"storage Zone adjustment authority", pricing, "CREATE TABLE billing.storage_zone_price_adjustment_versions"},
		{"storage admission queue", readMigration(t, "000002_tables_core.up.sql"), "CREATE TABLE billing.storage_pending_activation_reconcile"},
		{"version overlap guard", pricing, "ex_pricing_schedule_version_effective_window"},
		{"bracket coverage guard", enforcement, "enforce_scalar_bracket_coverage"},
		{"bracket deferred trigger", enforcement, "pricing_schedule_scalar_bracket_coverage"},
		{"storage settlement evidence", settlement, "CREATE TABLE billing.storage_usage_line_inbox"},
		{"hypervisor vCPU catalog schedule", seeds, "hypervisor-vcpu-payg"},
		{"hypervisor memory catalog schedule", seeds, "hypervisor-memory-payg"},
		{"hypervisor disk catalog schedule", seeds, "hypervisor-disk-payg"},
		{"hypervisor network-in catalog schedule", seeds, "hypervisor-network-in-payg"},
		{"hypervisor network-out catalog schedule", seeds, "hypervisor-network-out-payg"},
		{"hypervisor network report inbox", settlement, "CREATE TABLE billing.hypervisor_network_usage_report_inbox"},
		{"hypervisor flat network lines", settlement, "CREATE TABLE billing.hypervisor_network_usage_lines"},
	} {
		if !strings.Contains(required.content, required.needle) {
			t.Fatalf("%s is missing required PAYG authority: %s", required.name, required.needle)
		}
	}
	if strings.Contains(seeds, "FREE_TIER") || strings.Contains(seeds, "monthly_price") {
		t.Fatal("PAYG baseline must not seed legacy free/pro commercial plans")
	}
	if strings.Contains(enums, "pricing_scope") || strings.Contains(pricing, "scope_type") {
		t.Fatal("PAYG base schedules must remain Global-only; module scope belongs to module adjustment workflows")
	}
	for _, forbidden := range []string{"Initial PAYG hypervisor schedule", "hypervisor-vcpu-initial-price", "hypervisor-memory-initial-price", "hypervisor-disk-initial-price", "hypervisor-network-in-initial-price", "hypervisor-network-out-initial-price"} {
		if strings.Contains(seeds, forbidden) {
			t.Fatalf("Hypervisor catalog must not invent a commercial price: %s", forbidden)
		}
	}
	if strings.Contains(pricing, "usage_settlement_runs") && strings.Contains(pricing, "zone_id                   UUID NOT NULL REFERENCES billing.zone_catalog") {
		t.Fatal("generic settlement runs must not own a Zone foreign key")
	}
}
