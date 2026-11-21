import { useEffect } from "react";
import type { ReactElement } from "react";
import type { Bootstrap } from "./api/types";
import { useApi } from "./api/useApi";
import { Layout } from "./components/Layout";
import { localStore } from "./history/browser";
import { purgeOldHistory } from "./history/store";
import { ErrorNotice, Loading } from "./components/Notices";
import { useRoute } from "./lib/history";
import type { Route } from "./lib/router";
import { ConsolePage } from "./pages/ConsolePage";
import { CollectionPage, KeyPage } from "./pages/DocPages";
import { NotFound } from "./pages/NotFound";
import { SchemaPage } from "./schema/SchemaView";
import { Overview } from "./pages/Overview";
import { SourcePage } from "./pages/SourcePage";
import { TablePage } from "./pages/TablePage";

function titleFor(route: Route): string {
  switch (route.name) {
    case "overview":
      return "Sources";
    case "source":
      return route.source;
    case "table":
      return `${route.table} - ${route.source}`;
    case "console":
      return `Console - ${route.source}`;
    case "schema":
      return `Schema - ${route.source}`;
    case "collection":
      return `${route.collection} - ${route.source}`;
    case "key":
      return `${route.key} - ${route.source}`;
    case "notfound":
      return "Not found";
  }
}

function Page({ bootstrap, route }: { bootstrap: Bootstrap; route: Route }): ReactElement {
  switch (route.name) {
    case "overview":
      return <Overview sources={bootstrap.sources} />;
    case "source": {
      const source = bootstrap.sources.find((s) => s.name === route.source);
      return source ? <SourcePage source={source} /> : <NotFound />;
    }
    case "table":
      return bootstrap.sources.some((s) => s.name === route.source && s.kind === "sql") ? (
        <TablePage source={route.source} table={route.table} write={bootstrap.write} refreshSeconds={bootstrap.refreshSeconds} />
      ) : (
        <NotFound />
      );
    case "console": {
      const source = bootstrap.sources.find((s) => s.name === route.source);
      return source ? <ConsolePage source={source} write={bootstrap.write} refreshSeconds={bootstrap.refreshSeconds} sessionKey={bootstrap.sessionKey} /> : <NotFound />;
    }
    case "schema":
      return bootstrap.sources.some((s) => s.name === route.source && s.kind === "sql") ? <SchemaPage source={route.source} /> : <NotFound />;
    case "collection":
      return bootstrap.sources.some((s) => s.name === route.source && s.kind === "mongo") ? (
        <CollectionPage source={route.source} collection={route.collection} write={bootstrap.write} />
      ) : (
        <NotFound />
      );
    case "key":
      return bootstrap.sources.some((s) => s.name === route.source && s.kind === "redis") ? (
        <KeyPage source={route.source} keyName={route.key} write={bootstrap.write} />
      ) : (
        <NotFound />
      );
    case "notfound":
      return <NotFound />;
  }
}

export function App(): ReactElement {
  const route = useRoute();
  const { data, error, loading, reload } = useApi<Bootstrap>("/bootstrap");

  useEffect(() => {
    document.title = `${titleFor(route)} - Vukem Studio`;
  }, [route]);

  const sessionKey = data?.sessionKey;
  useEffect(() => {
    // A new Studio run starts with an empty history: statements of earlier runs can hold values nobody wants kept.
    if (sessionKey) {
      purgeOldHistory(localStore(), sessionKey);
    }
  }, [sessionKey]);

  if (loading) {
    return (
      <div className="boot">
        <Loading what="Studio" />
      </div>
    );
  }

  if (error || !data) {
    return <div className="boot">{error ? <ErrorNotice error={error} onRetry={reload} /> : null}</div>;
  }

  return (
    <Layout bootstrap={data} route={route}>
      <Page bootstrap={data} route={route} />
    </Layout>
  );
}
