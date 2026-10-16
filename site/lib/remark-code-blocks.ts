/**
 * Remark plugin: turns fenced code blocks into <CodeBlock lang title code />
 * JSX elements so they are highlighted by a server component. The title
 * comes from the fence meta: ```go title="main.go" or ```go main.go.
 */
type Node = {
  type: string;
  lang?: string | null;
  meta?: string | null;
  value?: string;
  children?: Node[];
  [key: string]: unknown;
};

function attr(name: string, value: string) {
  return { type: "mdxJsxAttribute", name, value };
}

export function parseTitle(meta?: string | null): string | undefined {
  if (!meta) return undefined;
  const quoted = meta.match(/title=(?:"([^"]*)"|'([^']*)')/);
  if (quoted) return quoted[1] ?? quoted[2];
  const bare = meta.trim().split(/\s+/)[0];
  return bare && !bare.includes("=") ? bare : undefined;
}

function visit(node: Node) {
  if (!node.children) return;
  node.children = node.children.map((child) => {
    if (child.type !== "code") {
      visit(child);
      return child;
    }
    const attributes = [attr("code", child.value ?? "")];
    if (child.lang) attributes.push(attr("lang", child.lang));
    const title = parseTitle(child.meta);
    if (title) attributes.push(attr("title", title));
    return { type: "mdxJsxFlowElement", name: "CodeBlock", attributes, children: [] };
  });
}

export default function remarkCodeBlocks() {
  return (tree: Node) => visit(tree);
}
