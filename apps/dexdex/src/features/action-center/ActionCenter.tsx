import { createClient } from "@connectrpc/connect";
import { useQueryClient } from "@tanstack/react-query";
import { type FormEvent, useMemo, useState } from "react";
import {
  AgentCliType,
  PlanDecision,
  SessionAdapterFixturePreset,
  TaskService,
} from "../../gen/v1/dexdex_pb";
import type { DexDexPageDefinition } from "../../contracts/dexdex-page";
import { DexDexPageId } from "../../contracts/dexdex-page";
import type { SharedSelectionState } from "../../contracts/selection-state";
import type { ResolvedWorkspaceConnection } from "../../contracts/workspace-connection";
import { createDexDexTransport } from "../../lib/connect-query-provider";
import { describeConnectError } from "../../lib/connect-error";
import type { ActionCenterState } from "../../App";
import { ActionResultStatus } from "../../App";

type ActionCenterProps = {
  activePage: DexDexPageDefinition | null;
  workspaceId: string;
  connection: ResolvedWorkspaceConnection;
  selection: SharedSelectionState;
  actionState: ActionCenterState;
  onActionStateChange: (next: ActionCenterState) => void;
  onSelectionChange: (patch: Partial<SharedSelectionState>) => void;
};

export function ActionCenter({
  activePage,
  workspaceId,
  connection,
  selection,
  actionState,
  onActionStateChange,
  onSelectionChange,
}: ActionCenterProps) {
  const queryClient = useQueryClient();
  const transport = useMemo(
    () => createDexDexTransport(connection.endpointUrl, connection.token),
    [connection.endpointUrl, connection.token],
  );
  const taskClient = useMemo(() => createClient(TaskService, transport), [transport]);

  const [planDecision, setPlanDecision] = useState<PlanDecision>(PlanDecision.APPROVE);
  const [planRevisionNote, setPlanRevisionNote] = useState("");
  const [runCliType, setRunCliType] = useState<AgentCliType>(AgentCliType.CODEX_CLI);
  const [runFixturePreset, setRunFixturePreset] = useState<SessionAdapterFixturePreset>(
    SessionAdapterFixturePreset.CODEX_CLI_FAILURE,
  );
  const [runRawJsonlInput, setRunRawJsonlInput] = useState('{"type":"text","part":{"text":"hello"}}');
  const [runInputMode, setRunInputMode] = useState<"preset" | "raw">("preset");

  const isThreadActionPage = activePage?.id === DexDexPageId.Threads;

  const statusVariant =
    actionState.status === ActionResultStatus.Success
      ? "resolved"
      : actionState.status === ActionResultStatus.Error
        ? "error"
        : actionState.status === ActionResultStatus.Pending
          ? "resolving"
          : "idle";

  async function handleSubmitPlanDecision(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!selection.selectedSubTaskId) {
      onActionStateChange({
        label: "Plan Decision",
        status: ActionResultStatus.Error,
        message: "Select a sub task first.",
      });
      return;
    }
    if (planDecision === PlanDecision.REVISE && planRevisionNote.trim().length === 0) {
      onActionStateChange({
        label: "Plan Decision",
        status: ActionResultStatus.Error,
        message: "Revision note required.",
      });
      return;
    }

    onActionStateChange({
      label: "Plan Decision",
      status: ActionResultStatus.Pending,
      message: "Submitting...",
    });

    try {
      const response = await taskClient.submitPlanDecision({
        workspaceId,
        subTaskId: selection.selectedSubTaskId,
        decision: planDecision,
        revisionNote: planDecision === PlanDecision.REVISE ? planRevisionNote : "",
      });
      onSelectionChange({
        selectedSubTaskId:
          response.createdSubTask?.subTaskId ??
          response.updatedSubTask?.subTaskId ??
          selection.selectedSubTaskId,
        selectedUnitTaskId: response.updatedSubTask?.unitTaskId ?? selection.selectedUnitTaskId,
      });
      await queryClient.invalidateQueries();
      onActionStateChange({
        label: "Plan Decision",
        status: ActionResultStatus.Success,
        message: "Decision submitted.",
      });
    } catch (error) {
      onActionStateChange({
        label: "Plan Decision",
        status: ActionResultStatus.Error,
        message: describeConnectError(error, "Failed."),
      });
    }
  }

  async function handleRunSessionAdapter(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (
      !selection.selectedUnitTaskId ||
      !selection.selectedSubTaskId ||
      !selection.selectedSessionId
    ) {
      onActionStateChange({
        label: "Session Adapter",
        status: ActionResultStatus.Error,
        message: "Select unit task, sub task, and session.",
      });
      return;
    }
    if (runInputMode === "raw" && runRawJsonlInput.trim().length === 0) {
      onActionStateChange({
        label: "Session Adapter",
        status: ActionResultStatus.Error,
        message: "Raw JSONL required.",
      });
      return;
    }

    onActionStateChange({
      label: "Session Adapter",
      status: ActionResultStatus.Pending,
      message: "Running...",
    });

    try {
      await taskClient.runSubTaskSessionAdapter({
        workspaceId,
        unitTaskId: selection.selectedUnitTaskId,
        subTaskId: selection.selectedSubTaskId,
        sessionId: selection.selectedSessionId,
        cliType: runCliType,
        input:
          runInputMode === "preset"
            ? { case: "fixturePreset", value: runFixturePreset }
            : { case: "rawJsonl", value: runRawJsonlInput },
      });
      await queryClient.invalidateQueries();
      onActionStateChange({
        label: "Session Adapter",
        status: ActionResultStatus.Success,
        message: "Completed.",
      });
    } catch (error) {
      onActionStateChange({
        label: "Session Adapter",
        status: ActionResultStatus.Error,
        message: describeConnectError(error, "Failed."),
      });
    }
  }

  return (
    <aside className="right-panel" aria-label="Action center">
      <section className="right-panel-section">
        <h3 className="right-panel-title">Status</h3>
        <div className="inline-gap">
          <span className={`topbar-status topbar-status-${statusVariant}`} />
          <span>{actionState.label}</span>
        </div>
        <p className="text-muted text-sm mt-2">{actionState.message}</p>
      </section>

      <section className="right-panel-section">
        <h3 className="right-panel-title">Selection</h3>
        <div className="kv-grid">
          <span className="kv-key">Unit task</span>
          <span className="kv-value">{selection.selectedUnitTaskId ?? "—"}</span>
          <span className="kv-key">Sub task</span>
          <span className="kv-value">{selection.selectedSubTaskId ?? "—"}</span>
          <span className="kv-key">Session</span>
          <span className="kv-value">{selection.selectedSessionId ?? "—"}</span>
          <span className="kv-key">PR</span>
          <span className="kv-value">{selection.selectedPrTrackingId ?? "—"}</span>
        </div>
      </section>

      {isThreadActionPage ? (
        <section className="right-panel-section">
          <h3 className="right-panel-title">Plan Decision</h3>
          <form onSubmit={handleSubmitPlanDecision} className="form-stack">
            <div className="form-group">
              <label className="form-label" htmlFor="rp-plan-decision">Decision</label>
              <select
                id="rp-plan-decision"
                className="form-select"
                value={planDecision}
                onChange={(event) => setPlanDecision(Number(event.target.value) as PlanDecision)}
              >
                <option value={PlanDecision.APPROVE}>APPROVE</option>
                <option value={PlanDecision.REVISE}>REVISE</option>
                <option value={PlanDecision.REJECT}>REJECT</option>
              </select>
            </div>
            {planDecision === PlanDecision.REVISE ? (
              <div className="form-group">
                <label className="form-label" htmlFor="rp-revision-note">Revision Note</label>
                <textarea
                  id="rp-revision-note"
                  className="form-textarea"
                  value={planRevisionNote}
                  onChange={(event) => setPlanRevisionNote(event.target.value)}
                  rows={3}
                />
              </div>
            ) : null}
            <div className="form-actions">
              <button type="submit" className="btn btn-primary btn-sm">Submit</button>
            </div>
          </form>
        </section>
      ) : null}

      {isThreadActionPage ? (
        <section className="right-panel-section">
          <h3 className="right-panel-title">Session Adapter</h3>
          <form onSubmit={handleRunSessionAdapter} className="form-stack">
            <div className="form-group">
              <label className="form-label" htmlFor="rp-cli-type">CLI Type</label>
              <select
                id="rp-cli-type"
                className="form-select"
                value={runCliType}
                onChange={(event) => setRunCliType(Number(event.target.value) as AgentCliType)}
              >
                <option value={AgentCliType.CODEX_CLI}>CODEX_CLI</option>
                <option value={AgentCliType.CLAUDE_CODE}>CLAUDE_CODE</option>
                <option value={AgentCliType.OPENCODE}>OPENCODE</option>
              </select>
            </div>
            <div className="form-group">
              <label className="form-label" htmlFor="rp-input-mode">Input Mode</label>
              <select
                id="rp-input-mode"
                className="form-select"
                value={runInputMode}
                onChange={(event) => setRunInputMode(event.target.value as "preset" | "raw")}
              >
                <option value="preset">Preset Fixture</option>
                <option value="raw">Raw JSONL</option>
              </select>
            </div>
            {runInputMode === "preset" ? (
              <div className="form-group">
                <label className="form-label" htmlFor="rp-fixture">Fixture</label>
                <select
                  id="rp-fixture"
                  className="form-select"
                  value={runFixturePreset}
                  onChange={(event) =>
                    setRunFixturePreset(Number(event.target.value) as SessionAdapterFixturePreset)
                  }
                >
                  <option value={SessionAdapterFixturePreset.CODEX_CLI_FAILURE}>CODEX_CLI_FAILURE</option>
                  <option value={SessionAdapterFixturePreset.CLAUDE_CODE_STREAM}>CLAUDE_CODE_STREAM</option>
                  <option value={SessionAdapterFixturePreset.OPENCODE_RUN}>OPENCODE_RUN</option>
                </select>
              </div>
            ) : (
              <div className="form-group">
                <label className="form-label" htmlFor="rp-raw-jsonl">Raw JSONL</label>
                <textarea
                  id="rp-raw-jsonl"
                  className="form-textarea"
                  value={runRawJsonlInput}
                  onChange={(event) => setRunRawJsonlInput(event.target.value)}
                  rows={4}
                />
              </div>
            )}
            <div className="form-actions">
              <button type="submit" className="btn btn-primary btn-sm">Run</button>
            </div>
          </form>
        </section>
      ) : null}

      <section className="right-panel-section">
        <h3 className="right-panel-title">Connection</h3>
        <div className="kv-grid">
          <span className="kv-key">Workspace</span>
          <span className="kv-value">{workspaceId}</span>
          <span className="kv-key">Mode</span>
          <span className="kv-value">{connection.mode}</span>
          <span className="kv-key">Endpoint</span>
          <span className="kv-value">{connection.endpointUrl}</span>
          <span className="kv-key">Source</span>
          <span className="kv-value">{connection.endpointSource}</span>
        </div>
      </section>
    </aside>
  );
}
