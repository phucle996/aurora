import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const consumersTab = new URL(
  "../src/app/(console)/mail/components/ConsumersTab.tsx",
  import.meta.url,
);
const runtimeEntrypoint = new URL("../runtime-entrypoint.mjs", import.meta.url);
const proxy = new URL("../src/proxy.ts", import.meta.url);

test("mail runtime reads use the generic Zone Edge contract", async () => {
  const source = await readFile(consumersTab, "utf8");
  assert.match(source, /mintMailConsumerRuntimeRead\(\s*detailConsumerID,\s*"health",\s*60,/);
  assert.match(source, /`https:\/\/\$\{ticket\.zone_code\}\.\$\{baseDomain\}\$\{ticket\.path\}`/);
  assert.match(source, /"X-Aurora-Runtime-Assertion": ticket\.assertion/);
  assert.match(source, /credentials: "omit"/);
  assert.doesNotMatch(source, /new EventSource/);
  assert.doesNotMatch(source, /runtime\/mail\/consumers/);
  assert.doesNotMatch(source, /runtime[-_/]watch/i);
});

test("runtime assertion is an admission ticket, not a ten-second renewal lease", async () => {
  const source = await readFile(consumersTab, "utf8");
  assert.equal(source.match(/mintMailConsumerRuntimeRead\(/g)?.length, 1);
  assert.match(source, /while \(!controller\.signal\.aborted\)/);
  assert.match(source, /1_000 \+ Math\.floor\(Math\.random\(\) \* 500\)/);
  assert.doesNotMatch(source, /setInterval\([^)]*mintMailConsumerRuntimeRead/s);
});

test("Zone domain and CSP suffix are runtime-injected", async () => {
  const [consumerSource, entrypointSource, proxySource] = await Promise.all([
    readFile(consumersTab, "utf8"),
    readFile(runtimeEntrypoint, "utf8"),
    readFile(proxy, "utf8"),
  ]);
  assert.match(consumerSource, /publicRuntimeConfig\(\)\?\.zonePublicBaseDomain/);
  assert.match(entrypointSource, /NEXT_PUBLIC_ZONE_PUBLIC_BASE_DOMAIN/);
  assert.match(entrypointSource, /process\.env\.AURORA_ZONE_PUBLIC_BASE_DOMAIN = config\.zonePublicBaseDomain/);
  assert.match(entrypointSource, /process\.env\.AURORA_CENTRIFUGO_WS_ORIGIN/);
  assert.match(proxySource, /https:\/\/\*\.\$\{zonePublicBaseDomain\}/);
  assert.match(proxySource, /\$\{centrifugoWsOrigin\}/);
  assert.doesNotMatch(proxySource, /connect-src 'self' https: wss:/);
});

test("stopped mail slots are not counted as active", async () => {
  const source = await readFile(consumersTab, "utf8");
  assert.match(
    source,
    /activeInstances:\s*states\.filter\(\(state\) => state !== 1\)\.length/,
  );
});

test("runtime health expires from the Victoria sample timestamp", async () => {
  const source = await readFile(consumersTab, "utf8");
  assert.match(source, /const sampleAt = Number\(latest\[0\]\) \* 1_000/);
  assert.match(source, /Date\.now\(\) - newestSampleAt <= 45_000/);
  assert.match(source, /Date\.now\(\) - latestSampleAt > 45_000/);
  assert.doesNotMatch(source, /frame\.observed_at/);
});
