import { extractHeadings } from "../lib/toc.ts";

/** Anchors a docs page answers to: h2/h3 slugs plus explicit id="..." attributes (e.g. <details id>). */
export function pageAnchors(body: string): Set<string> {
  const ids = new Set(extractHeadings(body).map((h) => h.id));
  for (const m of body.matchAll(/\bid="([^"]+)"/g)) ids.add(m[1]);
  return ids;
}

/** Returns an error message when the link fragment matches no anchor of the target page. */
export function checkFragment(href: string, anchors: Set<string>): string | undefined {
  const hash = href.split("?")[0].split("#")[1];
  if (!hash) return undefined;
  return anchors.has(decodeURIComponent(hash)) ? undefined : `internal link ${href} points to an anchor that does not exist on the page`;
}
