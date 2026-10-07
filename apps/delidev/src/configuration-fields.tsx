import { RunnerWorkflow, useRunnerPreference } from "./runner-device-preferences";
import { statusLabel } from "./product-status";
import { LocalizedText, copy, useLocale } from "./localization";
import { defaultRemediationPolicy, RemediationDetailPresentation, RemediationPolicyFields } from "./remediation-policy";
import { createContext, useCallback, useContext, useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { AgentConfiguration, AgentReadProblem } from "./agent-configuration";
import { Code, ConnectError } from "@connectrpc/connect";
import { createQueryOptions, useQuery, useTransport } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { useConnectPaginationReader, usePaginationChain, usePaginationRefresh } from "./scroll-pagination-query";
import { ScrollPicker } from "./scroll-picker";
import type { MessageShape } from "@bufbuild/protobuf";
import { FailureCode, clientFailure, EntityKind, NativeModelSourceKind, SubscriptionServiceId, subscriptionService, subscriptionServiceHarnesses, subscriptionServiceNames, supportsResourceSchema, ProviderQuery, ResourceQuery, WorkerQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, items, object, resourceName, text, type Document } from "./documents";
import { Problem, ServiceProblem } from "./ui";
import { useRetainedMutation } from "./mutation";
import { JobState, TrackedJob } from "./jobs";
import { providerInventoryReady } from "./provider-model-settings";
import { CodexSubagentConfiguration } from "./codex-subagent-configuration";
import { ReasoningEffortField, claudeEffortSuggestions, codexEffortSuggestions } from "./reasoning-effort-field";
import { ProviderAPIFormatFields, AccountAPIFormatField } from "./api-format-fields";
import { useProjectRepositoryNames } from "./project-repositories";
import { RepositoryEditSections, RepositoryCheckoutIdentity } from "./repository-editor";

export enum Harness { Codex = "codex", Claude = "claude-code", OpenCode = "opencode", Grok = "grok-build" }
export enum Protocol { Responses = "openai-responses", Chat = "openai-chat", Anthropic = "anthropic-messages", Subscription = "native-subscription" }
export enum Authentication { Bearer = "bearer", Key = "api-key", Keyless = "keyless", Subscription = "subscription" }
export enum Routing { Fixed = "fixed", Priority = "priority", RoundRobin = "round-robin", Quota = "remaining-quota", Reset = "reset-window", Sequential = "sequential-exhaustion" }
enum Permission { Default = "default", Read = "read-only", Workspace = "workspace-write", Full = "full-access" }
enum ClaudePermission { Default = "default", Plan = "plan", AcceptEdits = "acceptEdits", DontAsk = "dontAsk", Bypass = "bypassPermissions", Auto = "auto" }
export const editableKinds = [EntityKind.PROVIDER, EntityKind.MODEL, EntityKind.ACCOUNT, EntityKind.AGENT, EntityKind.TEMPLATE, EntityKind.PROJECT, EntityKind.REPOSITORY, EntityKind.SETTINGS];
export const kindNames: Partial<Record<EntityKind, string>> = { get [EntityKind.INTEGRATION]() { return copy("configuration-fields.githubProfile_5a175c"); }, get [EntityKind.PROVIDER]() { return copy("configuration-fields.provider_472590"); }, get [EntityKind.MODEL]() { return copy("configuration-fields.model_5e2c61"); }, get [EntityKind.ACCOUNT]() { return copy("configuration-fields.aiAccount_042001"); }, get [EntityKind.AGENT]() { return copy("configuration-fields.agentWorker_a4caa7"); }, get [EntityKind.TEMPLATE]() { return copy("configuration-fields.instructions_934652"); }, get [EntityKind.PROJECT]() { return copy("configuration-fields.project_985959"); }, get [EntityKind.MACHINE]() { return copy("configuration-fields.runnerDevice_37efe3"); }, get [EntityKind.REPOSITORY]() { return copy("configuration-fields.repository_13d6ff"); }, get [EntityKind.SETTINGS]() { return copy("configuration-fields.serverPreferences_eba66b"); } };
export function newConfiguration(kind: EntityKind): Document {
  switch (kind) {
    case EntityKind.PROVIDER: return { name: "", endpoint: "", protocol: Protocol.Responses, authentication: Authentication.Bearer, api_formats: [], discovery: true, enabled: true };
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
export function NativeOptionExplanation({ value, clear, label }: { value: unknown; clear: () => void; label: string }) {
  useLocale();
  const retained = value !== undefined && value !== "" && value !== 0;
  return <><p>{copy("configuration-fields.nativeOptionUnavailable")}</p>{retained ? <button type="button" aria-label={`${copy("configuration-fields.clearRetainedNativeOption")}: ${label}`} onClick={clear}>{copy("configuration-fields.clearRetainedNativeOption")}</button> : null}</>;
}
export function TextField({ label, value, change, required = false, max = 256, disabled = false, markRequired = false, placeholder, unavailable = false }: { label: string; value: unknown; change: (value: string) => void; required?: boolean; max?: number; disabled?: boolean; markRequired?: boolean; placeholder?: string; unavailable?: boolean }) {
  useLocale();
  const help = useId();
  return <><label>{markRequired ? <span>{label}<span className="agent-required" aria-hidden="true"> *</span></span> : label}<input aria-label={markRequired ? label : undefined} aria-describedby={unavailable ? help : undefined} placeholder={placeholder} value={text(value)} required={required} maxLength={max} disabled={disabled || unavailable} onChange={(event) => change(event.target.value)} /></label>{unavailable ? <div id={help}><NativeOptionExplanation label={label} value={value} clear={() => change("")} /></div> : null}</>;
}
function Choice({ label, value, choices, change, disabled = false, inherited = false }: { label: string; value: unknown; choices: readonly string[]; change: (value: string) => void; disabled?: boolean; inherited?: boolean }) {
  useLocale();
  const selected = text(value);
  return <label>{label}<select value={selected} disabled={disabled} onChange={(event) => change(event.target.value)}>{inherited ? <option value="">{copy("configuration-fields.useServerDefault_2bbfe2")}</option> : null}{selected && !choices.includes(selected) ? <option value={selected}><LocalizedText id="configuration-fields.unsupportedSelection_7c6fcd" components={{ s0: <>{selected}</> }} /></option> : null}{choices.map((choice) => <option key={choice} value={choice}>{choice}</option>)}</select></label>;
}

function AgentPermissions({ harness, options, change }: { harness: unknown; options: Document; change: (value: Document) => void }) {
  useLocale();
  if (harness === Harness.Claude) {
    const conflict = options.permission !== Permission.Default || Boolean(text(options.approval_policy));
    const selected = options.claude_permission ?? ClaudePermission.Default;
    return <fieldset><legend>{copy("configuration-fields.claudeToolPermissions_145abc")}</legend>
      <Choice label={copy("configuration-fields.claudePermissionMode_6cb4cf")} value={selected} choices={Object.values(ClaudePermission)} change={(claude_permission) => change({ ...options, claude_permission })} />
      <p>{copy("configuration-fields.planInputsUseClaudeSNative_a3fec5")}</p>
      {selected === ClaudePermission.AcceptEdits ? <p className="notice">{copy("configuration-fields.claudeCanAcceptFileEditsAnd_71cb59")}</p> : null}
      {selected === ClaudePermission.DontAsk ? <p>{copy("configuration-fields.claudeDeniesOperationsThatWouldRequire_4763c5")}</p> : null}
      {selected === ClaudePermission.Bypass ? <p className="notice">{copy("configuration-fields.bypassSkipsNativePermissionPromptsSelect_8c3404")}</p> : null}
      {conflict ? <><p role="alert"><LocalizedText id="configuration-fields.retainedSandboxOrApprovalPolicySettings_7733d3" components={{ s0: <>{text(options.permission)}</>, s1: <>{text(options.approval_policy)}</> }} /></p><button type="button" onClick={() => { const next: Document = { ...options, permission: Permission.Default }; delete next.approval_policy; change(next); }}>{copy("configuration-fields.clearIncompatiblePermissionSettings_2e8a8c")}</button></> : null}
      <p>{copy("configuration-fields.savingSettingsDoesNotEnableClaude_05773c")}</p>
    </fieldset>;
  }
  return <>
    <Choice label={copy("configuration-fields.permissionMode_c7a8e6")} value={options.permission} choices={harness === Harness.Codex ? Object.values(Permission) : [Permission.Default]} change={(permission) => change({ ...options, permission })} />
    {options.permission === Permission.Default ? <p>{copy("configuration-fields.usesTheHarnessDefaultReviewPermissions_077a8d")}</p> : null}
    {harness !== Harness.Codex ? <p>{copy("configuration-fields.nativePermissionUnavailable")}</p> : null}
    {options.permission === Permission.Full ? <p className="notice">{copy("configuration-fields.fullAccessPermitsNativeOperationsBeyond_c45c2a")}</p> : null}
    {options.claude_permission !== undefined ? <><p role="alert"><LocalizedText id="configuration-fields.aClaudePermissionSelectionIsRetained_c20333" components={{ s0: <>{text(options.claude_permission)}</> }} /></p><button type="button" onClick={() => { const next = { ...options }; delete next.claude_permission; change(next); }}>{copy("configuration-fields.clearClaudePermissionSelection_e65f5d")}</button></> : null}
  </>;
}
function Check({ label, value, change }: { label: string; value: unknown; change: (value: boolean) => void }) {
  useLocale(); return <label className="checkbox"><input type="checkbox" checked={value === true} onChange={(event) => change(event.target.checked)} />{label}</label>; }

export const ResourceSelectionPending = createContext<((identity: string, pending: boolean) => void) | undefined>(undefined);

// Selectors accumulate bounded display projections. Exact resources are read
// only for the retained selection and a deliberate selection callback.
export function ResourceChoice({ label, resourceLabel = label, emptyLabel, kind, value, change, active, disabled = false, required = false, autoFocus = false, allowed, activeApiOnly = false, showStatus = false, markRequired = false, resolvedChoice }: { label: string; resourceLabel?: string; kind: EntityKind; value: string; change: (id: string, data?: Document, resource?: Resource) => void; active: boolean; disabled?: boolean; required?: boolean; autoFocus?: boolean; allowed?: readonly unknown[]; activeApiOnly?: boolean; showStatus?: boolean; markRequired?: boolean; emptyLabel?: string; resolvedChoice?: Resource }) {
  useLocale();
  const reportRead = useContext(AgentReadProblem), reportPending = useContext(ResourceSelectionPending), readIdentity = useId();
  const transport = useTransport(), client = useQueryClient(), generation = useRef(0);
  const latestChange = useRef(change); latestChange.current = change;
  const [selectionBusy, setSelectionBusy] = useState(false), [selectionError, setSelectionError] = useState<unknown>();
  const needsProviderCapability = activeApiOnly && (kind === EntityKind.PROVIDER || kind === EntityKind.MODEL);
  const inventory = useQuery(ProviderQuery.listProviderInventory, { query: "", enabledOnly: true, pageSize: 1 }, { enabled: active && needsProviderCapability });
  const ready = providerInventoryReady(inventory.data?.capabilities);
  const allowedKey = allowed === undefined ? "*" : JSON.stringify(allowed);
  interface ChoiceRow { id: string; revision: bigint; name: string; health: string }
  const projectResources = useCallback((resources: Resource[]) => {
    if (resources.length > 50 || new Set(resources.map(row => row.id)).size !== resources.length || resources.some(row => row.kind !== kind || !row.id || row.revision <= 0n || !supportsResourceSchema(row))) throw new ConnectError("Invalid resource choices", Code.DataLoss);
    return resources.filter(row => allowedKey === "*" || (JSON.parse(allowedKey) as unknown[]).includes(row.id)).map(row => {
      const data = document(row); return { id: row.id, revision: row.revision, name: text(data.name) || text(data.alias), health: text(data.health) };
    });
  }, [kind, allowedKey]);
  const requestResources = useCallback((token: string) => ({ filter: { kind, pageSize: 50, pageToken: token } }), [kind]);
  const projectList = useCallback((response: MessageShape<typeof ResourceQuery.listResources.output>) => ({ rows: projectResources(response.resources), nextPageToken: response.nextPageToken }), [projectResources]);
  const resourceReader = useConnectPaginationReader(ResourceQuery.listResources, requestResources, projectList);
  const resources = usePaginationChain<ChoiceRow>(`resource-choice:${kind}:${allowedKey}`, active && !disabled && !needsProviderCapability, resourceReader);
  const requestProviders = useCallback((token: string) => ({ query: "", enabledOnly: true, pageSize: 50, pageToken: token }), []);
  const projectInventory = useCallback((response: MessageShape<typeof ProviderQuery.listProviderInventory.output>) => {
    if (response.entries.length > 50 || new Set(response.entries.map(entry => entry.providerId)).size !== response.entries.length || !providerInventoryReady(response.capabilities) || response.entries.some(entry => !entry.provider || entry.providerId !== entry.provider.id || !entry.enabled)) throw new ConnectError("Invalid provider choices", Code.DataLoss);
    return { rows: projectResources(response.entries.flatMap(entry => entry.provider ? [entry.provider] : [])), nextPageToken: response.nextPageToken };
  }, [projectResources]);
  const providerReader = useConnectPaginationReader(ProviderQuery.listProviderInventory, requestProviders, projectInventory);
  const providers = usePaginationChain<ChoiceRow>(`provider-choice:${allowedKey}`, active && !disabled && needsProviderCapability && kind === EntityKind.PROVIDER && ready, providerReader);
  const requestModels = useCallback((token: string) => ({ query: "", providerId: "", includeHidden: true, pageSize: 50, pageToken: token, enabledProvidersOnly: true }), []);
  const projectModels = useCallback((response: MessageShape<typeof ProviderQuery.searchModels.output>) => ({ rows: projectResources(response.models), nextPageToken: response.nextPageToken }), [projectResources]);
  const modelReader = useConnectPaginationReader(ProviderQuery.searchModels, requestModels, projectModels);
  const models = usePaginationChain<ChoiceRow>(`model-choice:${allowedKey}`, active && !disabled && needsProviderCapability && kind === EntityKind.MODEL && ready, modelReader);
  const result = needsProviderCapability ? kind === EntityKind.PROVIDER ? providers : models : resources;
  usePaginationRefresh(ResourceQuery.listResources, requestResources(""), active && !needsProviderCapability, resources.refresh);
  usePaginationRefresh(ProviderQuery.listProviderInventory, requestProviders(""), active && needsProviderCapability && kind === EntityKind.PROVIDER, providers.refresh);
  usePaginationRefresh(ProviderQuery.searchModels, requestModels(""), active && needsProviderCapability && kind === EntityKind.MODEL, models.refresh);
  const selected = useQuery(ResourceQuery.getResource, { kind, id: value }, { enabled: active && Boolean(value) });
  // Creation owns this independently verified exact-ID record. It supplies only
  // the selected label; reached pages continue to retain metadata projections.
  const resolvedSelection = resolvedChoice?.id === value && resolvedChoice.kind === kind && resolvedChoice.revision > 0n && supportsResourceSchema(resolvedChoice) && (allowedKey === "*" || (JSON.parse(allowedKey) as unknown[]).includes(value)) ? resolvedChoice : undefined;
  const selectedResource = selected.data?.resource?.id === value && selected.data.resource.kind === kind ? selected.data.resource : resolvedSelection;
  const selectedData = document(selectedResource);
  const selectedProvider = useQuery(ResourceQuery.getResource, { kind: EntityKind.PROVIDER, id: kind === EntityKind.MODEL ? text(selectedData.provider_id) : "" }, { enabled: active && needsProviderCapability && kind === EntityKind.MODEL && Boolean(text(selectedData.provider_id)) });
  const selectedProviderOff = needsProviderCapability && (kind === EntityKind.PROVIDER ? selectedData.protocol !== Protocol.Subscription && selectedData.enabled === false : document(selectedProvider.data?.resource).enabled === false);
  const failure = selectionError ?? (needsProviderCapability ? inventory.error : undefined) ?? selected.error ?? (needsProviderCapability && kind === EntityKind.MODEL ? selectedProvider.error : undefined);
  const choiceFailure = result.error?.failure ?? (failure ? clientFailure(failure) : undefined);
  const reason = choiceFailure?.code === FailureCode.PermissionDenied || choiceFailure?.code === FailureCode.Unauthenticated
    ? copy("configuration-fields.choices.denied")
    : choiceFailure?.code === FailureCode.Unavailable || choiceFailure?.code === FailureCode.ServerUnavailable
      ? copy("configuration-fields.choices.connection") : copy("configuration-fields.choices.request");
  const readProblem = Boolean(failure || result.error || selectedProviderOff || active && needsProviderCapability && inventory.data && !ready);
  useEffect(() => { reportRead?.(readIdentity, readProblem); return () => reportRead?.(readIdentity, false); }, [readIdentity, readProblem, reportRead]);
  useLayoutEffect(() => { generation.current++; reportPending?.(readIdentity, false); setSelectionBusy(false); setSelectionError(undefined); return () => { generation.current++; reportPending?.(readIdentity, false); }; }, [active, disabled, kind, allowedKey, transport, reportPending, readIdentity]);
  // Release only after the accepted callback and its parent draft update commit.
  useLayoutEffect(() => { if (!selectionBusy) reportPending?.(readIdentity, false); });
  const select = async (id: string) => {
    if (!active || disabled || selectionBusy) return;
    if (!id) { latestChange.current(""); return; }
    const original = generation.current; reportPending?.(readIdentity, true); setSelectionBusy(true); setSelectionError(undefined);
    try {
      const response = await client.fetchQuery({ ...createQueryOptions(ResourceQuery.getResource, { kind, id }, { transport }), staleTime: 0, retry: false });
      if (generation.current !== original) return;
      const row = response.resource;
      if (!row || row.id !== id || row.kind !== kind || !supportsResourceSchema(row) || allowedKey !== "*" && !(JSON.parse(allowedKey) as unknown[]).includes(id)) throw new ConnectError("Selected resource is unavailable", Code.NotFound);
      if (needsProviderCapability) {
        const provider = kind === EntityKind.PROVIDER ? row : (await client.fetchQuery({ ...createQueryOptions(ResourceQuery.getResource, { kind: EntityKind.PROVIDER, id: text(document(row).provider_id) }, { transport }), staleTime: 0, retry: false })).resource;
        if (generation.current !== original) return;
        if (!provider || provider.kind !== EntityKind.PROVIDER || !supportsResourceSchema(provider) || document(provider).enabled !== true || document(provider).protocol === Protocol.Subscription) throw new ConnectError("Selected provider is unavailable", Code.FailedPrecondition);
      }
      latestChange.current(id, document(row), row);
    } catch (error) { if (generation.current === original) setSelectionError(error); }
    finally { if (generation.current === original) setSelectionBusy(false); }
  };
  const options = [{ id: "", label: emptyLabel ?? copy("configuration-fields.select_586618", { v0: resourceLabel.toLowerCase() }) }, ...result.rows.map(row => ({ id: row.id, label: (row.name || copy("documents.extra.e504e6152194")) + (kind === EntityKind.ACCOUNT ? copy("configuration-fields.message_2fa20b", { v0: statusLabel(row.health) }) : ""), disabled: row.id === value && selectedProviderOff }))];
  return <div className="resource-choice"><ScrollPicker label={label} options={options} value={value} selectedLabel={value ? selectedResource ? resourceName(selectedResource) : copy("configuration-fields.sentence.38798a0275ce", { v0: resourceLabel }) : undefined} placeholder={emptyLabel} change={id => void select(id)} query={result} active={active} disabled={disabled || selectionBusy} required={required} autoFocus={autoFocus} markRequired={markRequired} />
    {needsProviderCapability && active && !ready ? <p role="status">{copy("configuration-fields.providerAndModelChoicesRequireA_5726bc")}</p> : null}
    {(showStatus || reportRead || markRequired) && result.loading ? <p role="status">{copy("configuration-fields.sentence.3d9404257563", { v0: resourceLabel })}</p> : null}
    {(showStatus || reportRead || markRequired) && result.loaded && !result.rows.length && !result.error && !result.loading ? <p role="status">{copy(result.nextPageToken ? "configuration-fields.sentence.244a41434b15" : "configuration-fields.sentence.9117e85a4bce", { v0: resourceLabel })}</p> : null}
    {(showStatus || reportRead || markRequired) && value && selected.data?.resource && !result.rows.some(row => row.id === value) ? <p role="status">{copy("configuration-fields.sentence.5398fd2fa5b0", { v0: resourceLabel })}</p> : null}
    {(showStatus || reportRead || markRequired) && choiceFailure ? <p role="status">{copy(result.loaded ? "configuration-fields.sentence.054bff468121" : "configuration-fields.sentence.1ad5938a045c", { v0: result.loaded ? resourceLabel : reason, v1: reason })}</p> : null}
    {result.error ? <ServiceProblem code={result.error.failure.code}><p>{result.error.failure.message}</p><p>{result.error.failure.guidance}</p></ServiceProblem> : null}
    <Problem error={failure} />
  </div>;
}

function OrderedLinks({ label, kind, links, change, active, weighted = false, explanation, names }: { label: string; kind: EntityKind; links: unknown[]; change: (values: unknown[]) => void; active: boolean; weighted?: boolean; explanation?: string; names?: ReadonlyMap<string, string> }) {
  useLocale();
  const [selected, setSelected] = useState("");
  const id = (value: unknown) => weighted ? text(object(value).id) : text(value);
  return <fieldset><legend>{label}</legend><p><LocalizedText id="configuration-fields.orderIsPreserved_62a111" components={{ s0: <>{explanation ?? (weighted ? copy("configuration-fields.weightsAreRelativeValuesFrom1_08eeb6") : copy("configuration-fields.instructionsAreAppendedInThisOrder_659f26"))}</> }} /></p>
    <ResourceChoice label={copy("configuration-fields.add_b69dce", { v0: kindNames[kind] })} kind={kind} value={selected} change={setSelected} active={active} /><button type="button" disabled={!selected || links.some((value) => id(value) === selected) || links.length >= 1000} onClick={() => { change([...links, weighted ? { id: selected, weight: 1 } : selected]); setSelected(""); }}>{copy("configuration-fields.addSelected_967e2a")}</button>
    <ol>{links.map((link, index) => <li key={id(link)}>{names ? <><span>{names.get(id(link)) ?? copy("project-creation.nameUnavailable")}</span><RepositoryIdentity id={id(link)} /></> : <code>{id(link)}</code>}{weighted ? <label>{copy("configuration-fields.relativeWeight_e91886")}<input type="number" min={1} max={1000} value={Number(object(link).weight)} onChange={(event) => change(links.map((value, i) => i === index ? { ...object(value), weight: Number(event.target.value) } : value))} /></label> : null}<div className="actions"><button type="button" aria-label={copy("configuration-fields.moveEntryUp_b22154", { v0: index + 1 })} disabled={index === 0} onClick={() => { const next = [...links]; [next[index - 1], next[index]] = [next[index], next[index - 1]]; change(next); }}>{copy("configuration-fields.up_55490a")}</button><button type="button" aria-label={copy("configuration-fields.removeEntry_8a2d73", { v0: index + 1 })} onClick={() => change(links.filter((_, i) => i !== index))}>{copy("configuration-fields.remove_c3812f")}</button></div></li>)}</ol>
  </fieldset>;
}

function ProviderFields({ data, change, subscriptionOnly = false, ...props }: FieldsProps) {
  useLocale();
  if (subscriptionOnly) return <><TextField label={copy("configuration-fields.name_dcd1d5")} value={data.name} change={(name) => change({ ...data, name })} required /><p>{copy("configuration-fields.subscriptionProvidersUseNativeSubscriptionProtocol_8f0299")}</p></>;
  return <><TextField label={copy("configuration-fields.name_dcd1d5")} value={data.name} change={(name) => change({ ...data, name })} required />
    <ProviderAPIFormatFields data={data} change={change} {...props} /><p>{copy("configuration-fields.localhostRefersToTheServerComputer_cfc90a")}</p><Check label={copy("configuration-fields.discoverModelsAutomaticallyForConnectedEntries_6f1cb4")} value={data.discovery} change={(discovery) => change({ ...data, discovery })} />
  </>;
}
export enum ServerPreferenceSection { All = "all", AccountRouting = "account-routing", GitWorkflow = "git-workflow" }
interface FieldsProps { initial?: Resource; keepsFormatKey?: (ready: boolean) => void; saveBlocked?: (blocked: boolean) => void; data: Document; change: (value: Document) => void; active: boolean; existing: boolean; pendingOperation?: (pending: boolean) => void; subscriptionOnly?: boolean; serverPreferenceSection?: ServerPreferenceSection; workerWizard?: boolean }
export function ConfigurationFields({ kind, ...props }: FieldsProps & { kind: EntityKind }) {
  useLocale();
  const { data, change, active, existing, serverPreferenceSection = ServerPreferenceSection.All } = props;
  const field = (key: string) => (value: unknown) => change({ ...data, [key]: value });
  if (kind === EntityKind.SETTINGS) return <>
    {serverPreferenceSection !== ServerPreferenceSection.GitWorkflow ? <section className="server-preference-section"><h4>{copy("configuration-fields.accountRouting_0c3707")}</h4><div className="server-routing-field"><Choice label={copy("configuration-fields.defaultAccountRouting_bb44ea")} value={data.default_routing} choices={Object.values(Routing)} change={field("default_routing")} /><p>{copy("configuration-fields.usedByAgentWorkersThatInherit_4e05b2")}</p></div></section> : null}
    {serverPreferenceSection !== ServerPreferenceSection.AccountRouting ? <>
      <section className="server-preference-section"><h4>{copy("configuration-fields.worktreePreparation_24002c")}</h4><Check label={copy("configuration-fields.allowAutomaticFetchBeforeWorktreePreparation_6c9a8c")} value={data.automatic_fetch} change={field("automatic_fetch")} /><p>{copy("configuration-fields.fetchingRequiresBothThisServerPreference_10697f")}</p></section>
      <section className="server-preference-section"><RemediationFields workflow={RunnerWorkflow.ServerRemediation} value={object(data.remediation)} change={field("remediation")} active={active} presentation={RemediationDetailPresentation.Collapsible} /></section>
    </> : null}
  </>;
  if (kind === EntityKind.PROJECT) return <ProjectFields {...props} />;
  if (kind === EntityKind.REPOSITORY) return <RepositoryFields {...props} />;
  if (kind === EntityKind.PROVIDER) return <ProviderFields {...props} />;
  if (kind === EntityKind.TEMPLATE) return <><TextField label={copy("configuration-fields.name_dcd1d5")} value={data.name} change={field("name")} required /><label>{copy("configuration-fields.instructions_934652")}<textarea rows={12} required maxLength={131072} value={text(data.contents)} onChange={(event) => field("contents")(event.target.value)} /></label><p>{copy("configuration-fields.appendedToTheSelectedHarnessS_14ecc2")}</p></>;
  if (kind === EntityKind.ACCOUNT) {
    const isApi = data.type === "api";
    return <><TextField label={isApi ? copy("configuration-fields.entryName_978463") : copy("configuration-fields.accountAlias_332faa")} value={data.alias} change={field("alias")} required />{isApi ? <ResourceChoice label={copy("configuration-fields.provider_472590")} kind={EntityKind.PROVIDER} value={text(data.provider_id)} activeApiOnly active={active} disabled={existing} required change={(provider_id) => change({ ...data, provider_id })} /> : <p><LocalizedText id="configuration-fields.subscriptionService_78d697" components={{ s0: <>{subscriptionServiceNames[subscriptionService(data.subscription_service)!] ?? copy("configuration-fields.unsupportedService_724094")}</> }} /></p>}{isApi ? <AccountAPIFormatField {...props} /> : null}<Check label={isApi ? copy("configuration-fields.enableThisEntry_9d9bf5") : copy("configuration-fields.enableThisAccount_c76498")} value={data.enabled} change={field("enabled")} /><Check label={isApi ? copy("configuration-fields.excludeFromAutomaticEntrySelection_464713") : copy("configuration-fields.excludeFromAutomaticAccountSelection_016878")} value={data.exclude_automatic} change={field("exclude_automatic")} /><Check label={isApi ? copy("configuration-fields.notifyWhenEntryQuotaRecovers_b06486") : copy("configuration-fields.notifyWhenAccountQuotaRecovers_02ed43")} value={data.recovery_notifications} change={field("recovery_notifications")} /><p><LocalizedText id="configuration-fields.connectionHealthAndQuotaAreManaged_2751ec" components={{ s0: <>{isApi ? copy("configuration-fields.entry_923fe5") : copy("configuration-fields.account_9af211")}</> }} /></p></>;
  }
  if (kind === EntityKind.MODEL) return <ModelFields {...props} />;
  if (kind === EntityKind.AGENT) {
    const options = object(data.options);
    const option = (name: string) => (value: unknown) => change({ ...data, options: { ...options, [name]: value } });
    return <AgentConfiguration data={data} routingProblem={data.routing !== undefined && data.routing !== "" && !Object.values(Routing).includes(data.routing as Routing)}
      core={<>{!props.workerWizard ? <AgentReconfiguration data={data} change={change} active={active} /> : null}<TextField label={copy("configuration-fields.name_dcd1d5")} value={data.name} change={field("name")} required markRequired placeholder={copy("configuration-fields.eGCodeReviewer_5f269c")} />{!props.workerWizard ? <div className="agent-core-columns"><Choice label={copy("configuration-fields.harness_e3b5b4")} value={data.harness} choices={Object.values(Harness)} change={field("harness")} /><ResourceChoice label={copy("configuration-fields.model_5e2c61")} kind={EntityKind.MODEL} value={text(data.model_id)} change={field("model_id")} activeApiOnly active={active} required markRequired /></div> : null}</>}
      permissions={<AgentPermissions harness={data.harness} options={options} change={field("options")} />}
      reasoning={<><ReasoningEffortField label={copy("configuration-fields.reasoningEffort_3236ae")} value={data.effort} change={field("effort")} disabled={data.harness === Harness.Grok} suggestions={data.harness === Harness.Codex ? codexEffortSuggestions : data.harness === Harness.Claude ? claudeEffortSuggestions : undefined} />{data.harness === Harness.Grok ? <NativeOptionExplanation label={copy("configuration-fields.reasoningEffort_3236ae")} value={data.effort} clear={() => field("effort")("")} /> : null}</>}
      accounts={props.workerWizard ? undefined : <><Choice label={copy("configuration-fields.accountRouting_0c3707")} value={data.routing} choices={Object.values(Routing)} change={(routing) => { const next = { ...data }; if (routing) next.routing = routing; else delete next.routing; change(next); }} inherited /><OrderedLinks label={copy("configuration-fields.accounts_8a7c8b")} kind={EntityKind.ACCOUNT} links={items(data.accounts)} change={field("accounts")} active={active} weighted /></>}
      instructions={<OrderedLinks label={copy("configuration-fields.instructionTemplates_6b009f")} kind={EntityKind.TEMPLATE} links={items(data.templates)} change={field("templates")} active={active} />}
      native={<>
        {data.harness === Harness.Codex ? <CodexSubagentConfiguration options={options} active={active} change={(key, value) => option(key)(value)} /> : <>
          <TextField label={copy("configuration-fields.subagentModel_28463c")} value={options.subagent_model} change={option("subagent_model")} unavailable />
          <ReasoningEffortField label={copy("configuration-fields.subagentEffort_eea2b1")} value={options.subagent_effort} change={option("subagent_effort")} disabled />
          <NativeOptionExplanation label={copy("configuration-fields.subagentEffort_eea2b1")} value={options.subagent_effort} clear={() => option("subagent_effort")("")} />
          <label>{copy("configuration-fields.maximumConcurrency0UsesNativeDefault_451d39")}<input type="number" min={0} max={4294967295} step={1} value={Number(options.max_concurrency ?? 0)} disabled /></label>
          <NativeOptionExplanation label={copy("configuration-fields.maximumConcurrency0UsesNativeDefault_451d39")} value={options.max_concurrency} clear={() => option("max_concurrency")(0)} />
        </>}
        <TextField label={copy("configuration-fields.approvalPolicy_89d24f")} value={options.approval_policy} change={option("approval_policy")} unavailable={data.harness !== Harness.Codex} />
        <TextField label={copy("configuration-fields.approvalReviewModel_ef091f")} value={options.approval_review_model} change={option("approval_review_model")} unavailable />
        <TextField label={copy("configuration-fields.serviceTier_e9cf60")} value={options.service_tier} change={option("service_tier")} unavailable={data.harness !== Harness.Codex} />
        <p>{copy("configuration-fields.unsupportedNativeOptionsProduceAServer_af4e7d")}</p>
      </>}
    />;
  }
  return null;
}

export function RestrictionFields({ label, kind, value, change, active }: { label: string; kind: EntityKind; value: unknown; change: (value: Document) => void; active: boolean }) {
  useLocale();
  const restriction = object(value);
  return <fieldset className="project-field-group"><legend>{label}</legend><Check label={copy("configuration-fields.restrict_b3faa2", { v0: label.toLowerCase() })} value={restriction.configured} change={(configured) => change({ configured, ids: configured ? items(restriction.ids) : [] })} />{restriction.configured === true ? <><p>{copy("configuration-fields.anEmptySelectionPermitsNoneTurning_60a4d7")}</p><OrderedLinks label={copy("configuration-fields.allowed_54fcb8", { v0: label.toLowerCase() })} kind={kind} links={items(restriction.ids)} active={active} explanation="Only these explicitly selected entries are allowed." change={(ids) => change({ ...restriction, ids })} /></> : <p>{copy("configuration-fields.everyOtherwiseEligibleEntryIsAllowed_67c17b")}</p>}</fieldset>;
}
export function RepositoryIdentity({ id, descriptionId }: { id: string; descriptionId?: string }) {
  return <><details className="project-repository-identity"><summary>{copy("project-creation.repositoryDetails")}</summary><code>{id}</code></details>{descriptionId ? <span id={descriptionId} className="project-repository-id"><code>{id}</code></span> : null}</>;
}
export function projectRepositoryOption(id: string, index: number, names: ReadonlyMap<string, string>) {
  const name = names.get(id) ?? copy("project-creation.nameUnavailable");
  return [...names.values()].filter(value => value === name).length > 1 || !names.has(id) ? copy("project-creation.distinctRepository", { name, position: index + 1 }) : name;
}
function ProjectFields({ data, change, active }: FieldsProps) {
  useLocale();
  const repositories = items(data.repositories).map(text);
  const { names, loading, error, retry } = useProjectRepositoryNames(repositories, active);
  return <>
    <fieldset className="project-field-group"><legend>{copy("configuration-fields.name_dcd1d5")}</legend><TextField label={copy("configuration-fields.name_dcd1d5")} value={data.name} required change={(name) => change({ ...data, name })} /></fieldset>
    <fieldset className="project-field-group"><legend>{copy("configuration-fields.repositories_1e32af")}</legend>
      <OrderedLinks label={copy("configuration-fields.orderedRepositories_f1a12d")} kind={EntityKind.REPOSITORY} links={repositories} active={active} names={names} explanation={copy("project-creation.workspaceHelp")} change={(values) => change({ ...data, repositories: values, primary_repository: values.includes(data.primary_repository) ? data.primary_repository : "" })} />
      {loading ? <p role="status">{copy("project-creation.loadingNames")}</p> : null}<Problem error={error} />
      {error ? <button type="button" disabled={loading} onClick={retry}>{copy("project-creation.retryRead")}</button> : null}
      <label>{copy("configuration-fields.primaryRepository_b2bbc5")}<select required value={text(data.primary_repository)} onChange={(event) => change({ ...data, primary_repository: event.target.value })}><option value="">{copy("configuration-fields.selectThePrimaryRepository_bd9082")}</option>{repositories.map((id, index) => <option key={id} value={id}>{projectRepositoryOption(id, index, names)}</option>)}</select></label>
      <p>{copy("configuration-fields.theHarnessStartsInThisRepository_8c3af5")}</p>
    </fieldset>
    <RestrictionFields label={copy("configuration-fields.agentWorkers_e60c23")} kind={EntityKind.AGENT} value={data.agents} active={active} change={(agents) => change({ ...data, agents })} />
    <RestrictionFields label={copy("configuration-fields.aiAccounts_050a21")} kind={EntityKind.ACCOUNT} value={data.accounts} active={active} change={(accounts) => change({ ...data, accounts })} />
  </>;
}
enum ReferenceType { Local = "local-branch", Remote = "remote-branch", Commit = "commit" }
export function ReferenceFields({ label, value, change }: { label: string; value: unknown; change: (value: Document) => void }) {
  useLocale();
  const reference = object(value);
  return <fieldset><legend>{label}</legend><label><LocalizedText id="configuration-fields.type_afa281" components={{ s0: <>{label}</> }} /><select value={text(reference.type)} onChange={(event) => change(event.target.value ? { type: event.target.value, name: text(reference.name), ...(event.target.value === ReferenceType.Remote ? { remote: text(reference.remote) } : {}) } : {})}><option value="">{copy("configuration-fields.useInspectedDefault_495882")}</option>{Object.values(ReferenceType).map((type) => <option key={type} value={type}>{type}</option>)}</select></label>{reference.type ? <><TextField label={copy("configuration-fields.name_c1e789", { v0: label })} value={reference.name} required max={1024} change={(name) => change({ ...reference, name })} />{reference.type === ReferenceType.Remote ? <TextField label={copy("configuration-fields.remote_aa8dc0", { v0: label })} value={reference.remote} required change={(remote) => change({ ...reference, remote })} /> : null}</> : null}</fieldset>;
}
function RemediationFields({ value, change, active, workflow = RunnerWorkflow.RepositoryRemediation, presentation = RemediationDetailPresentation.Expanded }: { value: Document; change: (value: Document) => void; active: boolean; workflow?: RunnerWorkflow; presentation?: RemediationDetailPresentation }) {
  useLocale();
  const runner = useRunnerPreference(workflow, active);
  const touched = useRef(Boolean(value.machine_id));
  useEffect(() => { if (active && !touched.current && !value.machine_id && runner.suggestion) change({ ...value, machine_id: runner.suggestion.id }); }, [active, runner.suggestion, value.machine_id]);
  const identity = (key: string, id: string) => { const next = { ...value }; if (id) next[key] = id; else delete next[key]; change(next); };
  return <RemediationPolicyFields value={value} change={change} presentation={presentation}>
    <ResourceChoice label={copy("configuration-fields.remediationAgentWorker_6bee33")} kind={EntityKind.AGENT} value={text(value.agent_id)} active={active} change={id => identity("agent_id", id)} />
    <ResourceChoice label={copy("configuration-fields.remediationRunnerDevice_c56ba6")} kind={EntityKind.MACHINE} value={text(value.machine_id)} active={active} change={id => { touched.current = true; runner.touch(); identity("machine_id", id); }} />{runner.guidance}
  </RemediationPolicyFields>;
}

export function RepositoryFields({ data, change, active, pendingOperation, requiredCheckout, registration = false, existing }: FieldsProps & { registration?: boolean; requiredCheckout?: { machine_id: string; path: string } }) {
  useLocale();
  const [machine, setMachine] = useState(""), [path, setPath] = useState("");
  const runner = useRunnerPreference(RunnerWorkflow.Checkout, active);
  const touched = useRef(false);
  const [unknownInspection, setUnknownInspection] = useState(false);
  const [inspection, setInspection] = useState<{ job: Resource; machine: string }>();
  const inspect = useRetainedMutation("repository-editor:inspect", WorkerQuery.inspectRepository, (result) => { if (result.job && text(document(result.job).machine_id)) setInspection({ job: result.job, machine: text(document(result.job).machine_id) }); else setUnknownInspection(true); });
  const checkouts = items(data.checkouts).map(object), blocked = inspect.busy || inspect.uncertain || Boolean(inspection) || unknownInspection;
  useEffect(() => { if (active && !touched.current && !blocked && !machine && runner.suggestion) setMachine(runner.suggestion.id); }, [active, runner.suggestion, blocked, machine]);
  useEffect(() => { pendingOperation?.(blocked); return () => pendingOperation?.(false); }, [blocked, pendingOperation]);
  const checkoutBacked = Array.isArray(data.checkouts) && data.checkouts.length > 0;
  // Share every field and operation between presentations; disclosure state
  // changes layout only and never changes the original controller lifetime.
  const identityName = <><TextField label={copy("configuration-fields.name_dcd1d5")} value={data.name} required change={(name) => change({ ...data, name })} /></>;
  const identityRemote = <>{!registration ? <TextField label={copy("configuration-fields.repositoryEdit.remoteUrl")} value={data.remote_url} max={4096} required={!checkoutBacked} change={(remote_url) => change({ ...data, remote_url })} /> : null}</>;
  const preferredRemote = <><TextField label={copy("configuration-fields.preferredGitRemote_ef1241")} value={data.preferred_remote} change={(preferred_remote) => change({ ...data, preferred_remote })} /></>;
  const githubIdentity = <><TextField label={copy("configuration-fields.githubRepositoryOwner_47e01a")} value={data.github_owner} max={100} change={(github_owner) => change({ ...data, github_owner })} /><TextField label={copy("configuration-fields.githubRepositoryName_b09ffb")} value={data.github_name} max={100} change={(github_name) => change({ ...data, github_name })} /></>;
  const githubProfile = <><ResourceChoice label={copy("configuration-fields.githubProfile_5a175c")} kind={EntityKind.INTEGRATION} value={text(data.integration_id)} active={active} change={(integration_id) => change({ ...data, integration_id })} /><p>{copy("configuration-fields.aMissingOrDeletedProfileRequires_1ea24e")}</p></>;
  const inspectionGuidance = <><p>{copy("configuration-fields.inspectionReadsTheSelectedWorkerS_2a92e4")}</p></>;
  const checkoutAddition = <>{runner.guidance}<fieldset disabled={blocked}><legend>{copy("configuration-fields.addACheckout_0ee4d5")}</legend><ResourceChoice label={copy("configuration-fields.runnerDevice_37efe3")} kind={EntityKind.MACHINE} value={machine} active={active} change={id => { touched.current = true; runner.touch(); setMachine(id); }} /><TextField label={copy("configuration-fields.absoluteCheckoutPathOnThisWorker_4274b3")} value={path} max={4096} change={setPath} /><button type="button" disabled={!machine || !path || checkouts.some((checkout) => checkout.machine_id === machine) || checkouts.length >= 1000} onClick={() => void inspect.send({ requestId: newRequestId(), machineId: machine, path, preferredRemote: text(data.preferred_remote) })}>{copy("configuration-fields.inspectCheckout_55462c")}</button></fieldset><Problem error={inspect.error} />{inspect.uncertain ? <button type="button" disabled={inspect.busy} onClick={inspect.retry}>{copy("configuration-fields.retryTheSameInspection_8ce3eb")}</button> : null}
    {unknownInspection ? <p role="alert">{copy("configuration-fields.inspectionWasAcknowledgedWithoutAReadable_568e22")}</p> : null}{inspection ? <TrackedJob initial={inspection.job} active={active}>{(state, output) => <>{state === JobState.Succeeded ? <><p><LocalizedText id="configuration-fields.inspectedRoot_722a3f" components={{ s0: <>{text(output.root)}</> }} /></p><p><LocalizedText id="configuration-fields.remotes_c63ee4" components={{ s0: <>{items(output.remotes).map(text).join(", ") || copy("configuration-fields.extra.dc937b598926")}</> }} /></p><p><LocalizedText id="configuration-fields.recordedRemoteDefaults_76261a" components={{ s0: <>{Object.entries(object(output.default_refs)).map(([remote, ref]) => `${remote}: ${text(ref)}`).join(", ") || copy("configuration-fields.extra.b764cdc0eab7")}</> }} /></p><button type="button" disabled={!text(output.root) || checkouts.some((checkout) => checkout.machine_id === inspection.machine)} onClick={() => { change({ ...data, checkouts: [...checkouts, { machine_id: inspection.machine, path: text(output.root) }] }); setInspection(undefined); setPath(""); setMachine(""); }}>{copy("configuration-fields.addInspectedCheckout_5f3da2")}</button></> : null}{[JobState.Succeeded, JobState.Failed, JobState.Canceled].includes(state as JobState) ? <button type="button" onClick={() => setInspection(undefined)}>{copy("configuration-fields.closeInspection_009fe6")}</button> : null}</>}</TrackedJob> : null}
</>;
  const checkoutList = <><ol>{checkouts.map((checkout) => {
      const required = Boolean(requiredCheckout && checkout.machine_id === requiredCheckout.machine_id && checkout.path === requiredCheckout.path);
      return <li key={text(checkout.machine_id)}>{existing && !registration ? <RepositoryCheckoutIdentity machineId={text(checkout.machine_id)} path={text(checkout.path)} active={active} /> : <p>{text(checkout.machine_id)} · {text(checkout.path)}</p>}{required ? <p>{copy("configuration-fields.useChangeFolderToReplaceThis_dd80d8")}</p> : null}<button type="button" disabled={required} onClick={() => change({ ...data, checkouts: checkouts.filter((row) => row.machine_id !== checkout.machine_id) })}>{copy("configuration-fields.removeCheckout_3cea97")}</button></li>;
    })}</ol></>;
  const policy = <><ReferenceFields label={copy("configuration-fields.baseReference_40fc78")} value={data.base} change={(base) => change({ ...data, base })} /><ReferenceFields label={copy("configuration-fields.startingReference_208e66")} value={data.starting} change={(starting) => change({ ...data, starting })} /><p>{copy("configuration-fields.baseControlsComparisonsStartingControlsPrepared_b7c9a3")}</p><Check label={copy("configuration-fields.fetchTheExactSelectedRemoteBefore_4a94d3")} value={data.auto_fetch} change={(auto_fetch) => change({ ...data, auto_fetch })} />
    <fieldset><legend>{copy("configuration-fields.repositoryRemediation_d12228")}</legend>
      {data.remediation == null ? <><p>{copy("configuration-fields.thisRepositoryInheritsTheCompleteServer_f085fd")}</p><button type="button" onClick={() => change({ ...data, remediation: defaultRemediationPolicy() })}>{copy("configuration-fields.setRepositoryPolicyWithAutomationOff_3afe45")}</button></> : <><p>{copy("configuration-fields.thisCompleteRepositoryPolicyReplacesThe_cbecd8")}</p><RemediationFields value={object(data.remediation)} active={active} change={remediation => change({ ...data, remediation })} /><button type="button" onClick={() => { const next = { ...data }; delete next.remediation; change(next); }}>{copy("configuration-fields.useServerRemediationPolicy_02f78a")}</button></>}
    </fieldset></>;
  if (!existing || registration) return <>{identityRemote}{identityName}{preferredRemote}{githubIdentity}{githubProfile}{inspectionGuidance}{checkoutAddition}{checkoutList}{policy}</>;
  return <RepositoryEditSections inspectionProblem={Boolean(inspect.error || inspect.uncertain || unknownInspection)}
    identity={<>{identityName}{identityRemote}</>}
    github={<><div className="repository-github-columns">{githubIdentity}</div>{githubProfile}</>}
    checkouts={checkouts.length ? checkoutList : <p className="repository-empty">{copy("configuration-fields.repositoryEdit.noCheckouts")}</p>}
    addition={<>{checkoutAddition}{inspectionGuidance}</>}
    advanced={<>{preferredRemote}{policy}</>} />;
}

function ModelFields({ data, change, active, existing }: FieldsProps) {
  useLocale();
  const native = data.source_kind === NativeModelSourceKind.Subscription;
  const service = subscriptionService(data.subscription_service);
  const field = (key: string) => (value: unknown) => change({ ...data, [key]: value });
  const chooseSource = (source: string) => {
    const next = { ...data };
    if (source === NativeModelSourceKind.Subscription) {
      delete next.provider_id;
      next.source_kind = NativeModelSourceKind.Subscription;
      next.subscription_service = SubscriptionServiceId.ChatGPT;
      next.harnesses = [subscriptionServiceHarnesses[SubscriptionServiceId.ChatGPT]];
    } else {
      delete next.source_kind; delete next.subscription_service;
      next.provider_id = ""; next.harnesses = [];
    }
    change(next);
  };
  return <>
    <label>{copy("configuration-fields.modelSource_9fb88c")}<select disabled={existing} value={native ? NativeModelSourceKind.Subscription : "api"} onChange={(event) => chooseSource(event.target.value)}><option value="api">{copy("configuration-fields.apiProvider_aa64ce")}</option><option value={NativeModelSourceKind.Subscription}>{copy("configuration-fields.subscriptionService_0e16df")}</option></select></label>
    {native ? <label>{copy("configuration-fields.subscriptionService_0e16df")}<select required disabled={existing} value={service ?? ""} onChange={(event) => { const service = subscriptionService(event.target.value); if (service) change({ ...data, subscription_service: service, harnesses: [subscriptionServiceHarnesses[service]] }); }}><option value="" disabled>{copy("configuration-fields.selectService_75c533")}</option>{Object.values(SubscriptionServiceId).map((service) => <option key={service} value={service}>{subscriptionServiceNames[service]}</option>)}</select></label> : <ResourceChoice label={copy("configuration-fields.provider_472590")} kind={EntityKind.PROVIDER} value={text(data.provider_id)} change={field("provider_id")} activeApiOnly active={active} disabled={existing} required />}
    <TextField label={copy("configuration-fields.nativeModelId_3f1d9b")} value={data.native_id} change={field("native_id")} disabled={existing} required /><TextField label={copy("configuration-fields.displayName_2b7f6a")} value={data.name} change={field("name")} required /><TextField label={copy("configuration-fields.cliAlias_839cba")} value={data.alias} change={field("alias")} max={128} />
    <fieldset><legend>{copy("configuration-fields.configuredHarnessCompatibility_0115e8")}</legend>{native ? <p>{service ? subscriptionServiceHarnesses[service] : copy("configuration-fields.unsupportedService_724094")}</p> : Object.values(Harness).map((harness) => <Check key={harness} label={harness} value={items(data.harnesses).includes(harness)} change={(selected) => field("harnesses")(selected ? [...items(data.harnesses), harness] : items(data.harnesses).filter((value) => value !== harness))} />)}<p>{copy("configuration-fields.configuredCompatibilityIsCheckedAgainstThe_80980f")}</p></fieldset>
    <Check label={copy("configuration-fields.hideFromDefaultModelLists_e0398b")} value={data.hidden} change={field("hidden")} /><label>{copy("configuration-fields.displayOrder_540fc6")}<input type="number" min={-2147483648} max={2147483647} value={Number(data.order)} onChange={(event) => field("order")(Number(event.target.value))} /></label>{data.new === true ? <Check label={copy("configuration-fields.keepNewMarkerUntilReviewed_52aeec")} value={data.new} change={field("new")} /> : null}<p><LocalizedText id="configuration-fields.metadataContextLimit_91b325" components={{ s0: <>{text(data.metadata_source)}</>, s1: <>{data.context_limit == null ? copy("configuration-fields.unknown_b764cd") : String(data.context_limit)}</> }} /></p>
  </>;
}

function AgentReconfiguration({ data, change, active }: { data: Document; change: (value: Document) => void; active: boolean }) {
  useLocale();
  const model = useQuery(ResourceQuery.getResource, { kind: EntityKind.MODEL, id: text(data.model_id) }, { enabled: active && data.reconfiguration_required === true && Boolean(text(data.model_id)), retry: false });
  if (data.reconfiguration_required !== true) return null;
  const valid = model.data?.resource && supportsResourceSchema(model.data.resource) && document(model.data.resource).retired !== true && model.data.resource.id === data.model_id;
  return <section role="status"><p>{copy("configuration-fields.legacySubscriptionConfigurationWasRetiredChoose_bb6be6")}</p><Problem error={model.error} /><button type="button" disabled={!valid || Boolean(model.error || model.isFetching)} onClick={() => change({ ...data, reconfiguration_required: false })}>{copy("configuration-fields.confirmReconfiguredModelAndAccounts_021890")}</button></section>;
}
