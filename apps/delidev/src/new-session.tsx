import { useProjectPromptHistory } from "./project-prompt-history";
import { useSkillCompletion } from "./skill-completion";
import { acknowledgeImages } from "./image-input";
import { ImageAttachmentInput, imageEntryHandlers } from "./image-attachments";
import { useImageDraft, useImageRoute } from "./image-drafts";
import { productError, ownedMessage, useProductMessage, LocalizedText, copy, useLocale  } from "./localization";
import { useCallback, useEffect, useLayoutEffect, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { EntityKind, ResourceService, ResourceQuery, SessionQuery, SystemQuery, SystemCapability, newRequestId, type Resource, WorkerCapability, supportsResourceSchema } from "@delinoio/delidev-api-client";
import { creationPreferenceProblemMessage, useCreationPreferences, type CreationPreferenceBridge, type CreationPreferenceScope } from "./session-creation-preferences";
import { BudgetFields, budgetInput, emptyBudget } from "./session-budget";
import { useGeneralChatPlacement } from "./general-chat-placement";
import { document, encode, items, Mode, object, text, Workspace } from "./documents";
import { ResourceChoice } from "./configuration-fields";
import { StartingBranches } from "./starting-branches";
import { StartingReferences } from "./schedules";
import { useRetainedMutation } from "./mutation";
import { useLocalWorkerProof, type ReadLocalWorkerProof } from "./local-worker";
import { Problem } from "./ui";
import { Surface } from "./surface";
import { useShortcuts } from "./shortcut-provider";
import { ShortcutExecution, ShortcutId, ShortcutInput } from "./shortcuts";

// A failed connection cannot prove that an existing choice became ineligible.
// Explicit denial/missing responses can; successful reads validate the resource.
function eligibilityReadSettled(fetching: boolean, error: unknown) {
  return !fetching && (!error || [Code.PermissionDenied, Code.Unauthenticated, Code.NotFound].includes(ConnectError.from(error).code));
}

const UUID_V7 = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

export enum NewSessionKind { Session = "session", GeneralChat = "general-chat" }

function sessionResource(resource?: Resource): resource is Resource {
  return Boolean(resource && resource.kind === EntityKind.SESSION && resource.schemaVersion === 1 && resource.revision > 0n && UUID_V7.test(resource.id));
}

export function NewSession({ kind = NewSessionKind.Session, active, ownsActivation, activation, entryProjectId, projectSelectionBlockedChanged, readLocalWorker, preferenceBridge, preferenceScope, back, openSettings, open, created }: {
  kind?: NewSessionKind;
  active: boolean;
  ownsActivation: boolean;
  activation: number;
  entryProjectId?: string;
  projectSelectionBlockedChanged?: (blocked: boolean) => void;
  readLocalWorker?: ReadLocalWorkerProof;
  preferenceBridge?: CreationPreferenceBridge;
  preferenceScope?: CreationPreferenceScope;
  back: () => void;
  openSettings: () => void;
  open: (id: string) => void;
  created: () => void;
}) {
  const language = useLocale();
  const generalChat = kind === NewSessionKind.GeneralChat;
  const placement = useGeneralChatPlacement(generalChat && active, language);
  const idPrefix = generalChat ? "new-general-chat" : "new-session";
  const images = useImageDraft(idPrefix);
  const local = useLocalWorkerProof(readLocalWorker);
  const transport = useTransport();
  const [defaultProblem, setDefaultProblem] = useState<unknown>();
  const localIdentity = useRef<Promise<string>>(undefined);
  const preferences = useCreationPreferences(kind, active, preferenceBridge, preferenceScope);
  const touched = useRef(false);
  const restoration = useRef({ agent: false, machine: false });
  const [workspace, setWorkspace] = useState(Workspace.GeneralChat);
  const [project, setProject] = useState("");
  const [agent, setAgent] = useState("");
  const [machine, setMachine] = useState("");
  const [localDefaultID, setLocalDefaultID] = useState("");
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
  const accepted = useCallback((result: { change?: { session?: Resource } }, request: { documentJson: Uint8Array; requestId: string; attachments: import("@delinoio/delidev-api-client").ImageAttachment[] }) => {
    const session = result.change?.session;
    if (!sessionResource(session)) {
      setInvalidAcknowledgment(true);
      return;
    }
    // The original retained request, including receipt retries, owns history.
    const submitted = JSON.parse(new TextDecoder().decode(request.documentJson));
    if (UUID_V7.test(submitted.agent_id) && UUID_V7.test(submitted.machine_id)) preferences.remember({ agent_id: submitted.agent_id, machine_id: submitted.machine_id });
    images.controller.accepted(request.requestId, request.attachments.map(image => image.id));
    endHistoryCycle.current();
    setPrompt("");
    skills.clearAccepted();
    setCreatedElsewhere(undefined);
    created();
    if (navigation.current.ownsActivation && navigation.current.activation === submittedActivation.current) {
      open(session.id);
    } else {
      setCreatedElsewhere(session);
    }
  }, [created, open, preferences.remember]);
  const submittedActivation = useRef(-1);
  const mutation = useRetainedMutation(generalChat ? "create-general-chat" : "create-session", SessionQuery.createSession, accepted);
  useEffect(() => { if (mutation.error && !mutation.uncertain && !mutation.busy) images.controller.operationId = undefined; }, [mutation.error, mutation.uncertain, mutation.busy, images.controller]);
  const restrictions = object(document(selectedProject.data?.resource).agents);
  const blocked = mutation.busy || mutation.uncertain || local.busy || invalidAcknowledgment || images.busy;
  const projectChanged = useCallback((id: string) => {
    touched.current = false;
    restoration.current = { agent: false, machine: false };
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

  const rememberedAgent = useQuery(ResourceQuery.getResource, { kind: EntityKind.AGENT, id: preferences.pair?.agent_id ?? "" }, { enabled: active && Boolean(preferences.pair?.agent_id), retry: false });
  const rememberedMachine = useQuery(ResourceQuery.getResource, { kind: EntityKind.MACHINE, id: preferences.pair?.machine_id ?? "" }, { enabled: active && Boolean(preferences.pair?.machine_id), retry: false });
  const agentChoice = rememberedAgent.data?.resource;
  const machineChoice = rememberedMachine.data?.resource;
  const restorationBlocked = useRef(false);
  restorationBlocked.current = blocked;
  const projectEligible = !project || !selectedProject.isFetching && !selectedProject.error && selectedProject.data?.resource?.id === project && selectedProject.data.resource.kind === EntityKind.PROJECT && supportsResourceSchema(selectedProject.data.resource);
  const agentEligible = projectEligible && !rememberedAgent.isFetching && !rememberedAgent.error && agentChoice?.id === preferences.pair?.agent_id && agentChoice?.kind === EntityKind.AGENT && supportsResourceSchema(agentChoice) && agentChoice.revision > 0n && document(agentChoice).disabled !== true && document(agentChoice).enabled !== false && document(agentChoice).reconfiguration_required !== true && (restrictions.configured !== true || items(restrictions.ids).includes(agentChoice.id));
  const machineCapabilities = items(document(machineChoice).worker_capabilities);
  const machineEligible = projectEligible && !rememberedMachine.isFetching && !rememberedMachine.error && machineChoice?.id === preferences.pair?.machine_id && machineChoice?.kind === EntityKind.MACHINE && supportsResourceSchema(machineChoice) && machineChoice.revision > 0n && document(machineChoice).disabled !== true && document(machineChoice).enabled !== false && (!project || workspace !== Workspace.Worktree || items(document(selectedProject.data?.resource).repositories).length === 0 || machineCapabilities.includes("remote-workspace-clone-v1") || machineCapabilities.includes(WorkerCapability.REMOTE_WORKSPACE_CLONE_V1));
  useEffect(() => {
    if (!active || restorationBlocked.current || !preferences.pair) return;
    // Automatic ownership is independent per field. Draft edits stop restoration,
    // but only a manual choice releases that field from eligibility revalidation.
    if (restoration.current.agent && eligibilityReadSettled(rememberedAgent.isFetching, rememberedAgent.error) && (!project || eligibilityReadSettled(selectedProject.isFetching, selectedProject.error)) && !agentEligible) setAgent(value => value === preferences.pair?.agent_id ? "" : value);
    if (restoration.current.machine && workspace !== Workspace.Local && eligibilityReadSettled(rememberedMachine.isFetching, rememberedMachine.error) && (!project || eligibilityReadSettled(selectedProject.isFetching, selectedProject.error)) && !machineEligible) setMachine(value => value === preferences.pair?.machine_id ? "" : value);
    if (touched.current || !projectEligible) return;
    if (!restoration.current.agent && agentEligible) { restoration.current.agent = true; setAgent(agentChoice!.id); }
    if (!restoration.current.machine && workspace !== Workspace.Local && machineEligible) { restoration.current.machine = true; setMachine(machineChoice!.id); }
  }, [active, preferences.pair, agentChoice, machineChoice, agentEligible, machineEligible, projectEligible, rememberedAgent.isFetching, rememberedAgent.error, rememberedMachine.isFetching, rememberedMachine.error, selectedProject.isFetching, selectedProject.error, project, workspace, blocked]);
  const editAgent = (id: string) => { touched.current = true; restoration.current.agent = false; setAgent(id); };
  const editMachine = (id: string) => { touched.current = true; restoration.current.machine = false; setLocalDefaultID(""); setMachine(id); };

  useEffect(() => {
    if (!active || blocked || touched.current || machine || workspace === Workspace.Local || !projectEligible || preferences.reading || !readLocalWorker || preferences.pair && (!eligibilityReadSettled(rememberedMachine.isFetching, rememberedMachine.error) || machineEligible)) return;
    let disposed = false;
    const originalProject = project;
    localIdentity.current ??= readLocalWorker().then(proof => proof.machineId);
    void localIdentity.current.then(async machineId => {
      if (disposed || touched.current || restorationBlocked.current) return;
      // This metadata default does not adopt the native proof or change modes.
      const result = await createClient(ResourceService, transport).getResource({ kind: EntityKind.MACHINE, id: machineId });
      if (disposed || touched.current || restorationBlocked.current) return;
      const row = result.resource, data = document(row), capabilities = items(data.worker_capabilities);
      if (row?.id === machineId && row.kind === EntityKind.MACHINE && row.revision > 0n && supportsResourceSchema(row) && data.disabled !== true && data.enabled !== false && (!originalProject || workspace !== Workspace.Worktree || !items(document(selectedProject.data?.resource).repositories).length || capabilities.includes("remote-workspace-clone-v1") || capabilities.includes(WorkerCapability.REMOTE_WORKSPACE_CLONE_V1))) { setLocalDefaultID(row.id); setMachine(row.id); }
    }).catch(error => { if (!disposed && !touched.current) setDefaultProblem(error); });
    return () => { disposed = true; };
  }, [active, blocked, machine, workspace, project, projectEligible, preferences.reading, preferences.pair, rememberedMachine.isFetching, rememberedMachine.error, machineEligible, readLocalWorker, transport]);
  const localDefault = useQuery(ResourceQuery.getResource, { kind: EntityKind.MACHINE, id: localDefaultID }, { enabled: active && Boolean(localDefaultID) && machine === localDefaultID, retry: false });
  const localDefaultRow = localDefault.data?.resource, localDefaultData = document(localDefaultRow), localDefaultCapabilities = items(localDefaultData.worker_capabilities);
  const localDefaultEligible = !localDefault.isFetching && !localDefault.error && localDefaultRow?.id === localDefaultID && localDefaultRow.kind === EntityKind.MACHINE && localDefaultRow.revision > 0n && supportsResourceSchema(localDefaultRow) && localDefaultData.disabled !== true && localDefaultData.enabled !== false && projectEligible && (!project || workspace !== Workspace.Worktree || !items(document(selectedProject.data?.resource).repositories).length || localDefaultCapabilities.includes("remote-workspace-clone-v1") || localDefaultCapabilities.includes(WorkerCapability.REMOTE_WORKSPACE_CLONE_V1));
  useEffect(() => { if (!blocked && workspace !== Workspace.Local && machine === localDefaultID && localDefaultID && eligibilityReadSettled(localDefault.isFetching, localDefault.error) && projectEligible && !localDefaultEligible) { setMachine(""); setLocalDefaultID(""); } }, [blocked, workspace, machine, localDefaultID, localDefault.isFetching, localDefault.error, projectEligible, localDefaultEligible]);
  const automaticChoicesEligible = (!localDefaultID || machine !== localDefaultID || workspace === Workspace.Local || localDefaultEligible) && (!restoration.current.agent || agent !== preferences.pair?.agent_id || agentEligible) && (!restoration.current.machine || workspace === Workspace.Local || machine !== preferences.pair?.machine_id || machineEligible);
  const historyEditing = useRef(false);
  const endHistoryCycle = useRef<() => void>(() => {});
  const updatePrompt = (value: string) => {
    if (!historyEditing.current) endHistoryCycle.current();
    if (new TextEncoder().encode(value).byteLength > (256 << 10)) {
      setPromptLimit(true);
      return false;
    }
    touched.current = true;
    setPrompt(value);
    setPromptLimit(false);
    return true;
  };
  const skills = useSkillCompletion({ value: prompt, change: updatePrompt, textarea: firstMessage, machineId: machine, agentId: agent, projectId: project, active, disabled: blocked, enabled: status.data?.capabilities.includes(SystemCapability.NATIVE_SKILLS_V1) ?? false });
  const promptHistory = useProjectPromptHistory({ projectId: generalChat ? "" : project, enabled: status.data?.capabilities.includes(SystemCapability.PROJECT_PROMPT_HISTORY_V1) ?? false, active, blocked, textarea: firstMessage, replace: (value, caret) => { historyEditing.current = true; try { skills.replaceUnbound(value, caret); } finally { historyEditing.current = false; } } });
  endHistoryCycle.current = promptHistory.onEdit;
  const imageRoute = useImageRoute(machine, agent, active, images.images.length > 0);
  const canCreate = active && automaticTitles && Boolean(agent && machine && (prompt.trim() || images.images.length)) && (!images.images.length || imageRoute.ready) && !blocked && !skills.blocked && automaticChoicesEligible;

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
    const proof = workspaceType === Workspace.Local ? await local.load(machine) : undefined;
    if (workspaceType === Workspace.Local && !proof) return;
    touched.current = true;
    submittedActivation.current = navigation.current.activation;
    const requestId = images.images.length ? images.controller.operationId ?? newRequestId() : newRequestId();
    let attachments;
    try { attachments = images.images.length && imageRoute.machine ? await images.controller.prepare(imageRoute.machine, requestId) : []; } catch { return; }
    void mutation.send({ requestId, documentJson: encode(selection), localWorkerToken: proof?.token, skills: skills.selections.length ? { selections: skills.selections } : undefined, attachments }, attachments.length ? (result, request) => acknowledgeImages(result.change, request.requestId, request.attachments) : undefined);
  };

  const shortcutScope = generalChat ? Surface.NewGeneralChat : Surface.NewSession;
  const shortcuts = useShortcuts([
    { id: ShortcutId.NewSessionFocus, scope: shortcutScope, active, label: "shortcuts.focusFirstMessage", bindings: [{ key: "i", primary: true }], input: ShortcutInput.Allow, enabled: !blocked, unavailableReason: "shortcuts.pending", run: () => firstMessage.current?.focus() },
    { id: ShortcutId.NewSessionSend, scope: shortcutScope, active, label: "shortcuts.createSession", bindings: [{ key: "Enter" }, { key: "Enter", primary: true }], target: firstMessage, input: ShortcutInput.Target, enabled: canCreate, unavailableReason: blocked ? "shortcuts.pending" : !automaticTitles ? "shortcuts.updateServer" : "shortcuts.creationRequired", run: () => firstMessage.current?.form?.requestSubmit() },
    { id: ShortcutId.NewSessionNewline, scope: shortcutScope, active, label: "shortcuts.newline", bindings: [{ key: "Enter", shift: true }], target: firstMessage, input: ShortcutInput.Target, execution: ShortcutExecution.Native, enabled: !blocked, unavailableReason: "shortcuts.pending" },
  ]);

  const submitForm = (event: FormEvent) => {
    event.preventDefault();
    void submit();
  };
  const title = createdElsewhere ? text(document(createdElsewhere).name) || copy("new-session.extra.cffdba22adf2") : copy("new-session.extra.cffdba22adf2");
  const submitLabel = generalChat ? copy("new-session.startGeneralChat") : copy("new-session.createSession_38b6ef");

  return <section ref={placement.page} hidden={!active} className={`new-session-page${generalChat ? " new-general-chat-page" : ""}`} aria-labelledby={`${idPrefix}-heading`}>
    <div className="new-session-content" ref={placement.content}>
      <header className="new-session-header" ref={placement.heading}>
        <button type="button" className="new-session-back" onClick={back}>{copy("new-session.backToSessions_3740d2")}</button>
        <h2 id={`${idPrefix}-heading`}>{generalChat ? copy("new-session.whatWouldYouLikeToTalkAbout") : copy("new-session.whatWouldYouLikeToWork_3c9309")}</h2>
        {generalChat ? <p className="new-general-chat-description">{copy("new-session.generalChatDescription")}</p> : null}
      </header>
      {!generalChat ? <ResourceChoice label={copy("new-session.project_985959")} kind={EntityKind.PROJECT} value={project} active={active} showStatus disabled={blocked} change={projectChanged} /> : null}
      {!generalChat && !project ? <p className="new-session-project-note">{copy("new-session.generalChatIsolatedProjectlessDirectoryOn_aac210")}</p> : null}
      <form {...imageEntryHandlers(images, blocked || !imageRoute.systemSupported)} onSubmit={submitForm}>
        <fieldset className="new-session-fieldset" disabled={blocked}>
          {!generalChat && project ? <div className="new-session-workspace">
            <fieldset className="workspace-mode"><legend>{copy("new-session.workspaceLabel")}</legend>
              <label><input type="radio" name={`${idPrefix}-workspace`} checked={workspace === Workspace.Worktree} onChange={() => { touched.current = true; setWorkspace(Workspace.Worktree); setMachine(""); setStarting([]); }} />{copy("new-session.worktreeMode")}</label>
              <label><input type="radio" name={`${idPrefix}-workspace`} disabled={!local.available} checked={workspace === Workspace.Local} onChange={() => { touched.current = true; void local.load().then(proof => { if (proof) { setWorkspace(Workspace.Local); setMachine(proof.machineId); setStarting([]); } }); }} />{copy("new-session.localMode")}</label>
            </fieldset>
            {workspace === Workspace.Worktree && selectedProject.data?.resource ? <StartingBranches key={project} project={selectedProject.data.resource} machineId={machine} starting={starting} active={active && !blocked} supported={status.data?.capabilities.includes(SystemCapability.REPOSITORY_BRANCH_DISCOVERY_V1) ?? false} change={value => { touched.current = true; setStarting(value); }} /> : null}
          </div> : null}
          <div className="new-session-composer" ref={placement.composer}>
            <label className="new-session-message-label" htmlFor={`${idPrefix}-message`}>{copy("new-session.firstMessage_ecffa2")}</label>
            <textarea
              ref={firstMessage}
              id={`${idPrefix}-message`}
              name="first-message"
              aria-label={copy("new-session.firstMessage_ecffa2")}
              aria-describedby={!generalChat && project && status.data?.capabilities.includes(SystemCapability.PROJECT_PROMPT_HISTORY_V1) ? "project-prompt-history-guidance" : undefined}
              placeholder={generalChat ? copy("new-session.generalChatPlaceholder") : copy("new-session.describeATaskAskAQuestion_4ed4ad")}
              value={prompt}
              onChange={(event) => skills.onChange(event.target.value,event.target.selectionStart)}
              onSelect={skills.onSelect} onCompositionStart={() => { skills.onCompositionStart(); promptHistory.onCompositionStart(); }} onCompositionEnd={() => { skills.onCompositionEnd(); promptHistory.onCompositionEnd(); }} {...skills.attributes}
              onKeyDown={event => { if (!skills.onKeyDown(event) && !promptHistory.onKeyDown(event) && !event.nativeEvent.isComposing) shortcuts.onKeyDown(event); }}
              aria-keyshortcuts={shortcuts.aria(ShortcutId.NewSessionFocus, ShortcutId.NewSessionSend, ShortcutId.NewSessionNewline)}
              rows={5}
              required={!images.images.length}
              autoComplete="off"
            />
            {skills.list}{skills.warning}{promptHistory.feedback}
            <ImageAttachmentInput draft={images} disabled={blocked} available={imageRoute.systemSupported} routeReady={imageRoute.ready} routeLoading={imageRoute.loading} machineId={machine} />
            <div className="new-session-toolbar">
              <div className="new-session-selectors">
                <ResourceChoice label={copy("new-session.agentWorker_a4caa7")} kind={EntityKind.AGENT} value={agent} active={active} showStatus required allowed={restrictions.configured === true ? items(restrictions.ids) : undefined} resolvedChoice={agentChoice} change={editAgent} />
                <ResourceChoice label={copy("new-session.runsOn_88a550")} resourceLabel={copy("new-session.runnerDevice_37efe3")} kind={EntityKind.MACHINE} value={machine} active={active} showStatus disabled={Boolean(project) && workspace === Workspace.Local} required resolvedChoice={machineChoice} change={editMachine} />
                <label className="new-session-mode plan-mode"><input type="checkbox" checked={mode === Mode.Plan} onChange={(event) => { touched.current = true; setMode(event.target.checked ? Mode.Plan : Mode.Execute); }} />{copy("new-session.planMode")}</label>
              </div>
              <div className="new-session-submit-row">
                <button type="button" className="new-session-options-toggle" aria-expanded={optionsOpen} onClick={() => setOptionsOpen((value) => !value)}>{copy("new-session.options_d0db8b")}</button>
                <button className="new-session-submit" type="submit" aria-keyshortcuts={shortcuts.aria(ShortcutId.NewSessionSend)} aria-label={submitLabel} title={submitLabel} disabled={!canCreate}>
                  <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 19V5M6 11l6-6 6 6" /></svg>
                </button>
              </div>
            </div>
          </div>
          <div className="new-session-hints" ref={placement.hints}><span>{generalChat ? copy("new-session.conversationsAreNamedAutomatically") : copy("new-session.sessionsAreNamedAutomatically_ba66e8")}</span><span>{copy("new-session.shiftEnterForANewLine_5e4b35")}</span></div>
          {optionsOpen ? <section className="new-session-options" aria-label={copy("new-session.sessionOptions_0bccf5")}>
            {!generalChat && project ? <>
              <p>{copy("new-session.aSeparateDetachedWorktreeIsPrepared_8c300d")}</p>
              {workspace === Workspace.Local ? <p>{copy("new-session.localUsesThePairedWorkerAnd_ea38f6")}</p> : <StartingReferences key={project} project={project} starting={starting} change={(value) => { touched.current = true; setStarting(value); }} active={active} />}
            </> : !generalChat ? <p>{copy("new-session.generalChatUsesAPrivateProjectless_64e0ee")}</p> : null}
            {generalChat ? <>
              <h3>{copy("new-session.optionalEstimatedCostBudget_e9d798")}</h3>
              <BudgetFields draft={budget} change={(value) => { touched.current = true; setBudget(value); }} />
              <p>{copy("new-session.thisIsACeilingAgainstKnown_0260b5")}</p>
            </> : <details><summary>{copy("new-session.optionalEstimatedCostBudget_e9d798")}</summary><BudgetFields draft={budget} change={(value) => { touched.current = true; setBudget(value); }} /><p>{copy("new-session.thisIsACeilingAgainstKnown_0260b5")}</p></details>}
          </section> : null}
        </fieldset>
      </form>
      {restrictions.configured === true && items(restrictions.ids).length === 0 ? <p role="alert">{copy("new-session.thisProjectExplicitlyAllowsNoAgent_7e04ae")}</p> : null}
      {!automaticTitles ? <div className="notice" role="status"><strong>{copy("new-session.automaticSessionTitlesAreUnavailable_807e33")}</strong><p>{copy("new-session.updateTheDelidevServerAndConnect_039a1d")}</p><button type="button" onClick={openSettings}>{copy("new-session.openSettings_3f9401")}</button><Problem error={status.error} /></div> : null}
      {preferences.pair && (rememberedAgent.isFetching || rememberedMachine.isFetching) ? <p role="status">{copy("new-session.preferencesResolving")}</p> : null}
      {preferences.reading ? <p role="status">{copy("new-session.preferencesReading")}</p> : null}
      {preferences.problem ? <div role="alert"><p>{copy("new-session.preferencesProblem", { v0: creationPreferenceProblemMessage(preferences.problem) })}</p><button type="button" disabled={preferences.reading} onClick={() => void preferences.reinspect()}>{copy("new-session.preferencesInspect")}</button></div> : null}
      {preferences.canRetry ? <button type="button" onClick={preferences.retrySave}>{copy("new-session.preferencesSave")}</button> : null}
      <Problem error={defaultProblem || localDefault.error} /><Problem error={rememberedAgent.error || rememberedMachine.error} />
      {budgetProblem ? <p role="alert">{budgetProblem}</p> : null}
      {promptLimit ? <p role="alert">{copy("new-session.theFirstMessageExceeds256Kib_9ced04")}</p> : null}
      {local.problem ? <p role="alert">{local.problem}</p> : null}
      <Problem error={selectedProject.error || mutation.error} />
      {mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>{copy("new-session.retryTheSameSessionCreation_c70ddb")}</button> : null}
      {invalidAcknowledgment ? <div className="problem" role="alert"><strong>{copy("new-session.creationWasAcknowledgedWithoutAReadable_4432e2")}</strong><p>{copy("new-session.yourMessageIsRetainedRefreshThe_295fd7")}</p><button type="button" onClick={back}>{copy("new-session.inspectSessions_aa8dcc")}</button></div> : null}
      {createdElsewhere ? <p className="new-session-open-notice" role="status"><LocalizedText id={generalChat ? "new-session.conversationCreatedAs" : "new-session.sessionCreatedAs_362cd1"} components={{ s0: <>{title}</> }} /><button type="button" onClick={() => open(createdElsewhere.id)}>{generalChat ? copy("new-session.openConversation") : copy("new-session.openSession_b205bb")}</button></p> : null}
    </div>
  </section>;
}
