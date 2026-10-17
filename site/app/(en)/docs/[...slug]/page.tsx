import type { Metadata } from "next";
import { notFound, redirect } from "next/navigation";
import { DocArticle, docMetadata } from "@/components/doc-article";
import { getDoc, getEveryDoc, versionHome } from "@/lib/content";
import { versions, type DocVersion } from "@/lib/versions";

type Props = { params: Promise<{ slug: string[] }> };

export const dynamicParams = false;

export function generateStaticParams(): { slug: string[] }[] {
  const roots = versions.filter((v) => v.prefix).map((v) => ({ slug: [v.prefix] })); // /docs/0.20 → its introduction
  return [...roots, ...getEveryDoc().map((d) => ({ slug: d.slug }))];
}

function snapshotRoot(slug: string[]): DocVersion | undefined {
  return slug.length === 1 ? versions.find((v) => v.prefix !== "" && v.prefix === slug[0]) : undefined;
}

export async function generateMetadata({ params }: Props): Promise<Metadata> {
  const { slug } = await params;
  const doc = getDoc(slug);
  return doc ? docMetadata(doc) : {};
}

export default async function DocPage({ params }: Props) {
  const { slug } = await params;
  const root = snapshotRoot(slug);
  if (root) redirect(versionHome(root));

  const doc = getDoc(slug);
  if (!doc) notFound();
  return <DocArticle doc={doc} />;
}
