// Money travels as int64 cents with the ISO 4217 currency of the workspace.
// Intl formats it for the reader; nothing here does arithmetic on floats.

export function formatMoney(cents: number, currency: string): string {
  return new Intl.NumberFormat(undefined, { style: "currency", currency }).format(cents / 100);
}

// toInput turns cents into the "12.50" a form field shows.
export function toInput(cents: number): string {
  return (cents / 100).toFixed(2);
}

// monthLabel turns "2026-11" into "Nov 2026".
export function monthLabel(month: string): string {
  const [year, m] = month.split("-").map(Number);
  return new Date(year, m - 1, 1).toLocaleDateString(undefined, { month: "short", year: "numeric" });
}
