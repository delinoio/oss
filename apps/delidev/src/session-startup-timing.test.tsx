// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import {
  act,
  fireEvent,
  render,
  renderHook,
  screen,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import {
  EntityKind,
  ResourceSchema,
  SessionStartupTimingSchema,
  StartupGroupTimingSchema,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { startupTimingGroups, useStartupTimes } from "./session-startup-timing";
import { SessionProgressStatus } from "./session-progress-status";
import { SessionProgressPhase } from "./session-progress";
import type { ReactNode } from "react";

const sessionId = newRequestId(),
  workspaceJob = newRequestId(),
  agentJob = newRequestId(),
  execution = newRequestId(),
  machineId = newRequestId(),
  instanceId = newRequestId();
const transport = createRouterTransport(() => {});
const Wrapper = ({ children }: { children: ReactNode }) => (
  <TransportProvider transport={transport}>{children}</TransportProvider>
);
function fixture() {
  const session = create(ResourceSchema, {
    id: sessionId,
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 1n,
    documentJson: new TextEncoder().encode(
      JSON.stringify({
        machine_id: machineId,
        preparation: { job_id: workspaceJob },
        initial_execution: { id: execution },
        startup: { job_id: agentJob, execution_id: execution },
        startup_progress: {
          workspace: {
            job_id: workspaceJob,
            machine_id: machineId,
            instance_id: instanceId,
          },
          native: {
            job_id: agentJob,
            execution_id: execution,
            machine_id: machineId,
            instance_id: instanceId,
          },
        },
      }),
    ),
    startupTiming: {
      workspace: {
        jobId: workspaceJob,
        startedAt: "2026-10-12T00:00:00Z",
        finishedAt: "2026-10-12T00:00:12Z",
      },
      agent: {
        jobId: agentJob,
        executionId: execution,
        startedAt: "2026-10-12T00:00:12Z",
      },
    },
  });
  const machine = create(ResourceSchema, {
    id: machineId,
    kind: EntityKind.MACHINE,
    schemaVersion: 1,
    revision: 1n,
    documentJson: new TextEncoder().encode(
      JSON.stringify({
        disabled: false,
        last_seen: "2026-10-12T00:00:20Z",
        network: { instance_id: instanceId },
      }),
    ),
  });
  return {
    session,
    observation: {
      transport,
      resource: machine,
      observedAt: "2026-10-12T00:00:20Z",
      startedAt: 0,
      clockReceivedAt: 0,
    },
  };
}
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});
it("validates exact jobs/executions and UTC intervals without historical backfill", () => {
  const f = fixture();
  expect(startupTimingGroups(f.session).workspace?.end).toBe(
    Date.parse("2026-10-12T00:00:12Z"),
  );
  for (const change of [
    { jobId: newRequestId() },
    { executionId: newRequestId() },
    { startedAt: "not-a-time" },
    { startedAt: "2026-10-12T00:00:12+00:00" },
    { finishedAt: "2026-10-12T00:00:11.999999999Z" },
  ]) {
    const changed = create(ResourceSchema, {
      ...f.session,
      startupTiming: create(SessionStartupTimingSchema, {
        ...f.session.startupTiming!,
        agent: create(StartupGroupTimingSchema, {
          ...f.session.startupTiming!.agent!,
          ...change,
        }),
      }),
    });
    expect(startupTimingGroups(changed).agent).toBeUndefined();
  }
  expect(
    startupTimingGroups(
      create(ResourceSchema, { ...f.session, startupTiming: undefined }),
    ),
  ).toEqual({});
});
it("ticks closed groups from server time, freezes at acceptance, and stops on stale presence", () => {
  vi.useFakeTimers();
  let monotonic = 0;
  vi.spyOn(performance, "now").mockImplementation(() => monotonic);
  const f = fixture();
  const props = {
    session: f.session,
    observation: f.observation,
    enabled: true,
  };
  const hook = renderHook(
    ({ session, observation, enabled }) =>
      useStartupTimes(session, observation, enabled, "original"),
    { initialProps: props, wrapper: Wrapper },
  );
  expect(hook.result.current.workspace?.seconds).toBe(12);
  expect(hook.result.current.agent).toMatchObject({
    seconds: 8,
    running: true,
  });
  act(() => {
    monotonic = 1000;
    vi.advanceTimersByTime(1000);
  });
  expect(hook.result.current.agent?.seconds).toBe(9);
  act(() => hook.rerender({ ...props, enabled: false }));
  act(() => {
    monotonic = 5000;
    vi.advanceTimersByTime(4000);
  });
  expect(hook.result.current.agent).toMatchObject({
    seconds: 9,
    running: false,
    unconfirmed: true,
  });
  const finished = create(ResourceSchema, {
    ...f.session,
    startupTiming: create(SessionStartupTimingSchema, {
      ...f.session.startupTiming!,
      agent: create(StartupGroupTimingSchema, {
        ...f.session.startupTiming!.agent!,
        finishedAt: "2026-10-12T00:00:20Z",
      }),
    }),
  });
  act(() => hook.rerender({ ...props, session: finished }));
  expect(hook.result.current.agent).toMatchObject({
    seconds: 8,
    running: false,
    unconfirmed: false,
  });
  act(() => {
    monotonic = 65000;
    vi.advanceTimersByTime(60000);
  });
  expect(hook.result.current.agent?.seconds).toBe(8);
  hook.unmount();
  const stale = renderHook(
    () => useStartupTimes(f.session, f.observation, true, "original"),
    { wrapper: Wrapper },
  );
  expect(stale.result.current.agent).toMatchObject({
    seconds: undefined,
    running: false,
    unconfirmed: true,
  });
});
it("keeps detailed disclosures independent and timing outside the polite status", () => {
  const operations = {
    workspace: [{ operation: 1, state: "completed" as const }],
    native: [{ operation: 1, state: "running" as const }],
  };
  const rendered = render(
    <SessionProgressStatus
      phase={SessionProgressPhase.Starting}
      compact={false}
      operations={operations}
      timing={{
        workspace: { seconds: 12, running: false, unconfirmed: false },
        agent: { seconds: 8, running: true, unconfirmed: false },
      }}
    />,
  );
  const details = [...rendered.container.querySelectorAll("details")];
  expect(details).toHaveLength(2);
  expect(details.every((d) => !d.open)).toBe(true);
  expect(details[0].querySelector("summary")?.textContent).toContain("12s");
  expect(
    details[1].querySelector("summary .session-progress-spinner"),
  ).not.toBeNull();
  fireEvent.click(details[0].querySelector("summary")!);
  fireEvent.click(details[1].querySelector("summary")!);
  expect(details.every((d) => d.open)).toBe(true);
  fireEvent.click(details[0].querySelector("summary")!);
  expect(details[1].open).toBe(true);
  expect(screen.getByRole("status").textContent).not.toMatch(/12s|8s/);
  rendered.rerender(
    <SessionProgressStatus
      phase={SessionProgressPhase.Starting}
      compact
      operations={operations}
    />,
  );
  expect(rendered.container.querySelectorAll("details")).toHaveLength(1);
  expect(rendered.container.querySelectorAll("h4")).toHaveLength(2);
});
