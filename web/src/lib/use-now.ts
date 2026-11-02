import { useEffect, useState } from "react";

/** The current time, moved on every `intervalMs`, so relative times on a page that stays open keep up. */
export function useNow(intervalMs: number): Date {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const t = setInterval(() => setNow(new Date()), intervalMs);
    return () => clearInterval(t);
  }, [intervalMs]);
  return now;
}
