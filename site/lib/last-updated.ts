import path from "node:path";
import committed from "@/content/last-updated.json";
import { gitLastUpdated } from "@/lib/git-dates";

const CONTENT_ROOT = path.join(process.cwd(), "content");

/**
 * Last commit date of each content file. Git history wins when the build
 * has it; content/last-updated.json (refreshed with `npm run last-updated`)
 * covers builds without .git or with a shallow clone that stops before the
 * file's last change. See README.md, "Last updated".
 */
// Module-level, not React cache(): that one is per request, i.e. per page at build time.
let dates: Record<string, string> | undefined;

/** ISO 8601 date of the last commit touching content/<source>, if known. */
export function lastUpdated(source: string): string | undefined {
  dates ??= { ...(committed as Record<string, string>), ...gitLastUpdated(CONTENT_ROOT) };
  return dates[source];
}
