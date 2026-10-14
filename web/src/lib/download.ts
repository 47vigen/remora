// Derived from maximhq/bifrost ui/lib/utils/csv.ts (Apache-2.0); modified.
/**
 * Reusable export utilities.
 *
 * Usage:
 *   const csv = buildCSV(headers, rows);
 *   downloadFile(csv, "my-export.csv", "text/csv");
 */

/** Escape a cell value for CSV (RFC 4180). */
function escapeCell(value: unknown): string {
  const str = String(value ?? "");
  return `"${str.replace(/"/g, '""')}"`;
}

/**
 * Build a CSV string from headers and rows.
 *
 * Each row is an array of cell values (string | number | boolean | null | undefined).
 */
export function buildCSV(headers: string[], rows: unknown[][]): string {
  return [headers, ...rows].map((row) => row.map(escapeCell).join(",")).join("\n");
}

/**
 * Trigger a browser download of a text file.
 *
 * @param content  The file content
 * @param filename The full file name, extension included
 * @param mime     The media type, e.g. "text/csv"
 */
export function downloadFile(content: string, filename: string, mime: string): void {
  const blob = new Blob([content], { type: `${mime};charset=utf-8` });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 0);
}
