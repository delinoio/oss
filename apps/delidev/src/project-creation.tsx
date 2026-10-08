// SPDX-License-Identifier: Apache-2.0
import { ProjectRepositoryOrder, RepositorySecondaryID } from "./project-repository-order";
import { SettingsTaskDismissButton } from "./settings-task";
import { useCallback, useContext, useDeferredValue, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { EntityKind } from "@delinoio/delidev-api-client";
import { items, text, type Document } from "./documents";
import { RepositoryIdentity, RestrictionFields, projectRepositoryOption } from "./configuration-fields";
import { ProductError, copy, ownedMessage, useLocale, useProductMessage } from "./localization";
import { useProjectRepositoryCatalog } from "./project-repositories";
import { kindNames } from "./configuration-fields";
import { MutationIntents } from "./mutation";
import { ConfigurationEditor } from "./settings";
import { SettingsLifetime } from "./settings-lifetime";
import { SettingsDialogFocus, SettingsDialogSize, SettingsTaskActions, SettingsTaskDialog, SettingsTasks, SettingsTaskStatusOutlet } from "./settings-task";
import { SettingsTaskContext } from "./settings-task-context";
import { Problem } from "./ui";
import { ScrollContinuation, useScrollRoot } from "./scroll-continuation";
import { usePaginationChain } from "./scroll-pagination-query";
import "./project-creation.css";

enum Step { Repositories = 1, Configure, Restrictions }
const steps = [Step.Repositories, Step.Configure, Step.Restrictions];
const stepName = (step: Step) => copy(step === Step.Repositories ? "project-creation.repositoriesStep" : step === Step.Configure ? "project-creation.configureStep" : "project-creation.restrictionsStep");
function validName(name: string) { return Boolean(name.trim()) && !name.includes("\0") && new TextEncoder().encode(name).byteLength <= 256; }
function focusControl(form: HTMLFormElement | null, field: string) {
  const control = form?.querySelector<HTMLElement>(`[data-project-focus="${field}"]`);
  control?.focus({ preventScroll: true });
  control?.scrollIntoView?.({ block: "nearest" });
}

export function ProjectCreationWizard({ data, change, active, visible, blocked, busy, saveDisabled, submit, cancel, cancelDisabled, uncertain, retry, children }: {
  data: Document; change: (value: Document) => void; active: boolean; visible: boolean; blocked: boolean; saveDisabled: boolean;
  busy: boolean; submit: () => void; cancel: () => void; cancelDisabled: boolean; uncertain: boolean; retry: () => void; children?: ReactNode;
}) {
  useLocale();
  const [step, setStep] = useState(Step.Repositories);
  const [nameEdited, setNameEdited] = useState(Boolean(text(data.name)));
  const [search, setSearch] = useState("");
  const resultsRoot = useRef<HTMLDivElement>(null);
  const root = useScrollRoot(resultsRoot);
  const [focusAttempt, setFocusAttempt] = useState(0);
  const [validationField, setValidationField] = useState<"search" | "name" | "primary">();
  const [problem, setProblem] = useProductMessage("");
  const form = useRef<HTMLFormElement>(null), focused = useRef(false);
  const formId = useId(), inputId = useId();
  const catalog = useProjectRepositoryCatalog(active && visible);
  const query = useDeferredValue(search.trim().toLowerCase());
  const names = useMemo(() => new Map(catalog.rows.filter(row => row.supported).map(row => [row.id, row.name])), [catalog.rows]);
  const matches = useMemo(() => catalog.rows.filter(row => row.name.toLowerCase().includes(query)), [catalog.rows, query]);
  const readMatches = useCallback(async (token: string) => {
    const offset = Number(token || 0), rows = matches.slice(offset, offset + 50);
    return { rows, nextPageToken: offset + 50 < matches.length ? String(offset + 50) : "" };
  }, [matches]);
  const results = usePaginationChain(JSON.stringify([query, matches.map(row => [row.id, row.revision.toString()])]), active && visible && !blocked && step === Step.Repositories, readMatches);
  const choices = results.rows;
  const ids = items(data.repositories).map(text);
  const firstName = ids.length ? names.get(ids[0]!) : "";
  useEffect(() => {
    if (active && visible && !blocked && !nameEdited && firstName !== undefined && text(data.name) !== firstName) change({ ...data, name: firstName });
  }, [active, visible, blocked, nameEdited, firstName, data, change]);
  useLayoutEffect(() => {
    if (!active || !visible || !focused.current) return;
    const field = validationField ?? (step === Step.Repositories ? "search" : step === Step.Configure ? "name" : "heading");
    focusControl(form.current, field);
    // Visibility changes alone must not focus a previously hidden operation.
    // The shared task host owns restoration; only an explicit step owns this handoff.
  }, [step, focusAttempt]);
  useEffect(() => {
    if (!active || !visible || focused.current) return;
    // Let the modal host capture its real opener before creation focuses an
    // input. Child layout effects run before the parent's modal admission.
    const frame = requestAnimationFrame(() => {
      const field = step === Step.Repositories ? "search" : step === Step.Configure ? "name" : "heading";
      focusControl(form.current, field);
      focused.current = true;
    });
    return () => cancelAnimationFrame(frame);
  }, [active, visible, step]);
  const repositories = (values: string[]) => {
    if (blocked || !active || !visible) return;
    change({ ...data, repositories: values, primary_repository: !ids.length && values.length ? values[0] : values.includes(text(data.primary_repository)) ? data.primary_repository : "", ...(!nameEdited && (!values.length || names.has(values[0]!)) ? { name: values.length ? names.get(values[0]!) : "" } : {}) });
    setProblem("");
  };
  const validRepositories = ids.length > 0 && ids.length <= 1000 && new Set(ids).size === ids.length;
  const configured = validName(text(data.name)) && ids.includes(text(data.primary_repository));
  const validate = () => {
    if (!validRepositories) { setValidationField("search"); setStep(Step.Repositories); setFocusAttempt(value => value + 1); setProblem(ownedMessage("project-creation.selectRepository")); return false; }
    if (!configured) { setValidationField(validName(text(data.name)) ? "primary" : "name"); setStep(Step.Configure); setFocusAttempt(value => value + 1); setProblem(ownedMessage(validName(text(data.name)) ? "project-creation.selectPrimary" : "project-creation.invalidName")); return false; }
    return true;
  };
  const next = () => {
    if (blocked) return;
    if (step === Step.Repositories && !validRepositories) { setProblem(ownedMessage("project-creation.selectRepository")); return; }
    if (step === Step.Configure && !validate()) return;
    setProblem(""); setValidationField(undefined); setStep(step + 1);
  };
  const selected = (editable: boolean) => <ProjectRepositoryOrder ids={ids} names={names} editable={editable} active={active && visible && !blocked && step === Step.Repositories} change={repositories} />;
  return <form id={formId} ref={form} className="project-editor project-creation" onSubmit={event => {
    event.preventDefault();
    // Enter in search/name fields must never bypass the explicit wizard steps.
    if (step === Step.Restrictions && !saveDisabled && validate()) submit();
  }}>
    <ol className="project-creation-steps" aria-label={copy("project-creation.steps")}>
      {steps.map(value => <li key={value} aria-current={value === step ? "step" : undefined} data-complete={value < step}><span aria-hidden="true">{value < step ? "✓" : value}</span>{stepName(value)}</li>)}
    </ol>
    <section hidden={step !== Step.Repositories}>
      <h3>{stepName(Step.Repositories)}</h3><p>{copy("project-creation.selectHelp")}</p>
      <fieldset disabled={blocked || step !== Step.Repositories}>
        <label className="project-repository-search" htmlFor={inputId}>{copy("project-creation.search")}<input id={inputId} data-project-focus="search" type="search" autoComplete="off" maxLength={256} value={search} placeholder={copy("project-creation.search")} aria-controls={`${inputId}-results`} onChange={event => { setSearch(event.target.value); }} /></label>
        <div ref={resultsRoot} id={`${inputId}-results`} className="project-repository-results" role="group" aria-label={copy("project-creation.repositoryChoices")}>
          {choices.map((row, index) => { const duplicateName = catalog.rows.some(other => other.id !== row.id && other.name === row.name); const identityId = `${inputId}-repository-${index}-identity`; return <div key={row.id} className="project-repository-choice"><label className="checkbox"><input type="checkbox" aria-describedby={duplicateName ? identityId : undefined} checked={ids.includes(row.id)} disabled={!row.supported || (!ids.includes(row.id) && ids.length >= 1000)} onChange={event => repositories(event.target.checked ? [...ids, row.id] : ids.filter(id => id !== row.id))} />{row.name || copy("project-creation.nameUnavailable")}</label>{duplicateName || !row.supported ? <RepositoryIdentity id={row.id} descriptionId={duplicateName ? identityId : undefined} /> : null}{!row.supported ? <small>{copy("project-creation.unsupportedRepository")}</small> : null}</div>; })}
        <ScrollContinuation query={results} root={root} active={active && visible && !blocked && step === Step.Repositories} label={copy("project-creation.repositoryChoices")} />
        </div>

        {catalog.loading ? <p role="status">{copy(catalog.rows.length ? "project-creation.loadingMore" : "project-creation.loading")}</p> : null}
        {catalog.complete && !matches.length ? <p role="status">{copy(catalog.rows.length ? "project-creation.noMatches" : "project-creation.empty")}</p> : null}
        {catalog.error ? <><p role="status">{copy("project-creation.incomplete")}</p>{catalog.error instanceof ProductError ? <p role="alert">{copy(catalog.error.productMessage.key)}</p> : <Problem error={catalog.error} />}<div className="actions"><button type="button" onClick={catalog.retry}>{copy("project-creation.retryRead")}</button><button type="button" onClick={catalog.reload}>{copy("project-creation.reload")}</button></div></> : null}
        <h4>{copy("project-creation.selectedCount", { count: ids.length })}</h4>{selected(true)}<p>{copy("project-creation.orderHelp")}</p>
      </fieldset>
    </section>
    <section hidden={step !== Step.Configure}>
      <h3>{stepName(Step.Configure)}</h3><fieldset disabled={blocked || step !== Step.Configure}>
        <label>{copy("project-creation.name")}<input data-project-focus="name" required maxLength={256} value={text(data.name)} onChange={event => { setNameEdited(true); change({ ...data, name: event.target.value }); setProblem(""); }} /></label>
        {!nameEdited ? <p>{copy("project-creation.nameHelp")}</p> : null}
        {nameEdited && !validName(text(data.name)) ? <p role="alert">{copy("project-creation.invalidName")}</p> : null}
        <label>{copy("configuration-fields.primaryRepository_b2bbc5")}<select data-project-focus="primary" required value={text(data.primary_repository)} onChange={event => { change({ ...data, primary_repository: event.target.value }); setProblem(""); }}><option value="">{copy("configuration-fields.selectThePrimaryRepository_bd9082")}</option>{ids.map((id, index) => <option key={id} value={id}>{projectRepositoryOption(id, index, names)}</option>)}</select></label>
        <p>{copy("project-creation.primaryHelp")}</p><h4>{copy("project-creation.selectedRepositories")}</h4>{selected(false)}
      </fieldset>
    </section>
    <section hidden={step !== Step.Restrictions}>
      <h3 tabIndex={-1} data-project-focus="heading">{stepName(Step.Restrictions)}</h3>
      <fieldset disabled={blocked || step !== Step.Restrictions}>
        <RestrictionFields label={copy("configuration-fields.agentWorkers_e60c23")} kind={EntityKind.AGENT} value={data.agents} active={active && visible && step === Step.Restrictions} change={agents => change({ ...data, agents })} />
        <RestrictionFields label={copy("configuration-fields.aiAccounts_050a21")} kind={EntityKind.ACCOUNT} value={data.accounts} active={active && visible && step === Step.Restrictions} change={accounts => change({ ...data, accounts })} />
      </fieldset>
      <section className="project-creation-summary"><h4>{copy("project-creation.summary")}</h4><dl><dt>{copy("project-creation.name")}</dt><dd>{text(data.name)}</dd><dt>{copy("configuration-fields.primaryRepository_b2bbc5")}</dt><dd>{projectRepositoryOption(text(data.primary_repository), ids.indexOf(text(data.primary_repository)), names)}</dd><dt>{copy("configuration-fields.repositories_1e32af")}</dt><dd><ol>{ids.map(id => <li key={id}>{names.get(id) || copy("project-creation.nameUnavailable")}{!names.get(id) || ids.some(other => other !== id && names.get(other) === names.get(id)) ? <RepositorySecondaryID id={id} /> : null}</li>)}</ol></dd></dl></section>
    </section>
    {problem ? <p role="alert">{problem}</p> : null}
    {children}
    <SettingsTaskActions form={formId}>
      <SettingsTaskDismissButton type="button" data-settings-task-cancel disabled={cancelDisabled} onClick={cancel}>{copy("project-creation.cancel")}</SettingsTaskDismissButton>
      {step !== Step.Repositories ? <button type="button" disabled={blocked} onClick={() => { setProblem(""); setValidationField(undefined); setStep(step - 1); }}>{copy("project-creation.previous")}</button> : null}
      {uncertain ? <button type="button" disabled={busy} onClick={retry}>{copy("settings.retryTheSameConfiguration_630088")}</button> : null}
      {/* Keep the submit button separate: a browser can run the click's default
          action after React turns the clicked Next button into a submit button. */}
      {step === Step.Restrictions ? <button key="save" type="submit" className="primary" disabled={saveDisabled}>{copy("project-creation.save")}</button> : <button key="next" type="button" className="primary" disabled={blocked || (step === Step.Repositories ? !validRepositories : !configured)} onClick={event => { event.preventDefault(); next(); }}>{copy("project-creation.next")}</button>}
    </SettingsTaskActions>
  </form>;
}
export const ProjectCreation = ProjectCreationWizard;

export function ProjectCreationDialog({ activation, close, fallbackFocus }: { activation: number; close: () => void; fallbackFocus: () => HTMLElement | null }) {
  return <SettingsLifetime>{() => <MutationIntents><ProjectCreationTask activation={activation} close={close} fallbackFocus={fallbackFocus} /></MutationIntents>}</SettingsLifetime>;
}

function ProjectCreationTask({ activation, close, fallbackFocus }: { activation: number; close: () => void; fallbackFocus: () => HTMLElement | null }) {
  useLocale();
  const client = useQueryClient();
  const saved = useCallback(() => { close(); void client.invalidateQueries({ refetchType: "active" }); }, [client, close]);
  return <SettingsTasks>
    <SettingsTaskStatusOutlet className="page" />
    <SettingsTaskDialog title={`${copy("settings.new_18fdd5")} ${kindNames[EntityKind.PROJECT]}`} size={SettingsDialogSize.Form} focus={SettingsDialogFocus.Input} activation={activation} fallbackFocus={fallbackFocus} close={close}>
      <ProjectCreationEditor active saved={saved} cancel={close} />
    </SettingsTaskDialog>
  </SettingsTasks>;
}

function ProjectCreationEditor({ active, saved, cancel }: { active: boolean; saved: () => void; cancel: () => void }) {
  const task = useContext(SettingsTaskContext);
  const onSaved = useCallback(() => {
    // A successful save refreshes the Home inventory and can remove the empty-state
    // opener. Select the persistent fallback during dialog cleanup instead.
    task?.retireOpener(() => true);
    if (task) task.dismissWithClose(saved, true); else saved();
  }, [saved, task]);
  return <ConfigurationEditor kind={EntityKind.PROJECT} active={active} saved={onSaved} cancel={cancel} />;
}
