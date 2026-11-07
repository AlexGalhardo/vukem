export type TabItem = { label: string; content: React.ReactNode; action?: React.ReactNode };

/**
 * Static tabs. A page script switches the visible panel; the first panel
 * is shown without JavaScript.
 */
export function TabsClient({ items, variant = "default" }: { items: TabItem[]; variant?: "default" | "code" }) {
  const code = variant === "code";
  return (
    <div
      data-tabs
      className={code ? "overflow-hidden rounded-xl border border-border bg-code-bg" : "rounded-xl border border-border"}
    >
      <div
        role="tablist"
        className={`relative flex gap-1 overflow-x-auto border-b border-border px-2 ${code ? "h-10 pr-10" : ""}`}
        aria-label={code ? "Code examples" : "Tabs"}
      >
        {items.map((item, i) => (
          <button
            key={i}
            type="button"
            role="tab"
            data-tab={i}
            aria-selected={i === 0}
            className={`relative cursor-pointer px-2.5 whitespace-nowrap text-muted transition-colors outline-none hover:text-fg focus-visible:text-fg focus-visible:underline aria-selected:text-fg ${
              code ? "font-mono text-xs" : "py-3 text-sm font-medium"
            }`}
          >
            {item.label}
          </button>
        ))}
      </div>
      {items.map((item, i) => (
        <div key={i} role="tabpanel" data-panel={i} hidden={i !== 0} className={code ? "relative" : "prose px-5 py-4"}>
          {item.action ? <div className="absolute -top-[2.3rem] right-1.5">{item.action}</div> : null}
          {item.content}
        </div>
      ))}
    </div>
  );
}
