import { formatMoney } from "../../lib/money";

type Slice = { category: string; cents: number };

const colors = ["#4f46e5", "#0ea5e9", "#10b981", "#f59e0b", "#ef4444", "#a855f7", "#64748b"];

// DonutChart draws the spending by category as SVG circle strokes with a
// legend; each slice is a dash array of one circle, so there is no path maths.
export default function DonutChart({ slices, currency }: { slices: Slice[]; currency: string }) {
  const total = slices.reduce((sum, s) => sum + s.cents, 0);
  const radius = 50;
  const circumference = 2 * Math.PI * radius;
  let offset = 0;

  if (total === 0) {
    return <p className="text-zinc-500">No spending this month.</p>;
  }

  return (
    <div className="flex items-center gap-6">
      <svg viewBox="0 0 140 140" role="img" aria-label="Spending by category" className="h-36 w-36 shrink-0 -rotate-90">
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
      <ul className="space-y-1 text-sm">
        {slices.map((s, i) => (
          <li key={s.category} className="flex items-center gap-2">
            <span className="inline-block h-3 w-3 rounded-sm" style={{ backgroundColor: colors[i % colors.length] }} />
            {s.category}: {formatMoney(s.cents, currency)}
          </li>
        ))}
      </ul>
    </div>
  );
}
