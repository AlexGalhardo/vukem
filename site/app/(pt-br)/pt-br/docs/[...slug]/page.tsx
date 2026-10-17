import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { DocArticle, docMetadata } from "@/components/doc-article";
import { getAllDocs, getDoc } from "@/lib/content";
import { currentVersion } from "@/lib/versions";

type Props = { params: Promise<{ slug: string[] }> };

export const dynamicParams = false;

// Every page of the current docs; snapshots (/docs/0.20/...) stay English-only.
export function generateStaticParams(): { slug: string[] }[] {
  return getAllDocs(currentVersion.id, "pt-br").map((d) => ({ slug: d.slug }));
}

export async function generateMetadata({ params }: Props): Promise<Metadata> {
  const { slug } = await params;
  const doc = getDoc(slug, "pt-br");
  return doc ? docMetadata(doc) : {};
}

export default async function PortugueseDocPage({ params }: Props) {
  const { slug } = await params;
  const doc = getDoc(slug, "pt-br");
  if (!doc) notFound();
  return <DocArticle doc={doc} />;
}
