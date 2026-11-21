export type Route =
  | { name: "overview" }
  | { name: "source"; source: string }
  | { name: "table"; source: string; table: string }
  | { name: "console"; source: string }
  | { name: "schema"; source: string }
  | { name: "collection"; source: string; collection: string }
  | { name: "key"; source: string; key: string }
  | { name: "notfound" };

export const basePath = "/ui";

function decode(segment: string): string | null {
  try {
    return decodeURIComponent(segment);
  } catch {
    return null;
  }
}

/** matchRoute maps a location pathname under /ui to a route. */
export function matchRoute(pathname: string): Route {
  if (pathname !== basePath && !pathname.startsWith(basePath + "/")) {
    return { name: "notfound" };
  }

  const parts = pathname
    .slice(basePath.length)
    .split("/")
    .filter((p) => p !== "")
    .map(decode);

  if (parts.some((p) => p === null)) {
    return { name: "notfound" };
  }

  const [first, source, kind, table, ...rest] = parts as string[];

  if (first === undefined) {
    return { name: "overview" };
  }

  if (first !== "s" || source === undefined) {
    return { name: "notfound" };
  }

  if (kind === undefined && table === undefined) {
    return { name: "source", source };
  }

  if (kind === "q" && table === undefined) {
    return { name: "console", source };
  }

  if (kind === "schema" && table === undefined) {
    return { name: "schema", source };
  }

  if (kind === "t" && table !== undefined && rest.length === 0) {
    return { name: "table", source, table };
  }

  if (kind === "c" && table !== undefined && rest.length === 0) {
    return { name: "collection", source, collection: table };
  }

  if (kind === "k" && table !== undefined && rest.length === 0) {
    return { name: "key", source, key: table };
  }

  return { name: "notfound" };
}

/** hrefFor is the inverse of matchRoute. */
export function hrefFor(route: Route): string {
  switch (route.name) {
    case "overview":
      return basePath + "/";
    case "source":
      return `${basePath}/s/${encodeURIComponent(route.source)}`;
    case "table":
      return `${basePath}/s/${encodeURIComponent(route.source)}/t/${encodeURIComponent(route.table)}`;
    case "console":
      return `${basePath}/s/${encodeURIComponent(route.source)}/q`;
    case "schema":
      return `${basePath}/s/${encodeURIComponent(route.source)}/schema`;
    case "collection":
      return `${basePath}/s/${encodeURIComponent(route.source)}/c/${encodeURIComponent(route.collection)}`;
    case "key":
      return `${basePath}/s/${encodeURIComponent(route.source)}/k/${encodeURIComponent(route.key)}`;
    case "notfound":
      return basePath + "/";
  }
}

/** isPlainLeftClick is true for a click the router may handle itself. */
export function isPlainLeftClick(e: {
  button: number;
  metaKey: boolean;
  ctrlKey: boolean;
  shiftKey: boolean;
  altKey: boolean;
  defaultPrevented: boolean;
}): boolean {
  return !e.defaultPrevented && e.button === 0 && !e.metaKey && !e.ctrlKey && !e.shiftKey && !e.altKey;
}
