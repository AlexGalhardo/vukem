import { useFinanceI18n } from "../../lib/i18n";

type Slice = { category: string; cents: number };

const colors = ["var(--chart-1)", "var(--chart-2)", "var(--chart-3)", "var(--chart-4)", "var(--chart-5)"];

// DonutChart draws the spending by category as SVG circle strokes with a
// legend; each slice is a dash array of one circle, so there is no path maths.
export default function DonutChart({ slices, currency }: { slices: Slice[]; currency: string }) {
  const {t,money:formatMoney}=useFinanceI18n();
  const total = slices.reduce((sum, s) => sum + s.cents, 0);
  const radius = 50;
  const circumference = 2 * Math.PI * radius;
  let offset = 0;

  if (total === 0) {
    return <p className="text-muted-foreground">{t("emptyTransactions")}</p>;
  }

  return (
    <div className="flex items-center gap-6">
      <svg viewBox="0 0 140 140" role="img" aria-label={t("spending")} className="size-36 shrink-0 -rotate-90">
        {slices.map((s, i) => {
          const length = (s.cents / total) * circumference;
          const circle = (
            <circle
              key={s.category}
              cx="70"
              cy="70"
              r={radius}
              fill="none"
              stroke={colors[i % colors.length]}
              strokeWidth="22"
              strokeDasharray={`${length} ${circumference - length}`}
              strokeDashoffset={-offset}
            >
              <title>{`${s.category}: ${formatMoney(s.cents, currency)}`}</title>
            </circle>
          );
          offset += length;
          return circle;
        })}
      </svg>
      <ul className="flex flex-col gap-1 text-sm">
        {slices.map((s, i) => (
          <li key={s.category} className="flex items-center gap-2">
            <span className="inline-block size-3 rounded-sm" style={{ backgroundColor: colors[i % colors.length] }} />
            {s.category}: {formatMoney(s.cents, currency)}
          </li>
        ))}
      </ul>
    </div>
  );
}
