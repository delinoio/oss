// SPDX-License-Identifier: Apache-2.0
import type { StartupTime, StartupTimes } from "./session-startup-timing";
import { copy, useLocale, type MessageKey } from "./localization";
import { SessionProgressPhase } from "./session-progress";
import {
  observedStartupOperation,
  type StartupOperations,
  type StartupOperationRow,
} from "./session-startup-operations";
import type { Resource } from "@delinoio/delidev-api-client";
import { Disclosure, DisclosureDensity, DisclosureSummary } from "./disclosure";
import "./session-progress.css";
const labels: Record<SessionProgressPhase, MessageKey> = {
  [SessionProgressPhase.Preparing]: "session-progress.preparing",
  [SessionProgressPhase.Queued]: "session-progress.queued",
  [SessionProgressPhase.Starting]: "session-progress.starting",
  [SessionProgressPhase.Response]: "session-progress.response",
};
const workspaceLabels: Record<number, MessageKey> = {
  1: "session-progress.setup",
  2: "session-progress.inspect",
  3: "session-progress.clone",
  4: "session-progress.reference",
  5: "session-progress.checkout",
  6: "session-progress.verify",
  7: "session-progress.publish",
};
const nativeLabels: Record<number, MessageKey> = {
  0: "session-progress.queued",
  1: "session-progress.resolve",
  2: "session-progress.launch",
  3: "session-progress.initialize",
  4: "session-progress.settings",
  5: "session-progress.input",
  6: "session-progress.response",
};
function Step({
  row,
  labels,
}: {
  row: StartupOperationRow;
  labels: Record<number, MessageKey>;
}) {
  return (
    <li className={`session-startup-operation is-${row.state}`}>
      <span
        className={
          row.state === "running"
            ? "session-progress-spinner"
            : "session-startup-marker"
        }
        aria-hidden="true"
      >
        {row.state === "completed" ? "✓" : ""}
      </span>
      <span>
        {copy(labels[row.operation])}
        {row.ordinal && row.count ? (
          <span className="session-startup-repository">
            {" "}
            {copy("session-progress.repository")} {row.ordinal}/{row.count}
          </span>
        ) : null}
        <span className="sr-only">
          {" "}
          — {copy(`session-progress.${row.state}` as MessageKey)}
        </span>
      </span>
    </li>
  );
}
function Timing({ time }: { time?: StartupTime }) {
  if (!time || time.seconds === undefined) return null;
  const label = time.unconfirmed
    ? "session-progress.timingUnconfirmed"
    : time.running
      ? "session-progress.timingRunning"
      : "session-progress.timingElapsed";
  return (
    <span className="session-startup-timing" aria-live="off">
      <span
        className="session-startup-seconds"
        aria-label={copy(label, { v0: time.seconds })}
      >
        {copy("session-progress.seconds", { v0: time.seconds })}
      </span>
      {time.unconfirmed ? (
        <span className="session-startup-unconfirmed">
          {copy("session-progress.unconfirmed")}
        </span>
      ) : null}
      {time.running ? (
        <span className="session-progress-spinner" aria-hidden="true" />
      ) : null}
    </span>
  );
}
export function RetainedStartupTimes({ timing }: { timing: StartupTimes }) {
  useLocale();
  return (
    <div className="session-startup-retained-time" aria-live="off">
      {(["workspace", "agent"] as const).map((key) =>
        timing[key]?.seconds !== undefined ? (
          <p key={key}>
            {copy(key === "workspace"
              ? "session-progress.workspace"
              : "session-progress.agent")} {" "}
            <Timing time={{
              ...timing[key]!,
              running: false,
              unconfirmed: timing[key]!.running || timing[key]!.unconfirmed,
            }} />
          </p>
        ) : null,
      )}
    </div>
  );
}
function Groups({
  operations,
  detailed = false,
  timing,
}: {
  operations: StartupOperations;
  detailed?: boolean;
  timing?: StartupTimes;
}) {
  const groups = [
    {
      key: "workspace" as const,
      title: "session-progress.workspace" as const,
      rows: operations.workspace,
      labels: workspaceLabels,
    },
    {
      key: "agent" as const,
      title: "session-progress.agent" as const,
      rows: operations.native,
      labels: nativeLabels,
    },
  ];
  return (
    <div className={`session-startup-groups${detailed ? " is-detailed" : ""}`}>
      {groups.map((group) => {
        const content = (
          <ol>
            {group.rows.map((row) => (
              <Step key={row.operation} row={row} labels={group.labels} />
            ))}
          </ol>
        );
        return detailed ? (
          <Disclosure
            key={group.key}
            density={DisclosureDensity.Settings}
            className="session-startup-group"
          >
            <DisclosureSummary>
              <span className="disclosure-label">
                <span className="session-startup-group-title">
                  {copy(group.title)}
                </span>
                <Timing time={timing?.[group.key]} />
              </span>
            </DisclosureSummary>
            {content}
          </Disclosure>
        ) : (
          <section key={group.key} aria-label={copy(group.title)}>
            <h4>{copy(group.title)}</h4>
            {content}
          </section>
        );
      })}
    </div>
  );
}
export function SessionProgressStatus({
  phase,
  compact,
  operations,
  timing,
}: {
  phase: SessionProgressPhase;
  compact: boolean;
  operations?: StartupOperations;
  timing?: StartupTimes;
}) {
  useLocale();
  const currentWorkspace = operations?.workspace.find(
      (row) => row.state === "running",
    ),
    currentNative = operations?.native.find((row) => row.state === "running");
  const current = currentNative ?? currentWorkspace;
  const label = current
    ? (currentNative ? nativeLabels : workspaceLabels)[current.operation]
    : labels[phase];
  return (
    <div
      className={`session-progress${compact ? " is-compact" : ""}${operations ? " has-operations" : ""}`}
    >
      <div
        className="session-startup-status"
        role="status"
        aria-live="polite"
        aria-atomic="true"
      >
        {!operations || compact ? (
          <span className="session-progress-spinner" aria-hidden="true" />
        ) : null}
        <span>
          {operations && !compact
            ? copy("session-progress.title")
            : copy(label)}
          {compact && current?.ordinal && current.count
            ? ` ${current.ordinal}/${current.count}`
            : ""}
        </span>
      </div>
      {operations ? (
        compact ? (
          <Disclosure
            density={DisclosureDensity.Compact}
            className="session-startup-disclosure"
          >
            <DisclosureSummary>
              {copy("session-progress.details")}
            </DisclosureSummary>
            <Groups operations={operations} />
          </Disclosure>
        ) : (
          <Groups operations={operations} detailed timing={timing} />
        )
      ) : null}
    </div>
  );
}

export function StartupObservedOperation({
  session,
}: {
  session: Resource | undefined;
}) {
  useLocale();
  const current = observedStartupOperation(session);
  if (!current) return null;
  return (
    <span>
      {copy("session-progress.observed")}:{" "}
      {copy(
        (current.native ? nativeLabels : workspaceLabels)[current.operation],
      )}
      {current.ordinal && current.count
        ? ` ${current.ordinal}/${current.count}`
        : ""}
    </span>
  );
}
