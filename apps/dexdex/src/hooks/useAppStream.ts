import { createClient, type Transport } from "@connectrpc/connect";
import { useTransport } from "@connectrpc/connect-query";
import { createQueryOptions } from "@connectrpc/connect-query-core";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";
import {
  AgentCliType,
  AgentSessionStatus,
  EventStreamService,
  SessionOutputKind,
  SubTaskStatus,
  type ListSessionsResponse,
  type ListSubTasksResponse,
  type SessionSummary,
  type StreamWorkspaceEventsResponse,
} from "../gen/v1/dexdex_pb";
import {
  getSessionOutput,
  listSessions,
} from "../gen/v1/dexdex-SessionService_connectquery";
import { listSubTasks } from "../gen/v1/dexdex-TaskService_connectquery";
import { describeConnectError } from "../lib/connect-error";
import type { DexDexLogger } from "../lib/logger";

const defaultListPageSize = 50;
const maxStreamEvents = 120;

function applyStreamEventToCaches(
  event: StreamWorkspaceEventsResponse,
  workspaceId: string,
  transport: Transport,
  queryClient: ReturnType<typeof useQueryClient>,
): void {
  const listSessionsInput = {
    workspaceId,
    status: AgentSessionStatus.UNSPECIFIED,
    cliType: AgentCliType.UNSPECIFIED,
    pageSize: defaultListPageSize,
    pageToken: "",
  };
  const listSubTasksInput = {
    workspaceId,
    unitTaskId: "",
    status: SubTaskStatus.UNSPECIFIED,
    pageSize: defaultListPageSize,
    pageToken: "",
  };

  const listSessionKey = createQueryOptions(listSessions, listSessionsInput, { transport }).queryKey;
  const listSubTaskKey = createQueryOptions(listSubTasks, listSubTasksInput, { transport }).queryKey;

  if (event.payload.case === "sessionOutput") {
    const sessionOutput = event.payload.value;
    queryClient.setQueryData<ListSessionsResponse>(listSessionKey, (previous) => {
      if (!previous) return previous;
      const existingIndex = previous.items.findIndex(
        (item) => item.sessionId === sessionOutput.sessionId,
      );
      const existing =
        existingIndex >= 0
          ? previous.items[existingIndex]
          : ({
              sessionId: sessionOutput.sessionId,
              status: AgentSessionStatus.UNSPECIFIED,
              cliType: AgentCliType.UNSPECIFIED,
              lastOutputKind: SessionOutputKind.UNSPECIFIED,
              updatedAt: undefined,
            } as unknown as SessionSummary);
      const nextStatus = sessionOutput.isTerminal
        ? sessionOutput.kind === SessionOutputKind.ERROR
          ? AgentSessionStatus.FAILED
          : AgentSessionStatus.COMPLETED
        : existing.status;
      const updated: SessionSummary = {
        ...existing,
        sessionId: sessionOutput.sessionId,
        status: nextStatus,
        cliType: sessionOutput.source?.cliType ?? existing.cliType,
        lastOutputKind: sessionOutput.kind,
        updatedAt: event.occurredAt,
      };
      const nextItems = [...previous.items];
      if (existingIndex >= 0) {
        nextItems[existingIndex] = updated;
      } else {
        nextItems.unshift(updated);
      }
      return { ...previous, items: nextItems };
    });
  }

  if (event.payload.case === "sessionStateChanged") {
    const changed = event.payload.value;
    queryClient.setQueryData<ListSessionsResponse>(listSessionKey, (previous) => {
      if (!previous) return previous;
      const existingIndex = previous.items.findIndex(
        (item) => item.sessionId === changed.sessionId,
      );
      const existing =
        existingIndex >= 0
          ? previous.items[existingIndex]
          : ({
              sessionId: changed.sessionId,
              status: AgentSessionStatus.UNSPECIFIED,
              cliType: AgentCliType.UNSPECIFIED,
              lastOutputKind: SessionOutputKind.UNSPECIFIED,
              updatedAt: undefined,
            } as unknown as SessionSummary);
      const updated: SessionSummary = {
        ...existing,
        status: changed.status,
        updatedAt: event.occurredAt,
      };
      const nextItems = [...previous.items];
      if (existingIndex >= 0) {
        nextItems[existingIndex] = updated;
      } else {
        nextItems.unshift(updated);
      }
      return { ...previous, items: nextItems };
    });
  }

  if (event.payload.case === "subTask") {
    const updatedSubTask = event.payload.value;
    queryClient.setQueryData<ListSubTasksResponse>(listSubTaskKey, (previous) => {
      if (!previous) return previous;
      const existingIndex = previous.items.findIndex(
        (item) => item.subTaskId === updatedSubTask.subTaskId,
      );
      const nextItems = [...previous.items];
      if (existingIndex >= 0) {
        nextItems[existingIndex] = updatedSubTask;
      } else {
        nextItems.unshift(updatedSubTask);
      }
      return { ...previous, items: nextItems };
    });
  }
}

export type StreamStatus = "idle" | "running" | "stopped" | "error";

export type UseAppStreamReturn = {
  streamStatus: StreamStatus;
  streamError: string | null;
  streamEvents: StreamWorkspaceEventsResponse[];
  startStream: () => Promise<void>;
  stopStream: () => void;
};

export function useAppStream(
  workspaceId: string,
  logger: DexDexLogger,
  visualMode: boolean,
  visualEvents: StreamWorkspaceEventsResponse[],
): UseAppStreamReturn {
  const queryClient = useQueryClient();
  const transport = useTransport();
  const eventStreamClient = useMemo(
    () => createClient(EventStreamService, transport),
    [transport],
  );

  const [streamStatus, setStreamStatus] = useState<StreamStatus>(
    visualMode ? "running" : "idle",
  );
  const [streamError, setStreamError] = useState<string | null>(null);
  const [streamEvents, setStreamEvents] = useState<StreamWorkspaceEventsResponse[]>(
    visualMode ? visualEvents : [],
  );
  const streamAbortControllerRef = useRef<AbortController | null>(null);

  useEffect(() => {
    return () => {
      streamAbortControllerRef.current?.abort();
      streamAbortControllerRef.current = null;
    };
  }, []);

  async function startStream() {
    if (visualMode) {
      setStreamStatus("running");
      setStreamError(null);
      setStreamEvents(visualEvents);
      return;
    }

    streamAbortControllerRef.current?.abort();
    const abortController = new AbortController();
    streamAbortControllerRef.current = abortController;
    setStreamStatus("running");
    setStreamError(null);

    logger.info("stream.start", { workspace_id: workspaceId, result: "pending" });

    try {
      for await (const event of eventStreamClient.streamWorkspaceEvents(
        { workspaceId, fromSequence: 0n },
        { signal: abortController.signal },
      )) {
        if (event.sequence === 0n) continue;
        setStreamEvents((previous) => [event, ...previous].slice(0, maxStreamEvents));
        applyStreamEventToCaches(event, workspaceId, transport, queryClient);
      }
      if (!abortController.signal.aborted) {
        setStreamStatus("stopped");
      }
    } catch (error) {
      if (abortController.signal.aborted) return;
      setStreamStatus("error");
      setStreamError(describeConnectError(error, "Live stream failed."));
    } finally {
      if (streamAbortControllerRef.current === abortController) {
        streamAbortControllerRef.current = null;
      }
    }
  }

  function stopStream() {
    if (visualMode) {
      setStreamStatus("stopped");
      return;
    }
    streamAbortControllerRef.current?.abort();
    streamAbortControllerRef.current = null;
    setStreamStatus("stopped");
  }

  return { streamStatus, streamError, streamEvents, startStream, stopStream };
}
