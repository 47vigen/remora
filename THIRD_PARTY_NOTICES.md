# Third-party notices

remora builds the following third-party software from its git submodule under `engines/` and ships it in the
remora image.

## Bifrost

Copyright © Maxim AI.

Licensed under the Apache License, Version 2.0: <https://github.com/maximhq/bifrost>. The licence text is the same
as remora's own [`LICENSE`](LICENSE).

Bifrost is built from source and shipped as a binary in the image.

## Bifrost UI by Maxim AI, Apache-2.0, https://github.com/maximhq/bifrost, files derived (modified, not synced)

These files in `web/src` are derived from Bifrost's `ui/` source and carry a header naming the file they come from.
They were copied once and modified; they are not kept in sync.

- `web/src/components/provider-icon/icons.tsx`
- `web/src/components/provider-icon/icons.test.ts`
- `web/src/lib/providers.ts`
- `web/src/lib/providers.test.ts`
- `web/src/lib/provider-meta.ts`
- `web/src/lib/numbers.ts`
- `web/src/lib/download.ts`
- `web/src/lib/chart.ts`
- `web/src/lib/live-socket.ts`
- `web/src/lib/live-socket.test.ts`
- `web/src/routes/_app/(overview)/-lib/export.ts`
- `web/src/routes/_app/(overview)/-components/charts/barShape.tsx`
- `web/src/routes/_app/(overview)/-components/charts/VolumeChart.tsx`
- `web/src/routes/_app/(overview)/-components/charts/TokenChart.tsx`
- `web/src/routes/_app/(overview)/-components/charts/CostChart.tsx`
- `web/src/routes/_app/(overview)/-components/charts/LatencyChart.tsx`
- `web/src/routes/_app/logs/-lib/format.ts`
- `web/src/routes/_app/logs/-hooks/useLiveLogs.ts`

## CLIProxyAPI

Copyright © Luis Pater and Router-For.ME.

Licensed under the MIT License: <https://github.com/router-for-me/CLIProxyAPI>. CLIProxyAPI is built from source
and shipped as a binary in the image. Its licence requires the full text below.

```text
MIT License

Copyright (c) 2025-2005.9 Luis Pater
Copyright (c) 2025.9-present Router-For.ME

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```
