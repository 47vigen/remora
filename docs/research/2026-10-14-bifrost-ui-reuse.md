# Reusing Bifrost's UI code

Date: 2026-10-14. Source: `bifrost/ui` at `72457fe`. Read, not run.
Decision: remora's UI stays on HeroUI; Bifrost's `ui/` is a one-time parts bin for logic, never run or served.

## Stack

- Vite 8, React 19.2, TypeScript 5.9, TanStack Router (file-based, 92 routes).
- Data: RTK Query, 30 API slices (`lib/store/apis/`, about 5,000 LOC) typed to Bifrost's `/api`.
- Components: shadcn new-york (zinc), 21 Radix packages, Tailwind v4; `components/ui/` 79 files, about 16.9k LOC.
  Also lucide and phosphor icons, recharts 3, monaco, react-hook-form + zod 4, `@tanstack/react-table`, nuqs,
  sonner, next-themes.
- npm, oxlint, oxfmt, about 81 vitest tests. 1,191 files, about 173k LOC.
- Apache-2.0 (root LICENSE); no per-file headers; no NOTICE.
- Enterprise code is not in the repo: a private build symlinks it into `app/enterprise`; the public build gets 28
  upsell stub folders. 21 provider files and 20 config files import `@enterprise`.
- Not published to npm. 201 upstream commits touched `ui/` in the last 30 days.

## Reuse inventory

Copy once (logic, little or no shadcn):

| piece | path | LOC | on HeroUI |
|---|---|---|---|
| provider logos (34 SVGs) and lookup | `lib/constants/icons.tsx` | 1,013 | drop-in |
| provider metadata | `lib/constants/config.ts` | 296 | drop-in (cut `IS_ENTERPRISE`) |
| log constants | `lib/constants/logs.ts` | 497 | drop-in |
| chart and export helpers | `app/workspace/dashboard/utils/chartUtils.ts`, `exportUtils.ts` | 465 | drop-in |
| live-log WebSocket hook | `hooks/useWebSocket.tsx` + utils | 355 | logic; remora adds its own ticket endpoint |
| charts (cost, latency, tokens, throughput, model usage) | `app/workspace/dashboard/components/charts/*.tsx` | about 3,500 | keep recharts, swap card wrappers |
| logs volume chart | `app/workspace/logs/views/logsVolumeChart.tsx` | 485 | same |
| log column formatters | `app/workspace/logs/views/columns.tsx` | 865 | keep formatters, re-render cells |
| provider form schema | `lib/schemas/providerForm.ts` | 356 | drop-in if the model matches |

Reference only (field checklists for remora's OpenAPI): `lib/types/logs.ts`, `config.ts`, `governance.ts`,
`routingRules.ts`, `schemas.ts`.

Too coupled: `logs/sheets/logDetailView.tsx` (3,455 LOC), `providers/fragments/apiKeysFormFragment.tsx` (2,226),
`components/filters/logsFilterSidebar.tsx` (1,622), `logs/views/logsHeaderView.tsx` (554).

## HeroUI or Bifrost's stack

Adopting shadcn would make the primitives and charts reusable (25 to 30k LOC) but not the pages: every page reads
through RTK slices typed to Bifrost's API and many import `@enterprise`. It would save about a week on logs,
charts and forms, and cost HeroUI's compound components, React Aria accessibility and the zero-custom-CSS rule
(Bifrost's `globals.css` alone is 23.7k). remora's own pages (OAuth accounts, nested aliases, alias-level
allowlists) gain nothing either way.

## Licence obligations

Both projects are Apache-2.0. Each copied file gets a header such as
`// Derived from maximhq/bifrost ui/<path> (Apache-2.0); modified.`; `THIRD_PARTY_NOTICES.md` credits Bifrost UI by
Maxim AI. No brand strings or logos (`public/bifrost-*`, `public/maxim-*`), nothing from enterprise stubs. No sync
with upstream is planned.
