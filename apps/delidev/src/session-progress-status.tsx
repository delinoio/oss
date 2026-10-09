// SPDX-License-Identifier: Apache-2.0
import { copy, useLocale, type MessageKey } from "./localization";
import { SessionProgressPhase } from "./session-progress";
import { observedStartupOperation, type StartupOperations, type StartupOperationRow } from "./session-startup-operations";
import type { Resource } from "@delinoio/delidev-api-client";
import "./session-progress.css";
const labels: Record<SessionProgressPhase, MessageKey> = {
 [SessionProgressPhase.Preparing]: "session-progress.preparing",
 [SessionProgressPhase.Queued]: "session-progress.queued",
 [SessionProgressPhase.Starting]: "session-progress.starting",
 [SessionProgressPhase.Response]: "session-progress.response",
};
const workspaceLabels: Record<number, MessageKey> = { 1:"session-progress.setup",2:"session-progress.inspect",3:"session-progress.clone",4:"session-progress.reference",5:"session-progress.checkout",6:"session-progress.verify",7:"session-progress.publish" };
const nativeLabels: Record<number, MessageKey> = {0:"session-progress.queued",1:"session-progress.resolve",2:"session-progress.launch",3:"session-progress.initialize",4:"session-progress.settings",5:"session-progress.input",6:"session-progress.response"};
function Step({ row, labels }: { row: StartupOperationRow; labels: Record<number,MessageKey> }) {
 return <li className={`session-startup-operation is-${row.state}`}><span className={row.state === "running" ? "session-progress-spinner" : "session-startup-marker"} aria-hidden="true">{row.state === "completed" ? "✓" : ""}</span><span>{copy(labels[row.operation])}{row.ordinal && row.count ? <span className="session-startup-repository"> {copy("session-progress.repository")} {row.ordinal}/{row.count}</span> : null}<span className="sr-only"> — {copy(`session-progress.${row.state}` as MessageKey)}</span></span></li>;
}
function Groups({ operations }: { operations: StartupOperations }) {
 return <div className="session-startup-groups"><section aria-label={copy("session-progress.workspace")}><h4>{copy("session-progress.workspace")}</h4><ol>{operations.workspace.map(row => <Step key={row.operation} row={row} labels={workspaceLabels}/>)}</ol></section><section aria-label={copy("session-progress.agent")}><h4>{copy("session-progress.agent")}</h4><ol>{operations.native.map(row => <Step key={row.operation} row={row} labels={nativeLabels}/>)}</ol></section></div>;
}
export function SessionProgressStatus({ phase, compact, operations }: { phase: SessionProgressPhase; compact: boolean; operations?: StartupOperations }) {
 useLocale();
 const currentWorkspace = operations?.workspace.find(row => row.state === "running"), currentNative = operations?.native.find(row => row.state === "running");
 const current = currentNative ?? currentWorkspace;
 const label = current ? (currentNative ? nativeLabels : workspaceLabels)[current.operation] : labels[phase];
 return <div className={`session-progress${compact ? " is-compact" : ""}${operations ? " has-operations" : ""}`}>
 <div className="session-startup-status" role="status" aria-live="polite" aria-atomic="true">{!operations || compact ? <span className="session-progress-spinner" aria-hidden="true"/> : null}<span>{operations && !compact ? copy("session-progress.title") : copy(label)}{compact && current?.ordinal && current.count ? ` ${current.ordinal}/${current.count}` : ""}</span></div>
 {operations ? compact ? <details className="session-startup-disclosure"><summary>{copy("session-progress.details")}</summary><Groups operations={operations}/></details> : <Groups operations={operations}/> : null}
 </div>;
}

export function StartupObservedOperation({ session }: { session: Resource | undefined }) {
 useLocale(); const current = observedStartupOperation(session); if (!current) return null;
 return <span>{copy("session-progress.observed")}: {copy((current.native ? nativeLabels : workspaceLabels)[current.operation])}{current.ordinal && current.count ? ` ${current.ordinal}/${current.count}` : ""}</span>;
}
