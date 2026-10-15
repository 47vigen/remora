/** A small line of a series, scaled to its own peak; decorative, the figure beside it carries the value. */
export function Sparkline({ values, className }: { values: number[]; className?: string }) {
  if (values.length < 2) return null;
  const max = Math.max(...values, 1);
  const step = 100 / (values.length - 1);
  const points = values.map((v, i) => `${(i * step).toFixed(2)},${(28 - (v / max) * 26).toFixed(2)}`).join(" ");
  return (
    <svg viewBox="0 0 100 30" preserveAspectRatio="none" aria-hidden="true" className={className ?? "h-7 w-full"}>
      <polyline points={`0,30 ${points} 100,30`} className="fill-accent/15 stroke-none" />
      <polyline
        points={points}
        className="stroke-ink-strong fill-none"
        strokeWidth="1.25"
        vectorEffect="non-scaling-stroke"
      />
    </svg>
  );
}
