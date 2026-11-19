import { useEffect, useState } from "react";

import { SEARCH_DEBOUNCE_MS } from "./list-search";

export function useDebouncedSearch(text: string) {
  const q = text.trim();
  const [query, setQuery] = useState(q);
  useEffect(() => {
    if (q === query) return;
    const timer = setTimeout(() => setQuery(q), SEARCH_DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [q, query]);
  return query;
}
