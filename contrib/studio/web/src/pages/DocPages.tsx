import type { ReactElement } from "react";
import { CollectionView } from "../mongo/CollectionView";
import { KeyView } from "../redis/KeyView";
import { Link, navigate } from "../lib/history";
import { hrefFor } from "../lib/router";

/** CollectionPage shows one MongoDB collection as a page of its own (the workspace shows it in a tab). */
export function CollectionPage({ source, collection, write }: { source: string; collection: string; write: boolean }): ReactElement {
  return (
    <section className="doc-page" aria-labelledby="collection-title">
      <p className="crumbs">
        <Link route={{ name: "source", source }}>{source}</Link>
      </p>
      <h1 id="collection-title">{collection}</h1>
      <CollectionView source={source} collection={collection} write={write} />
    </section>
  );
}

/** KeyPage shows one Redis key as a page of its own (the workspace shows it in a tab). */
export function KeyPage({ source, keyName, write }: { source: string; keyName: string; write: boolean }): ReactElement {
  return (
    <section className="key-page" aria-labelledby="key-title">
      <p className="crumbs">
        <Link route={{ name: "source", source }}>{source}</Link>
      </p>
      <h1 id="key-title" className="mono">
        {keyName}
      </h1>
      <KeyView source={source} keyName={keyName} write={write} onDeleted={() => navigate(hrefFor({ name: "source", source }))} />
    </section>
  );
}
