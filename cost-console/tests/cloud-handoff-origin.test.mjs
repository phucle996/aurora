import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const envExample = new URL("../.env.example", import.meta.url);
const authStore = new URL("../src/lib/store/useAuthStore.ts", import.meta.url);

test("Cost PKCE handoff returns to the public Cloud authority", async () => {
  const [envSource, authSource] = await Promise.all([
    readFile(envExample, "utf8"),
    readFile(authStore, "utf8"),
  ]);

  assert.match(envSource, /^VITE_CLOUD_CONSOLE_URL=https:\/\/cloud\.aurora\.local$/m);
  assert.doesNotMatch(envSource, /^VITE_CLOUD_CONSOLE_URL=https:\/\/localhost$/m);
  assert.match(authSource, /cloudConsoleRuntimeUrl\(\)/);
  assert.match(authSource, /\$\{cloudOrigin\}\/billing\/authorize\?/);
});
