import { readFileSync } from "node:fs";

// readCommittedDates loads the fallback "Last updated" map. A copied site
// that has not generated the file yet builds with no dates instead of
// failing the import. Invalid JSON is the same as a missing file.
export function readCommittedDates(file: string): Record<string, string> {
  let raw: string;
  try {
    raw = readFileSync(file, "utf8");
  } catch {
    return {};
  }

  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return {};
  }

  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
    return {};
  }

  const out: Record<string, string> = {};
  for (const [key, value] of Object.entries(parsed)) {
    if (typeof value === "string") {
      out[key] = value;
    }
  }

  return out;
}
