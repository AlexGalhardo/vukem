import { execFileSync } from "node:child_process";
import fs from "node:fs";

// Plain Node module (no "@/" imports, no Next APIs): scripts/last-updated.ts
// runs it directly with Node's type stripping.

function git(cwd: string, args: string[]): string {
  return execFileSync("git", ["-c", "core.quotePath=false", ...args], {
    cwd,
    encoding: "utf8",
    maxBuffer: 64 * 1024 * 1024,
    stdio: ["ignore", "pipe", "ignore"],
  });
}

/** Commits that are cut-off points of a shallow clone (empty for a full clone). */
function shallowBoundaries(cwd: string): Set<string> {
  const file = git(cwd, ["rev-parse", "--path-format=absolute", "--git-path", "shallow"]).trim();
  if (!fs.existsSync(file)) return new Set();
  return new Set(fs.readFileSync(file, "utf8").split(/\s+/).filter(Boolean));
}

/**
 * Last commit date (ISO 8601) of every file under dir, keyed by its path
 * relative to dir with forward slashes. Returns {} when git or the history
 * is unavailable.
 *
 * A shallow clone (Vercel clones with a small depth) has no parent for its
 * boundary commit, so git reports that commit as touching every file. Those
 * commits are skipped: files last changed before the boundary are simply
 * missing from the result and the caller falls back to another source.
 */
export function gitLastUpdated(dir: string): Record<string, string> {
  let log: string;
  let boundaries: Set<string>;
  try {
    log = git(dir, ["log", "--format=%x00%H %cI", "--name-only", "--relative", "--", "."]);
    boundaries = shallowBoundaries(dir);
  } catch {
    return {};
  }

  const dates: Record<string, string> = {};
  for (const entry of log.split("\0")) {
    const [header, ...files] = entry.split("\n").filter((l) => l.trim() !== "");
    if (!header) continue;
    const [sha, date] = header.split(" ");
    if (!sha || !date || boundaries.has(sha)) continue;
    for (const file of files) dates[file] ??= date; // log is newest first
  }
  return dates;
}

/**
 * True when the newest of two `git log --format=%cI%x09%s` rows is a squash
 * merge ("... (#123)") and the recorded date is not older than the change
 * before it. A squash commit lands after the date the PR branch recorded;
 * the page itself is unchanged since that PR.
 */
export function squashExplainsDate(log: string, entry: string): boolean {
  const [last, prev] = log.split("\n").filter(Boolean).map((l) => l.split("\t"));
  if (!last || !/\(#\d+\)\s*$/.test(last[1] ?? "")) return false;
  return !prev || Date.parse(entry) >= Date.parse(prev[0] ?? "");
}

/** Reads the two newest commits of a page and applies squashExplainsDate. */
export function pageDateExplainedBySquash(dir: string, file: string, entry: string): boolean {
  try {
    return squashExplainsDate(git(dir, ["log", "-2", "--format=%cI%x09%s", "--", file]), entry);
  } catch {
    return false;
  }
}
