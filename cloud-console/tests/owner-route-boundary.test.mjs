import assert from "node:assert/strict";
import { readdir, readFile, stat } from "node:fs/promises";
import path from "node:path";
import test from "node:test";

async function sourceFiles(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = [];
  for (const entry of entries) {
    const fullPath = path.join(directory, entry.name);
    if (entry.isDirectory()) files.push(...await sourceFiles(fullPath));
    if (entry.isFile() && /\.(?:ts|tsx|js|jsx)$/.test(entry.name)) files.push(fullPath);
  }
  return files;
}

test("browser API clients never address internal owner-prefixed routes", async () => {
  const root = path.resolve("src");
  const violations = [];
  for (const file of await sourceFiles(root)) {
    const source = await readFile(file, "utf8");
    if (/\/api\/v1\/(?:personal|tenant)(?:\/|["'`])/.test(source)) {
      violations.push(path.relative(root, file));
    }
  }
  assert.deepEqual(violations, []);
});

test("render context uses the neutral edge contract and both URL roots exist", async () => {
  const apiSource = await readFile(path.resolve("src/session/api.ts"), "utf8");
  assert.match(apiSource, /"\/api\/v1\/iam\/context\/read"/);
  assert.doesNotMatch(apiSource, /\/api\/v1\/me\/iam\/context\/read/);
  assert.equal((await stat(path.resolve("src/app/personal/layout.tsx"))).isFile(), true);
  assert.equal((await stat(path.resolve("src/app/tenant/layout.tsx"))).isFile(), true);
});

test("self-user critical APIs keep me before critical and never select an owner", async () => {
  const settingsAPI = await readFile(path.resolve("src/features/settings/api.ts"), "utf8");
  assert.match(settingsAPI, /\/api\/v1\/me\/critical\/iam\/social-link/);
  assert.doesNotMatch(settingsAPI, /\/api\/v1\/critical\/me\//);
});
