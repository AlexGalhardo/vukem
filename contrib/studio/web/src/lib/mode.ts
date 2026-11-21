import type { Bootstrap } from "../api/types";

export interface ModeBadge {
  label: string;
  tone: "readonly" | "write";
  hint: string;
}

/** modeBadge says, in words and not only color, whether Studio can change data. */
export function modeBadge(bootstrap: Pick<Bootstrap, "write">): ModeBadge {
  if (bootstrap.write) {
    return {
      label: "Write enabled",
      tone: "write",
      hint: "Studio was started with --write: edits change the database.",
    };
  }

  return {
    label: "Read-only",
    tone: "readonly",
    hint: "Studio cannot change data. Start it with --write to edit.",
  };
}

/** formatCount shows a row or document estimate; a negative one is unknown. */
export function formatCount(n: number): string {
  return n < 0 ? "unknown" : n.toLocaleString("en-US");
}

/** formatTTL shows a time to live in seconds; a negative one never expires. */
export function formatTTL(seconds: number): string {
  if (seconds < 0) {
    return "no expiry";
  }

  if (seconds < 60) {
    return `${seconds}s`;
  }

  if (seconds < 3600) {
    return `${Math.floor(seconds / 60)}m`;
  }

  if (seconds < 86400) {
    return `${Math.floor(seconds / 3600)}h`;
  }

  return `${Math.floor(seconds / 86400)}d`;
}
