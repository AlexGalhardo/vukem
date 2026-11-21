import type { ReactElement } from "react";
import type { Source } from "../api/types";
import { DatabaseIcon } from "../components/icons";
import { EmptyState } from "../components/Notices";
import { Link } from "../lib/history";

/** Overview lists the data sources Studio was started with. */
export function Overview({ sources }: { sources: Source[] }): ReactElement {
  return (
    <section aria-labelledby="overview-title">
      <h1 id="overview-title">Sources</h1>

      {sources.length === 0 ? (
        <EmptyState title="No data source is configured">
          Set DATABASE_URL, MONGO_URL or REDIS_URL and start Studio again.
        </EmptyState>
      ) : (
        <ul className="cards">
          {sources.map((source) => (
            <li key={source.name} className="card">
              <Link route={{ name: "source", source: source.name }} className="card-link">
                <span className="card-title">
                  <DatabaseIcon />
                  {source.name}
                </span>
                <span className="card-kind">{source.kind}</span>
              </Link>
              <p className="muted mono">{source.label}</p>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
