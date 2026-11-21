import type { ReactElement } from "react";
import { EmptyState } from "../components/Notices";
import { Link } from "../lib/history";

export function NotFound(): ReactElement {
  return (
    <section aria-labelledby="notfound-title">
      <h1 id="notfound-title">Page not found</h1>
      <EmptyState title="There is nothing at this address.">Pick a source from the list.</EmptyState>
      <p>
        <Link route={{ name: "overview" }}>Back to the sources</Link>
      </p>
    </section>
  );
}
