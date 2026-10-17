import Link from "next/link";
import type { MDXComponents } from "mdx/types";
import { Callout } from "@/components/mdx/callout";
import { Card, CardGroup } from "@/components/mdx/card";
import { CodeBlock } from "@/components/mdx/code-block";
import { Step, Steps } from "@/components/mdx/steps";
import { CodeGroup, Tab, Tabs } from "@/components/mdx/tabs";
import { currentVersion, versionHref, type DocVersion } from "@/lib/versions";

type HeadingProps = React.ComponentPropsWithoutRef<"h2">;

function anchored(Tag: "h2" | "h3" | "h4") {
  function Heading({ id, children, ...rest }: HeadingProps) {
    return (
      <Tag id={id} {...rest}>
        {children}
        {id ? (
          <a href={`#${id}`} className="heading-anchor" aria-label="Link to this section">
            #
          </a>
        ) : null}
      </Tag>
    );
  }
  return Heading;
}

function A({ href = "", children, ...rest }: React.ComponentPropsWithoutRef<"a">) {
  if (href.startsWith("/")) {
    return (
      <Link href={href} {...rest}>
        {children}
      </Link>
    );
  }
  if (href.startsWith("#")) {
    return (
      <a href={href} {...rest}>
        {children}
      </a>
    );
  }
  return (
    <a href={href} target="_blank" rel="noreferrer" {...rest}>
      {children}
    </a>
  );
}

function Table(props: React.ComponentPropsWithoutRef<"table">) {
  return (
    <div className="table-wrap">
      <table {...props} />
    </div>
  );
}

export const mdxComponents: MDXComponents = {
  h2: anchored("h2"),
  h3: anchored("h3"),
  h4: anchored("h4"),
  a: A,
  table: Table,
  Callout,
  Card,
  CardGroup,
  CodeBlock,
  CodeGroup,
  Step,
  Steps,
  Tab,
  Tabs,
};

/**
 * Components for a page of version v: in a snapshot, links to /docs/...
 * (written against the current layout) stay inside that snapshot.
 */
export function mdxComponentsFor(v: DocVersion): MDXComponents {
  if (v.id === currentVersion.id) return mdxComponents;

  function VersionedA({ href = "", ...rest }: React.ComponentPropsWithoutRef<"a">) {
    return <A href={versionHref(href, v)} {...rest} />;
  }
  function VersionedCard({ href, ...rest }: React.ComponentProps<typeof Card>) {
    return <Card href={href ? versionHref(href, v) : href} {...rest} />;
  }

  return { ...mdxComponents, a: VersionedA, Card: VersionedCard };
}
