// Fails when a page under content/docs/ changed in this branch but its
// "Last updated" entry in content/last-updated.json was not refreshed
// (`npm run last-updated`) after its last commit. Run from site/ with full
// history: `node scripts/check-last-updated.ts origin/main`.
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { gitLastUpdated } from "../lib/git-dates.ts";

/** Problems for the changed pages (paths relative to content/). */
export function staleEntries(
  changed: string[],
  entries: Record<string, string>,
  dates: Record<string, string>,
  exists: (file: string) => boolean,
): string[] {
  const problems: string[] = [];
  for (const file of changed) {
    if (!file.startsWith("docs/") || !file.endsWith(".mdx")) continue;
    const entry = entries[file];
    if (!exists(file)) {
      if (entry) problems.push(`${file}: deleted, but still in last-updated.json`);
      continue;
    }
    if (!entry) {
      problems.push(`${file}: missing from last-updated.json`);
      continue;
    }
    const last = dates[file];
    if (last && Date.parse(entry) < Date.parse(last)) {
      problems.push(`${file}: last-updated.json says ${entry}, last commit is ${last}`);
    }
  }
  return problems;
}

function main(base: string): number {
  const contentDir = path.join(import.meta.dirname, "..", "content");
  const changed = execFileSync(
    "git",
    ["-c", "core.quotePath=false", "diff", "--name-only", "--relative", `${base}...HEAD`, "--", "."],
    { cwd: contentDir, encoding: "utf8" },
  )
    .split("\n")
    .filter(Boolean);
  const entries = JSON.parse(fs.readFileSync(path.join(contentDir, "last-updated.json"), "utf8"));
  const problems = staleEntries(changed, entries, gitLastUpdated(contentDir), (f) =>
    fs.existsSync(path.join(contentDir, f)),
  );
  for (const p of problems) console.error(p);
  if (problems.length > 0) {
    console.error("Run `npm run last-updated` in site/ and commit content/last-updated.json in this PR.");
    return 1;
  }
  console.log(`last-updated.json: ${changed.length} changed content files checked`);
  return 0;
}

if (import.meta.main) process.exit(main(process.argv[2] ?? "origin/main"));
