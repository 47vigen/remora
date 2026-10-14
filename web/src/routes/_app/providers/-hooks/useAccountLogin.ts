import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRef, useState } from "react";

import { LOGIN_DEADLINE_MS, LOGIN_POLL_MS, parseRedirectUrl } from "../-lib/login";
import {
  cancelAccountLoginMutation,
  completeAccountLoginMutation,
  getAccountLoginOptions,
  startAccountLoginMutation,
} from "../../../../client/@tanstack/react-query.gen";
import { errorMessage } from "../../../../lib/api";
import { invalidateOps } from "../../../../lib/query";

export type LoginPhase =
  | { phase: "idle" }
  | { phase: "starting" }
  | { phase: "redirect"; state: string; authUrl: string; submitting: boolean; error?: string }
  | { phase: "device"; state: string; verificationUrl: string; userCode: string }
  | { phase: "waiting"; state: string }
  | { phase: "done" }
  | { phase: "failed"; message: string };

const TIMED_OUT = "Sign-in timed out. Start again.";

class TimedOut extends Error {
  constructor() {
    super(TIMED_OUT);
  }
}

/**
 * Runs one account sign-in for a subscription provider: start, then either the pasted redirect address (remote
 * browser) or the device code, then poll until the server reports the account. This is the only place the login
 * calls the network.
 */
export function useAccountLogin(providerId: string) {
  const qc = useQueryClient();
  const [login, setLogin] = useState<LoginPhase>({ phase: "idle" });
  const startedAt = useRef(0);

  const startM = useMutation(startAccountLoginMutation());
  const completeM = useMutation(completeAccountLoginMutation());
  const cancelM = useMutation(cancelAccountLoginMutation());

  const polling = login.phase === "waiting" || login.phase === "device";
  const state = polling ? login.state : "";
  const base = getAccountLoginOptions({ path: { state } });
  const status = useQuery({
    ...base,
    queryFn: async (ctx) => {
      if (Date.now() - startedAt.current > LOGIN_DEADLINE_MS) throw new TimedOut();
      const r = await base.queryFn!(ctx);
      if (r.status === "ok") void invalidateOps(qc, "listAccounts", "listProviders");
      return r;
    },
    enabled: polling,
    refetchInterval: (q) =>
      !q.state.error && (q.state.data === undefined || q.state.data.status === "wait") ? LOGIN_POLL_MS : false,
    retry: (count, err) => !(err instanceof TimedOut) && count < 3,
    gcTime: 0,
  });

  // The server's answer settles the phase; it is derived here rather than copied into state.
  const current: LoginPhase = !polling
    ? login
    : status.data?.status === "ok"
      ? { phase: "done" }
      : status.data?.status === "error"
        ? { phase: "failed", message: status.data.message || "The sign-in failed. Start again." }
        : status.error
          ? { phase: "failed", message: status.error instanceof TimedOut ? TIMED_OUT : errorMessage(status.error) }
          : login;

  const stateOf = (l: LoginPhase) => ("state" in l ? l.state : undefined);

  return {
    login: current,
    start() {
      startedAt.current = Date.now();
      setLogin({ phase: "starting" });
      startM.mutate(
        { body: { provider: providerId } },
        {
          onSuccess: (r) =>
            setLogin(
              r.user_code
                ? { phase: "device", state: r.state, verificationUrl: r.url, userCode: r.user_code }
                : { phase: "redirect", state: r.state, authUrl: r.url, submitting: false },
            ),
          onError: (e) => setLogin({ phase: "failed", message: errorMessage(e) }),
        },
      );
    },
    submitRedirect(pasted: string) {
      if (login.phase !== "redirect" || login.submitting) return;
      const parsed = parseRedirectUrl(pasted, login.state);
      if (!parsed.ok) {
        setLogin({ ...login, error: parsed.error });
        return;
      }
      if (Date.now() - startedAt.current > LOGIN_DEADLINE_MS) {
        setLogin({ phase: "failed", message: TIMED_OUT });
        return;
      }
      const { state: loginState } = login;
      setLogin({ ...login, submitting: true, error: undefined });
      completeM.mutate(
        { path: { state: loginState }, body: { redirect_url: parsed.url } },
        {
          onSuccess: () => setLogin({ phase: "waiting", state: loginState }),
          onError: (e) => setLogin({ ...login, submitting: false, error: errorMessage(e) }),
        },
      );
    },
    cancel() {
      const s = stateOf(current);
      if (s) cancelM.mutate({ path: { state: s } });
      setLogin({ phase: "idle" });
    },
  };
}
