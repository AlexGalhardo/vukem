import { DocsFrame } from "@/components/docs-frame";

export default function DocsLayout({ children }: { children: React.ReactNode }) {
  return <DocsFrame locale="en">{children}</DocsFrame>;
}
