// Refreshes content/last-updated.json, the fallback "Last updated" map for
// builds without full git history. Run from site/ with full history:
// `npm run last-updated`. Node strips the types (no build step).
import fs from "node:fs";
import path from "node:path";
import { gitLastUpdated } from "../lib/git-dates.ts";

const contentDir = path.join(import.meta.dirname, "..", "content");
const out = path.join(contentDir, "last-updated.json");

const previous: Record<string, string> = fs.existsSync(out) ? JSON.parse(fs.readFileSync(out, "utf8")) : {};
const merged: Record<string, string> = { ...previous, ...gitLastUpdated(contentDir) };

const mdx = Object.fromEntries(
  Object.entries(merged)
    .filter(([file]) => file.endsWith(".mdx") && fs.existsSync(path.join(contentDir, file)))
    .sort(([a], [b]) => a.localeCompare(b)),
);

fs.writeFileSync(out, `${JSON.stringify(mdx, null, 2)}\n`);
console.log(`content/last-updated.json: ${Object.keys(mdx).length} pages`);
