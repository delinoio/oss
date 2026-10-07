import { ownedMessage, useProductMessage, LocalizedText, copy, useLocale  } from "./localization";
import { useEffect, useRef, useState } from "react";

export enum LocalWorkerAction { Register = "register", Start = "start", Stop = "stop", Status = "status" }
export enum LocalWorkerState { NotStarted = "not-started", Starting = "starting", Running = "running", Stopping = "stopping", Exited = "exited", Uncertain = "uncertain" }
export enum LocalWorkerPresentation { Default = "default", RunnerDevices = "runner-devices" }
export interface LocalWorkerStatus { state: LocalWorkerState; machine_id: string; generation?: string; controller_active: boolean }
export type ControlLocalWorker = (action: LocalWorkerAction, generation?: string) => Promise<LocalWorkerStatus>;
const descriptions: Record<LocalWorkerState, string> = {
  get [LocalWorkerState.NotStarted]() { return copy("local-worker-controls.registeredOnThisComputerNotStarted_429f06"); },
  get [LocalWorkerState.Starting]() { return copy("local-worker-controls.startingWaitingForTheServerTo_5d140b"); },
  get [LocalWorkerState.Running]() { return copy("local-worker-controls.workerControllerRunningServerConnectivityAnd_d14dd1"); },
  get [LocalWorkerState.Stopping]() { return copy("local-worker-controls.stopIntentSavedWaitingForThe_bbbf38"); },
  get [LocalWorkerState.Exited]() { return copy("local-worker-controls.workerControllerExitedExistingSessionCleanup_a58050"); },
  get [LocalWorkerState.Uncertain]() { return copy("local-worker-controls.workerExitIsUnconfirmedInspectIts_ed152e"); },
};
const badges: Record<LocalWorkerState, string> = {
  get [LocalWorkerState.NotStarted]() { return copy("local-worker-controls.extra.ba35f0c47d86"); },
  get [LocalWorkerState.Starting]() { return copy("local-worker-controls.extra.aeed4d26bb5f"); },
  get [LocalWorkerState.Running]() { return copy("local-worker-controls.extra.dff8b845bd8d"); },
  get [LocalWorkerState.Stopping]() { return copy("local-worker-controls.extra.a71ee1d4ac5c"); },
  get [LocalWorkerState.Exited]() { return copy("local-worker-controls.extra.85fc46c17cd7"); },
  get [LocalWorkerState.Uncertain]() { return copy("local-worker-controls.extra.7564cd2e0474"); },
};

export function LocalWorkerControls({ control, active, changed, allowRegistration = true, pendingChanged, presentation = LocalWorkerPresentation.Default }: { control: ControlLocalWorker; active: boolean; changed: () => void; allowRegistration?: boolean; pendingChanged?: (pending: boolean) => void; presentation?: LocalWorkerPresentation }) {
  useLocale();
  const [status, setStatus] = useState<LocalWorkerStatus>();
  const [problem, setProblem] = useProductMessage("");
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
    catch { if (alive.current) setProblem(allowRegistration ? ownedMessage("local-worker-controls.extra.2f06614e997d") : ownedMessage("local-worker-controls.extra.e5f1eb4d93a8")); }
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
      setProblem(action === LocalWorkerAction.Stop ? ownedMessage("local-worker-controls.extra.fac1a4d002f4") : ownedMessage("local-worker-controls.extra.7c9605e7ec95"));
    } finally {
      gate.current = false;
      if (alive.current) { setBusy(false); void refresh(); }
    }
  };
  const canStart = status && !status.controller_active && [LocalWorkerState.NotStarted, LocalWorkerState.Exited, LocalWorkerState.Uncertain].includes(status.state);
  const stale = confirmation && status?.generation !== confirmation;
  const runnerDevices = presentation === LocalWorkerPresentation.RunnerDevices;
  return <section aria-label={copy("local-worker-controls.workerOnThisComputer_53d365")} className={runnerDevices ? "settings-runner-worker" : undefined}>
    {runnerDevices ? <div className="settings-runner-worker-heading"><h2>{copy("local-worker-controls.thisComputerSWorker_80a5ac")}</h2>{status ? <span className="settings-runner-badge">{badges[status.state]}</span> : null}</div> : <h3>{copy("local-worker-controls.thisComputerSWorker_80a5ac")}</h3>}
    <p>{copy("local-worker-controls.registrationIsSeparateFromStartupThe_f724cc")}</p>
    {status ? <><p role="status" className={runnerDevices && status.state === LocalWorkerState.Uncertain ? "settings-runner-uncertain" : undefined}>{runnerDevices && status.state === LocalWorkerState.Uncertain ? <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="M12 3 2 21h20L12 3ZM12 9v5m0 3h.01" /></svg> : null}{descriptions[status.state]}</p><small><LocalizedText id="local-worker-controls.executionMachine_37d7c0" components={{ s0: <>{status.machine_id}</> }} /></small></> : null}
    {problem ? <p role="alert">{problem}</p> : null}
    <div className="actions"><button disabled={busy} onClick={() => void refresh(true)}>{copy("local-worker-controls.refreshLocalWorker_167e1d")}</button>
      {!status && allowRegistration ? <button disabled={busy} onClick={() => void perform(LocalWorkerAction.Register)}>{copy("local-worker-controls.registerThisComputer_cbad75")}</button> : null}
      {canStart ? <button className={runnerDevices ? "primary" : undefined} disabled={busy || Boolean(pendingStop || confirmation)} onClick={() => void perform(LocalWorkerAction.Start)}>{copy("local-worker-controls.startLocalWorker_9c00b5")}</button> : null}
      {status?.generation && status.state !== LocalWorkerState.Exited && !confirmation && !pendingStop ? <button className={runnerDevices ? "settings-runner-stop" : undefined} disabled={busy} onClick={() => setConfirmation(status.generation)}>{copy("local-worker-controls.stopLocalWorker_0e4a48")}</button> : null}
    </div>
    {confirmation && !pendingStop ? <><p>{copy("local-worker-controls.stoppingThisWorkerInterruptsItsActive_09fca2")}</p>{stale ? <p role="alert">{copy("local-worker-controls.theWorkerGenerationChangedRefreshAnd_62391f")}</p> : null}<button disabled={busy || Boolean(stale)} onClick={() => void perform(LocalWorkerAction.Stop, confirmation)}>{copy("local-worker-controls.confirmWorkerStop_c7e9d5")}</button><button disabled={busy} onClick={() => setConfirmation(undefined)}>{copy("local-worker-controls.keepWorkerRunning_6abcfd")}</button></> : null}
    {pendingStop ? <><p>{copy("local-worker-controls.theRetainedStopTargetsOnlyIts_b08b1d")}</p><button disabled={busy} onClick={() => void perform(LocalWorkerAction.Stop, pendingStop)}>{copy("local-worker-controls.retryOriginalWorkerStop_70aea2")}</button>{status?.generation !== pendingStop || status.state === LocalWorkerState.Exited ? <button disabled={busy} onClick={() => { setPendingStop(undefined); setConfirmation(undefined); setProblem(""); }}>{copy("local-worker-controls.acknowledgeRefreshedWorkerState_afc0fe")}</button> : null}</> : null}
  </section>;
}
