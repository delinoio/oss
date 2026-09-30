import { useCallback, useEffect, useRef, useState, type FormEvent, type KeyboardEvent } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, SessionQuery, SystemQuery, SystemCapability, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { BudgetFields, budgetInput, emptyBudget } from "./session-budget";
import { document, encode, items, Mode, object, text, Workspace } from "./documents";
import { ResourceChoice } from "./configuration-fields";
import { StartingReferences } from "./schedules";
import { useRetainedMutation } from "./mutation";
import { useLocalWorkerProof, type ReadLocalWorkerProof } from "./local-worker";
import { Problem } from "./ui";

const UUID_V7 = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

function sessionResource(resource?: Resource): resource is Resource {
  return Boolean(resource && resource.kind === EntityKind.SESSION && resource.schemaVersion === 1 && resource.revision > 0n && UUID_V7.test(resource.id));
}

export function NewSession({ active, ownsActivation, activation, readLocalWorker, back, openSettings, open, created }: {
  active: boolean;
  ownsActivation: boolean;
  activation: number;
  readLocalWorker?: ReadLocalWorkerProof;
  back: () => void;
  openSettings: () => void;
  open: (id: string) => void;
  created: () => void;
}) {
  const local = useLocalWorkerProof(readLocalWorker);
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
  const [budgetProblem, setBudgetProblem] = useState("");
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
  const selectedProject = useQuery(ResourceQuery.getResource, { kind: EntityKind.PROJECT, id: project }, { enabled: active && Boolean(project) });
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
  const mutation = useRetainedMutation("create-session", SessionQuery.createSession, accepted);
  const restrictions = object(document(selectedProject.data?.resource).agents);
  const blocked = mutation.busy || mutation.uncertain || local.busy || invalidAcknowledgment;

  const submit = async () => {
    if (blocked || !automaticTitles) return;
    let estimatedBudget;
    try {
      estimatedBudget = budgetInput(budget);
      setBudgetProblem("");
    } catch (error) {
      setBudgetProblem(error instanceof Error ? error.message : "Review the budget.");
      return;
    }
    const workspaceType = project ? workspace : Workspace.GeneralChat;
    const selection = {
      name_mode: "automatic",
      estimated_cost_budget: estimatedBudget,
      prompt,
      agent_id: agent,
      machine_id: machine,
      project_id: project || undefined,
      workspace: workspaceType,
      starting: project && workspaceType === Workspace.Worktree ? starting : undefined,
      mode,
      source: "MANUAL",
    };
    const proof = workspaceType === Workspace.Local ? await local.load(machine) : undefined;
    if (workspaceType === Workspace.Local && !proof) return;
    submittedActivation.current = navigation.current.activation;
    void mutation.send({ requestId: newRequestId(), documentJson: encode(selection), localWorkerToken: proof?.token });
  };

  const enter = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key !== "Enter" || event.shiftKey || event.nativeEvent.isComposing || event.nativeEvent.keyCode === 229) return;
    event.preventDefault();
    event.currentTarget.form?.requestSubmit();
  };
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
  const projectChanged = (id: string) => {
    setProject(id);
    setAgent("");
    setMachine("");
    setStarting([]);
    setWorkspace(id ? Workspace.Worktree : Workspace.GeneralChat);
  };
  const title = createdElsewhere ? text(document(createdElsewhere).name) || "New session" : "New session";

  return <section hidden={!active} className="new-session-page" aria-labelledby="new-session-heading">
    <div className="new-session-content">
      <header className="new-session-header">
        <button type="button" className="new-session-back" onClick={back}>Back to sessions</button>
        <h2 id="new-session-heading">What would you like to work on?</h2>
      </header>
      <ResourceChoice label="Project" kind={EntityKind.PROJECT} value={project} active={active} showStatus disabled={blocked} change={projectChanged} />
      {!project ? <p className="new-session-project-note">General Chat · isolated projectless directory on the selected Worker</p> : null}
      <form onSubmit={submitForm}>
        <fieldset className="new-session-fieldset" disabled={blocked}>
          <div className="new-session-composer">
            <label className="new-session-message-label" htmlFor="new-session-message">First message</label>
            <textarea
              ref={firstMessage}
              id="new-session-message"
              name="first-message"
              aria-label="First message"
              placeholder="Describe a task, ask a question, or share an idea…"
              value={prompt}
              onChange={(event) => updatePrompt(event.target.value)}
              onKeyDown={enter}
              rows={5}
              required
              autoComplete="off"
            />
            <div className="new-session-toolbar">
              <div className="new-session-selectors">
                <ResourceChoice label="Agent Worker" kind={EntityKind.AGENT} value={agent} active={active} showStatus required allowed={restrictions.configured === true ? items(restrictions.ids) : undefined} change={setAgent} />
                <ResourceChoice label="Runs on" resourceLabel="Runner Device" kind={EntityKind.MACHINE} value={machine} active={active} showStatus disabled={Boolean(project) && workspace === Workspace.Local} required change={setMachine} />
                <label className="new-session-mode">Mode<select value={mode} onChange={(event) => setMode(event.target.value as Mode)}><option value={Mode.Execute}>Execute</option><option value={Mode.Plan}>Plan</option></select></label>
              </div>
              <div className="new-session-submit-row">
                <button type="button" className="new-session-options-toggle" aria-expanded={optionsOpen} onClick={() => setOptionsOpen((value) => !value)}>Options</button>
                <button className="new-session-submit" type="submit" aria-label="Create session" title="Create session" disabled={!automaticTitles || !agent || !machine || !prompt.trim() || blocked}>
                  <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 19V5M6 11l6-6 6 6" /></svg>
                </button>
              </div>
            </div>
          </div>
          <div className="new-session-hints"><span>Sessions are named automatically.</span><span>Shift + Enter for a new line</span></div>
          {optionsOpen ? <section className="new-session-options" aria-label="Session options">
            {project ? <>
              <p>A separate detached worktree is prepared for every project repository. Choose Local to use this computer's existing checkouts as-is.</p>
              <div className="actions">
                <button type="button" aria-pressed={workspace === Workspace.Worktree} onClick={() => { setWorkspace(Workspace.Worktree); setMachine(""); setStarting([]); }}>Use separate Worktrees</button>
                <button type="button" disabled={!local.available} aria-pressed={workspace === Workspace.Local} onClick={() => { void local.load().then((proof) => { if (proof) { setWorkspace(Workspace.Local); setMachine(proof.machineId); setStarting([]); } }); }}>Use this computer's Local checkouts</button>
              </div>
              {workspace === Workspace.Local ? <p>Local uses the paired Worker and shares its original checkouts, current branches and uncommitted changes. Creation revalidates the Worker proof.</p> : <StartingReferences key={project} project={project} starting={starting} change={setStarting} active={active} />}
            </> : <p>General Chat uses a private projectless directory on the selected Worker. Git references are unavailable.</p>}
            <details><summary>Optional estimated-cost budget</summary><BudgetFields draft={budget} change={setBudget} /><p>This is a ceiling against known historical token-price estimates. Missing usage or pricing remains unavailable; it is not a billing guarantee.</p></details>
          </section> : null}
        </fieldset>
      </form>
      {restrictions.configured === true && items(restrictions.ids).length === 0 ? <p role="alert">This project explicitly allows no Agent Workers. Update its restrictions before creating a session.</p> : null}
      {!automaticTitles ? <div className="notice" role="status"><strong>Automatic session titles are unavailable.</strong><p>Update the DeliDev server and connect a Worker with the verified Codex title capability. Your message and selections stay in this page.</p><button type="button" onClick={openSettings}>Open Settings</button><Problem error={status.error} /></div> : null}
      {budgetProblem ? <p role="alert">{budgetProblem}</p> : null}
      {promptLimit ? <p role="alert">The first message exceeds 256 KiB. The previous valid draft is retained.</p> : null}
      {local.problem ? <p role="alert">{local.problem}</p> : null}
      <Problem error={selectedProject.error || mutation.error} />
      {mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>Retry the same session creation</button> : null}
      {invalidAcknowledgment ? <div className="problem" role="alert"><strong>Creation was acknowledged without a readable session identity.</strong><p>Your message is retained. Refresh the session list and inspect the original operation before creating another session.</p><button type="button" onClick={back}>Inspect sessions</button></div> : null}
      {createdElsewhere ? <p className="new-session-open-notice" role="status">Session created as “{title}”. <button type="button" onClick={() => open(createdElsewhere.id)}>Open session</button></p> : null}
    </div>
  </section>;
}
