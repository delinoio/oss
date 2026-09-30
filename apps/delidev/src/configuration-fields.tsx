import { defaultRemediationPolicy, RemediationPolicyFields } from "./remediation-policy";
import { useContext, useEffect, useState } from "react";
import { AgentConfiguration, AgentReadProblem } from "./agent-configuration";
import { Code, ConnectError } from "@connectrpc/connect";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ProviderQuery, ResourceQuery, WorkerQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, items, object, resourceName, text, type Document } from "./documents";
import { Problem } from "./ui";
import { useRetainedMutation } from "./mutation";
import { JobState, TrackedJob } from "./jobs";
import { providerInventoryReady } from "./provider-model-settings";

export enum Harness { Codex = "codex", Claude = "claude-code", OpenCode = "opencode", Grok = "grok-build" }
export enum Protocol { Responses = "openai-responses", Chat = "openai-chat", Anthropic = "anthropic-messages", Subscription = "native-subscription" }
export enum Authentication { Bearer = "bearer", Key = "api-key", Keyless = "keyless", Subscription = "subscription" }
enum Routing { Fixed = "fixed", Priority = "priority", RoundRobin = "round-robin", Quota = "remaining-quota", Reset = "reset-window", Sequential = "sequential-exhaustion" }
enum Permission { Default = "default", Read = "read-only", Workspace = "workspace-write", Full = "full-access" }
enum ClaudePermission { Default = "default", Plan = "plan", AcceptEdits = "acceptEdits", DontAsk = "dontAsk", Bypass = "bypassPermissions" }
export const editableKinds = [EntityKind.PROVIDER, EntityKind.MODEL, EntityKind.ACCOUNT, EntityKind.AGENT, EntityKind.TEMPLATE, EntityKind.PROJECT, EntityKind.REPOSITORY, EntityKind.SETTINGS];
export const kindNames: Partial<Record<EntityKind, string>> = { [EntityKind.INTEGRATION]: "GitHub profile", [EntityKind.PROVIDER]: "Provider", [EntityKind.MODEL]: "Model", [EntityKind.ACCOUNT]: "AI account", [EntityKind.AGENT]: "Agent Worker", [EntityKind.TEMPLATE]: "Instructions", [EntityKind.PROJECT]: "Project", [EntityKind.MACHINE]: "Runner Device", [EntityKind.REPOSITORY]: "Repository", [EntityKind.SETTINGS]: "Server preferences" };
export function newConfiguration(kind: EntityKind): Document {
  switch (kind) {
    case EntityKind.PROVIDER: return { name: "", endpoint: "", protocol: Protocol.Responses, authentication: Authentication.Bearer, discovery: true, enabled: true };
    case EntityKind.MODEL: return { name: "", provider_id: "", native_id: "", alias: "", harnesses: [], hidden: false, order: 0, manual: true, new: false, metadata_source: "unknown" };
    case EntityKind.ACCOUNT: return { alias: "", provider_id: "", type: "api", enabled: true, exclude_automatic: false, recovery_notifications: true, health: "disconnected", quota: [], confirmed_exhausted: false };
    case EntityKind.AGENT: return { name: "", harness: Harness.Codex, model_id: "", accounts: [], templates: [], options: { permission: Permission.Default } };
    case EntityKind.PROJECT: return { name: "", repositories: [], primary_repository: "", agents: { configured: false, ids: [] }, accounts: { configured: false, ids: [] } };
    case EntityKind.REPOSITORY: return { name: "", checkouts: [], base: {}, starting: {}, auto_fetch: true };
    case EntityKind.TEMPLATE: return { name: "", contents: "" };
    case EntityKind.SETTINGS: return { default_routing: Routing.Sequential, notifications: true, automatic_fetch: true, remediation: defaultRemediationPolicy() };
    default: throw new Error("Unsupported configuration editor");
  }
}
export function TextField({ label, value, change, required = false, max = 256, disabled = false, markRequired = false, placeholder }: { label: string; value: unknown; change: (value: string) => void; required?: boolean; max?: number; disabled?: boolean; markRequired?: boolean; placeholder?: string }) {
  return <label>{markRequired ? <span>{label}<span className="agent-required" aria-hidden="true"> *</span></span> : label}<input aria-label={markRequired ? label : undefined} placeholder={placeholder} value={text(value)} required={required} maxLength={max} disabled={disabled} onChange={(event) => change(event.target.value)} /></label>;
}
function Choice({ label, value, choices, change, disabled = false, inherited = false }: { label: string; value: unknown; choices: readonly string[]; change: (value: string) => void; disabled?: boolean; inherited?: boolean }) {
  const selected = text(value);
  return <label>{label}<select value={selected} disabled={disabled} onChange={(event) => change(event.target.value)}>{inherited ? <option value="">Use server default</option> : null}{selected && !choices.includes(selected) ? <option value={selected}>Unsupported selection · {selected}</option> : null}{choices.map((choice) => <option key={choice} value={choice}>{choice}</option>)}</select></label>;
}

function AgentPermissions({ harness, options, change }: { harness: unknown; options: Document; change: (value: Document) => void }) {
  if (harness === Harness.Claude) {
    const conflict = options.permission !== Permission.Default || Boolean(text(options.approval_policy));
    const selected = options.claude_permission ?? ClaudePermission.Default;
    return <fieldset><legend>Claude tool permissions</legend>
      <Choice label="Claude permission mode" value={selected} choices={Object.values(ClaudePermission)} change={(claude_permission) => change({ ...options, claude_permission })} />
      <p>Plan inputs use Claude's native plan mode. These choices control tool approvals; they do not establish filesystem sandbox isolation.</p>
      {selected === ClaudePermission.AcceptEdits ? <p className="notice">Claude can accept file edits and common filesystem operations without asking.</p> : null}
      {selected === ClaudePermission.DontAsk ? <p>Claude denies operations that would require a permission prompt.</p> : null}
      {selected === ClaudePermission.Bypass ? <p className="notice">Bypass skips native permission prompts. Select it only for an environment you intend to give this access.</p> : null}
      {conflict ? <><p role="alert">Retained sandbox or approval-policy settings do not apply to Claude: {text(options.permission)} · {text(options.approval_policy)}. Clear them explicitly before saving.</p><button type="button" onClick={() => { const next: Document = { ...options, permission: Permission.Default }; delete next.approval_policy; change(next); }}>Clear incompatible permission settings</button></> : null}
      <p>Saving settings does not enable Claude execution; its public execution integration is still unavailable.</p>
    </fieldset>;
  }
  return <>
    <Choice label="Permission mode" value={options.permission} choices={Object.values(Permission)} change={(permission) => change({ ...options, permission })} />
    {options.permission === Permission.Default ? <p>Uses the harness default. Review permissions before execution.</p> : null}
    {options.permission === Permission.Full ? <p className="notice">Full access permits native operations beyond the workspace when supported. Review this scope before using the Agent Worker.</p> : null}
    {options.claude_permission !== undefined ? <><p role="alert">A Claude permission selection is retained: {text(options.claude_permission)}. It cannot be applied to this harness.</p><button type="button" onClick={() => { const next = { ...options }; delete next.claude_permission; change(next); }}>Clear Claude permission selection</button></> : null}
  </>;
}
function Check({ label, value, change }: { label: string; value: unknown; change: (value: boolean) => void }) { return <label className="checkbox"><input type="checkbox" checked={value === true} onChange={(event) => change(event.target.checked)} />{label}</label>; }

// Selectors retain only one bounded page. An already selected identity outside
// that page remains explicit rather than falling back to its first result.
export function ResourceChoice({ label, resourceLabel = label, kind, value, change, active, disabled = false, required = false, allowed, activeApiOnly = false, showStatus = false, markRequired = false }: { label: string; resourceLabel?: string; kind: EntityKind; value: string; change: (id: string, data?: Document, resource?: Resource) => void; active: boolean; disabled?: boolean; required?: boolean; allowed?: readonly unknown[]; activeApiOnly?: boolean; showStatus?: boolean; markRequired?: boolean }) {
  const reportRead = useContext(AgentReadProblem);
  const agentPresentation = Boolean(reportRead) || markRequired;
  const [page, setPage] = useState("");
  const [subscriptionPage, setSubscriptionPage] = useState("");
  const needsProviderCapability = activeApiOnly && (kind === EntityKind.PROVIDER || kind === EntityKind.MODEL);
  const inventory = useQuery(ProviderQuery.listProviderInventory, { query: "", enabledOnly: true, pageSize: 200, pageToken: kind === EntityKind.PROVIDER ? page : "" }, { enabled: active && needsProviderCapability });
  const ready = providerInventoryReady(inventory.data?.capabilities);
  const result = useQuery(ResourceQuery.listResources, { filter: { kind, pageSize: 50, pageToken: kind === EntityKind.PROVIDER && needsProviderCapability ? subscriptionPage : page } }, { enabled: active && (!needsProviderCapability || kind === EntityKind.PROVIDER) });
  const modelSearch = useQuery(ProviderQuery.searchModels, { query: "", providerId: "", includeHidden: true, pageSize: 50, pageToken: page, enabledProvidersOnly: true }, { enabled: active && kind === EntityKind.MODEL && needsProviderCapability && ready });
  const selected = useQuery(ResourceQuery.getResource, { kind, id: value }, { enabled: active && Boolean(value) && needsProviderCapability });
  const selectedData = document(selected.data?.resource);
  const selectedProvider = useQuery(ResourceQuery.getResource, { kind: EntityKind.PROVIDER, id: kind === EntityKind.MODEL ? text(selectedData.provider_id) : value }, { enabled: active && kind === EntityKind.MODEL && Boolean(text(selectedData.provider_id)) });
  const selectedProviderOff = kind === EntityKind.PROVIDER ? selectedData.protocol !== Protocol.Subscription && selectedData.enabled === false : selectedProvider.data?.resource ? document(selectedProvider.data.resource).enabled === false : false;
  const activeProviders = (inventory.data?.entries ?? []).flatMap((entry) => entry.provider ? [entry.provider] : []);
  const subscriptionProviders = kind === EntityKind.PROVIDER && needsProviderCapability ? (result.data?.resources ?? []).filter((row) => document(row).protocol === Protocol.Subscription) : [];
  let rows = kind === EntityKind.MODEL && needsProviderCapability ? (modelSearch.data?.models ?? []) : kind === EntityKind.PROVIDER && needsProviderCapability ? [...activeProviders, ...subscriptionProviders] : (result.data?.resources ?? []);
  rows = rows.filter((row) => !allowed || allowed.includes(row.id));
  const pageRows = rows;
  const selectedOffPage = Boolean(selected.data?.resource && !pageRows.some((row) => row.id === selected.data!.resource!.id));
  if (selected.data?.resource && !rows.some((row) => row.id === selected.data!.resource!.id)) rows = [...rows, selected.data.resource];
  const nextPage = kind === EntityKind.MODEL && needsProviderCapability ? modelSearch.data?.nextPageToken : kind === EntityKind.PROVIDER && needsProviderCapability ? inventory.data?.nextPageToken : result.data?.nextPageToken;
  const fetching = kind === EntityKind.MODEL && needsProviderCapability ? modelSearch.isFetching : kind === EntityKind.PROVIDER && needsProviderCapability ? inventory.isFetching : result.isFetching;
  const pageData = kind === EntityKind.MODEL && needsProviderCapability ? modelSearch.data : kind === EntityKind.PROVIDER && needsProviderCapability ? inventory.data : result.data;
  const pageError = kind === EntityKind.MODEL && needsProviderCapability ? modelSearch.error : kind === EntityKind.PROVIDER && needsProviderCapability ? inventory.error : result.error;
  const pageFailure = pageError ?? (kind === EntityKind.MODEL && needsProviderCapability ? modelSearch.failureReason : kind === EntityKind.PROVIDER && needsProviderCapability ? inventory.failureReason : result.failureReason);
  const failure = pageFailure ?? (agentPresentation && needsProviderCapability ? inventory.error ?? inventory.failureReason ?? selected.error ?? selected.failureReason ?? selectedProvider.error ?? selectedProvider.failureReason : undefined);
  const initialFetching = fetching || (agentPresentation && needsProviderCapability && inventory.isFetching && !ready);
  const readProblem = Boolean(failure || selectedProviderOff || (active && needsProviderCapability && inventory.data && !ready));
  useEffect(() => { reportRead?.(label, readProblem); }, [label, readProblem, reportRead]);
  const errorCode = failure instanceof ConnectError ? failure.code : undefined;
  let statusMessage = "";
  if (active && (showStatus || agentPresentation)) {
    if (failure) {
      const reason = errorCode === Code.PermissionDenied || errorCode === Code.Unauthenticated
        ? `The server denied access to these choices${fetching ? "; retrying." : "."}`
        : errorCode === Code.Unavailable || errorCode === Code.DeadlineExceeded
          ? `The server connection failed while loading these choices${fetching ? "; retrying." : "."}`
          : `The latest request for these choices failed${fetching ? "; retrying." : "."}`;
      statusMessage = pageData
        ? `Showing cached ${resourceLabel} choices. ${reason} Your current selection is retained.`
        : `${reason} Your current selection is retained.`;
    } else if (initialFetching && !pageData) {
      statusMessage = `Loading ${resourceLabel} choices…`;
    } else if (pageData && (agentPresentation ? pageRows : rows).length === 0) {
      statusMessage = nextPage
        ? `No selectable ${resourceLabel} choices are on this page. More choices are available.`
        : `No selectable ${resourceLabel} choices are on this page.`;
    } else if (value && selectedOffPage) {
      statusMessage = `The selected ${resourceLabel} is outside this page. Its exact identity remains selected.`;
    } else if (value && !rows.some((row) => row.id === value)) {
      statusMessage = `The selected ${resourceLabel} is outside this page or unavailable. Its identity is retained; no other choice was selected.`;
    }
  }
  return <div className="resource-choice"><label>{markRequired ? <span>{label}<span className="agent-required" aria-hidden="true"> *</span></span> : label}<select aria-label={markRequired ? label : undefined} disabled={disabled} required={required} value={value} onChange={(event) => change(event.target.value, document(rows.find((row) => row.id === event.target.value)), rows.find((row) => row.id === event.target.value))}><option value="">Select {resourceLabel.toLowerCase()}</option>{value && !rows.some((row) => row.id === value) ? <option value={value} disabled>Selected {kindNames[kind]} · {value}</option> : null}{rows.map((row) => { const off = row.id === value && selectedProviderOff; const name = resourceName(row); return <option key={row.id} value={row.id} disabled={off}>{off ? `Off provider · ${name}` : name}{kind === EntityKind.ACCOUNT ? ` · ${text(document(row).health)}` : ""}</option>; })}</select></label>
    {statusMessage ? <p role="status">{statusMessage}</p> : null}
    {needsProviderCapability && active && !ready && (!agentPresentation || (!inventory.isFetching && !failure)) ? <p role="status">Provider and model choices require a server that reports the provider inventory capabilities.</p> : null}
    {kind === EntityKind.PROVIDER && needsProviderCapability ? <>
      {page || nextPage ? <nav className="actions" aria-label="Active API provider choices"><button type="button" disabled={!page || fetching || disabled} onClick={() => setPage("")}>First API providers</button><button type="button" disabled={!nextPage || fetching || disabled || !ready} onClick={() => setPage(nextPage ?? "")}>More API providers</button></nav> : null}
      {subscriptionPage || result.data?.nextPageToken ? <nav className="actions" aria-label="Native subscription provider choices"><button type="button" disabled={!subscriptionPage || result.isFetching || disabled} onClick={() => setSubscriptionPage("")}>First subscription providers</button><button type="button" disabled={!result.data?.nextPageToken || result.isFetching || disabled} onClick={() => setSubscriptionPage(result.data!.nextPageToken)}>More subscription providers</button></nav> : null}
    </> : page || nextPage ? <div className="actions"><button type="button" disabled={!page || fetching || disabled} onClick={() => setPage("")}>First choices</button><button type="button" disabled={!nextPage || fetching || disabled || (needsProviderCapability && !ready)} onClick={() => setPage(nextPage ?? "")}>More choices</button></div> : null}<Problem error={result.error || inventory.error || modelSearch.error || selected.error || selectedProvider.error} />
  </div>;
}

function OrderedLinks({ label, kind, links, change, active, weighted = false, explanation }: { label: string; kind: EntityKind; links: unknown[]; change: (values: unknown[]) => void; active: boolean; weighted?: boolean; explanation?: string }) {
  const [selected, setSelected] = useState("");
  const id = (value: unknown) => weighted ? text(object(value).id) : text(value);
  return <fieldset><legend>{label}</legend><p>Order is preserved. {explanation ?? (weighted ? "Weights are relative values from 1 to 1,000." : "Instructions are appended in this order.")}</p>
    <ResourceChoice label={`Add ${kindNames[kind]}`} kind={kind} value={selected} change={setSelected} active={active} /><button type="button" disabled={!selected || links.some((value) => id(value) === selected) || links.length >= 1000} onClick={() => { change([...links, weighted ? { id: selected, weight: 1 } : selected]); setSelected(""); }}>Add selected</button>
    <ol>{links.map((link, index) => <li key={id(link)}><code>{id(link)}</code>{weighted ? <label>Relative weight<input type="number" min={1} max={1000} value={Number(object(link).weight)} onChange={(event) => change(links.map((value, i) => i === index ? { ...object(value), weight: Number(event.target.value) } : value))} /></label> : null}<div className="actions"><button type="button" aria-label={`Move entry ${index + 1} up`} disabled={index === 0} onClick={() => { const next = [...links]; [next[index - 1], next[index]] = [next[index], next[index - 1]]; change(next); }}>Up</button><button type="button" aria-label={`Remove entry ${index + 1}`} onClick={() => change(links.filter((_, i) => i !== index))}>Remove</button></div></li>)}</ol>
  </fieldset>;
}

function ProviderFields({ data, change, subscriptionOnly = false }: FieldsProps) {
  if (subscriptionOnly) return <><TextField label="Name" value={data.name} change={(name) => change({ ...data, name })} required /><p>Subscription providers use native-subscription protocol, subscription authentication and an empty endpoint. Login is not available.</p></>;
  return <><TextField label="Name" value={data.name} change={(name) => change({ ...data, name })} required />
    <Choice label="API protocol" value={data.protocol} choices={[Protocol.Responses, Protocol.Chat, Protocol.Anthropic]} change={(protocol) => change({ ...data, protocol })} />
    <TextField label="API base URL" value={data.endpoint} max={4096} change={(endpoint) => change({ ...data, endpoint })} required /><p>Localhost refers to the server computer. Keys are entered separately in the entry connection form.</p><Choice label="Authentication" value={data.authentication} choices={[Authentication.Bearer, Authentication.Key, Authentication.Keyless]} change={(authentication) => change({ ...data, authentication })} /><Check label="Discover models automatically for connected entries" value={data.discovery} change={(discovery) => change({ ...data, discovery })} />
  </>;
}
interface FieldsProps { data: Document; change: (value: Document) => void; active: boolean; existing: boolean; pendingOperation?: (pending: boolean) => void; subscriptionOnly?: boolean }
export function ConfigurationFields({ kind, ...props }: FieldsProps & { kind: EntityKind }) {
  const { data, change, active, existing } = props;
  const field = (key: string) => (value: unknown) => change({ ...data, [key]: value });
  if (kind === EntityKind.SETTINGS) return <><Choice label="Default account routing" value={data.default_routing} choices={Object.values(Routing)} change={field("default_routing")} /><p>Used by Agent Workers that inherit the server default. Existing execution snapshots keep their original selection and routing.</p><Check label="Allow automatic fetch before Worktree preparation" value={data.automatic_fetch} change={field("automatic_fetch")} /><p>Fetching requires both this server preference and the repository's fetch preference. Disabling it uses retained remote-tracking references or reports missing references. Local checkouts remain unchanged.</p><RemediationFields value={object(data.remediation)} change={field("remediation")} active={active} /></>;
  if (kind === EntityKind.PROJECT) return <ProjectFields {...props} />;
  if (kind === EntityKind.REPOSITORY) return <RepositoryFields {...props} />;
  if (kind === EntityKind.PROVIDER) return <ProviderFields {...props} />;
  if (kind === EntityKind.TEMPLATE) return <><TextField label="Name" value={data.name} change={field("name")} required /><label>Instructions<textarea rows={12} required maxLength={131072} value={text(data.contents)} onChange={(event) => field("contents")(event.target.value)} /></label><p>Appended to the selected harness's base instructions; the base instructions are preserved.</p></>;
  if (kind === EntityKind.ACCOUNT) {
    const isApi = data.type === "api";
    return <><TextField label={isApi ? "Entry name" : "Account alias"} value={data.alias} change={field("alias")} required /><ResourceChoice label="Provider" kind={EntityKind.PROVIDER} value={text(data.provider_id)} activeApiOnly active={active} disabled={existing} required change={(provider_id, provider) => change({ ...data, provider_id, type: provider?.protocol === Protocol.Subscription ? "subscription" : "api" })} /><Check label={isApi ? "Enable this entry" : "Enable this account"} value={data.enabled} change={field("enabled")} /><Check label={isApi ? "Exclude from automatic entry selection" : "Exclude from automatic account selection"} value={data.exclude_automatic} change={field("exclude_automatic")} /><Check label={isApi ? "Notify when entry quota recovers" : "Notify when account quota recovers"} value={data.recovery_notifications} change={field("recovery_notifications")} /><p>Connection, health and quota are managed separately. Saving preferences never marks the {isApi ? "entry" : "account"} ready.</p></>;
  }
  if (kind === EntityKind.MODEL) return <><ResourceChoice label="Provider" kind={EntityKind.PROVIDER} value={text(data.provider_id)} change={field("provider_id")} activeApiOnly active={active} disabled={existing} required /><TextField label="Native model ID" value={data.native_id} change={field("native_id")} disabled={existing} required /><TextField label="Display name" value={data.name} change={field("name")} required /><TextField label="CLI alias" value={data.alias} change={field("alias")} max={128} /><fieldset><legend>Configured harness compatibility</legend>{Object.values(Harness).map((harness) => <Check key={harness} label={harness} value={items(data.harnesses).includes(harness)} change={(selected) => field("harnesses")(selected ? [...items(data.harnesses), harness] : items(data.harnesses).filter((value) => value !== harness))} />)}<p>Configured compatibility is checked against the installed harness before execution. It does not prove execution support.</p></fieldset><Check label="Hide from default model lists" value={data.hidden} change={field("hidden")} /><label>Display order<input type="number" min={-2147483648} max={2147483647} value={Number(data.order)} onChange={(event) => field("order")(Number(event.target.value))} /></label>{data.new === true ? <Check label="Keep NEW marker until reviewed" value={data.new} change={field("new")} /> : null}<p>Metadata: {text(data.metadata_source)} · context limit: {data.context_limit == null ? "Unknown" : String(data.context_limit)}</p></>;
  if (kind === EntityKind.AGENT) {
    const options = object(data.options);
    const option = (name: string) => (value: unknown) => change({ ...data, options: { ...options, [name]: value } });
    return <AgentConfiguration data={data} routingProblem={data.routing !== undefined && data.routing !== "" && !Object.values(Routing).includes(data.routing as Routing)}
      core={<><TextField label="Name" value={data.name} change={field("name")} required markRequired placeholder="e.g. Code reviewer" /><div className="agent-core-columns"><Choice label="Harness" value={data.harness} choices={Object.values(Harness)} change={field("harness")} /><ResourceChoice label="Model" kind={EntityKind.MODEL} value={text(data.model_id)} change={field("model_id")} activeApiOnly active={active} required markRequired /></div></>}
      permissions={<AgentPermissions harness={data.harness} options={options} change={field("options")} />}
      reasoning={<TextField label="Reasoning effort" value={data.effort} change={field("effort")} />}
      accounts={<><Choice label="Account routing" value={data.routing} choices={Object.values(Routing)} change={(routing) => { const next = { ...data }; if (routing) next.routing = routing; else delete next.routing; change(next); }} inherited /><OrderedLinks label="Accounts" kind={EntityKind.ACCOUNT} links={items(data.accounts)} change={field("accounts")} active={active} weighted /></>}
      instructions={<OrderedLinks label="Instruction templates" kind={EntityKind.TEMPLATE} links={items(data.templates)} change={field("templates")} active={active} />}
      native={<><TextField label="Subagent model" value={options.subagent_model} change={option("subagent_model")} /><TextField label="Subagent effort" value={options.subagent_effort} change={option("subagent_effort")} /><label>Maximum concurrency (0 uses native default)<input type="number" min={0} max={64} value={Number(options.max_concurrency ?? 0)} onChange={(event) => option("max_concurrency")(Number(event.target.value))} /></label>{data.harness !== Harness.Claude ? <TextField label="Approval policy" value={options.approval_policy} change={option("approval_policy")} /> : null}<TextField label="Approval review model" value={options.approval_review_model} change={option("approval_review_model")} /><TextField label="Service tier" value={options.service_tier} change={option("service_tier")} /><p>Unsupported native options produce a server error; no fallback harness or account is selected.</p></>}
    />;
  }
  return null;
}

function RestrictionFields({ label, kind, value, change, active }: { label: string; kind: EntityKind; value: unknown; change: (value: Document) => void; active: boolean }) {
  const restriction = object(value);
  return <fieldset><legend>{label}</legend><Check label={`Restrict ${label.toLowerCase()}`} value={restriction.configured} change={(configured) => change({ configured, ids: configured ? items(restriction.ids) : [] })} />{restriction.configured === true ? <><p>An empty selection permits none. Turning this restriction off permits every otherwise eligible entry.</p><OrderedLinks label={`Allowed ${label.toLowerCase()}`} kind={kind} links={items(restriction.ids)} active={active} explanation="Only these explicitly selected entries are allowed." change={(ids) => change({ ...restriction, ids })} /></> : <p>Every otherwise eligible entry is allowed.</p>}</fieldset>;
}
function ProjectFields({ data, change, active }: FieldsProps) {
  const repositories = items(data.repositories).map(text);
  return <><TextField label="Name" value={data.name} required change={(name) => change({ ...data, name })} /><OrderedLinks label="Ordered repositories" kind={EntityKind.REPOSITORY} links={repositories} active={active} explanation="All selected repositories form one workspace." change={(values) => change({ ...data, repositories: values, primary_repository: values.includes(data.primary_repository) ? data.primary_repository : "" })} /><label>Primary repository<select required value={text(data.primary_repository)} onChange={(event) => change({ ...data, primary_repository: event.target.value })}><option value="">Select the primary repository</option>{repositories.map((id) => <option key={id} value={id}>{id}</option>)}</select></label><p>The harness starts in this repository. Select it explicitly after adding repositories.</p><RestrictionFields label="Agent Workers" kind={EntityKind.AGENT} value={data.agents} active={active} change={(agents) => change({ ...data, agents })} /><RestrictionFields label="AI accounts" kind={EntityKind.ACCOUNT} value={data.accounts} active={active} change={(accounts) => change({ ...data, accounts })} /></>;
}
enum ReferenceType { Local = "local-branch", Remote = "remote-branch", Commit = "commit" }
export function ReferenceFields({ label, value, change }: { label: string; value: unknown; change: (value: Document) => void }) {
  const reference = object(value);
  return <fieldset><legend>{label}</legend><label>{label} type<select value={text(reference.type)} onChange={(event) => change(event.target.value ? { type: event.target.value, name: text(reference.name), ...(event.target.value === ReferenceType.Remote ? { remote: text(reference.remote) } : {}) } : {})}><option value="">Use inspected default</option>{Object.values(ReferenceType).map((type) => <option key={type} value={type}>{type}</option>)}</select></label>{reference.type ? <><TextField label={`${label} name`} value={reference.name} required max={1024} change={(name) => change({ ...reference, name })} />{reference.type === ReferenceType.Remote ? <TextField label={`${label} remote`} value={reference.remote} required change={(remote) => change({ ...reference, remote })} /> : null}</> : null}</fieldset>;
}
function RemediationFields({ value, change, active }: { value: Document; change: (value: Document) => void; active: boolean }) {
  const identity = (key: string, id: string) => { const next = { ...value }; if (id) next[key] = id; else delete next[key]; change(next); };
  return <RemediationPolicyFields value={value} change={change}>
    <ResourceChoice label="Remediation Agent Worker" kind={EntityKind.AGENT} value={text(value.agent_id)} active={active} change={id => identity("agent_id", id)} />
    <ResourceChoice label="Remediation Runner Device" kind={EntityKind.MACHINE} value={text(value.machine_id)} active={active} change={id => identity("machine_id", id)} />
  </RemediationPolicyFields>;
}

function RepositoryFields({ data, change, active, pendingOperation }: FieldsProps) {
  const [machine, setMachine] = useState(""), [path, setPath] = useState("");
  const [unknownInspection, setUnknownInspection] = useState(false);
  const [inspection, setInspection] = useState<{ job: Resource; machine: string }>();
  const inspect = useRetainedMutation("repository-editor:inspect", WorkerQuery.inspectRepository, (result) => { if (result.job && text(document(result.job).machine_id)) setInspection({ job: result.job, machine: text(document(result.job).machine_id) }); else setUnknownInspection(true); });
  const checkouts = items(data.checkouts).map(object), blocked = inspect.busy || inspect.uncertain || Boolean(inspection) || unknownInspection;
  useEffect(() => { pendingOperation?.(blocked); return () => pendingOperation?.(false); }, [blocked, pendingOperation]);
  return <><TextField label="Name" value={data.name} required change={(name) => change({ ...data, name })} /><TextField label="Preferred Git remote" value={data.preferred_remote} change={(preferred_remote) => change({ ...data, preferred_remote })} /><TextField label="GitHub repository owner" value={data.github_owner} max={100} change={(github_owner) => change({ ...data, github_owner })} /><TextField label="GitHub repository name" value={data.github_name} max={100} change={(github_name) => change({ ...data, github_name })} /><ResourceChoice label="GitHub profile" kind={EntityKind.INTEGRATION} value={text(data.integration_id)} active={active} change={(integration_id) => change({ ...data, integration_id })} /><p>A missing or deleted profile requires reconfiguration. GitHub lookup never chooses another token or changes this Worker's Git authentication.</p><p>Inspection reads the selected Worker's Git checkout without fetching. Saving revalidates every checkout on its owning Worker before publishing the configuration.</p>
    <fieldset disabled={blocked}><legend>Add a checkout</legend><ResourceChoice label="Runner Device" kind={EntityKind.MACHINE} value={machine} active={active} change={setMachine} /><TextField label="Absolute checkout path on this Worker" value={path} max={4096} change={setPath} /><button type="button" disabled={!machine || !path || checkouts.some((checkout) => checkout.machine_id === machine) || checkouts.length >= 1000} onClick={() => void inspect.send({ requestId: newRequestId(), machineId: machine, path, preferredRemote: text(data.preferred_remote) })}>Inspect checkout</button></fieldset><Problem error={inspect.error} />{inspect.uncertain ? <button type="button" disabled={inspect.busy} onClick={inspect.retry}>Retry the same inspection</button> : null}
    {unknownInspection ? <p role="alert">Inspection was acknowledged without a readable job. Inspect the original request before submitting another.</p> : null}{inspection ? <TrackedJob initial={inspection.job} active={active}>{(state, output) => <>{state === JobState.Succeeded ? <><p>Inspected root: {text(output.root)}</p><p>Remotes: {items(output.remotes).map(text).join(", ") || "None"}</p><p>Recorded remote defaults: {Object.entries(object(output.default_refs)).map(([remote, ref]) => `${remote}: ${text(ref)}`).join(", ") || "Unknown"}</p><button type="button" disabled={!text(output.root) || checkouts.some((checkout) => checkout.machine_id === inspection.machine)} onClick={() => { change({ ...data, checkouts: [...checkouts, { machine_id: inspection.machine, path: text(output.root) }] }); setInspection(undefined); setPath(""); setMachine(""); }}>Add inspected checkout</button></> : null}{[JobState.Succeeded, JobState.Failed, JobState.Canceled].includes(state as JobState) ? <button type="button" onClick={() => setInspection(undefined)}>Close inspection</button> : null}</>}</TrackedJob> : null}
    <ol>{checkouts.map((checkout) => <li key={text(checkout.machine_id)}><p>{text(checkout.machine_id)} · {text(checkout.path)}</p><button type="button" onClick={() => change({ ...data, checkouts: checkouts.filter((row) => row.machine_id !== checkout.machine_id) })}>Remove checkout</button></li>)}</ol><ReferenceFields label="Base reference" value={data.base} change={(base) => change({ ...data, base })} /><ReferenceFields label="Starting reference" value={data.starting} change={(starting) => change({ ...data, starting })} /><p>Base controls comparisons; starting controls prepared code. A missing or ambiguous default is reported by the Worker. Local sessions keep their existing checkout unchanged.</p><Check label="Fetch the exact selected remote before Worktree preparation" value={data.auto_fetch} change={(auto_fetch) => change({ ...data, auto_fetch })} />
    <fieldset><legend>Repository remediation</legend>
      {data.remediation == null ? <><p>This repository inherits the complete server remediation policy, including future changes.</p><button type="button" onClick={() => change({ ...data, remediation: defaultRemediationPolicy() })}>Set repository policy with automation off</button></> : <><p>This complete repository policy replaces the server policy. Empty reviewer or execution selections remain empty.</p><RemediationFields value={object(data.remediation)} active={active} change={remediation => change({ ...data, remediation })} /><button type="button" onClick={() => { const next = { ...data }; delete next.remediation; change(next); }}>Use server remediation policy</button></>}
    </fieldset>
  </>;
}
