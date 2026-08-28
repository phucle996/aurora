import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const screenPath = new URL("../src/features/tenants/rbac-screen.tsx", import.meta.url);
const apiPath = new URL("../src/features/tenants/api.ts", import.meta.url);

test("tenant RBAC read does not require permission-catalog authority", async () => {
  const source = await readFile(screenPath, "utf8");

  assert.match(source, /const roleItems = await listTenantRoles/);
  assert.match(source, /if \(canReadPermissions\)/);
  assert.doesNotMatch(
    source,
    /Promise\.all\(\[listTenantRoles[^\]]*listTenantPermissions/,
  );
});

test("tenant RBAC mutations are gated independently and rollout is confirmed", async () => {
  const source = await readFile(screenPath, "utf8");

  assert.match(source, /checkPermission\("iam:role", "write"\)/);
  assert.match(source, /checkPermission\("iam:role", "assign"\)/);
  assert.match(source, /canWriteRole && <Button[^>]*>.*New role/s);
  assert.match(source, /canAssignRole && !creating/);
  assert.match(source, /window\.confirm\(/);
});

test("tenant RBAC browser client keeps neutral owner routes", async () => {
  const source = await readFile(apiPath, "utf8");

  assert.match(source, /"\/api\/v1\/iam\/rbac\/role"/);
  assert.match(source, /"\/api\/v1\/critical\/iam\/rbac\/role"/);
  assert.doesNotMatch(source, /\/api\/v1\/tenant\//);
});
