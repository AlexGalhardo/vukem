import { DocsFrame } from "@/components/docs-frame";

export default function PortugueseDocsLayout({ children }: { children: React.ReactNode }) {
  return <DocsFrame locale="pt-br">{children}</DocsFrame>;
}
