import { productError, ownedMessage, useProductMessage, LocalizedText, copy, useLocale  } from "./localization";
import { useCallback, useEffect, useLayoutEffect, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, SessionQuery, SystemQuery, SystemCapability, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { BudgetFields, budgetInput, emptyBudget } from "./session-budget";
import { document, encode, items, Mode, object, text, Workspace } from "./documents";
import { ResourceChoice } from "./configuration-fields";
import { StartingReferences } from "./schedules";
import { useRetainedMutation } from "./mutation";
import { type ReadLocalWorkerProof } from "./local-worker";
import { Problem } from "./ui";
import { Surface } from "./surface";
import { useShortcuts } from "./shortcut-provider";
import { ShortcutExecution, ShortcutId, ShortcutInput } from "./shortcuts";

const UUID_V7 = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

export enum NewSessionKind { Session = "session", GeneralChat = "general-chat" }

function sessionResource(resource?: Resource): resource is Resource {
  return Boolean(resource && resource.kind === EntityKind.SESSION && resource.schemaVersion === 1 && resource.revision > 0n && UUID_V7.test(resource.id));
}

export function NewSession({ kind = NewSessionKind.Session, active, ownsActivation, activation, entryProjectId, projectSelectionBlockedChanged, readLocalWorker, back, openSettings, open, created }: {
  kind?: NewSessionKind;
  active: boolean;
  ownsActivation: boolean;
  activation: number;
  entryProjectId?: string;
  projectSelectionBlockedChanged?: (blocked: boolean) => void;
  readLocalWorker?: ReadLocalWorkerProof;
  back: () => void;
  openSettings: () => void;
  open: (id: string) => void;
  created: () => void;
}) {
  useLocale();
  const generalChat = kind === NewSessionKind.GeneralChat;
  const idPrefix = generalChat ? "new-general-chat" : "new-session";
  const [workspace, setWorkspace] = useState(Workspace.GeneralChat);
  const [project, setProject] = useState("");
  const [agent, setAgent] = useState("");
  const [machine, setMachine] = useState("");
  const [prompt, setPrompt] = useState("");
  const [mode, setMode] = useState(Mode.Execute);
  const [starting, setStarting] = useState<unknown[]>([]);
  const [optionsOpen, setOptionsOpen] = useState(false);
  const [promptLimit, setPromptLimit] = useState(false);
  const [budget, setBudget] = useState(emptyBudget);
  const [budgetProblem, setBudgetProblem] = useProductMessage("");
  const [invalidAcknowledgment, setInvalidAcknowledgment] = useState(false);
  const [createdElsewhere, setCreatedElsewhere] = useState<Resource>();
  const firstMessage = useRef<HTMLTextAreaElement>(null);
  const lastEntry = useRef({ active: false, activation: -1 });
  const navigation = useRef({ ownsActivation: false, activation });
  navigation.current = { ownsActivation, activation };

  useEffect(() => {
    const previous = lastEntry.current;
    if (active && (!previous.active || previous.activation !== activation)) firstMessage.current?.focus();
    lastEntry.current = { active, activation };
  }, [active, activation]);

  const status = useQuery(SystemQuery.getStatus, {}, { refetchInterval: 30000 });
  const automaticTitles = status.data?.capabilities.includes(SystemCapability.AUTOMATIC_TITLES_V1) ?? false;
  const selectedProject = useQuery(ResourceQuery.getResource, { kind: EntityKind.PROJECT, id: generalChat ? "" : project }, { enabled: active && !generalChat && Boolean(project) });
  const accepted = useCallback((result: { change?: { session?: Resource } }) => {
    const session = result.change?.session;
    if (!sessionResource(session)) {
      setInvalidAcknowledgment(true);
      return;
    }
    setPrompt("");
    setCreatedElsewhere(undefined);
    created();
    if (navigation.current.ownsActivation && navigation.current.activation === submittedActivation.current) {
      open(session.id);
    } else {
      setCreatedElsewhere(session);
    }
  }, [created, open]);
  const submittedActivation = useRef(-1);
  const mutation = useRetainedMutation(generalChat ? "create-general-chat" : "create-session", SessionQuery.createSession, accepted);
  const restrictions = object(document(selectedProject.data?.resource).agents);
  const blocked = mutation.busy || mutation.uncertain || invalidAcknowledgment;
  const projectChanged = useCallback((id: string) => {
    setProject(id);
    setAgent("");
    setMachine("");
    setStarting([]);
    setWorkspace(id ? Workspace.Worktree : Workspace.GeneralChat);
  }, []);
  const consumedProjectActivation = useRef(-1);
  useLayoutEffect(() => {
    projectSelectionBlockedChanged?.(blocked);
  }, [blocked, projectSelectionBlockedChanged]);
  useLayoutEffect(() => {
    if (!active || consumedProjectActivation.current === activation) return;
    // Consume blocked entries too: settlement must never apply an old navigation intent.
    consumedProjectActivation.current = activation;
    if (entryProjectId && !blocked && entryProjectId !== project) projectChanged(entryProjectId);
  }, [active, activation, entryProjectId, blocked, project, projectChanged]);

  const canCreate = active && automaticTitles && Boolean(agent && machine && prompt.trim()) && !blocked;

  const submit = async () => {
    if (!canCreate) return;
    let estimatedBudget;
    try {
      estimatedBudget = budgetInput(budget);
      setBudgetProblem("");
    } catch (error) {
      setBudgetProblem(productError(error, "new-session.extra.08fb485eeaf6"));
      return;
    }
    const selectedProjectId = generalChat ? "" : project;
    const workspaceType = selectedProjectId ? workspace : Workspace.GeneralChat;
    const selection = {
      name_mode: "automatic",
      estimated_cost_budget: estimatedBudget,
      prompt,
      agent_id: agent,
      machine_id: machine,
      project_id: selectedProjectId || undefined,
      workspace: workspaceType,
      starting: selectedProjectId && workspaceType === Workspace.Worktree ? starting : undefined,
      mode,
      source: "MANUAL",
    };
    submittedActivation.current = navigation.current.activation;
    void mutation.send({ requestId: newRequestId(), documentJson: encode(selection) });
  };

  const shortcutScope = generalChat ? Surface.NewGeneralChat : Surface.NewSession;
  const shortcuts = useShortcuts([
    { id: ShortcutId.NewSessionFocus, scope: shortcutScope, active, label: "shortcuts.focusFirstMessage", bindings: [{ key: "i", primary: true }], input: ShortcutInput.Allow, enabled: !blocked, unavailableReason: "shortcuts.pending", run: () => firstMessage.current?.focus() },
    { id: ShortcutId.NewSessionSend, scope: shortcutScope, active, label: "shortcuts.createSession", bindings: [{ key: "Enter" }, { key: "Enter", primary: true }], target: firstMessage, input: ShortcutInput.Target, enabled: canCreate, unavailableReason: blocked ? "shortcuts.pending" : !automaticTitles ? "shortcuts.updateServer" : "shortcuts.creationRequired", run: () => firstMessage.current?.form?.requestSubmit() },
    { id: ShortcutId.NewSessionNewline, scope: shortcutScope, active, label: "shortcuts.newline", bindings: [{ key: "Enter", shift: true }], target: firstMessage, input: ShortcutInput.Target, execution: ShortcutExecution.Native, enabled: !blocked, unavailableReason: "shortcuts.pending" },
  ]);
  const updatePrompt = (value: string) => {
    if (new TextEncoder().encode(value).byteLength > 256 << 10) {
      setPromptLimit(true);
      return;
    }
    setPrompt(value);
    setPromptLimit(false);
  };
  const submitForm = (event: FormEvent) => {
    event.preventDefault();
    void submit();
  };
  const title = createdElsewhere ? text(document(createdElsewhere).name) || copy("new-session.extra.cffdba22adf2") : copy("new-session.extra.cffdba22adf2");
  const submitLabel = generalChat ? copy("new-session.startGeneralChat") : copy("new-session.createSession_38b6ef");

  return <section hidden={!active} className={`new-session-page${generalChat ? " new-general-chat-page" : ""}`} aria-labelledby={`${idPrefix}-heading`}>
    <div className="new-session-content">
      <header className="new-session-header">
        <button type="button" className="new-session-back" onClick={back}>{copy("new-session.backToSessions_3740d2")}</button>
        <h2 id={`${idPrefix}-heading`}>{generalChat ? copy("new-session.whatWouldYouLikeToTalkAbout") : copy("new-session.whatWouldYouLikeToWork_3c9309")}</h2>
        {generalChat ? <p className="new-general-chat-description">{copy("new-session.generalChatDescription")}</p> : null}
      </header>
      {!generalChat ? <ResourceChoice label={copy("new-session.project_985959")} kind={EntityKind.PROJECT} value={project} active={active} showStatus disabled={blocked} change={projectChanged} /> : null}
      {!generalChat && !project ? <p className="new-session-project-note">{copy("new-session.generalChatIsolatedProjectlessDirectoryOn_aac210")}</p> : null}
      <form onSubmit={submitForm}>
        <fieldset className="new-session-fieldset" disabled={blocked}>
          <div className="new-session-composer">
            <label className="new-session-message-label" htmlFor={`${idPrefix}-message`}>{copy("new-session.firstMessage_ecffa2")}</label>
            <textarea
              ref={firstMessage}
              id={`${idPrefix}-message`}
              name="first-message"
              aria-label={copy("new-session.firstMessage_ecffa2")}
              placeholder={generalChat ? copy("new-session.generalChatPlaceholder") : copy("new-session.describeATaskAskAQuestion_4ed4ad")}
              value={prompt}
              onChange={(event) => updatePrompt(event.target.value)}
              onKeyDown={shortcuts.onKeyDown}
              aria-keyshortcuts={shortcuts.aria(ShortcutId.NewSessionFocus, ShortcutId.NewSessionSend, ShortcutId.NewSessionNewline)}
              rows={5}
              required
              autoComplete="off"
            />
            <div className="new-session-toolbar">
              <div className="new-session-selectors">
                <ResourceChoice label={copy("new-session.agentWorker_a4caa7")} kind={EntityKind.AGENT} value={agent} active={active} showStatus required allowed={restrictions.configured === true ? items(restrictions.ids) : undefined} change={setAgent} />
                <ResourceChoice label={copy("new-session.runsOn_88a550")} resourceLabel={copy("new-session.runnerDevice_37efe3")} kind={EntityKind.MACHINE} value={machine} active={active} showStatus required change={setMachine} />
                <label className="new-session-mode">{copy("new-session.mode_5e23ec")}<select value={mode} onChange={(event) => setMode(event.target.value as Mode)}><option value={Mode.Execute}>{copy("new-session.execute_e3a67d")}</option><option value={Mode.Plan}>{copy("new-session.plan_fa8ed0")}</option></select></label>
              </div>
              <div className="new-session-submit-row">
                <button type="button" className="new-session-options-toggle" aria-expanded={optionsOpen} onClick={() => setOptionsOpen((value) => !value)}>{copy("new-session.options_d0db8b")}</button>
                <button className="new-session-submit" type="submit" aria-keyshortcuts={shortcuts.aria(ShortcutId.NewSessionSend)} aria-label={submitLabel} title={submitLabel} disabled={!canCreate}>
                  <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 19V5M6 11l6-6 6 6" /></svg>
                </button>
              </div>
            </div>
          </div>
          <div className="new-session-hints"><span>{generalChat ? copy("new-session.conversationsAreNamedAutomatically") : copy("new-session.sessionsAreNamedAutomatically_ba66e8")}</span><span>{copy("new-session.shiftEnterForANewLine_5e4b35")}</span></div>
          {optionsOpen ? <section className="new-session-options" aria-label={copy("new-session.sessionOptions_0bccf5")}>
            {!generalChat && project ? <>
              <p>{copy("new-session.aSeparateDetachedWorktreeIsPrepared_8c300d")}</p>
              <div className="actions">
                <button type="button" aria-pressed={workspace === Workspace.Worktree} onClick={() => { setWorkspace(Workspace.Worktree); setMachine(""); setStarting([]); }}>{copy("new-session.useSeparateWorktrees_5cd0b6")}</button>
                <button type="button"  aria-pressed={workspace === Workspace.Local} onClick={() => { setWorkspace(Workspace.Local); setStarting([]); }}>{copy("new-session.useThisComputerSLocalCheckouts_eadaad")}</button>
              </div>
              {workspace === Workspace.Local ? <p>{copy("new-session.localUsesThePairedWorkerAnd_ea38f6")}</p> : <StartingReferences key={project} project={project} starting={starting} change={setStarting} active={active} />}
            </> : !generalChat ? <p>{copy("new-session.generalChatUsesAPrivateProjectless_64e0ee")}</p> : null}
            <details><summary>{copy("new-session.optionalEstimatedCostBudget_e9d798")}</summary><BudgetFields draft={budget} change={setBudget} /><p>{copy("new-session.thisIsACeilingAgainstKnown_0260b5")}</p></details>
          </section> : null}
        </fieldset>
      </form>
      {restrictions.configured === true && items(restrictions.ids).length === 0 ? <p role="alert">{copy("new-session.thisProjectExplicitlyAllowsNoAgent_7e04ae")}</p> : null}
      {!automaticTitles ? <div className="notice" role="status"><strong>{copy("new-session.automaticSessionTitlesAreUnavailable_807e33")}</strong><p>{copy("new-session.updateTheDelidevServerAndConnect_039a1d")}</p><button type="button" onClick={openSettings}>{copy("new-session.openSettings_3f9401")}</button><Problem error={status.error} /></div> : null}
      {budgetProblem ? <p role="alert">{budgetProblem}</p> : null}
      {promptLimit ? <p role="alert">{copy("new-session.theFirstMessageExceeds256Kib_9ced04")}</p> : null}

      <Problem error={selectedProject.error || mutation.error} />
      {mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>{copy("new-session.retryTheSameSessionCreation_c70ddb")}</button> : null}
      {invalidAcknowledgment ? <div className="problem" role="alert"><strong>{copy("new-session.creationWasAcknowledgedWithoutAReadable_4432e2")}</strong><p>{copy("new-session.yourMessageIsRetainedRefreshThe_295fd7")}</p><button type="button" onClick={back}>{copy("new-session.inspectSessions_aa8dcc")}</button></div> : null}
      {createdElsewhere ? <p className="new-session-open-notice" role="status"><LocalizedText id={generalChat ? "new-session.conversationCreatedAs" : "new-session.sessionCreatedAs_362cd1"} components={{ s0: <>{title}</> }} /><button type="button" onClick={() => open(createdElsewhere.id)}>{generalChat ? copy("new-session.openConversation") : copy("new-session.openSession_b205bb")}</button></p> : null}
    </div>
  </section>;
}
