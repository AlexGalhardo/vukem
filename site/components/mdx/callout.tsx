import { CircleAlert, Info, Lightbulb, OctagonAlert } from "lucide-react";

const STYLES = {
  info: {
    icon: Info,
    label: "Note",
    box: "border-sky-200 bg-sky-50/60 dark:border-sky-900/60 dark:bg-sky-950/30",
    tone: "text-sky-700 dark:text-sky-400",
  },
  tip: {
    icon: Lightbulb,
    label: "Tip",
    box: "border-teal-200 bg-teal-50/60 dark:border-teal-900/60 dark:bg-teal-950/30",
    tone: "text-teal-700 dark:text-teal-400",
  },
  warning: {
    icon: CircleAlert,
    label: "Warning",
    box: "border-amber-200 bg-amber-50/60 dark:border-amber-900/50 dark:bg-amber-950/25",
    tone: "text-amber-700 dark:text-amber-400",
  },
  danger: {
    icon: OctagonAlert,
    label: "Danger",
    box: "border-red-200 bg-red-50/60 dark:border-red-900/50 dark:bg-red-950/25",
    tone: "text-red-700 dark:text-red-400",
  },
} as const;

export type CalloutType = keyof typeof STYLES;

export function Callout({
  type = "info",
  title,
  children,
}: {
  type?: CalloutType;
  title?: string;
  children: React.ReactNode;
}): React.ReactElement {
  const s = STYLES[type] ?? STYLES.info;
  const Icon = s.icon;
  return (
    <aside role="note" aria-label={title ?? s.label} className={`flex gap-3 rounded-xl border px-4 py-3 text-sm leading-relaxed ${s.box}`}>
      <Icon aria-hidden className={`mt-1 size-4 shrink-0 ${s.tone}`} />
      <div className="min-w-0 text-fg/85 [&_p]:m-0 [&>*+*]:mt-2">
        {title ? <p className={`font-semibold ${s.tone}`}>{title}</p> : null}
        {children}
      </div>
    </aside>
  );
}
