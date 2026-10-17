/**
 * Documentation versions, newest first. The first entry is the current
 * docs: content/docs + content/navigation.ts, served at /docs/*. Every
 * other entry is a frozen snapshot in content/versions/<id>/ (docs/ and
 * navigation.ts), served at /docs/<id>/*. Snapshots are registered in
 * lib/content.ts too. Client-safe: no file system access here.
 */
export type DocVersion = {
  id: string;
  label: string;
  /** First URL segment after /docs; empty for the current version. */
  prefix: string;
  /** Content folder relative to site/content. */
  contentDir: string;
};

export const versions: readonly DocVersion[] = [
  { id: "1.0", label: "v1.0", prefix: "", contentDir: "docs" },
  { id: "0.20", label: "v0.20", prefix: "0.20", contentDir: "versions/0.20/docs" },
];

export const currentVersion: DocVersion = versions[0];

export function docsBase(v: DocVersion): string {
  return v.prefix ? `/docs/${v.prefix}` : "/docs";
}

/** The version a pathname belongs to (the current one outside snapshots). */
export function versionOfPath(pathname: string): DocVersion {
  const seg = pathname.split("/")[2] ?? "";
  return versions.find((v) => v.prefix !== "" && v.prefix === seg) ?? currentVersion;
}

/** Moves a /docs/... link of the current docs into version v. */
export function versionHref(href: string, v: DocVersion): string {
  if (!v.prefix || !href.startsWith("/docs/")) return href;
  return `${docsBase(v)}${href.slice("/docs".length)}`;
}

/** The page path relative to its version base, e.g. "/basics/routing". */
export function pageOfPath(pathname: string): string {
  const v = versionOfPath(pathname);
  const base = docsBase(v);
  return pathname.startsWith(base) ? pathname.slice(base.length) : "";
}
