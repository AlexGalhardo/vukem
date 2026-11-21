import type { ReactElement } from "react";
import { apiPath } from "../api/client";
import type { CollectionsResponse, KeysResponse, Source, TablesResponse } from "../api/types";
import { useApi } from "../api/useApi";
import { Link } from "../lib/history";
import type { Route } from "../lib/router";
import { DatabaseIcon, TableIcon } from "./icons";

const maxListed = 50;

function Items({ names, route }: { names: { label: string; route?: Route }[]; route: Route }): ReactElement {
  const shown = names.slice(0, maxListed);

  return (
    <ul className="nav-items">
      {shown.map((item) => (
        <li key={item.label}>
          {item.route ? (
            <Link
              route={item.route}
              className="nav-link nav-link-child"
              current={route.name === "table" && item.route.name === "table" && route.table === item.route.table}
            >
              <TableIcon />
              <span className="truncate">{item.label}</span>
            </Link>
          ) : (
            <span className="nav-link nav-link-child nav-static">
              <span className="truncate">{item.label}</span>
            </span>
          )}
        </li>
      ))}
      {names.length > maxListed ? <li className="muted nav-more">and {names.length - maxListed} more</li> : null}
    </ul>
  );
}

function SourceChildren({ source, route }: { source: Source; route: Route }): ReactElement | null {
  const path =
    source.kind === "sql"
      ? apiPath("sources", source.name, "tables")
      : source.kind === "mongo"
        ? apiPath("sources", source.name, "collections")
        : apiPath("sources", source.name, "keys");

  const { data, error, loading } = useApi<TablesResponse & CollectionsResponse & KeysResponse>(path);

  if (loading) {
    return <p className="muted nav-note">Loading…</p>;
  }

  if (error || !data) {
    return <p className="muted nav-note">Could not list items.</p>;
  }

  if (source.kind === "sql") {
    return (
      <Items
        route={route}
        names={data.tables.map((t) => ({ label: t.name, route: { name: "table", source: source.name, table: t.name } }))}
      />
    );
  }

  if (source.kind === "mongo") {
    return <Items route={route} names={data.collections.map((c) => ({ label: c.name }))} />;
  }

  return <Items route={route} names={data.keys.map((k) => ({ label: k.key }))} />;
}

/** SideNav lists the sources; the open one also lists its tables, collections or keys. */
export function SideNav({ sources, route }: { sources: Source[]; route: Route }): ReactElement {
  const open = route.name === "source" || route.name === "table" || route.name === "console" ? route.source : null;

  return (
    <nav className="sidenav" aria-label="Sources">
      <ul className="nav-sources">
        {sources.map((source) => (
          <li key={source.name}>
            <Link
              route={{ name: "source", source: source.name }}
              className="nav-link nav-source"
              current={route.name === "source" && route.source === source.name}
            >
              <DatabaseIcon />
              <span className="truncate">{source.name}</span>
              <span className="nav-kind">{source.kind}</span>
            </Link>
            {open === source.name && source.kind === "sql" ? (
              <Link
                route={{ name: "console", source: source.name }}
                className="nav-link nav-link-child"
                current={route.name === "console"}
              >
                <span className="truncate">SQL console</span>
              </Link>
            ) : null}
            {open === source.name ? <SourceChildren source={source} route={route} /> : null}
          </li>
        ))}
      </ul>
    </nav>
  );
}
