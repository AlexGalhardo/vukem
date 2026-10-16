import Link from "next/link";
import {
  ArrowUpRight,
  BookOpen,
  Boxes,
  Code,
  Database,
  Layers,
  Lock,
  Rocket,
  Route,
  Shield,
  Terminal,
  Zap,
  type LucideIcon,
} from "lucide-react";

const ICONS: Record<string, LucideIcon> = {
  book: BookOpen,
  boxes: Boxes,
  code: Code,
  database: Database,
  layers: Layers,
  lock: Lock,
  rocket: Rocket,
  route: Route,
  shield: Shield,
  terminal: Terminal,
  zap: Zap,
};

export function CardGroup({ cols = "2", children }: { cols?: "1" | "2" | "3"; children: React.ReactNode }) {
  const grid = cols === "3" ? "sm:grid-cols-2 lg:grid-cols-3" : cols === "1" ? "" : "sm:grid-cols-2";
  return <div className={`grid grid-cols-1 gap-3 ${grid}`}>{children}</div>;
}

export function Card({
  title,
  href,
  icon,
  children,
}: {
  title: string;
  href?: string;
  icon?: string;
  children?: React.ReactNode;
}) {
  const Icon = icon ? ICONS[icon] : undefined;
  const external = href?.startsWith("http");
  const body = (
    <>
      {Icon || external ? (
        <div className="mb-3 flex items-start justify-between gap-2">
          {Icon ? (
            <span className="inline-flex size-8 items-center justify-center rounded-lg border border-border bg-subtle text-fg">
              <Icon aria-hidden className="size-4" />
            </span>
          ) : (
            <span />
          )}
          {external ? <ArrowUpRight aria-hidden className="size-4 text-faint" /> : null}
        </div>
      ) : null}
      <div className="text-sm font-semibold text-fg">{title}</div>
      {children ? <div className="mt-1 text-sm leading-relaxed text-muted [&_p]:m-0">{children}</div> : null}
    </>
  );
  const cls = "block rounded-xl border border-border p-4 no-underline! font-normal! transition-colors";
  if (!href) return <div className={cls}>{body}</div>;
  const hover = `${cls} hover:border-border-strong hover:bg-subtle`;
  return external ? (
    <a href={href} className={hover} target="_blank" rel="noreferrer">
      {body}
    </a>
  ) : (
    <Link href={href} className={hover}>
      {body}
    </Link>
  );
}
