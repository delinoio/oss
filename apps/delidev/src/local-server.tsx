import { copy, useLocale } from "./localization";
import { useEffect, useState } from "react";
import { SystemQuery, newRequestId } from "@delinoio/delidev-api-client";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";

export enum LocalServerState { Checking = "checking", Ready = "ready", Retrying = "retrying", Stopped = "stopped", Blocked = "blocked" }
export interface LocalServerStatus { state: LocalServerState; attempts: number; retry_ms: number; failure?: string }
export function LocalServerStatusText({ status }: { status?: LocalServerStatus }) {
  useLocale();
  return <p role="status">{!status || status.state === LocalServerState.Checking ? copy("local-server.checkingLocalServer_c92e51") : status.state === LocalServerState.Ready ? copy("local-server.localServerReady_18acea") : status.state === LocalServerState.Stopped ? copy("local-server.automaticRestartIsStopped_f8e4c7") : status.state === LocalServerState.Retrying ? copy("local-server.localServerUnavailableAttemptRetryDelay_9f679d", { v0: status.attempts, v1: Math.ceil(status.retry_ms / 1000) }) : copy("local-server.localServerNeedsAttentionCheckIts_b02a69")}</p>;
}
export function LocalServerControls({ status, restart, busy, problem }: { status?: LocalServerStatus; restart: () => void; busy: boolean; problem?: React.ReactNode }) {
  useLocale();
  const [accepted, setAccepted] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const mutation = useRetainedMutation("stop-local-server", SystemQuery.stopServer, () => { setAccepted(true); setConfirm(false); });
  useEffect(() => { if (status?.state === LocalServerState.Stopped) setAccepted(false); }, [status?.state]);
  return <details className="local-server"><summary>{copy("local-server.localServer_caf011")}</summary><LocalServerStatusText status={status} />
    {status?.state === LocalServerState.Stopped || status?.state === LocalServerState.Blocked ? <button disabled={busy || mutation.busy} onClick={restart}>{busy ? copy("local-server.starting_bbe5fc") : copy("local-server.startLocalServer_4d64e3")}</button> : null}
    {accepted ? <p>{copy("local-server.stopAcceptedWaitingForShutdownSession_631795")}</p> : null}
    {status?.state === LocalServerState.Ready && !accepted ? <>{confirm ? <><p>{copy("local-server.stoppingTheServerDisconnectsAllClients_c28c49")}</p><button disabled={busy || mutation.busy || mutation.uncertain} onClick={() => void mutation.send({ requestId: newRequestId() })}>{copy("local-server.confirmServerStop_61a825")}</button><button disabled={mutation.busy || mutation.uncertain} onClick={() => setConfirm(false)}>{copy("local-server.keepServerRunning_60fe0a")}</button></> : <button disabled={busy || mutation.busy || mutation.uncertain} onClick={() => setConfirm(true)}>{copy("local-server.stopLocalServer_c1eeb5")}</button>}</> : null}
    <Problem error={mutation.error} />{mutation.uncertain ? <button disabled={mutation.busy} onClick={mutation.retry}>{copy("local-server.retryTheSameServerStop_b047eb")}</button> : null}{problem}
  </details>;
}
