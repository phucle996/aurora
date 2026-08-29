import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const page = new URL('../src/page/pricing-schedules/page.tsx', import.meta.url);
const zoneTab = new URL('../src/page/pricing-schedules/ZonePriceAdjustmentsTab.tsx', import.meta.url);
const billingApi = new URL('../src/lib/api/billing.ts', import.meta.url);
const criticalFetcher = new URL('../src/lib/api/criticalFetcher.ts', import.meta.url);
const fetcher = new URL('../src/lib/api/fetcher.ts', import.meta.url);

test('Pricing keeps base schedules and resource plans in URL-backed top-level tabs', async () => {
  const source = await readFile(page, 'utf8');

  assert.match(source, /useSearchParams/);
  assert.match(source, /type PricingTab = 'base-pricing' \| 'resource-plans' \| 'zone-adjustments'/);
  assert.match(source, /activeTab === 'resource-plans' \? <HypervisorResourcePlansPanel/);
  assert.doesNotMatch(source, /<HypervisorResourcePlansPanel canPublish=\{canPublish\} \/>\n    <\/div>/);
});

test('Zone adjustments are module-scoped in their own URL-backed tab', async () => {
  const source = await readFile(page, 'utf8');

  assert.match(source, /Zone adjustments/);
  assert.match(source, /activeTab === 'zone-adjustments' \? <ZonePriceAdjustmentsTab/);
  assert.match(source, /searchParams\.get\('zone'\)/);
  assert.match(source, /case 'hypervisor\.vcpu\.allocated_second': return 'vCPU-second'/);
});

test('Base price tiers explain their commercial meaning and enforce contiguous ranges before publish', async () => {
  const source = await readFile(page, 'utf8');

  assert.match(source, /How a tier is priced/);
  assert.match(source, /Usage from \(\{unitHint\}\)/);
  assert.match(source, /Up to, exclusive \(\{unitHint\}\)/);
  assert.match(source, /Price \(micro \{selected\.currency\}\)/);
  assert.match(source, /Billed quantity \(\{unitHint\}\)/);
  assert.match(source, /Tier \$\{index \+ 2\} must begin exactly where tier/);
  assert.match(source, /Set a finite upper bound for the current tier before adding the next one/);
});

test('Zone adjustment selection uses a zone code and never puts a Zone ID in the mutation body', async () => {
  const [tabSource, apiSource, criticalSource, fetcherSource] = await Promise.all([
    readFile(zoneTab, 'utf8'),
    readFile(billingApi, 'utf8'),
    readFile(criticalFetcher, 'utf8'),
    readFile(fetcher, 'utf8'),
  ]);

  assert.match(tabSource, /Target Zone/);
  assert.match(tabSource, /\{zone\.name\} \(\{zone\.code\}\)/);
  assert.match(apiSource, /zone_code: zoneCode/);
  assert.match(apiSource, /zone-price-adjustments\/versions\?zone_code=\$\{encodeURIComponent\(zoneCode\)\}/);
  assert.match(criticalSource, /const pathWithoutQuery = normalizedPath\.split\('\?', 1\)\[0\]/);
  assert.match(criticalSource, /apiRequestPath\(normalizedPath\)/);
  assert.match(fetcherSource, /return `\$\{parsed\.pathname\}\$\{parsed\.search\}`/);
});
