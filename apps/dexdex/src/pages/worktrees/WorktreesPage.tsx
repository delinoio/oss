import { useQuery } from "@connectrpc/connect-query";
import { useEffect } from "react";
import { listSessions } from "../../gen/v1/dexdex-SessionService_connectquery";
import {
  AgentCliType,
  AgentSessionStatus,
  StreamEventType,
} from "../../gen/v1/dexdex_pb";
import type { SharedSelectionState } from "../../contracts/selection-state";
import { visualSessions, visualStreamEvents } from "../../lib/visual-fixtures";
import { stringifyForUi } from "../../lib/safe-json";
import { sessionDotClass } from "../../components/ui/StatusDot";
import { useAppStream } from "../../hooks/useAppStream";
import type { DexDexLogger } from "../../lib/logger";

const defaultListPageSize = 50;

function enumLabel<T extends Record<string, string | number>>(enumType: T, value: number): string {
  const maybeLabel = enumType[value as unknown as keyof T];
  return typeof maybeLabel === "string" ? maybeLabel : "UNSPECIFIED";
}

type WorktreesPageProps = {
  workspaceId: string;
  selection: SharedSelectionState;
  onSelectionChange: (patch: Partial<SharedSelectionState>) => void;
  logger: DexDexLogger;
  visualMode: boolean;
};

export function WorktreesPage({
  workspaceId,
  selection,
  onSelectionChange,
  logger,
  visualMode,
}: WorktreesPageProps) {
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

  const sessions = visualMode ? visualSessions : (sessionListQuery.data?.items ?? []);

  const { streamStatus, streamError, streamEvents, startStream, stopStream } = useAppStream(
    workspaceId,
    logger,
    visualMode,
    visualStreamEvents,
  );

  useEffect(() => {
    if (selection.selectedSessionId || sessions.length === 0) return;
    onSelectionChange({ selectedSessionId: sessions[0].sessionId });
  }, [onSelectionChange, selection.selectedSessionId, sessions]);

  return (
    <div className="content-split">
      <section className="content-list-pane">
        <div className="section-label">Sessions</div>
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
          <p className="empty-state">No sessions available.</p>
        )}
      </section>

      <section className="content-detail-pane">
        <div className="panel">
          <header className="panel-header">Event Timeline</header>
          <div className="panel-body">
            <div className="toolbar-row">
              <button
                type="button"
                className="btn btn-primary btn-sm"
                onClick={() => void startStream()}
                disabled={streamStatus === "running"}
              >
                {streamStatus === "running" ? "Streaming..." : "Start Stream"}
              </button>
              <button
                type="button"
                className="btn btn-secondary btn-sm"
                onClick={stopStream}
                disabled={streamStatus !== "running"}
              >
                Stop
              </button>
              <span className="status-pill">{streamStatus.toUpperCase()}</span>
            </div>

            {streamError ? <p className="error-inline">{streamError}</p> : null}

            {streamEvents.length > 0 ? (
              <div className="stream-list mt-3">
                {streamEvents.map((event) => (
                  <article
                    key={`${event.sequence.toString()}-${event.eventType}`}
                    className="stream-item"
                  >
                    <header className="stream-item-header">
                      <span>#{event.sequence.toString()}</span>
                      <span>{enumLabel(StreamEventType, event.eventType)}</span>
                    </header>
                    <pre className="stream-item-body">{stringifyForUi(event)}</pre>
                  </article>
                ))}
              </div>
            ) : (
              <p className="empty-state">No stream events yet.</p>
            )}
          </div>
        </div>
      </section>
    </div>
  );
}
