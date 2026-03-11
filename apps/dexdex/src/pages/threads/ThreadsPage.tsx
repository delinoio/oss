import { useQuery } from "@connectrpc/connect-query";
import { useEffect } from "react";
import { getSessionOutput, listSessions } from "../../gen/v1/dexdex-SessionService_connectquery";
import { getSubTask, listSubTasks } from "../../gen/v1/dexdex-TaskService_connectquery";
import {
  AgentCliType,
  AgentSessionStatus,
  SessionOutputKind,
  SubTaskStatus,
} from "../../gen/v1/dexdex_pb";
import type { SharedSelectionState } from "../../contracts/selection-state";
import {
  visualSessionOutputEvents,
  visualSessions,
  visualSubTasks,
} from "../../lib/visual-fixtures";
import { subTaskDotClass, sessionDotClass } from "../../components/ui/StatusDot";

const defaultListPageSize = 50;

function enumLabel<T extends Record<string, string | number>>(enumType: T, value: number): string {
  const maybeLabel = enumType[value as unknown as keyof T];
  return typeof maybeLabel === "string" ? maybeLabel : "UNSPECIFIED";
}

type ThreadsPageProps = {
  workspaceId: string;
  selection: SharedSelectionState;
  onSelectionChange: (patch: Partial<SharedSelectionState>) => void;
  visualMode: boolean;
};

export function ThreadsPage({ workspaceId, selection, onSelectionChange, visualMode }: ThreadsPageProps) {
  const subTasksQuery = useQuery(
    listSubTasks,
    {
      workspaceId,
      unitTaskId: selection.selectedUnitTaskId ?? "",
      status: SubTaskStatus.UNSPECIFIED,
      pageSize: defaultListPageSize,
      pageToken: "",
    },
    { enabled: !visualMode && selection.selectedUnitTaskId !== null },
  );

  const sessionListQuery = useQuery(
    listSessions,
    {
      workspaceId,
      status: AgentSessionStatus.UNSPECIFIED,
      cliType: AgentCliType.UNSPECIFIED,
      pageSize: defaultListPageSize,
      pageToken: "",
    },
    { enabled: !visualMode },
  );

  const selectedSubTaskQuery = useQuery(
    getSubTask,
    selection.selectedSubTaskId
      ? { workspaceId, subTaskId: selection.selectedSubTaskId }
      : undefined,
    { enabled: !visualMode && selection.selectedSubTaskId !== null },
  );

  const selectedSessionOutputQuery = useQuery(
    getSessionOutput,
    selection.selectedSessionId
      ? { workspaceId, sessionId: selection.selectedSessionId }
      : undefined,
    { enabled: !visualMode && selection.selectedSessionId !== null },
  );

  const subTasks = visualMode
    ? visualSubTasks.filter((subTask) =>
        selection.selectedUnitTaskId
          ? subTask.unitTaskId === selection.selectedUnitTaskId
          : true,
      )
    : (subTasksQuery.data?.items ?? []);
  const sessions = visualMode ? visualSessions : (sessionListQuery.data?.items ?? []);
  const selectedSubTask = visualMode
    ? visualSubTasks.find((item) => item.subTaskId === selection.selectedSubTaskId)
    : selectedSubTaskQuery.data?.subTask;
  const selectedSessionEvents = visualMode
    ? visualSessionOutputEvents.filter((event) =>
        selection.selectedSessionId ? event.sessionId === selection.selectedSessionId : true,
      )
    : selectedSessionOutputQuery.data?.events ?? [];

  useEffect(() => {
    if (selection.selectedSubTaskId || subTasks.length === 0) return;
    onSelectionChange({ selectedSubTaskId: subTasks[0].subTaskId });
  }, [onSelectionChange, selection.selectedSubTaskId, subTasks]);

  useEffect(() => {
    if (selection.selectedSessionId || sessions.length === 0) return;
    onSelectionChange({ selectedSessionId: sessions[0].sessionId });
  }, [onSelectionChange, selection.selectedSessionId, sessions]);

  return (
    <div className="content-split">
      <section className="content-list-pane">
        <div className="section-label">Inbox</div>
        {selection.selectedUnitTaskId ? null : (
          <p className="empty-state">Select a unit task in the left sidebar.</p>
        )}
        {selection.selectedUnitTaskId && subTasks.length === 0 ? (
          subTasksQuery.isPending ? (
            <p className="text-muted text-sm">Loading sub tasks...</p>
          ) : (
            <p className="empty-state">No sub tasks for this task.</p>
          )
        ) : null}
        {subTasks.length > 0 ? (
          <ul className="item-list">
            {subTasks.map((subTask) => (
              <li key={subTask.subTaskId}>
                <button
                  type="button"
                  className={`item-row ${selection.selectedSubTaskId === subTask.subTaskId ? "item-row-active" : ""}`}
                  onClick={() =>
                    onSelectionChange({
                      selectedSubTaskId: subTask.subTaskId,
                      selectedUnitTaskId: subTask.unitTaskId,
                    })
                  }
                >
                  <span className={`item-row-dot ${subTaskDotClass(subTask.status)}`} />
                  <span className="item-row-body">
                    <span className="item-row-title">{subTask.subTaskId}</span>
                    <span className="item-row-sub">{enumLabel(SubTaskStatus, subTask.status)}</span>
                  </span>
                </button>
              </li>
            ))}
          </ul>
        ) : null}
      </section>

      <section className="content-detail-pane">
        <div className="panel">
          <header className="panel-header">Thread Detail</header>
          <div className="panel-body">
            {selectedSubTask ? (
              <div className="kv-grid">
                <span className="kv-key">Sub task</span>
                <span className="kv-value">{selectedSubTask.subTaskId}</span>
                <span className="kv-key">Unit task</span>
                <span className="kv-value">{selectedSubTask.unitTaskId}</span>
                <span className="kv-key">Type</span>
                <span className="kv-value">{enumLabel(SubTaskStatus, selectedSubTask.status)}</span>
              </div>
            ) : (
              <p className="empty-state">Select a sub task to inspect details.</p>
            )}
          </div>
        </div>

        <div className="panel">
          <header className="panel-header">Timeline</header>
          <div className="panel-body">
            {sessions.length > 0 ? (
              <ul className="item-list">
                {sessions.map((session) => (
                  <li key={session.sessionId}>
                    <button
                      type="button"
                      className={`item-row ${selection.selectedSessionId === session.sessionId ? "item-row-active" : ""}`}
                      onClick={() => onSelectionChange({ selectedSessionId: session.sessionId })}
                    >
                      <span className={`item-row-dot ${sessionDotClass(session.status)}`} />
                      <span className="item-row-body">
                        <span className="item-row-title">{session.sessionId}</span>
                        <span className="item-row-sub">{enumLabel(AgentSessionStatus, session.status)}</span>
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
            ) : sessionListQuery.isPending ? (
              <p className="text-muted text-sm">Loading sessions...</p>
            ) : (
              <p className="empty-state">No sessions found.</p>
            )}

            {selectedSessionEvents.length > 0 ? (
              <div className="stream-list mt-4">
                {selectedSessionEvents.map((event, index) => (
                  <article key={`${event.sessionId}-${index}`} className="stream-item">
                    <header className="stream-item-header">
                      <span>{enumLabel(SessionOutputKind, event.kind)}</span>
                      <span>{event.isTerminal ? "terminal" : "active"}</span>
                    </header>
                    <pre className="stream-item-body">{event.body}</pre>
                  </article>
                ))}
              </div>
            ) : null}
          </div>
        </div>
      </section>
    </div>
  );
}
