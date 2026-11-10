/**
 * Adds the trailing slash the static site is built with (`trailingSlash: "always"`) to an
 * internal path, keeping any `?query` and `#hash`. External URLs, hash-only links and paths
 * to files (`/icon.svg`, `/llms.txt`) are returned as they are.
 */
export function withTrailingSlash(href: string): string {
  if (!href.startsWith("/") || href.startsWith("//")) return href;
  const cut = href.search(/[?#]/);
  const path = cut === -1 ? href : href.slice(0, cut);
  const rest = cut === -1 ? "" : href.slice(cut);
  if (path.endsWith("/") || /\.[a-z0-9]+$/i.test(path)) return href;
  return `${path}/${rest}`;
}
