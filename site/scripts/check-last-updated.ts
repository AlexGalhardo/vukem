// Fails unless every docs page has a "Last updated" entry in
// content/last-updated.json that is at least as new as the page's last
// commit. A shallow "changed files only" check let pages such as
// docs/deployment/releases-and-stability.mdx ship with the line hidden.
// Run from site/ with full history: `npm run check:last-updated`.
// Node strips the types (no build step).
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import { gitLastUpdated, pageDateExplainedBySquash } from "../lib/git-dates.ts";

/** A current or snapshotted docs page, relative to content/. */
export function isDocPage(file: string): boolean {
  return file.endsWith(".mdx") && (file.startsWith("docs/") || file.includes("/docs/"));
}

/**
 * Problems for the whole docs set. `files` are the pages that exist now.
 * `dates` come from git (full history). An extra argv base ref is ignored:
 * pages missing from the PR diff must still fail. `explained` accepts a
 * stale date caused by a squash merge (see squashExplainsDate).
 */
export function docPageProblems(
  files: string[],
  entries: Record<string, string>,
  dates: Record<string, string>,
  explained: (file: string, entry: string) => boolean = () => false,
): string[] {
  const problems: string[] = [];
  const live = new Set(files);
  for (const file of [...files].sort()) {
    if (!isDocPage(file)) continue;
    const entry = entries[file];
    if (!entry) {
      problems.push(`${file}: missing from last-updated.json`);
      continue;
    }
    const last = dates[file];
    if (!last) {
      problems.push(`${file}: git has no commit date, so the page cannot show Last updated`);
      continue;
    }
    if (Number.isNaN(Date.parse(entry)) || Date.parse(entry) < Date.parse(last)) {
      if (!Number.isNaN(Date.parse(entry)) && explained(file, entry)) continue;
      problems.push(`${file}: last-updated.json says ${entry}, last commit is ${last}`);
    }
  }
  for (const file of Object.keys(entries).sort()) {
    if (!isDocPage(file) || live.has(file)) continue;
    problems.push(`${file}: deleted, but still in last-updated.json`);
  }
  return problems;
}

function listDocPages(contentDir: string): string[] {
  const out: string[] = [];
  const walk = (dir: string): void => {
    for (const ent of fs.readdirSync(dir, { withFileTypes: true })) {
      const abs = path.join(dir, ent.name);
      if (ent.isDirectory()) {
        walk(abs);
        continue;
      }
      if (!ent.name.endsWith(".mdx")) continue;
      const rel = path.relative(contentDir, abs).split(path.sep).join("/");
      if (isDocPage(rel)) out.push(rel);
    }
  };
  walk(contentDir);
  return out;
}

function main(): number {
  const contentDir = path.join(import.meta.dirname, "..", "content");
  // Touch git so a missing checkout fails here, not as 153 "no commit date" lines.
  execFileSync("git", ["rev-parse", "--is-inside-work-tree"], { cwd: contentDir, stdio: "ignore" });
  const files = listDocPages(contentDir);
  const entries = JSON.parse(fs.readFileSync(path.join(contentDir, "last-updated.json"), "utf8")) as Record<string, string>;
  const problems = docPageProblems(files, entries, gitLastUpdated(contentDir), (f, e) =>
    pageDateExplainedBySquash(contentDir, f, e));
  for (const p of problems) console.error(p);
  if (problems.length > 0) {
    console.error("Run `npm run last-updated` in site/ and commit content/last-updated.json.");
    return 1;
  }
  console.log(`last-updated.json: ${files.length} doc pages checked`);
  return 0;
}

if (import.meta.main) process.exit(main());
