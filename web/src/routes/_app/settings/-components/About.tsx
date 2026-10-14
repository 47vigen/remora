/** Versions and credits. This is the only place the UI names the engines remora is built on. */
export function About({ remora, engines }: { remora: string; engines: { name: string; version: string }[] }) {
  return (
    <div className="flex max-w-md flex-col gap-4">
      <dl className="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-sm">
        <dt className="text-muted">remora</dt>
        <dd className="font-mono">{remora}</dd>
        {engines.map((e) => (
          <div key={e.name} className="contents">
            <dt className="text-muted">{e.name}</dt>
            <dd className="font-mono">{e.version}</dd>
          </div>
        ))}
      </dl>
      <p className="text-muted text-sm">Built on Bifrost (Apache-2.0) and CLIProxyAPI (MIT)</p>
    </div>
  );
}
