import { useEffect, useState } from "react";
import { SystemQuery, newRequestId } from "@delinoio/delidev-api-client";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";

export enum LocalServerState { Checking = "checking", Ready = "ready", Retrying = "retrying", Stopped = "stopped", Blocked = "blocked" }
export interface LocalServerStatus { state: LocalServerState; attempts: number; retry_ms: number; failure?: string }
export function LocalServerStatusText({ status }: { status?: LocalServerStatus }) {
  return <p role="status">{!status || status.state === LocalServerState.Checking ? "Checking local server…" : status.state === LocalServerState.Ready ? "Local server ready" : status.state === LocalServerState.Stopped ? "Automatic restart is stopped" : status.state === LocalServerState.Retrying ? `Local server unavailable · attempt ${status.attempts} · retry delay up to ${Math.ceil(status.retry_ms / 1000)} seconds` : "Local server needs attention · check its version, configuration and private state"}</p>;
}
export function LocalServerControls({ status, restart, busy, problem }: { status?: LocalServerStatus; restart: () => void; busy: boolean; problem?: React.ReactNode }) {
  const [accepted, setAccepted] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const mutation = useRetainedMutation("stop-local-server", SystemQuery.stopServer, () => { setAccepted(true); setConfirm(false); });
  useEffect(() => { if (status?.state === LocalServerState.Stopped) setAccepted(false); }, [status?.state]);
  return <details className="local-server"><summary>Local server</summary><LocalServerStatusText status={status} />
    {status?.state === LocalServerState.Stopped || status?.state === LocalServerState.Blocked ? <button disabled={busy || mutation.busy} onClick={restart}>{busy ? "Starting…" : "Start local server"}</button> : null}
    {accepted ? <p>Stop accepted. Waiting for shutdown; session cleanup is not yet confirmed.</p> : null}
    {status?.state === LocalServerState.Ready && !accepted ? <>{confirm ? <><p>Stopping the server disconnects all clients and Workers. Retained sessions stay saved; automatic restart stays stopped until you explicitly start it or launch a fresh DeliDev process.</p><button disabled={busy || mutation.busy || mutation.uncertain} onClick={() => void mutation.send({ requestId: newRequestId() })}>Confirm server stop</button><button disabled={mutation.busy || mutation.uncertain} onClick={() => setConfirm(false)}>Keep server running</button></> : <button disabled={busy || mutation.busy || mutation.uncertain} onClick={() => setConfirm(true)}>Stop local server</button>}</> : null}
    <Problem error={mutation.error} />{mutation.uncertain ? <button disabled={mutation.busy} onClick={mutation.retry}>Retry the same server stop</button> : null}{problem}
  </details>;
}
