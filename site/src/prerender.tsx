import { prerender } from "react-dom/static";
import { ChangelogPage } from "@/components/changelog-page";
import { DocsFrame } from "@/components/docs-frame";
import { DocArticle } from "@/components/doc-article";
import type { Doc } from "@/lib/content";

async function markup(node: React.ReactNode): Promise<string> {
  const { prelude } = await prerender(node);
  return new Response(prelude).text();
}

/** Docs frame and article as HTML. Interactive tabs and copy are plain DOM. */
export function renderDoc(doc: Doc): Promise<string> {
  return markup(
    <DocsFrame pathname={doc.href}>
      <DocArticle doc={doc} />
    </DocsFrame>,
  );
}

export function renderChangelog(): Promise<string> {
  return markup(<ChangelogPage />);
}
