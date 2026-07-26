const ZERO = BigInt(0);
const ONE_HUNDRED = BigInt(100);
const MICRO_UNITS_PER_CENT = BigInt(10_000);

function formatWholeUnits(value: bigint): string {
  return value.toString().replace(/\B(?=(\d{3})+(?!\d))/g, ",");
}

export function formatMicroUnits(microUnits: string, currency: string): string | null {
  try {
    const value = BigInt(microUnits);
    const negative = value < ZERO;
    const absolute = negative ? -value : value;
    const cents = (absolute + MICRO_UNITS_PER_CENT / BigInt(2)) / MICRO_UNITS_PER_CENT;
    const whole = cents / ONE_HUNDRED;
    const fraction = (cents % ONE_HUNDRED).toString().padStart(2, "0");
    return `${currency} ${negative ? "-" : ""}${formatWholeUnits(whole)}.${fraction}`;
  } catch {
    return null;
  }
}

export function formatWalletBalance(cashMicroUnits: string, promotionalMicroUnits: string, currency: string): string | null {
  try {
    const total = BigInt(cashMicroUnits) + BigInt(promotionalMicroUnits);
    return formatMicroUnits(total.toString(), currency);
  } catch {
    return null;
  }
}
