/** Versions and credits. This is the only place the UI names the engines remora is built on. */
export function About({ remora, engines }: { remora: string; engines: { name: string; version: string }[] }) {
  return (
    <div className="flex flex-col gap-3">
      <dl className="border-border divide-separator flex max-w-md flex-col divide-y rounded-sm border text-sm">
        {[{ name: "remora", version: remora }, ...engines].map((e) => (
          <div key={e.name} className="flex items-center justify-between gap-6 px-3 py-2">
            <dt className="text-muted">{e.name}</dt>
            <dd className="font-mono text-[13px]">{e.version}</dd>
          </div>
        ))}
      </dl>
      <p className="text-muted text-xs">Built on Bifrost (Apache-2.0) and CLIProxyAPI (MIT)</p>
    </div>
  );
}
