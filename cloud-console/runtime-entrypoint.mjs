import { spawn } from "node:child_process";
import { writeFile } from "node:fs/promises";

function requiredPublicUrl(name, protocols) {
  const value = process.env[name]?.trim();
  if (!value) throw new Error(`${name} is required`);

  const url = new URL(value);
  if (!protocols.includes(url.protocol) || url.username || url.password) {
    throw new Error(`${name} must be an absolute ${protocols.join(" or ")} URL without credentials`);
  }
  return value;
}

function requiredPublicDomain(name) {
  const value = process.env[name]?.trim().toLowerCase();
  if (
    !value ||
    value.length > 253 ||
    !value.split(".").every((label) => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label))
  ) {
    throw new Error(`${name} must be a canonical DNS base domain`);
  }
  return value;
}

const config = Object.freeze({
  envoyUrl: requiredPublicUrl("NEXT_PUBLIC_ENVOY_URL", ["https:", "http:"]),
  centrifugoWsUrl: requiredPublicUrl("NEXT_PUBLIC_CENTRIFUGO_WS_URL", ["wss:", "ws:"]),
  costConsoleUrl: requiredPublicUrl("NEXT_PUBLIC_COST_CONSOLE_URL", ["https:", "http:"]),
  zonePublicBaseDomain: requiredPublicDomain("NEXT_PUBLIC_ZONE_PUBLIC_BASE_DOMAIN"),
});

// Keep the browser-facing value in runtime-config.js. The server-only alias is
// consumed by src/proxy.ts so CSP is built per deployment, not baked into the
// reusable image by Next's NEXT_PUBLIC compile-time substitution.
process.env.AURORA_ZONE_PUBLIC_BASE_DOMAIN = config.zonePublicBaseDomain;
process.env.AURORA_CENTRIFUGO_WS_ORIGIN = new URL(config.centrifugoWsUrl).origin;

await writeFile(
  new URL("./public/runtime-config.js", import.meta.url),
  `window.__AURORA_RUNTIME_CONFIG__ = Object.freeze(${JSON.stringify(config)});\n`,
  "utf8",
);

const next = spawn(process.execPath, ["node_modules/next/dist/bin/next", "start"], { stdio: "inherit" });
for (const signal of ["SIGINT", "SIGTERM"]) {
  process.on(signal, () => next.kill(signal));
}
next.on("exit", (code) => process.exit(code ?? 1));
