import { formatTimestamp } from "./localization";
import { LocalizedText, copy, useLocale } from "./localization";
import { SettingsTaskDialog, SettingsTaskActions, SettingsDialogSize, SettingsDialogFocus } from "./settings-task";
import { useSettingsTaskDismiss, useCloseSettingsTask } from "./settings-task-context";
import { SettingsHeading, SettingsEmpty, SettingsLoading } from "./settings-presentation";
import { useEffect, useRef, useState, useId } from "react";
import { useMutation, useQuery } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { EntityKind, FailureCode, IntegrationQuery, ResourceQuery, SystemCapability, SystemQuery, clientFailure, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { document, encode, object, resourceName, text, type Document } from "./documents";
import { useRetainedMutation } from "./mutation";
import { ServiceProblem, Problem  } from "./ui";
import { GitHubTokenForm } from "./github-opening";
import { GitHubOnboarding, type GitHubTokenRetry } from "./github-onboarding";
import { useSettingsOpening } from "./settings-lifetime";

enum TokenKind { FineGrained = "fine-grained", Classic = "classic" }
enum IdentityState {
  Verified = "identity-verified", Invalid = "invalid-token", Restricted = "access-restricted",
  SsoRequired = "sso-required", RateLimited = "rate-limited", Unavailable = "unavailable",
}
const identityLabels: Record<IdentityState, string> = {
  get [IdentityState.Verified]() { return copy("integrations.identityVerified_99b52f"); }, get [IdentityState.Invalid]() { return copy("integrations.invalidToken_086efe"); },
  get [IdentityState.Restricted]() { return copy("integrations.accessRestricted_233f64"); }, get [IdentityState.SsoRequired]() { return copy("integrations.ssoRequired_49d6fb"); },
  get [IdentityState.RateLimited]() { return copy("integrations.rateLimited_a06130"); }, get [IdentityState.Unavailable]() { return copy("integrations.unavailable_ca1844"); },
};
function profileDescription(data: Document): string {
  const kind = data.token_kind === TokenKind.FineGrained ? copy("integrations.extra.ccbe41029c83") : data.token_kind === TokenKind.Classic ? copy("integrations.extra.41e9c09d008a") : copy("integrations.extra.3471e3c6191a");
  return `${kind} · ${text(data.resource_owner) || copy("integrations.extra.6ae50316d97d")}`;
}
function ProfileFacts({ profile }: { profile: Resource }) {
  useLocale();
  const data = document(profile), validation = object(object(data.connection).validation);
  const state = text(validation.state);
  const supported = profile.schemaVersion === 1;
  return <dl className="integration-facts">
    <div><dt>{copy("integrations.tokenStorage_9e7378")}</dt><dd>{!supported ? copy("integrations.unsupportedProfileVersion_2e278a") : data.pending ? copy("integrations.changePendingTokenUseDisabled_8ff1c5") : data.connection ? copy("integrations.tokenStored_4475d6") : copy("integrations.noTokenConnected_1d18ae")}</dd></div>
    <div><dt>{copy("integrations.identityValidation_657b2c")}</dt><dd>{!supported ? copy("integrations.unsupportedProfileVersion_2e278a") : !state ? copy("integrations.notVerified_151339") : Object.hasOwn(identityLabels, state) ? identityLabels[state as IdentityState] : copy("integrations.unknownIdentityObservation_bb9e25")}</dd>
      {supported && text(validation.checked_at) ? <dd className="integration-secondary"><LocalizedText id="integrations.checkedAt_0485ac" components={{ s0: <>{formatTimestamp(text(validation.checked_at))}</> }} /></dd> : null}
    </div>
  </dl>;
}
enum IntegrationIconKind { GitHub, Link, Shield }
function IntegrationIcon({ kind }: { kind: IntegrationIconKind }) {
  useLocale();
  return <svg className="integration-icon" viewBox="0 0 24 24" aria-hidden="true" focusable="false">
    {kind === IntegrationIconKind.GitHub ? <path d="M7 19c-4 1-4-2-6-2m7 5v-4c0-1 .5-2 1-2-4 0-6-2-6-5 0-2 1-3 2-4 0-1 0-3 1-4l4 2h4l4-2c1 1 1 3 1 4 1 1 2 2 2 4 0 3-2 5-6 5 1 0 1 1 1 2v4" />
      : kind === IntegrationIconKind.Link ? <path d="m9 15 6-6m-6 8-2 2a4 4 0 0 1-6-6l4-4a4 4 0 0 1 6 0m2-2 2-2a4 4 0 0 1 6 6l-4 4a4 4 0 0 1-6 0" />
      : <path d="m12 2 8 3v6c0 5-4 8-8 11-4-3-8-6-8-11V5Zm-4 9 3 3 5-6" />}
  </svg>;
}
const tokenStorageNote = () => copy("integrations.extra.59b5da9c12c7");
type MutationIdentity = { id: string; expectedRevision: bigint; requestId: string };
function pendingIdentity(id: string, pending: Document): MutationIdentity | undefined {
  const revision = text(pending.expected_revision), requestId = text(pending.request_id);
  if (!/^[1-9][0-9]{0,18}$/.test(revision) || !/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(requestId)) return undefined;
  return { id, expectedRevision: BigInt(revision), requestId };
}
function problemDocument(raw: Uint8Array): Document | undefined {
  if (!raw.byteLength) return undefined;
  try { return object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(raw))); } catch { return { message: "The operation needs inspection. Refresh the profile before continuing." }; }
}
function IntegrationEditor({ initial, active, close }: { initial?: Resource; active: boolean; close: () => void }) {
  useLocale();
  const formId = useId(), closeTask = useCloseSettingsTask(close);
  const [name, setName] = useState(() => text(document(initial).name));
  const [kind, setKind] = useState(() => text(document(initial).token_kind) || TokenKind.FineGrained);
  const [owner, setOwner] = useState(() => text(document(initial).resource_owner));
  const save = useRetainedMutation(`integration-save:${initial?.id ?? "new"}`, IntegrationQuery.saveIntegrationProfile, close);
  const current = useQuery(ResourceQuery.getResource, { kind: EntityKind.INTEGRATION, id: initial?.id ?? "" }, { enabled: active && Boolean(initial), refetchInterval: active && initial ? 5000 : false });
  const stale = Boolean(initial && current.data?.resource && current.data.resource.revision !== initial.revision);
  const blocked = save.busy || save.uncertain;
  const nameInput = useRef<HTMLInputElement>(null);
  // Focus only when this editor first becomes visible. Category changes and
  // background reads retain its draft and must not steal focus on return.
  const entered = useRef(false);
  useEffect(() => { if (active && !entered.current) { entered.current = true; nameInput.current?.focus(); } }, [active]);
  return <form id={formId} className="integration-editor" onSubmit={(event) => { event.preventDefault(); if (blocked || stale || current.error) return; void save.send({ mutation: { id: initial?.id ?? "", expectedRevision: initial?.revision ?? 0n, requestId: newRequestId() }, schemaVersion: 1, documentJson: encode({ name, provider: copy("integrations.extra.3aeb00246038"), token_kind: kind, ...(owner ? { resource_owner: owner } : {}) }) }); }}>
    <h3>{initial ? copy("integrations.renameGithubProfile_1f9dd2") : copy("integrations.newGithubProfile_e9e486")}</h3>
    <fieldset disabled={blocked}><label>{copy("integrations.profileName_d36632")}<input ref={nameInput} required maxLength={160} value={name} onChange={(event) => setName(event.target.value)} /></label><label>{copy("integrations.tokenType_ced916")}<select disabled={Boolean(initial)} value={kind} onChange={(event) => setKind(event.target.value)}><option value={TokenKind.FineGrained}>{copy("integrations.fineGrainedPatPreferred_70fe9c")}</option><option value={TokenKind.Classic}>{copy("integrations.classicPat_41e9c0")}</option></select></label><label>{copy("integrations.resourceOwner_f8abf0")}<input disabled={Boolean(initial)} required={kind === TokenKind.FineGrained} maxLength={100} pattern="[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?" value={owner} onChange={(event) => setOwner(event.target.value)} /></label><p>{copy("integrations.useASeparateFineGrainedProfile_11aa24")}</p><p>{copy("integrations.tokenTypeAndOwnerCannotBe_4aad40")}</p></fieldset>
    {stale ? <p role="alert">{copy("integrations.thisProfileChangedReopenItsCurrent_50a08c")}</p> : null}<Problem error={current.error || save.error} />
    <SettingsTaskActions form={formId}><button className="primary" disabled={blocked || stale || Boolean(current.error)}>{copy("integrations.saveProfile_0c8209")}</button>{save.uncertain ? <button type="button" disabled={save.busy} onClick={save.retry}>{copy("integrations.retryTheSameProfileSave_c79c48")}</button> : null}<button type="button" data-settings-task-cancel onClick={closeTask}>{copy("integrations.cancelEdit_6fa271")}</button></SettingsTaskActions>
  </form>;
}
function IntegrationCreation({ active, close, connected }: { active: boolean; close: () => void; connected: (profile: Resource, retry?: GitHubTokenRetry, problem?: Document) => void }) {
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active, retry: false });
  const negotiated = useRef<boolean | undefined>(undefined);
  const capabilities = status.data?.capabilities;
  const supported = !status.error && capabilities?.includes(SystemCapability.GITHUB_TOKEN_ONBOARDING_V1) && new Set(capabilities).size === capabilities.length && capabilities.every(value => value !== SystemCapability.UNSPECIFIED && Object.values(SystemCapability).includes(value));
  // Keep a settled older-server editor mounted across inactive query states so
  // its exact metadata retry cannot be replaced by the negotiation placeholder.
  if (!status.isPending) negotiated.current = Boolean(supported);
  if (negotiated.current === undefined) return <><SettingsLoading label="Checking GitHub connection support…" /><button onClick={close}>Cancel</button></>;
  return negotiated.current ? <GitHubOnboarding active={active} close={close} connected={connected} /> : <><p role="status">Update the selected server to verify a token before creating a profile. You can still create a profile first below.</p><IntegrationEditor active={active} close={close} /></>;
}
function IntegrationConnection({ initial, active, close, initialRetry, initialProblem }: { initial: Resource; active: boolean; close: () => void; initialRetry?: GitHubTokenRetry; initialProblem?: Document }) {
  useLocale();
  const tokenFormId = useId(), closeTask = useCloseSettingsTask(close);
  const result = useQuery(ResourceQuery.getResource, { kind: EntityKind.INTEGRATION, id: initial.id }, { enabled: active, refetchInterval: active ? 5000 : false });
  const [acknowledged, setAcknowledged] = useState<Resource>();
  const current = [initial, result.data?.resource, acknowledged].filter((row): row is Resource => Boolean(row)).reduce((a, b) => a.revision >= b.revision ? a : b);
  const data = document(current), pending = object(data.pending), connection = object(data.connection), validation = object(connection.validation), identity = object(validation.identity);
  const [token, setToken] = useState("");
  const [retryIdentity, setRetryIdentity] = useState<MutationIdentity | undefined>(initialRetry);
  const [tokenError, setTokenError] = useState<unknown>();
  const [problem, setProblem] = useState<Document | undefined>(initialProblem);
  const [confirm, setConfirm] = useState(false);
  const opening = useSettingsOpening();
  const initiallyExplainToken = useRef(!document(initial).connection);

  const replace = useMutation(IntegrationQuery.replaceIntegrationToken, { retry: false, gcTime: 0, meta: opening?.mutationMeta });
  const changed = (row?: Resource) => { if (row) setAcknowledged(row); void result.refetch(); };
  const validate = useRetainedMutation(`integration-validate:${initial.id}`, IntegrationQuery.validateIntegrationProfile, (reply) => { changed(reply.profile); setProblem(problemDocument(reply.problemJson)); });
  const remove = useRetainedMutation(`integration-delete:${initial.id}`, IntegrationQuery.deleteIntegrationProfile, (reply) => { setConfirm(false); setProblem(problemDocument(reply.problemJson)); if (reply.deleted) close(); else changed(reply.profile); });
  useSettingsTaskDismiss(() => setToken(""));
  const blocked = replace.isPending || validate.busy || validate.uncertain || remove.busy || remove.uncertain;
  const original = pendingIdentity(initial.id, pending);
  const deleting = pending.operation === "delete-profile";
  const mutation = (): MutationIdentity => ({ id: initial.id, expectedRevision: current.revision, requestId: newRequestId() });
  useEffect(() => { if (!active) setToken(""); }, [active]);
  const sendToken = async () => {
    if (blocked || deleting || !/^[!-~]{1,512}$/.test(token) || (pending.operation === "replace-token" && !original)) return;
    const selected = retryIdentity ?? (pending.operation === "replace-token" ? original : undefined) ?? mutation();
    const bytes = new TextEncoder().encode(token);
    setToken(""); setTokenError(undefined); setProblem(undefined); setRetryIdentity(selected);
    try {
      const reply = await replace.mutateAsync({ mutation: selected, token: bytes });
      changed(reply.profile); setProblem(problemDocument(reply.problemJson));
      setRetryIdentity(undefined);
    } catch (error) {
      setTokenError(error);
      if (![FailureCode.Unavailable, FailureCode.ServerUnavailable, FailureCode.Canceled, FailureCode.Internal].includes(clientFailure(error).code)) setRetryIdentity(undefined);
    } finally {
      // PAT bytes are transient write-only input. Neither successful nor failed
      // mutations retain a token for replay; retries require explicit reentry.
      bytes.fill(0); replace.reset();
    }
  };
  return <section className="integration-manage">
    <header><div><h3>{resourceName(current)}</h3><p>{profileDescription(data)}</p></div><button disabled={replace.isPending} onClick={closeTask}>{copy("integrations.backToGithubProfiles_48e4d1")}</button></header>
    <ProfileFacts profile={current} />
    {text(identity.login) ? <p><LocalizedText id="integrations.authenticatedAsGithubId_5a4d87" components={{ s0: <>{text(identity.login)}</>, s1: <>{text(identity.id)}</> }} /></p> : null}
    <section className="integration-section" aria-label={copy("integrations.connectAToken_d30079")}><h4>{copy("integrations.connectAToken_d30079")}</h4>
      <details className="integration-token-guidance" open={initiallyExplainToken.current}>
        <summary>{copy("integrations.createATokenOnGithub_519994")}</summary>
        {active ? <GitHubTokenForm key={`${current.id}:${current.revision}`} profile={current} active={active} disabled={blocked || Boolean(retryIdentity || data.pending || result.error)} showHeading={false} /> : null}
      </details>
      <form id={tokenFormId} onSubmit={(event) => { event.preventDefault(); void sendToken(); }}><fieldset disabled={blocked || deleting || Boolean(result.error)}>
        <label>{copy("integrations.githubPersonalAccessToken_235203")}<input type="password" autoComplete="off" spellCheck={false} maxLength={512} placeholder={copy("integrations.enterAPersonalAccessToken_b4175a")} value={token} onChange={(event) => setToken(event.target.value)} /></label>
        <p className="integration-secondary">{tokenStorageNote()}</p>
        {retryIdentity || pending.operation === "replace-token" ? <p>{copy("integrations.reenterTheSameTokenToRetry_d082cd")}</p> : null}
      </fieldset><SettingsTaskActions form={tokenFormId}><button className="primary" disabled={blocked || deleting || Boolean(result.error) || !/^[!-~]{1,512}$/.test(token) || (pending.operation === "replace-token" && !original)}>{retryIdentity || pending.operation === "replace-token" ? copy("integrations.retryOriginalTokenReplacement_cdf40d") : copy("integrations.saveAndValidateToken_d79171")}</button></SettingsTaskActions></form>
      {retryIdentity ? <p><LocalizedText id="integrations.pendingRequest_2efde7" components={{ s0: <>{retryIdentity.requestId}</> }} /></p> : null}
    </section>
    <section className="integration-section" aria-label={copy("integrations.validateIdentity_1ba57b")}><h4>{copy("integrations.identityValidation_657b2c")}</h4>
      <p>{copy("integrations.identityValidationDoesNotVerifyAccess_7be91f")}</p>
      <button disabled={blocked || Boolean(retryIdentity || data.pending || !data.connection || result.error)} onClick={() => void validate.send({ mutation: mutation() })}>{copy("integrations.validateProfile_e7f4ea")}</button>
    </section>
    <section className="integration-section integration-delete" aria-label={copy("integrations.deleteProfile_47311a")}><h4>{copy("integrations.deleteProfile_47311a")}</h4>
      <p>{copy("integrations.deletionRequiresConfirmationRepositoryAssociationsWill_c1a76b")}</p>
      <button className="integration-danger" disabled={blocked || Boolean(result.error) || (deleting && !original)} onClick={() => deleting && original ? void remove.send({ mutation: original }) : setConfirm(true)}>{deleting ? copy("integrations.retryOriginalProfileDeletion_861363") : copy("integrations.deleteProfile_47311a")}</button>
      {confirm ? <SettingsTaskDialog title={copy("integrations.deleteProfile_47311a")} size={SettingsDialogSize.Confirmation} focus={SettingsDialogFocus.Cancel} close={() => setConfirm(false)}><div className="notice"><p>{copy("integrations.deleteThisProfileAndItsServer_d6827d")}</p><SettingsTaskActions className=""><button className="integration-danger" disabled={blocked} onClick={() => void remove.send({ mutation: mutation() })}>{copy("integrations.confirmProfileDeletion_079ac8")}</button><button data-settings-task-cancel disabled={blocked} onClick={() => setConfirm(false)}>{copy("integrations.keepProfile_8e76f0")}</button></SettingsTaskActions></div></SettingsTaskDialog> : null}
    </section>
    {text(object(validation.problem).message) ? <ServiceProblem code={text(object(validation.problem).code) || text(object(validation.problem).problem_code)}><p role="alert">{text(object(validation.problem).message)} {text(object(validation.problem).guidance)}</p></ServiceProblem> : null}{problem ? <p role="alert">{text(problem.message)} {text(problem.guidance)}</p> : null}
    <Problem error={result.error || tokenError} />{[validate, remove].map((operation, index) => <div key={index}><Problem error={operation.error} />{operation.uncertain ? <button disabled={operation.busy} onClick={operation.retry}><LocalizedText id="integrations.retryTheSame_4cb78a" components={{ s0: <>{index === 0 ? copy("integrations.validation_98c41d") : copy("integrations.deletion_7770ba")}</> }} /></button> : null}</div>)}
  </section>;
}

export function Integrations({ active, showCategoryIntro = true, onWorkflowReadyChange }: { active: boolean; showCategoryIntro?: boolean; onWorkflowReadyChange?: (active: boolean) => void }) {
  useLocale();
  const [page, setPage] = useState("");
  const [editing, setEditing] = useState<{ initial?: Resource; key: string }>();
  const [selected, setSelected] = useState<{ profile: Resource; retry?: GitHubTokenRetry; problem?: Document }>();
  const createButton = useRef<HTMLButtonElement>(null);
  const returning = useRef(false);
  const client = useQueryClient();
  const result = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.INTEGRATION, pageSize: 50, pageToken: page } }, { enabled: active });
  const done = () => { returning.current = true; setEditing(undefined); setSelected(undefined); void client.invalidateQueries({ refetchType: "active" }); };
  useEffect(() => {
    onWorkflowReadyChange?.(Boolean(editing || selected));
    return () => onWorkflowReadyChange?.(false);
  }, [editing, onWorkflowReadyChange, selected]);
  useEffect(() => { if (returning.current && !editing && !selected) { returning.current = false; createButton.current?.focus(); } }, [editing, selected]);
  const connected = (profile: Resource, retry?: GitHubTokenRetry, problem?: Document) => { setEditing(undefined); setSelected({ profile, retry, problem }); void client.invalidateQueries({ refetchType: "active" }); };
  const successfulEmpty = Boolean(result.data && !result.error && !page && result.data.resources.length === 0 && !result.data.nextPageToken);
  const createProfile = <button ref={createButton} className="primary" onClick={() => setEditing({ key: newRequestId() })}><LocalizedText id="integrations.newGithubProfile_faeff2" components={{ s0: <span aria-hidden="true">+ </span> }} /></button>;
  return <section className="github-integrations" aria-label={copy("integrations.githubIntegrations_edb779")}>
    {showCategoryIntro ? <SettingsHeading title={copy("integrations.integrations_090512")} description={copy("integrations.manageGithubProfilesForRepositoryAccess_42adb1")} actions={!editing && !selected ? <><button aria-label={copy("integrations.refreshGithubProfiles_c84a3b")} onClick={() => void result.refetch()}>{copy("integrations.refresh_0e9161")}</button>{createProfile}</> : undefined} /> : null}
    <>
      <section className="integration-panel" aria-label={copy("integrations.githubProfiles_e47e4e")} aria-busy={result.isFetching}>
        <header className="integration-panel-header"><div className="integration-provider"><span className="integration-provider-mark"><IntegrationIcon kind={IntegrationIconKind.GitHub} /></span><div><h3>{copy("integrations.github_f911e4")}</h3><p>{copy("integrations.githubComPersonalAccessTokens_03ef1a")}</p></div></div>
          {!showCategoryIntro ? <div className="actions"><button aria-label={copy("integrations.refreshGithubProfiles_c84a3b")} onClick={() => void result.refetch()}>{copy("integrations.refresh_0e9161")}</button>{createProfile}</div> : null}
        </header>
        <div className="integration-panel-body">
          {!result.data && result.isPending ? <SettingsLoading label={copy("integrations.loadingGithubProfiles_9c7706")} /> : null}
          {result.data && result.isFetching ? <p role="status">{copy("integrations.refreshingGithubProfilesPreviousResultsAre_b4bc51")}</p> : null}
          <Problem error={result.error} />
          {result.data && result.error ? <p role="status">{copy("integrations.previousGithubProfileResultsAreStale_78ef2f")}</p> : null}
          {successfulEmpty ? <SettingsEmpty title={copy("integrations.addYourFirstGithubProfile_04b5e5")} icon={<IntegrationIcon kind={IntegrationIconKind.Link} />}><p>{copy("integrations.createANamedProfileThenConnect_b03600")}</p><p>{copy("integrations.eachRepositorySelectsItsProfileExplicitly_72df0e")}</p></SettingsEmpty> : <>
            {result.data?.resources.map((row) => <article className="integration-row" key={row.id}><div><h3>{resourceName(row)}</h3><p className="integration-secondary">{profileDescription(document(row))}</p><ProfileFacts profile={row} /></div>
              <div className="actions"><button aria-label={copy("integrations.manage_e9e199", { v0: resourceName(row) })} disabled={row.schemaVersion !== 1} onClick={() => setSelected({ profile: row })}>{copy("integrations.manage_5a2344")}</button><button aria-label={copy("integrations.rename_089ce7", { v0: resourceName(row) })} disabled={row.schemaVersion !== 1} onClick={() => setEditing({ initial: row, key: newRequestId() })}>{copy("integrations.rename_3064d7")}</button></div>
            </article>)}
            {result.data?.resources.length === 0 ? <p>{copy("integrations.noGithubProfilesOnThisPage_9dbef4")}</p> : null}
          </>}
        </div>
        {successfulEmpty ? <ol className="integration-steps" aria-label={copy("integrations.githubProfileSetupExplanation_75a33a")}><li>{copy("integrations.createAProfile_6d7bee")}</li><li>{copy("integrations.connectAToken_d30079")}</li><li>{copy("integrations.selectItInRepositories_80273c")}</li></ol> : null}
        <p className="integration-access-note">{copy("integrations.identityVerificationDoesNotConfirmRepository_7d904f")}</p>
      </section>
      <p className="integration-storage-note"><IntegrationIcon kind={IntegrationIconKind.Shield} /><span>{tokenStorageNote()}</span></p>
      {!successfulEmpty ? <nav className="settings-pages" aria-label={copy("integrations.githubProfilePages_677951")}><button disabled={!page || result.isFetching} onClick={() => setPage("")}>{copy("integrations.firstPage_0bdbb7")}</button><button disabled={!result.data?.nextPageToken || result.isFetching} onClick={() => setPage(result.data!.nextPageToken)}>{copy("integrations.nextPage_c08ac7")}</button></nav> : null}
    </>
    {editing ? <SettingsTaskDialog key={editing.key} title={editing.initial ? copy("integrations.renameGithubProfile_1f9dd2") : copy("integrations.newGithubProfile_e9e486")} size={SettingsDialogSize.Form} close={done}>{editing.initial ? <IntegrationEditor initial={editing.initial} active={active} close={done} /> : <IntegrationCreation active={active} close={done} connected={connected} />}</SettingsTaskDialog> : null}
    {selected ? <SettingsTaskDialog key={selected.profile.id} title={copy("integrations.manage_e9e199", { v0: resourceName(selected.profile) })} size={SettingsDialogSize.Wide} close={done}><IntegrationConnection initial={selected.profile} initialRetry={selected.retry} initialProblem={selected.problem} active={active} close={done} /></SettingsTaskDialog> : null}
  </section>;
}
