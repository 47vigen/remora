import { Alert, Link } from "@heroui/react";

import type { AccountPool } from "../../../../client/types.gen";

/** One warning per subscription provider whose enabled accounts dropped out of the pool, naming them. */
export function PoolAlert({ pool }: { pool: AccountPool[] }) {
  const short = pool.filter((p) => p.out.length > 0);
  if (short.length === 0) return null;
  return (
    <>
      {short.map((p) => (
        <Alert key={p.provider} status="warning" role="alert">
          <Alert.Indicator />
          <Alert.Content>
            <Alert.Title>
              {p.out.length} of {p.enabled} {p.provider} accounts are out of the pool
            </Alert.Title>
            <Alert.Description>
              <ul className="flex flex-col gap-0.5">
                {p.out.map((a) => (
                  <li key={a.id}>
                    {a.label} <span className="text-muted">({a.reason})</span>
                  </li>
                ))}
              </ul>
              <Link href={`/providers/${p.provider}`} className="text-sm">
                Open {p.provider} accounts
              </Link>
            </Alert.Description>
          </Alert.Content>
        </Alert>
      ))}
    </>
  );
}
