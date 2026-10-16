import { Children, isValidElement } from "react";
import { TabsClient } from "@/components/mdx/tabs-client";
import { CodeBlock, Highlighted, langLabel, type CodeBlockProps } from "@/components/mdx/code-block";
import { CopyButton } from "@/components/copy-button";

type TabProps = { title: string; children: React.ReactNode };

export function Tab({ children }: TabProps) {
  return <>{children}</>;
}

/** <Tabs><Tab title="…">…</Tab></Tabs> */
export function Tabs({ children }: { children: React.ReactNode }) {
  const items = Children.toArray(children)
    .filter((c): c is React.ReactElement<TabProps> => isValidElement(c))
    .map((c) => ({ label: c.props.title, content: c.props.children }));
  return <TabsClient items={items} />;
}

/** <CodeGroup> with fenced code blocks inside: one tab per block. */
export function CodeGroup({ children }: { children: React.ReactNode }) {
  const blocks = Children.toArray(children)
    .filter((c): c is React.ReactElement<CodeBlockProps> => isValidElement(c) && c.type === CodeBlock)
    .map((c) => c.props);

  return (
    <TabsClient
      variant="code"
      items={blocks.map((b) => ({
        label: b.title ?? langLabel(b.lang),
        content: <Highlighted code={b.code} lang={b.lang} />,
        action: <CopyButton text={b.code.replace(/\n$/, "")} />,
      }))}
    />
  );
}
