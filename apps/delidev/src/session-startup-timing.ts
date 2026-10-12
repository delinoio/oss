// SPDX-License-Identifier: Apache-2.0
import type { Transport } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import { useLayoutEffect, useRef, useState } from "react";
import type {
  Resource,
  StartupGroupTiming,
} from "@delinoio/delidev-api-client";
import { document as readDocument, object } from "./documents";
import { retainedTurnTiming } from "./turn-timing";
import { startupWorkerCurrent } from "./session-startup-operations";

export interface StartupTime {
  seconds?: number;
  running: boolean;
  unconfirmed: boolean;
}
export interface StartupTimes {
  workspace?: StartupTime;
  agent?: StartupTime;
}
export interface StartupClockObservation {
  transport: Transport;
  resource?: Resource;
  observedAt: string;
  startedAt: number;
  clockReceivedAt: number;
}
interface Group {
  owner: string;
  start: number;
  end?: number;
}
const uuid = (value: unknown) =>
  typeof value === "string" &&
  /^[a-f0-9]{8}-[a-f0-9]{4}-7[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/.test(
    value,
  );

/** Exact original attempt only; entity, turn and job timestamps are never backfill. */
export function startupTimingGroups(session?: Resource): {
  workspace?: Group;
  agent?: Group;
} {
  if (!session?.startupTiming) return {};
  const d = readDocument(session),
    progress = object(d.startup_progress),
    preparation = object(d.preparation),
    selected = object(d.current_execution ?? d.initial_execution),
    active = object(d.execution),
    startup = object(d.startup);
  function group(
    t: StartupGroupTiming | undefined,
    native: boolean,
  ): Group | undefined {
    if (!t) return;
    const attempt = object(native ? progress.native : progress.workspace);
    const execution = native ? selected.id : "";
    const job = native
      ? active.execution_id === execution
        ? active.job_id
        : startup.execution_id === execution
          ? startup.job_id
          : attempt.job_id
      : preparation.job_id;
    if (
      !uuid(job) ||
      t.jobId !== job ||
      attempt.job_id !== job ||
      t.executionId !== execution ||
      (native && (!uuid(execution) || attempt.execution_id !== execution))
    )
      return;
    const timing = retainedTurnTiming({
      accepted_at: t.startedAt,
      ...(t.finishedAt ? { terminal_at: t.finishedAt } : {}),
    });
    if (!timing) return;
    return {
      owner: JSON.stringify([session!.id, job, execution, t.startedAt]),
      start: timing.accepted,
      end: timing.terminal,
    };
  }
  return {
    workspace: group(session.startupTiming.workspace, false),
    agent: group(session.startupTiming.agent, true),
  };
}
interface Frame {
  owner: string;
  elapsed: number;
}

/** One monotonic timer uses the existing authenticated Machine/Overview pair. */
export function useStartupTimes(
  session: Resource | undefined,
  observation: StartupClockObservation | undefined,
  enabled: boolean,
  connectionOwner: string,
): StartupTimes {
  const frames = useRef<{ workspace?: Frame; agent?: Frame }>({});
  const transport = useTransport();
  const connection = useRef({ owner: connectionOwner, transport });
  const [, redraw] = useState(0);
  const groups = startupTimingGroups(session);
  const [visible, setVisible] = useState(
    () => document.visibilityState !== "hidden",
  );
  useLayoutEffect(() => {
    const changed = () => setVisible(document.visibilityState !== "hidden");
    document.addEventListener("visibilitychange", changed);
    return () => document.removeEventListener("visibilitychange", changed);
  }, []);
  const current = () =>
    enabled &&
    visible &&
    observation?.transport === transport &&
    startupWorkerCurrent(
      session,
      observation?.resource,
      true,
      observation?.observedAt,
      observation ? performance.now() - observation.startedAt : NaN,
    );
  useLayoutEffect(() => {
    if (
      connection.current.owner !== connectionOwner ||
      connection.current.transport !== transport
    ) {
      // Retain the last exact-owner value across connection replacement,
      // but only a new pair from that transport can resume its clock.
      if (connection.current.owner !== connectionOwner) frames.current = {};
      connection.current = { owner: connectionOwner, transport };
    }
    if (!current() || !observation) {
      redraw((v) => v + 1);
      return;
    }
    const clock = retainedTurnTiming({ accepted_at: observation.observedAt });
    if (!clock) return;
    let hasRunning = false;
    for (const key of ["workspace", "agent"] as const) {
      const group = groups[key];
      if (!group || group.end !== undefined || clock.accepted < group.start)
        continue;
      const elapsed =
        clock.accepted -
        group.start +
        Math.max(0, performance.now() - observation.clockReceivedAt);
      if (!Number.isFinite(elapsed) || elapsed > Number.MAX_SAFE_INTEGER)
        continue;
      const prior = frames.current[key];
      frames.current[key] = {
        owner: group.owner,
        elapsed:
          prior?.owner === group.owner
            ? Math.max(prior.elapsed, elapsed)
            : elapsed,
      };
      hasRunning = true;
    }
    redraw((v) => v + 1);
    if (!hasRunning) return;
    let previous = performance.now();
    const timer = setInterval(() => {
      const now = performance.now(),
        delta = now - previous;
      previous = now;
      if (!current()) {
        clearInterval(timer);
        redraw((v) => v + 1);
        return;
      }
      if (Number.isFinite(delta) && delta >= 0) {
        for (const key of ["workspace", "agent"] as const) {
          const group = groups[key],
            frame = frames.current[key];
          if (
            group &&
            group.end === undefined &&
            frame?.owner === group.owner &&
            frame.elapsed + delta <= Number.MAX_SAFE_INTEGER
          )
            frame.elapsed += delta;
        }
      }
      redraw((v) => v + 1);
    }, 1000);
    return () => clearInterval(timer);
  }, [
    transport,
    connectionOwner,
    enabled,
    visible,
    observation,
    groups.workspace?.owner,
    groups.workspace?.end,
    groups.agent?.owner,
    groups.agent?.end,
  ]);
  function display(key: "workspace" | "agent"): StartupTime | undefined {
    const group = groups[key];
    if (!group || connection.current.owner !== connectionOwner) return;
    if (group.end !== undefined)
      return {
        seconds: Math.floor((group.end - group.start) / 1000),
        running: false,
        unconfirmed: false,
      };
    const frame = frames.current[key],
      confirmed = current();
    return {
      seconds:
        frame?.owner === group.owner
          ? Math.floor(frame.elapsed / 1000)
          : undefined,
      running: confirmed && frame?.owner === group.owner,
      unconfirmed: !confirmed,
    };
  }
  return { workspace: display("workspace"), agent: display("agent") };
}
