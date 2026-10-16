"use client";

import { Tabs } from "@base-ui/react/tabs";

export type TabItem = { label: string; content: React.ReactNode; action?: React.ReactNode };

/** Base UI tabs; `variant="code"` renders them as a code block header. */
export function TabsClient({ items, variant = "default" }: { items: TabItem[]; variant?: "default" | "code" }) {
  const code = variant === "code";
  return (
    <Tabs.Root
      defaultValue={0}
      className={code ? "overflow-hidden rounded-xl border border-border bg-code-bg" : "rounded-xl border border-border"}
    >
      <Tabs.List
        className={`relative flex gap-1 overflow-x-auto border-b border-border px-2 ${code ? "h-10 pr-10" : ""}`}
        aria-label={code ? "Code examples" : "Tabs"}
      >
        {items.map((item, i) => (
          <Tabs.Tab
            key={i}
            value={i}
            className={`relative cursor-pointer px-2.5 whitespace-nowrap text-muted transition-colors outline-none hover:text-fg focus-visible:text-fg focus-visible:underline data-active:text-fg ${
              code ? "font-mono text-xs" : "py-3 text-sm font-medium"
            }`}
          >
            {item.label}
          </Tabs.Tab>
        ))}
        <Tabs.Indicator className="absolute bottom-[-1px] left-(--active-tab-left) h-px w-(--active-tab-width) bg-fg transition-all duration-200" />
      </Tabs.List>
      {items.map((item, i) => (
        <Tabs.Panel key={i} value={i} className={code ? "relative" : "prose px-5 py-4"}>
          {item.action ? <div className="absolute -top-[2.3rem] right-1.5">{item.action}</div> : null}
          {item.content}
        </Tabs.Panel>
      ))}
    </Tabs.Root>
  );
}
