import {
  createContext,
  use,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactNode,
} from "react";

import { CaptureMode, PointerInclusion } from "../capture";
import {
  MAX_FINAL_BODY_UTF8_BYTES,
  MAX_ISSUE_TITLE_UTF8_BYTES,
  PUBLIC_SCREENSHOT_WARNING,
} from "../onlineSubmission";
import { sanitizeCapturedUrl } from "../drafts/url";
import { Dialog } from "../../ui/Dialog";
import {
  subscribeToPersistenceReset,
  subscribeToSessionInvalidation,
  subscribeToSessionReauthentication,
} from "../../runtime/theme";
import { shortcutFromKeyboardInput, shortcutLabel } from "../../ui/SettingsPanel";
import {
  RealQaAccessMode,
  RealQaFailureCode,
  RealQaProductError,
  RealQaSelectorMode,
  realQaFailureGuidance,
  type RealQaDraft,
  type RealQaIssueField,
  type RealQaPreset,
  type RealQaProductAction,
  type RealQaProductGateway,
  type RealQaProductSnapshot,
} from "./contracts";
import { createUuidV7 } from "./uuid";
import {
  MAX_REALQA_PROCESS_URL_RULES,
  validateRealQaProcessUrlRules,
  type RealQaProcessUrlRule,
} from "../drafts/presets";

interface WorkspaceContextValue {
  readonly snapshot: RealQaProductSnapshot;
  readonly selectedDraft: RealQaDraft | null;
  readonly selectedDraftId: string | null;
  readonly busy: boolean;
  readonly progress: number;
  readonly failure: RealQaFailureCode | null;
  readonly status: string;
  readonly selectDraft: (draftId: string) => void;
  readonly replaceDraft: (draft: RealQaDraft) => void;
  readonly execute: (
    action: RealQaProductAction,
    success: string,
  ) => Promise<RealQaProductSnapshot | null>;
  readonly clearFailure: () => void;
}

const WorkspaceContext = createContext<WorkspaceContextValue | null>(null);

function useWorkspace(): WorkspaceContextValue {
  const value = use(WorkspaceContext);
  if (value === null) throw new Error("RealQA components require RealQaWorkspace.Provider.");
  return value;
}

function safeFailure(error: unknown): RealQaFailureCode {
  return error instanceof RealQaProductError
    ? error.code
    : RealQaFailureCode.ServiceUnavailable;
}

function RealQaWorkspaceProvider({
  children,
  gateway,
}: {
  readonly children: ReactNode;
  readonly gateway: RealQaProductGateway;
}) {
  const [snapshot, setSnapshot] = useState<RealQaProductSnapshot | null>(null);
  const [selectedDraftId, setSelectedDraftId] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [progress, setProgress] = useState(0);
  const [failure, setFailure] = useState<RealQaFailureCode | null>(null);
  const [status, setStatus] = useState("Loading RealQA…");
  const [loadRevision, setLoadRevision] = useState(0);
  const lifecycleRevision = useRef(0);

  useEffect(() => {
    let active = true;
    const revision = lifecycleRevision.current;
    void gateway.load().then(
      (loaded) => {
        if (!active || revision !== lifecycleRevision.current) return;
        setSnapshot(loaded);
        setSelectedDraftId(loaded.drafts[0]?.draftId ?? null);
        setStatus("RealQA is ready.");
      },
      (error: unknown) => {
        if (!active || revision !== lifecycleRevision.current) return;
        setFailure(safeFailure(error));
        setStatus("RealQA could not load.");
      },
    );
    return () => {
      active = false;
    };
  }, [gateway, loadRevision]);

  useEffect(() => {
    const invalidate = () => {
      lifecycleRevision.current += 1;
      setSnapshot(null);
      setSelectedDraftId(null);
      setBusy(false);
      setProgress(0);
      setFailure(RealQaFailureCode.AuthenticationRequired);
      setStatus("RealQA is locked.");
    };
    const unsubscribeReset = subscribeToPersistenceReset(invalidate);
    const unsubscribeSession = subscribeToSessionInvalidation(invalidate);
    const unsubscribeReauthentication = subscribeToSessionReauthentication(() => {
      setFailure(null);
      setStatus("Loading RealQA…");
      setLoadRevision((revision) => revision + 1);
    });
    return () => {
      unsubscribeReset();
      unsubscribeSession();
      unsubscribeReauthentication();
    };
  }, []);

  const replaceDraft = useCallback((draft: RealQaDraft) => {
    setSnapshot((current) =>
      current === null
        ? current
        : {
            ...current,
            drafts: current.drafts.map((candidate) =>
              candidate.draftId === draft.draftId ? draft : candidate,
            ),
          },
    );
  }, []);

  const execute = useCallback(
    async (action: RealQaProductAction, success: string) => {
      const revision = lifecycleRevision.current;
      setBusy(true);
      setProgress(0);
      setFailure(null);
      try {
        const updated = await gateway.execute(action, (next) =>
          revision === lifecycleRevision.current
            ? setProgress(Math.max(0, Math.min(100, next)))
            : undefined,
        );
        if (revision !== lifecycleRevision.current) return null;
        setSnapshot(updated);
        setSelectedDraftId((current) =>
          current !== null && updated.drafts.some((draft) => draft.draftId === current)
            ? current
            : updated.drafts[0]?.draftId ?? null,
        );
        setStatus(success);
        return updated;
      } catch (error: unknown) {
        if (revision !== lifecycleRevision.current) return null;
        setFailure(safeFailure(error));
        setStatus("The RealQA action did not finish.");
        return null;
      } finally {
        if (revision === lifecycleRevision.current) setBusy(false);
      }
    },
    [gateway],
  );

  const value = useMemo<WorkspaceContextValue | null>(() => {
    if (snapshot === null) return null;
    return {
      snapshot,
      selectedDraft:
        snapshot.drafts.find((draft) => draft.draftId === selectedDraftId) ?? null,
      selectedDraftId,
      busy,
      progress,
      failure,
      status,
      selectDraft: setSelectedDraftId,
      replaceDraft,
      execute,
      clearFailure: () => setFailure(null),
    };
  }, [busy, execute, failure, progress, replaceDraft, selectedDraftId, snapshot, status]);

  if (value === null) {
    return (
      <section aria-labelledby="realqa-loading-title" className="state-card">
        <h1 id="realqa-loading-title">Opening RealQA</h1>
        <p role={failure === null ? "status" : "alert"}>
          {failure === null ? status : realQaFailureGuidance[failure]}
        </p>
      </section>
    );
  }
  return <WorkspaceContext value={value}>{children}</WorkspaceContext>;
}

function AccessBanner() {
  const { snapshot } = useWorkspace();
  if (snapshot.access === RealQaAccessMode.Online) return null;
  return (
    <section aria-labelledby="realqa-offline-title" className="realqa-banner" role="status">
      <h2 id="realqa-offline-title">Offline draft mode</h2>
      <p>
        Only drafts for the previously bound account can be captured and edited.
        Connect and reauthenticate before upload, submission, or synchronized management.
      </p>
    </section>
  );
}

function WorkspaceStatus() {
  const { clearFailure, failure, progress, status } = useWorkspace();
  return (
    <div aria-live="polite" className="realqa-status-stack">
      <p aria-atomic="true" role="status">{status}</p>
      {progress > 0 && progress < 100 ? (
        <label>
          Upload progress
          <progress max={100} value={progress}>{progress}%</progress>
        </label>
      ) : null}
      {failure === null ? null : (
        <div className="realqa-error" role="alert">
          <p>{realQaFailureGuidance[failure]}</p>
          <button className="text-button" onClick={clearFailure} type="button">
            Dismiss
          </button>
        </div>
      )}
    </div>
  );
}

function csv(value: readonly string[]): string {
  return value.join(", ");
}

function parseCsv(value: string): readonly string[] {
  return [...new Set(value.split(",").map((entry) => entry.trim()).filter(Boolean))];
}

function issueAnswersWithDefaults(
  draft: RealQaDraft,
  definition: RealQaProductSnapshot["definitions"][number] | undefined,
): RealQaDraft["issueAnswers"] {
  if (definition === undefined) return draft.issueAnswers;
  const answers = { ...draft.issueAnswers };
  for (const field of definition.fields) {
    if (
      field.kind !== "checkboxes" &&
      answers[field.fieldId] === undefined &&
      field.defaultValue !== ""
    ) {
      answers[field.fieldId] = [field.defaultValue];
    }
  }
  return answers;
}

function requiredIssueAnswersComplete(
  draft: RealQaDraft,
  definition: RealQaProductSnapshot["definitions"][number] | undefined,
): boolean {
  if (definition === undefined) return true;
  const answers = issueAnswersWithDefaults(draft, definition);
  return definition.fields.every(
    (field) => {
      const values = answers[field.fieldId]?.filter((value) => value.trim() !== "") ?? [];
      if (field.kind === "checkboxes") {
        return field.options.every(
          (option) => !option.required || values.includes(option.value),
        ) && (!field.required || values.length > 0);
      }
      return !field.required || values.length > 0;
    },
  );
}

function codeFence(value: string): string {
  const runs = value.match(/`+/gu) ?? [];
  const longest = runs.reduce((length, run) => Math.max(length, run.length), 2);
  return "`".repeat(longest + 1);
}

function codeSpan(value: string): string {
  const fence = codeFence(value);
  return value.startsWith("`") || value.endsWith("`")
    ? `${fence} ${value} ${fence}`
    : `${fence}${value}${fence}`;
}

function issueFormBody(
  draft: RealQaDraft,
  definition: RealQaProductSnapshot["definitions"][number] | undefined,
): string {
  if (definition === undefined) return "";
  const answers = issueAnswersWithDefaults(draft, definition);
  return definition.fields.map((field) => {
    const values = answers[field.fieldId]?.filter((value) => value.trim() !== "") ?? [];
    let content = "_No response_";
    if (values.length > 0) {
      if (field.kind === "checkboxes") {
        content = values.map((value) => {
          const option = field.options.find((candidate) => candidate.value === value);
          return `- [x] ${option?.label ?? value}`;
        }).join("\n");
      } else if (field.kind === "textarea") {
        content = values.join("\n");
        if (field.renderLanguage) {
          const fence = codeFence(content);
          content = `${fence}${field.renderLanguage}\n${content}${content.endsWith("\n") ? "" : "\n"}${fence}`;
        }
      } else {
        content = values.join(", ");
      }
    }
    return `### ${field.label}\n\n${content}`;
  }).join("\n\n");
}

// The public URL is server-issued only after upload. Reserve a conservative
// fixed prefix here without embedding a remotely fetchable origin in the webview bundle.
const measuredPublicImagePathPrefix = "/".padEnd(64, "x");

export function serializeFinalIssueBody(
  draft: RealQaDraft,
  definition: RealQaProductSnapshot["definitions"][number] | undefined,
): string {
  const sections = [draft.body, issueFormBody(draft, definition)].filter(
    (section) => section.trim() !== "",
  );
  const capture = ["## RealQA capture"];
  for (const field of [...draft.environment, ...draft.dom]) {
    const key = field.label.trim();
    const value = field.value.trim();
    if (key !== "" && value !== "") {
      capture.push(`- **${key}:** ${codeSpan(value)}`);
    }
  }
  if (draft.url !== "") capture.push(`- **URL:** <${draft.url}>`);
  if (capture.length === 1) capture.push("_No capture metadata included._");
  sections.push(capture.join("\n"));
  const images = draft.images.filter((image) => image.selected).map((image) =>
    `![${image.name.replaceAll("[", "\\[").replaceAll("]", "\\]")}](${measuredPublicImagePathPrefix}/${image.imageId})`,
  );
  if (images.length > 0) sections.push(images.join("\n\n"));
  sections.push("<!-- realqa:submission:00000000-0000-7000-8000-000000000000 -->");
  return `${sections.join("\n\n")}\n`;
}

function billingScopeKey(
  scope: RealQaProductSnapshot["replacementBillingScopes"][number] | RealQaPreset["billing"],
): string {
  return `${scope.organizationId}/${scope.teamId}`;
}

function firstPresetDraft(snapshot: RealQaProductSnapshot): RealQaPreset | null {
  const destination = snapshot.destinations.find(
    (candidate) => candidate.connected && snapshot.definitions.some(
      (definition) => definition.destinationId === candidate.destinationId,
    ),
  );
  const definition = snapshot.definitions.find(
    (candidate) => candidate.destinationId === destination?.destinationId,
  );
  const billing = snapshot.replacementBillingScopes[0];
  if (destination === undefined || definition === undefined || billing === undefined) return null;
  return {
    presetId: "",
    revision: 0,
    name: "New RealQA preset",
    captureMode: CaptureMode.Region,
    pointer: PointerInclusion.Exclude,
    selectorMode: RealQaSelectorMode.Normal,
    destinationId: destination.destinationId,
    definitionId: definition.definitionId,
    labels: [],
    assignees: [],
    milestoneNumber: null,
    projectNodeIds: [],
    processUrlRules: [],
    billing: {
      organizationId: billing.organizationId,
      teamId: billing.teamId,
    },
    backgroundGrant: "rebind-required",
    shortcut: null,
  };
}

function PresetManagerContent({
  selectedId,
  setSelectedId,
}: {
  readonly selectedId: string;
  readonly setSelectedId: (presetId: string) => void;
}) {
  const { busy, execute, snapshot } = useWorkspace();
  const selected = snapshot.presets.find((preset) => preset.presetId === selectedId) ?? null;
  const [editing, setEditing] = useState<RealQaPreset | null>(selected);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [confirmDisconnect, setConfirmDisconnect] = useState(false);
  const [capturingShortcut, setCapturingShortcut] = useState(false);
  const [shortcutStatus, setShortcutStatus] = useState<{
    readonly error: boolean;
    readonly message: string;
  } | null>(null);
  const shortcutCaptureRef = useRef<HTMLButtonElement>(null);
  const createAttempt = useRef<string | null>(null);
  const disconnectAttempts = useRef(new Map<string, {
    readonly expectedRevision: number;
    readonly idempotencyKey: string;
  }>());
  const online = snapshot.access === RealQaAccessMode.Online && snapshot.online;
  const shortcutCount = snapshot.presets.filter((preset) => preset.shortcut !== null).length;
  const canCreatePreset = online && firstPresetDraft(snapshot) !== null;

  const createPreset = async () => {
    const draft = firstPresetDraft(snapshot);
    if (draft === null) return;
    const existingPresetIds = new Set(snapshot.presets.map((preset) => preset.presetId));
    const idempotencyKey = createAttempt.current ?? createUuidV7();
    createAttempt.current = idempotencyKey;
    const updated = await execute(
      { kind: "create-preset", preset: draft, idempotencyKey },
      "Preset created.",
    );
    const created = updated?.presets.find(
      (preset) => !existingPresetIds.has(preset.presetId),
    );
    if (created !== undefined) {
      createAttempt.current = null;
      setSelectedId(created.presetId);
      setEditing(created);
    }
  };

  const savePreset = async () => {
    if (editing === null) return;
    const updated = await execute(
      { kind: "save-preset", preset: editing },
      "Preset saved.",
    );
    const saved = updated?.presets.find(
      (preset) => preset.presetId === editing.presetId,
    );
    if (saved !== undefined) setEditing(saved);
  };

  const deletePreset = async () => {
    if (editing === null) return;
    const updated = await execute(
      {
        kind: "delete-preset",
        presetId: editing.presetId,
        expectedRevision: editing.revision,
      },
      "Preset deleted.",
    );
    if (updated === null) return;
    const next = updated.presets[0] ?? null;
    setSelectedId(next?.presetId ?? "");
    setEditing(next);
  };

  const disconnectDestination = async (target: RealQaProductSnapshot["destinations"][number]) => {
    const pending = disconnectAttempts.current.get(target.destinationId);
    const attempt = pending?.expectedRevision === target.revision
      ? pending
      : { expectedRevision: target.revision, idempotencyKey: createUuidV7() };
    disconnectAttempts.current.set(target.destinationId, attempt);
    const updated = await execute({
      kind: "disconnect-destination",
      destinationId: target.destinationId,
      expectedRevision: attempt.expectedRevision,
      idempotencyKey: attempt.idempotencyKey,
    }, "GitHub disconnected; presets were retained.");
    if (updated !== null) disconnectAttempts.current.delete(target.destinationId);
  };

  const beginShortcutCapture = () => {
    setCapturingShortcut(true);
    setShortcutStatus({
      error: false,
      message: "Press a shortcut with at least one modifier, or press Escape to cancel.",
    });
    queueMicrotask(() => shortcutCaptureRef.current?.focus());
  };

  const captureShortcut = (event: KeyboardEvent<HTMLButtonElement>) => {
    if (!capturingShortcut) return;
    event.preventDefault();
    event.stopPropagation();
    const captured = shortcutFromKeyboardInput(event);
    if (captured.kind === "ignored") return;
    if (captured.kind === "cancelled") {
      setCapturingShortcut(false);
      setShortcutStatus({
        error: false,
        message: "Shortcut capture cancelled. The previous shortcut is unchanged.",
      });
      return;
    }
    if (captured.kind === "invalid") {
      setShortcutStatus({
        error: true,
        message: "Use a supported letter, number, function key, Space, or Enter with a modifier.",
      });
      return;
    }
    setEditing((current) =>
      current === null ? current : { ...current, shortcut: captured.shortcut },
    );
    setCapturingShortcut(false);
    setShortcutStatus({ error: false, message: "Preset shortcut updated." });
  };

  if (editing === null) {
    const connectedDestination = snapshot.destinations.find(
      (destination) => destination.connected,
    );
    const disconnectedDestination = snapshot.destinations.find(
      (destination) => !destination.connected,
    );
    return (
      <section aria-labelledby="presets-title" className="realqa-card">
        <h2 id="presets-title">Presets and destinations</h2>
        <p>No presets are available.</p>
        <div className="button-row">
          {connectedDestination === undefined ? (
            <button
              className="primary-button"
              disabled={busy || !online}
              onClick={() => void execute(
                disconnectedDestination === undefined
                  ? { kind: "connect-destination" }
                  : {
                      kind: "reconnect-destination",
                      destinationId: disconnectedDestination.destinationId,
                    },
                "GitHub authorization opened.",
              )}
              type="button"
            >
              {disconnectedDestination === undefined ? "Connect GitHub" : "Reconnect GitHub"}
            </button>
          ) : (
            <button
              className="primary-button"
              disabled={busy || !canCreatePreset}
              onClick={() => void createPreset()}
              type="button"
            >
              Create first preset
            </button>
          )}
        </div>
        {!online ? <p className="muted">Connect online to create a synchronized preset.</p> : null}
      </section>
    );
  }
  const destination = snapshot.destinations.find(
    (candidate) => candidate.destinationId === editing.destinationId,
  );
  const definitionOptions = snapshot.definitions;
  const destinationDefinitionOptions = definitionOptions.filter(
    (definition) => definition.destinationId === editing.destinationId,
  );
  const definitionAvailable = destinationDefinitionOptions.some(
    (definition) => definition.definitionId === editing.definitionId,
  );
  const processUrlRulesValid = validateRealQaProcessUrlRules(editing.processUrlRules);
  const editingBillingScopeKey = billingScopeKey(editing.billing);
  const editingBillingScopeAvailable = snapshot.replacementBillingScopes.some(
    (scope) => billingScopeKey(scope) === editingBillingScopeKey,
  );
  const update = <K extends keyof RealQaPreset>(key: K, value: RealQaPreset[K]) =>
    setEditing((current) => current === null ? current : { ...current, [key]: value });
  const updateProcessUrlRule = (
    index: number,
    changes: Partial<RealQaProcessUrlRule>,
  ) => update(
    "processUrlRules",
    editing.processUrlRules.map((rule, candidateIndex) =>
      candidateIndex === index ? { ...rule, ...changes } : rule,
    ),
  );
  const moveProcessUrlRule = (index: number, offset: -1 | 1) => {
    const target = index + offset;
    if (target < 0 || target >= editing.processUrlRules.length) return;
    const rules = [...editing.processUrlRules];
    [rules[index], rules[target]] = [rules[target]!, rules[index]!];
    update("processUrlRules", rules);
  };

  return (
    <section aria-labelledby="presets-title" className="realqa-card">
      <div className="realqa-section-heading">
        <div>
          <p className="eyebrow">Synchronized</p>
          <h2 id="presets-title">Presets and destinations</h2>
        </div>
        <span>{shortcutCount}/20 shortcuts</span>
      </div>
      <label className="field" htmlFor="realqa-preset">
        Preset
        <select id="realqa-preset" onChange={(event) => { const nextId = event.target.value; setSelectedId(nextId); setEditing(snapshot.presets.find((preset) => preset.presetId === nextId) ?? null); }} value={selectedId}>
          {snapshot.presets.map((preset) => <option key={preset.presetId} value={preset.presetId}>{preset.name}</option>)}
        </select>
      </label>
      <div className="realqa-form-grid">
        <label className="field">Name<input disabled={!online} value={editing.name} onChange={(event) => update("name", event.target.value)} /></label>
        <label className="field">Capture mode<select disabled={!online} value={editing.captureMode} onChange={(event) => update("captureMode", event.target.value as CaptureMode)}>{Object.values(CaptureMode).map((mode) => <option key={mode} value={mode}>{mode}</option>)}</select></label>
        <label className="check-field"><input checked={editing.pointer === PointerInclusion.Include} disabled={!online} onChange={(event) => update("pointer", event.target.checked ? PointerInclusion.Include : PointerInclusion.Exclude)} type="checkbox" />Include pointer by default</label>
        <label className="check-field"><input checked={editing.selectorMode === RealQaSelectorMode.Dom} disabled={!online} onChange={(event) => update("selectorMode", event.target.checked ? RealQaSelectorMode.Dom : RealQaSelectorMode.Normal)} type="checkbox" />Use DOM selection by default</label>
        <label className="field">Destination<select disabled={!online} value={editing.destinationId} onChange={(event) => { const destinationId = event.target.value; const definitionId = snapshot.definitions.find((definition) => definition.destinationId === destinationId)?.definitionId ?? ""; setEditing((current) => current === null ? current : { ...current, destinationId, definitionId }); }}>{snapshot.destinations.map((item) => <option key={item.destinationId} value={item.destinationId}>{item.repository}{item.connected ? "" : " (disconnected)"}</option>)}</select></label>
        <label className="field">Template or form<select disabled={!online || destinationDefinitionOptions.length === 0} value={editing.definitionId} onChange={(event) => update("definitionId", event.target.value)}>{!definitionAvailable ? <option value="">No template available</option> : null}{destinationDefinitionOptions.map((definition) => <option key={definition.definitionId} value={definition.definitionId}>{definition.name} · {definition.issueType}</option>)}</select></label>
        <label className="field">Labels<input disabled={!online} value={csv(editing.labels)} onChange={(event) => update("labels", parseCsv(event.target.value))} /></label>
        <label className="field">Assignees<input disabled={!online} value={csv(editing.assignees)} onChange={(event) => update("assignees", parseCsv(event.target.value))} /></label>
        <label className="field">Milestone number<input disabled={!online} min={1} type="number" value={editing.milestoneNumber ?? ""} onChange={(event) => update("milestoneNumber", event.target.value === "" ? null : Number(event.target.value))} /></label>
        <label className="field">Project node IDs<input disabled={!online} value={csv(editing.projectNodeIds)} onChange={(event) => update("projectNodeIds", parseCsv(event.target.value))} /></label>
        <label className="field">Payer / team<select disabled={!online} value={editingBillingScopeKey} onChange={(event) => { const scope = snapshot.replacementBillingScopes.find((candidate) => billingScopeKey(candidate) === event.target.value); if (scope !== undefined) update("billing", { organizationId: scope.organizationId, teamId: scope.teamId }); }}>{!editingBillingScopeAvailable ? <option disabled value={editingBillingScopeKey}>Unavailable billing scope</option> : null}{snapshot.replacementBillingScopes.map((scope) => <option key={billingScopeKey(scope)} value={billingScopeKey(scope)}>{scope.label}</option>)}</select></label>
        <div className="field">
          <span>Global shortcut</span>
          <kbd>{shortcutLabel(editing.shortcut)}</kbd>
          <div className="button-row">
            <button
              aria-describedby="shortcut-limit"
              aria-pressed={capturingShortcut}
              className="secondary-button"
              disabled={!online || (editing.shortcut === null && shortcutCount >= 20)}
              onClick={beginShortcutCapture}
              onKeyDown={captureShortcut}
              ref={shortcutCaptureRef}
              type="button"
            >
              {capturingShortcut ? "Press shortcut…" : "Record shortcut"}
            </button>
            {editing.shortcut !== null ? (
              <button
                className="text-button"
                disabled={!online}
                onClick={() => update("shortcut", null)}
                type="button"
              >
                Clear shortcut
              </button>
            ) : null}
          </div>
          {shortcutStatus === null ? null : (
            <span role={shortcutStatus.error ? "alert" : "status"}>
              {shortcutStatus.message}
            </span>
          )}
        </div>
      </div>
      <fieldset className="realqa-fields">
        <legend>Process/title URL rules</legend>
        {editing.processUrlRules.map((rule, index) => (
          <div className="realqa-form-grid" key={rule.ruleId}>
            <label className="field">Exact process name<input disabled={!online} value={rule.exactProcessName} onChange={(event) => updateProcessUrlRule(index, { exactProcessName: event.target.value })} /></label>
            <label className="field">Safe window title pattern<input disabled={!online} value={rule.safeWindowTitlePattern} onChange={(event) => updateProcessUrlRule(index, { safeWindowTitlePattern: event.target.value })} /></label>
            <label className="field">URL template<input disabled={!online} value={rule.urlTemplate} onChange={(event) => updateProcessUrlRule(index, { urlTemplate: event.target.value })} /></label>
            <label className="check-field"><input checked={rule.enabled} disabled={!online} onChange={(event) => updateProcessUrlRule(index, { enabled: event.target.checked })} type="checkbox" />Enabled</label>
            <div className="button-row">
              <button aria-label={`Move URL rule ${index + 1} up`} className="text-button" disabled={!online || index === 0} onClick={() => moveProcessUrlRule(index, -1)} type="button">Move up</button>
              <button aria-label={`Move URL rule ${index + 1} down`} className="text-button" disabled={!online || index === editing.processUrlRules.length - 1} onClick={() => moveProcessUrlRule(index, 1)} type="button">Move down</button>
              <button aria-label={`Remove URL rule ${index + 1}`} className="text-button" disabled={!online} onClick={() => update("processUrlRules", editing.processUrlRules.filter((_, candidateIndex) => candidateIndex !== index))} type="button">Remove rule</button>
            </div>
          </div>
        ))}
        <button className="secondary-button" disabled={!online || editing.processUrlRules.length >= MAX_REALQA_PROCESS_URL_RULES} onClick={() => update("processUrlRules", [...editing.processUrlRules, { ruleId: createUuidV7(), exactProcessName: "", safeWindowTitlePattern: "", urlTemplate: "", enabled: true }])} type="button">Add URL rule</button>
        {!processUrlRulesValid ? <p className="error">Every URL rule requires a bounded exact process name and valid HTTP or HTTPS template and title pattern.</p> : null}
      </fieldset>
      {!definitionAvailable ? <p className="error">Choose a template that belongs to the selected destination.</p> : null}
      {!editingBillingScopeAvailable ? <p className="error">Choose an available payer / team before saving this preset.</p> : null}
      <p className="muted" id="shortcut-limit">At most 20 active RealQA shortcuts are registered per device. Conflicts remain inactive.</p>
      <p className={editing.backgroundGrant === "active" ? "muted" : "error"}>Background storage grant: {editing.backgroundGrant}</p>
      <div className="button-row">
        <button className="secondary-button" disabled={busy || !canCreatePreset} onClick={() => void createPreset()} type="button">New preset</button>
        <button className="primary-button" disabled={busy || !online || !definitionAvailable || !processUrlRulesValid || !editingBillingScopeAvailable} onClick={() => void savePreset()} type="button">Save preset</button>
        <button className="secondary-button" disabled={busy || !online} onClick={() => setConfirmDelete(true)} type="button">Delete preset</button>
        {destination?.connected ? <button className="secondary-button" disabled={busy || !online} onClick={() => setConfirmDisconnect(true)} type="button">Disconnect GitHub</button> : <button className="secondary-button" disabled={busy || !online || destination === undefined} onClick={() => destination === undefined ? undefined : void execute({ kind: "reconnect-destination", destinationId: destination.destinationId }, "GitHub authorization opened.")} type="button">Reconnect GitHub</button>}
      </div>
      {confirmDelete ? <Dialog title="Delete RealQA preset" onClose={() => setConfirmDelete(false)}><h2>Delete {editing.name}?</h2><p>This deletes the synchronized preset. Existing encrypted drafts remain.</p><button className="danger-button" onClick={() => { setConfirmDelete(false); void deletePreset(); }} type="button">Delete preset</button><button className="secondary-button" onClick={() => setConfirmDelete(false)} type="button">Cancel</button></Dialog> : null}
      {confirmDisconnect && destination ? <Dialog title="Disconnect RealQA from GitHub" onClose={() => setConfirmDisconnect(false)}><h2>Disconnect GitHub?</h2><p>Provider tokens are revoked. Presets and mappings remain in a disconnected state.</p><button className="danger-button" onClick={() => { setConfirmDisconnect(false); void disconnectDestination(destination); }} type="button">Disconnect</button><button className="secondary-button" onClick={() => setConfirmDisconnect(false)} type="button">Cancel</button></Dialog> : null}
    </section>
  );
}

function PresetManager() {
  const { snapshot } = useWorkspace();
  const [selectedId, setSelectedId] = useState(snapshot.presets[0]?.presetId ?? "");
  const effectiveSelectedId = snapshot.presets.some(
    (preset) => preset.presetId === selectedId,
  ) ? selectedId : snapshot.presets[0]?.presetId ?? "";
  const synchronizedPresetRevision = snapshot.presets
    .map((preset) => `${preset.presetId}:${preset.revision}`)
    .join("|");
  return (
    <PresetManagerContent
      key={synchronizedPresetRevision}
      selectedId={effectiveSelectedId}
      setSelectedId={setSelectedId}
    />
  );
}

function RemovableFields({
  fields,
  legend,
  onChange,
}: {
  readonly fields: RealQaDraft["environment"];
  readonly legend: string;
  readonly onChange: (fields: RealQaDraft["environment"]) => void;
}) {
  return (
    <fieldset className="realqa-fields"><legend>{legend}</legend>
      {fields.map((field) => <div className="realqa-removable-field" key={field.id}><label>{field.label}<input value={field.value} onChange={(event) => onChange(fields.map((candidate) => candidate.id === field.id ? { ...candidate, value: event.target.value } : candidate))} /></label><button aria-label={`Remove ${field.label}`} className="text-button" onClick={() => onChange(fields.filter((candidate) => candidate.id !== field.id))} type="button">Remove</button></div>)}
    </fieldset>
  );
}

function IssueField({ field, draft, update }: { readonly field: RealQaIssueField; readonly draft: RealQaDraft; readonly update: (draft: RealQaDraft) => void }) {
  const values = draft.issueAnswers[field.fieldId] ?? [];
  const setValues = (next: readonly string[]) => update({ ...draft, issueAnswers: { ...draft.issueAnswers, [field.fieldId]: next } });
  if (field.kind === "input") return <label className="field">{field.label}<input required={field.required} value={values[0] ?? field.defaultValue} onChange={(event) => setValues([event.target.value])} /></label>;
  if (field.kind === "textarea") return <label className="field">{field.label}{field.renderLanguage ? <span className="muted">Rendered as {field.renderLanguage}</span> : null}<textarea required={field.required} value={values[0] ?? field.defaultValue} onChange={(event) => setValues([event.target.value])} /></label>;
  if (field.kind === "dropdown") return <label className="field">{field.label}<select multiple={field.multiple} required={field.required} value={field.multiple ? [...values] : values[0] ?? field.defaultValue} onChange={(event) => setValues([...event.currentTarget.selectedOptions].map((option) => option.value))}>{field.options.map((option) => <option key={option}>{option}</option>)}</select></label>;
  if (field.kind === "checkboxes") return <fieldset className="realqa-fields"><legend>{field.label}</legend>{field.options.map((option) => <label className="check-field" key={option.value}><input checked={values.includes(option.value)} onChange={(event) => setValues(event.target.checked ? [...values, option.value] : values.filter((value) => value !== option.value))} required={option.required || (field.required && values.length === 0)} type="checkbox" />{option.label}</label>)}</fieldset>;
  return null;
}

function CaptureAndReviewContent() {
  const { busy, execute, replaceDraft, selectedDraft: draft, selectDraft, selectedDraftId, snapshot } = useWorkspace();
  const selectedPreset = snapshot.presets.find(
    (preset) => preset.presetId === draft?.presetId,
  );
  const presetCaptureMode = selectedPreset?.captureMode;
  const presetPointer = selectedPreset?.pointer;
  const presetSelectorMode = selectedPreset?.selectorMode;
  const [mode, setMode] = useState(presetCaptureMode ?? CaptureMode.Region);
  const [pointer, setPointer] = useState(
    presetPointer ?? PointerInclusion.Exclude,
  );
  const [selector, setSelector] = useState(
    presetSelectorMode ?? RealQaSelectorMode.Normal,
  );
  const [selection, setSelection] = useState({ x: 0, y: 0, width: 640, height: 480 });
  const [confirmDraftDelete, setConfirmDraftDelete] = useState(false);
  const [confirmPublic, setConfirmPublic] = useState(false);
  const [newDraftPresetId, setNewDraftPresetId] = useState(snapshot.presets[0]?.presetId ?? "");
  const effectiveNewDraftPresetId = snapshot.presets.some(
    (preset) => preset.presetId === newDraftPresetId,
  ) ? newDraftPresetId : snapshot.presets[0]?.presetId ?? "";
  const online = snapshot.access === RealQaAccessMode.Online && snapshot.online;
  const titleRef = useRef<HTMLInputElement>(null);
  useEffect(() => titleRef.current?.focus(), [selectedDraftId]);
  if (draft === null) return <section className="realqa-card"><h2>Capture and review</h2><p>No local drafts are available.</p>{snapshot.presets.length === 0 ? <p className="muted">Create a preset before starting a draft.</p> : <><label className="field">Preset<select value={effectiveNewDraftPresetId} onChange={(event) => setNewDraftPresetId(event.target.value)}>{snapshot.presets.map((preset) => <option key={preset.presetId} value={preset.presetId}>{preset.name}</option>)}</select></label><button className="primary-button" disabled={busy || effectiveNewDraftPresetId === ""} onClick={() => void execute({ kind: "create-draft", presetId: effectiveNewDraftPresetId }, "Draft created.")} type="button">Create draft</button></>}</section>;
  const update = (changes: Partial<RealQaDraft>) => replaceDraft({ ...draft, ...changes });
  const definition = snapshot.definitions.find((item) => {
    const preset = snapshot.presets.find((candidate) => candidate.presetId === draft.presetId);
    return item.definitionId === preset?.definitionId
      && item.destinationId === preset.destinationId;
  });
  const draftWithDefaults = {
    ...draft,
    issueAnswers: issueAnswersWithDefaults(draft, definition),
  };
  const bodyBytes = new TextEncoder().encode(
    serializeFinalIssueBody(draftWithDefaults, definition),
  ).byteLength;
  const normalizedTitle = draft.title.trim();
  const titleBytes = new TextEncoder().encode(normalizedTitle).byteLength;
  const titleValid = normalizedTitle !== ""
    && titleBytes <= MAX_ISSUE_TITLE_UTF8_BYTES
    && !draft.title.includes("\0")
    && !draft.title.includes("\r")
    && !draft.title.includes("\n");
  const selectedImages = draft.images.filter((image) => image.selected);
  const reviewedUrl = sanitizeCapturedUrl(draft.url);
  const reviewedUrlValid = draft.url === "" || reviewedUrl.ok;
  const requiredAnswersComplete = requiredIssueAnswersComplete(draftWithDefaults, definition);
  const selectedDestination = snapshot.destinations.find(
    (destination) => destination.destinationId === selectedPreset?.destinationId,
  );
  const submissionPresetAvailable = selectedPreset !== undefined && definition !== undefined;
  const submissionDestinationConnected = selectedDestination?.connected === true;
  const submissionBackgroundGrantActive = selectedPreset?.backgroundGrant === "active";
  const storageSubmissionBlocked = snapshot.submissions.some(
    (submission) => submission.state === "storage-billing-grace",
  );
  const submit = () => {
    if (storageSubmissionBlocked || !submissionPresetAvailable || !submissionDestinationConnected || !submissionBackgroundGrantActive || !reviewedUrlValid || !requiredAnswersComplete || !titleValid) return;
    setConfirmPublic(false);
    void execute({
      kind: "submit",
      draft: {
        ...draftWithDefaults,
        url: reviewedUrl.ok ? reviewedUrl.url.value : "",
        urlWarning: reviewedUrl.ok && reviewedUrl.url.warning !== null,
      },
      publicImageConfirmation: true,
    }, "Issue submitted and the local raw draft was deleted.");
  };
  return (
    <section aria-labelledby="capture-review-title" className="realqa-card">
      <div className="realqa-section-heading"><div><p className="eyebrow">Local and nondestructive</p><h2 id="capture-review-title">Capture and review</h2></div><label>Draft<select onChange={(event) => selectDraft(event.target.value)} value={selectedDraftId ?? ""}>{snapshot.drafts.map((item) => <option key={item.draftId} value={item.draftId}>{item.title || "Untitled draft"}</option>)}</select></label></div>
      <fieldset className="realqa-fields"><legend>Capture-time overrides</legend><div className="realqa-form-grid"><label className="field">Mode<select value={mode} onChange={(event) => setMode(event.target.value as CaptureMode)}>{Object.values(CaptureMode).map((item) => <option key={item}>{item}</option>)}</select></label><label className="check-field"><input checked={pointer === PointerInclusion.Include} onChange={(event) => setPointer(event.target.checked ? PointerInclusion.Include : PointerInclusion.Exclude)} type="checkbox" />Include pointer</label><label className="check-field"><input checked={selector === RealQaSelectorMode.Dom} onChange={(event) => setSelector(event.target.checked ? RealQaSelectorMode.Dom : RealQaSelectorMode.Normal)} type="checkbox" />Select a DOM target in Chrome</label></div>
        <fieldset className="selection-adjuster"><legend>Move or resize selection</legend><label>X<input type="number" value={selection.x} onChange={(event) => setSelection({ ...selection, x: Number(event.target.value) })} /></label><label>Y<input type="number" value={selection.y} onChange={(event) => setSelection({ ...selection, y: Number(event.target.value) })} /></label><label>Width<input min={1} type="number" value={selection.width} onChange={(event) => setSelection({ ...selection, width: Number(event.target.value) })} /></label><label>Height<input min={1} type="number" value={selection.height} onChange={(event) => setSelection({ ...selection, height: Number(event.target.value) })} /></label></fieldset>
        <button className="primary-button" disabled={busy} onClick={() => void execute({ kind: "capture", draftId: draft.draftId, captureMode: mode, pointer, selectorMode: selector, selection }, "Capture added. Open an image to edit without changing its raw original.")} type="button">Capture screenshot</button>
      </fieldset>
      <fieldset className="realqa-fields"><legend>Images ({draft.images.length})</legend><p className="muted">There is no image-count limit. Each image is limited to 25 MiB, the session to 250 MiB, and decoded input to 100 megapixels.</p><ul className="realqa-image-list">{draft.images.map((image) => <li key={image.imageId}><label className="check-field"><input checked={image.selected} onChange={(event) => update({ images: draft.images.map((candidate) => candidate.imageId === image.imageId ? { ...candidate, selected: event.target.checked } : candidate) })} type="checkbox" />{image.name} · {(image.encodedBytes / 1024).toFixed(1)} KiB · {image.uploadState}</label><button className="secondary-button" disabled={busy} onClick={() => void execute({ kind: "edit-image", draftId: draft.draftId, imageId: image.imageId }, "Nondestructive editor opened; the encrypted raw original is unchanged.")} type="button">Edit nondestructively</button>{image.uploadState === "uploading" ? <progress aria-label={`${image.name} upload progress`} max={100} value={image.uploadProgress} /> : null}</li>)}</ul></fieldset>
      <div className="realqa-form-grid"><label className="field">Issue title<input aria-describedby={titleValid ? undefined : "realqa-title-guidance"} aria-invalid={!titleValid} ref={titleRef} value={draft.title} onChange={(event) => update({ title: event.target.value })} /></label><label className="field">Issue body<textarea value={draft.body} onChange={(event) => update({ body: event.target.value })} /></label><label className="field">Sanitized URL<input aria-invalid={!reviewedUrlValid} value={draft.url} onBlur={() => { if (reviewedUrl.ok) update({ url: reviewedUrl.url.value, urlWarning: reviewedUrl.url.warning !== null }); }} onChange={(event) => update({ url: event.target.value })} /></label></div>
      {!titleValid ? <p className="error" id="realqa-title-guidance">Enter a single-line issue title between 1 and {MAX_ISSUE_TITLE_UTF8_BYTES} UTF-8 bytes.</p> : null}
      {!reviewedUrlValid ? <p className="error" role="alert">Use an HTTP or HTTPS URL without credentials or invalid escapes.</p> : null}
      {draft.urlWarning ? <p role="note">This URL points to localhost or a private network. Review it before submission.</p> : null}
      <RemovableFields fields={draft.environment} legend="Environment metadata" onChange={(environment) => update({ environment })} />
      <RemovableFields fields={draft.dom} legend="DOM metadata" onChange={(dom) => update({ dom })} />
      {definition ? <fieldset className="realqa-fields"><legend>{definition.name} fields</legend>{definition.fields.map((field) => <IssueField draft={draftWithDefaults} field={field} key={field.fieldId} update={replaceDraft} />)}</fieldset> : null}
      {!submissionPresetAvailable ? <p className="error">This draft's preset is no longer available. Recreate the preset before starting a replacement draft.</p> : null}
      {submissionPresetAvailable && !submissionDestinationConnected ? <div className="realqa-error"><p>This preset's GitHub destination is disconnected. Reconnect before submitting.</p>{selectedDestination ? <button className="secondary-button" disabled={busy || !online} onClick={() => void execute({ kind: "reconnect-destination", destinationId: selectedDestination.destinationId }, "GitHub authorization opened.")} type="button">Reconnect GitHub</button> : null}</div> : null}
      {submissionPresetAvailable && !submissionBackgroundGrantActive ? <p className="error">This preset's background storage grant is inactive. Rebind its RealQA storage authorization in DeliDev before submitting.</p> : null}
      {!requiredAnswersComplete ? <p className="error">Complete every required issue-form field before submission.</p> : null}
      <div className="realqa-form-grid"><label className="field">Labels<input value={csv(draft.labels)} onChange={(event) => update({ labels: parseCsv(event.target.value) })} /></label><label className="field">Assignees<input value={csv(draft.assignees)} onChange={(event) => update({ assignees: parseCsv(event.target.value) })} /></label><label className="field">Milestone number<input min={1} type="number" value={draft.milestoneNumber ?? ""} onChange={(event) => update({ milestoneNumber: event.target.value === "" ? null : Number(event.target.value) })} /></label><label className="field">Project node IDs<input value={csv(draft.projectNodeIds)} onChange={(event) => update({ projectNodeIds: parseCsv(event.target.value) })} /></label></div>
      <p className={bodyBytes > MAX_FINAL_BODY_UTF8_BYTES ? "error" : "muted"}>{bodyBytes.toLocaleString()} / {MAX_FINAL_BODY_UTF8_BYTES.toLocaleString()} body bytes</p>
      <div className="button-row"><button className="secondary-button" disabled={busy} onClick={() => void execute({ kind: "save-draft", draft: draftWithDefaults }, "Encrypted local draft saved.")} type="button">Save draft</button><button className="danger-button" disabled={busy} onClick={() => setConfirmDraftDelete(true)} type="button">Delete draft</button><button className="primary-button" disabled={busy || !online || storageSubmissionBlocked || !submissionPresetAvailable || !submissionDestinationConnected || !submissionBackgroundGrantActive || selectedImages.length === 0 || bodyBytes > MAX_FINAL_BODY_UTF8_BYTES || !reviewedUrlValid || !requiredAnswersComplete || !titleValid} onClick={() => setConfirmPublic(true)} type="button">Review and submit</button></div>
      {!online ? <p className="muted">Online reauthentication is required before upload or submission.</p> : null}
      {confirmDraftDelete ? <Dialog title="Delete local RealQA draft" onClose={() => setConfirmDraftDelete(false)}><h2>Delete this draft?</h2><p>This immediately deletes the encrypted local draft and its raw originals. It does not delete server feature data.</p><button className="danger-button" onClick={() => { setConfirmDraftDelete(false); void execute({ kind: "delete-draft", draftId: draft.draftId, expectedRevision: draft.revision }, "Local draft deleted."); }} type="button">Delete local draft</button><button className="secondary-button" onClick={() => setConfirmDraftDelete(false)} type="button">Cancel</button></Dialog> : null}
      {confirmPublic ? <Dialog descriptionId="public-image-warning" title="Confirm public screenshots" onClose={() => setConfirmPublic(false)}><h2>Submit a new GitHub issue?</h2><p id="public-image-warning">{PUBLIC_SCREENSHOT_WARNING} This confirmation is required for every submission.</p><button aria-describedby="public-image-warning" className="primary-button" onClick={submit} type="button">Confirm and submit</button><button className="secondary-button" onClick={() => setConfirmPublic(false)} type="button">Cancel</button></Dialog> : null}
    </section>
  );
}

function CaptureAndReview() {
  const { selectedDraftId } = useWorkspace();
  return <CaptureAndReviewContent key={selectedDraftId ?? "no-draft"} />;
}

function SubmissionLifecycle() {
  const { busy, execute, snapshot } = useWorkspace();
  const [replacementScopeKey, setReplacementScopeKey] = useState(
    snapshot.replacementBillingScopes[0]
      ? billingScopeKey(snapshot.replacementBillingScopes[0])
      : "",
  );
  const [retryConfirmation, setRetryConfirmation] = useState<{
    readonly submissionId: string;
    readonly replay: NonNullable<RealQaProductSnapshot["submissions"][number]["replay"]>;
  } | null>(null);
  const rebindAttempts = useRef(new Map<string, {
    readonly scopeKey: string;
    readonly expectedAuthorizationId: string;
    readonly expectedRevision: number;
    readonly idempotencyKey: string;
  }>());
  const deletionAttempts = useRef(new Map<string, {
    readonly expectedSubmissionRevision: number;
    readonly expectedImageRevision: number | null;
    readonly idempotencyKey: string;
  }>());
  const effectiveScope = snapshot.replacementBillingScopes.find(
    (scope) => billingScopeKey(scope) === replacementScopeKey,
  ) ?? snapshot.replacementBillingScopes[0];
  const effectiveScopeKey = effectiveScope ? billingScopeKey(effectiveScope) : "";
  const online = snapshot.access === RealQaAccessMode.Online && snapshot.online;

  const rebind = async (
    submission: RealQaProductSnapshot["submissions"][number],
  ) => {
    if (submission.authorizationId === null || effectiveScope === undefined) return;
    const pending = rebindAttempts.current.get(submission.submissionId);
    const attempt = pending?.scopeKey === effectiveScopeKey
      && pending.expectedAuthorizationId === submission.authorizationId
      && pending.expectedRevision === submission.authorizationRevision
      ? pending
      : {
          scopeKey: effectiveScopeKey,
          expectedAuthorizationId: submission.authorizationId,
          expectedRevision: submission.authorizationRevision,
          idempotencyKey: createUuidV7(),
        };
    rebindAttempts.current.set(submission.submissionId, attempt);
    const updated = await execute({
      kind: "rebind-authorization",
      submissionId: submission.submissionId,
      expectedAuthorizationId: attempt.expectedAuthorizationId,
      expectedRevision: attempt.expectedRevision,
      replacementBilling: {
        organizationId: effectiveScope.organizationId,
        teamId: effectiveScope.teamId,
      },
      idempotencyKey: attempt.idempotencyKey,
    }, "Storage authorization rebound without grace-period back-billing.");
    if (updated !== null) rebindAttempts.current.delete(submission.submissionId);
  };

  const deleteImage = async (
    submission: RealQaProductSnapshot["submissions"][number],
    image: RealQaProductSnapshot["submissions"][number]["images"][number],
  ) => {
    const attemptKey = `image/${submission.submissionId}/${image.imageId}`;
    const pending = deletionAttempts.current.get(attemptKey);
    const attempt = pending?.expectedSubmissionRevision === submission.revision
      && pending.expectedImageRevision === image.revision
      ? pending
      : {
          expectedSubmissionRevision: submission.revision,
          expectedImageRevision: image.revision,
          idempotencyKey: createUuidV7(),
        };
    deletionAttempts.current.set(attemptKey, attempt);
    const updated = await execute({
      kind: "delete-image",
      submissionId: submission.submissionId,
      imageId: image.imageId,
      expectedSubmissionRevision: attempt.expectedSubmissionRevision,
      expectedImageRevision: image.revision,
      idempotencyKey: attempt.idempotencyKey,
    }, "Image deleted; its public URL now returns the generic removed placeholder.");
    if (updated !== null) deletionAttempts.current.delete(attemptKey);
  };

  const deleteSubmissionAssets = async (
    submission: RealQaProductSnapshot["submissions"][number],
  ) => {
    const attemptKey = `submission/${submission.submissionId}`;
    const pending = deletionAttempts.current.get(attemptKey);
    const attempt = pending?.expectedSubmissionRevision === submission.revision
      ? pending
      : {
          expectedSubmissionRevision: submission.revision,
          expectedImageRevision: null,
          idempotencyKey: createUuidV7(),
        };
    deletionAttempts.current.set(attemptKey, attempt);
    const updated = await execute({
      kind: "delete-submission-assets",
      submissionId: submission.submissionId,
      expectedSubmissionRevision: attempt.expectedSubmissionRevision,
      idempotencyKey: attempt.idempotencyKey,
    }, "All submission images were deleted.");
    if (updated !== null) deletionAttempts.current.delete(attemptKey);
  };

  const retrySubmission = () => {
    if (retryConfirmation === null) return;
    const { replay, submissionId } = retryConfirmation;
    setRetryConfirmation(null);
    void execute({
      kind: "retry-submission",
      submissionId,
      expectedSubmissionRevision: replay.expectedSubmissionRevision,
      idempotencyKey: replay.idempotencyKey,
      originalDraft: replay.originalDraft,
      publicImageConfirmation: true,
    }, "Submission reconciled with its original identity.");
  };

  return (
    <section aria-labelledby="submission-lifecycle-title" className="realqa-card"><p className="eyebrow">Retained references only</p><h2 id="submission-lifecycle-title">Submissions and storage</h2>
      {snapshot.submissions.length === 0 ? <p>No submitted issues are retained.</p> : <><label className="field">Replacement payer<select disabled={!online || effectiveScope === undefined} value={effectiveScopeKey} onChange={(event) => setReplacementScopeKey(event.target.value)}>{snapshot.replacementBillingScopes.map((scope) => <option key={billingScopeKey(scope)} value={billingScopeKey(scope)}>{scope.label}</option>)}</select></label><ul className="realqa-submission-list">{snapshot.submissions.map((submission) => {
        const retryable = submission.state === "failed" || submission.state === "reconciling";
        const replay = submission.replay;
        return <li key={submission.submissionId}><h3>{submission.issueUrl ?? "Pending GitHub reconciliation"}</h3><p>State: {submission.state}</p>{submission.graceExpiresAt ? <p className="error">Public images are in billing grace until {submission.graceExpiresAt}. New submissions are blocked.</p> : null}{retryable && replay === null ? <p className="muted">Restore the retained encrypted draft before retrying reconciliation.</p> : null}<div className="button-row">{retryable && replay !== null ? <button className="primary-button" disabled={busy || !online} onClick={() => setRetryConfirmation({ submissionId: submission.submissionId, replay })} type="button">Retry reconciliation</button> : null}{submission.images.filter((image) => image.uploadState !== "removed").map((image) => <button className="secondary-button" disabled={busy || !online} key={image.imageId} onClick={() => void deleteImage(submission, image)} type="button">Delete {image.name}</button>)}<button className="secondary-button" disabled={busy || !online} onClick={() => void deleteSubmissionAssets(submission)} type="button">Delete all images</button>{submission.authorizationId ? <button className="secondary-button" disabled={busy || !online || effectiveScope === undefined} onClick={() => void rebind(submission)} type="button">Rebind payer</button> : null}</div></li>;
      })}</ul></>}
      {retryConfirmation ? <Dialog descriptionId="retry-public-image-warning" title="Confirm public screenshots for retry" onClose={() => setRetryConfirmation(null)}><h2>Retry this submission?</h2><p id="retry-public-image-warning">{PUBLIC_SCREENSHOT_WARNING} This confirmation is required for every submission attempt.</p><button aria-describedby="retry-public-image-warning" className="primary-button" onClick={retrySubmission} type="button">Confirm and retry</button><button className="secondary-button" onClick={() => setRetryConfirmation(null)} type="button">Cancel</button></Dialog> : null}
    </section>
  );
}

function FeatureDeletion() {
  const { busy, execute, snapshot } = useWorkspace();
  const [confirming, setConfirming] = useState(false);
  const deletionAttempt = useRef<string | null>(null);
  const online = snapshot.access === RealQaAccessMode.Online && snapshot.online;
  const deleteFeatureData = async () => {
    const idempotencyKey = deletionAttempt.current ?? createUuidV7();
    deletionAttempt.current = idempotencyKey;
    const updated = await execute(
      { kind: "delete-feature-data", idempotencyKey },
      "Server RealQA feature-data deletion was accepted.",
    );
    if (updated !== null) deletionAttempt.current = null;
  };
  return <section aria-labelledby="feature-deletion-title" className="realqa-card danger-zone"><h2 id="feature-deletion-title">Delete server RealQA data</h2><p>This is distinct from logout, Reset DevHud, GitHub disconnect, and local draft deletion. It deletes authorized server presets, submissions, and assets.</p><button className="danger-button" disabled={busy || !online} onClick={() => setConfirming(true)} type="button">Delete server feature data</button>{confirming ? <Dialog title="Delete server RealQA data" onClose={() => setConfirming(false)}><h2>Delete server RealQA data?</h2><p>Local drafts are not deleted by this server request. Account or organization authorization is checked online.</p><button className="danger-button" onClick={() => { setConfirming(false); void deleteFeatureData(); }} type="button">Confirm server deletion</button><button className="secondary-button" onClick={() => setConfirming(false)} type="button">Cancel</button></Dialog> : null}</section>;
}

function RealQaWorkspaceFrame({ children }: { readonly children: ReactNode }) {
  return <main className="realqa-workspace"><header className="app-header"><div aria-label="RealQA" className="wordmark"><span aria-hidden="true">RQ</span><strong>RealQA</strong></div><p className="muted">Desktop internal production tool</p></header>{children}</main>;
}

function RealQaWorkspaceRoot({ gateway }: { readonly gateway: RealQaProductGateway }) {
  return <RealQaWorkspaceProvider gateway={gateway}><RealQaWorkspaceFrame><AccessBanner /><WorkspaceStatus /><PresetManager /><CaptureAndReview /><SubmissionLifecycle /><FeatureDeletion /></RealQaWorkspaceFrame></RealQaWorkspaceProvider>;
}

export const RealQaWorkspace = Object.assign(RealQaWorkspaceRoot, {
  Provider: RealQaWorkspaceProvider,
  Frame: RealQaWorkspaceFrame,
  Presets: PresetManager,
  CaptureReview: CaptureAndReview,
  Submissions: SubmissionLifecycle,
  FeatureDeletion,
});
