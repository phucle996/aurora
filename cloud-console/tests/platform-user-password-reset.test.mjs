import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const panelPath = new URL("../src/features/iam/users/UserDetailPanel.tsx", import.meta.url);
const apiPath = new URL("../src/features/iam/users-api.ts", import.meta.url);

test("platform password reset mirrors the registration password policy while typing", async () => {
  const source = await readFile(panelPath, "utf8");

  for (const requirement of [
    "newPassword.length >= 8",
    "/[a-z]/.test(newPassword)",
    "/[A-Z]/.test(newPassword)",
    "/[0-9]/.test(newPassword)",
    "/[^A-Za-z0-9]/.test(newPassword)",
  ]) {
    assert.ok(source.includes(requirement), `missing live password requirement: ${requirement}`);
  }
  assert.match(source, /disabled=\{updatingId !== null \|\| resetPasswordMutation\.isPending \|\| !isPasswordValid\}/);
  assert.match(source, /id="reset-password-requirements"/);
});

test("only actors with the reset capability can reveal the reset action", async () => {
  const source = await readFile(panelPath, "utf8");
  assert.match(source, /const canManageUsers = checkPermission\("iam:users", "manage"\)/);
  assert.match(source, /\{canManageUsers && \(\s*<Button[\s\S]*?Reset Password/);
});

test("the password mutation remains a neutral critical browser route", async () => {
  const source = await readFile(apiPath, "utf8");
  assert.match(source, /criticalFetchJSON\(`\/api\/v1\/critical\/iam\/users\/\$\{id\}\/password`/);
  assert.doesNotMatch(source, /\/api\/v1\/personal\/critical\/iam\/users/);
});
