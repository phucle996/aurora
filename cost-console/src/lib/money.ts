export function formatUSDMicroUnits(value: string): string {
  try {
    const microUnits = BigInt(value);
    const negative = microUnits < 0n;
    const absolute = negative ? -microUnits : microUnits;
    const whole = absolute / 1_000_000n;
    const fraction = (absolute % 1_000_000n).toString().padStart(6, "0").replace(/0+$/, "");
    return `USD ${negative ? "-" : ""}${whole.toLocaleString("en-US")}${fraction ? `.${fraction}` : ""}`;
  } catch {
    return "USD —";
  }
}

export function usdToMicroUnits(value: string): string | null {
  const normalized = value.trim();
  if (!/^\d+(?:\.\d{1,6})?$/.test(normalized)) return null;
  const [whole, fraction = ""] = normalized.split(".");
  try {
    const microUnits = BigInt(whole) * 1_000_000n + BigInt(fraction.padEnd(6, "0"));
    if (microUnits <= 0n || microUnits > 9_223_372_036_854_775_807n) return null;
    return microUnits.toString();
  } catch {
    return null;
  }
}
