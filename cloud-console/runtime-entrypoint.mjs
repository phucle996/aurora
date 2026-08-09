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

const config = Object.freeze({
  envoyUrl: requiredPublicUrl("NEXT_PUBLIC_ENVOY_URL", ["https:", "http:"]),
  centrifugoWsUrl: requiredPublicUrl("NEXT_PUBLIC_CENTRIFUGO_WS_URL", ["wss:", "ws:"]),
  costConsoleUrl: requiredPublicUrl("NEXT_PUBLIC_COST_CONSOLE_URL", ["https:", "http:"]),
});

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
