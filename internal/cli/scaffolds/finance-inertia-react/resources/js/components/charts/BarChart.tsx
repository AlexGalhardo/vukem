import { useFinanceI18n } from "../../lib/i18n";

type Point = { month: string; incomeCents: number; expenseCents: number };

// BarChart draws income and expense per month as plain SVG: no chart
// dependency, and the values stay readable in the title of each bar.
export default function BarChart({ points, currency }: { points: Point[]; currency: string }) {
  const {t,money:formatMoney,month:monthLabel}=useFinanceI18n();
  const max = Math.max(1, ...points.flatMap((p) => [p.incomeCents, p.expenseCents]));
  const width = 480;
  const height = 180;
  const group = width / Math.max(points.length, 1);
  const bar = Math.min(28, group / 3);

  return (
    <svg viewBox={`0 0 ${width} ${height + 24}`} role="img" aria-label={t("cashFlow")} className="w-full">
      {points.map((p, i) => {
        const x = i * group + group / 2;
        const hi = (p.incomeCents / max) * height;
        const he = (p.expenseCents / max) * height;
        return (
          <g key={p.month}>
            <rect x={x - bar - 1} y={height - hi} width={bar} height={hi} className="fill-chart-1">
              <title>{`${monthLabel(p.month)}: ${t("income")} ${formatMoney(p.incomeCents, currency)}`}</title>
            </rect>
            <rect x={x + 1} y={height - he} width={bar} height={he} className="fill-chart-2">
              <title>{`${monthLabel(p.month)}: ${t("expenses")} ${formatMoney(p.expenseCents, currency)}`}</title>
            </rect>
            <text x={x} y={height + 16} textAnchor="middle" className="fill-muted-foreground text-[11px]">
              {monthLabel(p.month)}
            </text>
          </g>
        );
      })}
      <line x1="0" x2={width} y1={height} y2={height} className="stroke-border" />
    </svg>
  );
}
