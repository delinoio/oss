import { useEffect, useRef, useState } from "react";

export enum LocalWorkerAction { Register = "register", Start = "start", Stop = "stop", Status = "status" }
export enum LocalWorkerState { NotStarted = "not-started", Starting = "starting", Running = "running", Stopping = "stopping", Exited = "exited", Uncertain = "uncertain" }
export enum LocalWorkerPresentation { Default = "default", RunnerDevices = "runner-devices" }
export interface LocalWorkerStatus { state: LocalWorkerState; machine_id: string; generation?: string; controller_active: boolean }
export type ControlLocalWorker = (action: LocalWorkerAction, generation?: string) => Promise<LocalWorkerStatus>;
const descriptions: Record<LocalWorkerState, string> = {
  [LocalWorkerState.NotStarted]: "Registered on this computer; not started yet.",
  [LocalWorkerState.Starting]: "Starting; waiting for the server to accept this Worker. A previous connection lease can delay readiness.",
  [LocalWorkerState.Running]: "Worker controller running. Server connectivity and harness readiness are shown separately below.",
  [LocalWorkerState.Stopping]: "Stop intent saved; waiting for the original Worker controller to exit.",
  [LocalWorkerState.Exited]: "Worker controller exited. Existing session cleanup and recovery remain separate.",
  [LocalWorkerState.Uncertain]: "Worker exit is unconfirmed. Inspect its private log and original session recovery before explicitly replacing the controller.",
};
const badges: Record<LocalWorkerState, string> = {
  [LocalWorkerState.NotStarted]: "Not started",
  [LocalWorkerState.Starting]: "Starting",
  [LocalWorkerState.Running]: "Controller running",
  [LocalWorkerState.Stopping]: "Stopping",
  [LocalWorkerState.Exited]: "Exited",
  [LocalWorkerState.Uncertain]: "Exit unconfirmed",
};

export function LocalWorkerControls({ control, active, changed, allowRegistration = true, pendingChanged, presentation = LocalWorkerPresentation.Default }: { control: ControlLocalWorker; active: boolean; changed: () => void; allowRegistration?: boolean; pendingChanged?: (pending: boolean) => void; presentation?: LocalWorkerPresentation }) {
  const [status, setStatus] = useState<LocalWorkerStatus>();
  const [problem, setProblem] = useState("");
  const [busy, setBusy] = useState(false);
  const [confirmation, setConfirmation] = useState<string>();
  const [pendingStop, setPendingStop] = useState<string>();
  useEffect(() => { pendingChanged?.(busy || Boolean(confirmation || pendingStop)); }, [busy, confirmation, pendingStop, pendingChanged]);
  const gate = useRef(false), alive = useRef(false), controlRef = useRef(control);
  controlRef.current = control;
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  const refresh = async (clearProblem = false) => {
    if (gate.current) return;
    gate.current = true; setBusy(true);
    try { const value = await controlRef.current(LocalWorkerAction.Status); if (alive.current) { setStatus(value); if (clearProblem) setProblem(""); } }
    catch { if (alive.current) setProblem(allowRegistration ? "The local Worker could not be inspected. Register it if this computer has no Worker, or inspect its original private scope and device registration." : "No readable retained Worker was found. Preserve its original scope and inspect its device registration; removal does not create or replace a Worker."); }
    finally { gate.current = false; if (alive.current) setBusy(false); }
  };
  useEffect(() => {
    if (!active) return;
    let canceled = false;
    let timer: ReturnType<typeof setTimeout>;
    const read = async () => { await refresh(); if (!canceled) timer = setTimeout(() => void read(), 3000); };
    void read();
    return () => { canceled = true; clearTimeout(timer); };
  }, [active]);
  const perform = async (action: LocalWorkerAction, generation?: string) => {
    if (gate.current || !alive.current) return;
    gate.current = true; setBusy(true); setProblem("");
    try {
      const value = await controlRef.current(action, generation);
      if (!alive.current) return;
      setStatus(value); setConfirmation(undefined); setPendingStop(undefined); changed();
    } catch {
      if (!alive.current) return;
      if (action === LocalWorkerAction.Stop) setPendingStop(generation);
      setProblem(action === LocalWorkerAction.Stop ? "The original stop is unconfirmed. Refresh status or retry that same generation; another Worker will not be stopped." : "The local Worker action is unconfirmed. Refresh status before another action. Registration and accepted jobs are preserved.");
    } finally {
      gate.current = false;
      if (alive.current) { setBusy(false); void refresh(); }
    }
  };
  const canStart = status && !status.controller_active && [LocalWorkerState.NotStarted, LocalWorkerState.Exited, LocalWorkerState.Uncertain].includes(status.state);
  const stale = confirmation && status?.generation !== confirmation;
  const runnerDevices = presentation === LocalWorkerPresentation.RunnerDevices;
  return <section aria-label="Worker on this computer" className={runnerDevices ? "settings-runner-worker" : undefined}>
    {runnerDevices ? <div className="settings-runner-worker-heading"><h2>This computer's Worker</h2>{status ? <span className="settings-runner-badge">{badges[status.state]}</span> : null}</div> : <h3>This computer's Worker</h3>}
    <p>Registration is separate from startup. The Worker continues after you quit DeliDev. Harnesses must already be installed.</p>
    {status ? <><p role="status" className={runnerDevices && status.state === LocalWorkerState.Uncertain ? "settings-runner-uncertain" : undefined}>{runnerDevices && status.state === LocalWorkerState.Uncertain ? <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="M12 3 2 21h20L12 3ZM12 9v5m0 3h.01" /></svg> : null}{descriptions[status.state]}</p><small>Execution machine: {status.machine_id}</small></> : null}
    {problem ? <p role="alert">{problem}</p> : null}
    <div className="actions"><button disabled={busy} onClick={() => void refresh(true)}>Refresh local Worker</button>
      {!status && allowRegistration ? <button disabled={busy} onClick={() => void perform(LocalWorkerAction.Register)}>Register this computer</button> : null}
      {canStart ? <button className={runnerDevices ? "primary" : undefined} disabled={busy || Boolean(pendingStop || confirmation)} onClick={() => void perform(LocalWorkerAction.Start)}>Start local Worker</button> : null}
      {status?.generation && status.state !== LocalWorkerState.Exited && !confirmation && !pendingStop ? <button className={runnerDevices ? "settings-runner-stop" : undefined} disabled={busy} onClick={() => setConfirmation(status.generation)}>Stop local Worker</button> : null}
    </div>
    {confirmation && !pendingStop ? <><p>Stopping this Worker interrupts its active work and prevents this controller from reconnecting. Sessions remain saved and may require recovery.</p>{stale ? <p role="alert">The Worker generation changed. Refresh and select its current generation before a new stop.</p> : null}<button disabled={busy || Boolean(stale)} onClick={() => void perform(LocalWorkerAction.Stop, confirmation)}>Confirm Worker stop</button><button disabled={busy} onClick={() => setConfirmation(undefined)}>Keep Worker running</button></> : null}
    {pendingStop ? <><p>The retained stop targets only its original generation.</p><button disabled={busy} onClick={() => void perform(LocalWorkerAction.Stop, pendingStop)}>Retry original Worker stop</button>{status?.generation !== pendingStop || status.state === LocalWorkerState.Exited ? <button disabled={busy} onClick={() => { setPendingStop(undefined); setConfirmation(undefined); setProblem(""); }}>Acknowledge refreshed Worker state</button> : null}</> : null}
  </section>;
}
