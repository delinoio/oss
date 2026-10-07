// SPDX-License-Identifier: Apache-2.0
import { useDeferredValue, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { EntityKind } from "@delinoio/delidev-api-client";
import { items, text, type Document } from "./documents";
import { RepositoryIdentity, RestrictionFields, projectRepositoryOption } from "./configuration-fields";
import { ProductError, copy, ownedMessage, useLocale, useProductMessage } from "./localization";
import { useProjectRepositoryCatalog } from "./project-repositories";
import { SettingsTaskActions } from "./settings-task";
import { Problem } from "./ui";
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

export function ProjectCreation({ data, change, active, visible, blocked, busy, saveDisabled, submit, cancel, cancelDisabled, uncertain, retry, children }: {
  data: Document; change: (value: Document) => void; active: boolean; visible: boolean; blocked: boolean; saveDisabled: boolean;
  busy: boolean; submit: () => void; cancel: () => void; cancelDisabled: boolean; uncertain: boolean; retry: () => void; children?: ReactNode;
}) {
  useLocale();
  const [step, setStep] = useState(Step.Repositories);
  const [nameEdited, setNameEdited] = useState(Boolean(text(data.name)));
  const [search, setSearch] = useState("");
  const [page, setPage] = useState(0);
  const [focusAttempt, setFocusAttempt] = useState(0);
  const [validationField, setValidationField] = useState<"search" | "name" | "primary">();
  const [problem, setProblem] = useProductMessage("");
  const form = useRef<HTMLFormElement>(null), focused = useRef(false);
  const formId = useId(), inputId = useId();
  const catalog = useProjectRepositoryCatalog(active && visible);
  const query = useDeferredValue(search.trim().toLowerCase());
  const names = useMemo(() => new Map(catalog.rows.filter(row => row.supported).map(row => [row.id, row.name])), [catalog.rows]);
  const matches = useMemo(() => catalog.rows.filter(row => row.name.toLowerCase().includes(query)), [catalog.rows, query]);
  const pageIndex = Math.min(page, Math.max(0, Math.ceil(matches.length / 50) - 1));
  const choices = matches.slice(pageIndex * 50, (pageIndex + 1) * 50);
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
    change({ ...data, repositories: values, primary_repository: values.includes(text(data.primary_repository)) ? data.primary_repository : "", ...(!nameEdited && (!values.length || names.has(values[0]!)) ? { name: values.length ? names.get(values[0]!) : "" } : {}) });
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
  const selected = (editable: boolean) => <ol className="project-selected-repositories">{ids.map((id, index) => <li key={id}>
    <span className="project-repository-position" aria-hidden="true">{index + 1}</span><span className="project-repository-name">{names.get(id) ?? copy("project-creation.nameUnavailable")}</span>
    {!names.has(id) || ids.some(other => other !== id && names.get(other) === names.get(id)) ? <RepositoryIdentity id={id} /> : null}
    {editable ? <div className="actions"><button type="button" disabled={blocked || index === 0} aria-label={copy("configuration-fields.moveEntryUp_b22154", { v0: index + 1 })} onClick={() => { const values = [...ids]; [values[index - 1], values[index]] = [values[index]!, values[index - 1]!]; repositories(values); }}>↑</button><button type="button" disabled={blocked} aria-label={copy("configuration-fields.removeEntry_8a2d73", { v0: index + 1 })} onClick={() => repositories(ids.filter(value => value !== id))}>×</button></div> : null}
  </li>)}</ol>;
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
        <label className="project-repository-search" htmlFor={inputId}>{copy("project-creation.search")}<input id={inputId} data-project-focus="search" type="search" autoComplete="off" maxLength={256} value={search} placeholder={copy("project-creation.search")} aria-controls={`${inputId}-results`} onChange={event => { setSearch(event.target.value); setPage(0); }} /></label>
        <div id={`${inputId}-results`} className="project-repository-results" role="group" aria-label={copy("project-creation.repositoryChoices")}>
          {choices.map((row, index) => { const duplicateName = catalog.rows.some(other => other.id !== row.id && other.name === row.name); const identityId = `${inputId}-repository-${index}-identity`; return <div key={row.id} className="project-repository-choice"><label className="checkbox"><input type="checkbox" aria-describedby={duplicateName ? identityId : undefined} checked={ids.includes(row.id)} disabled={!row.supported || (!ids.includes(row.id) && ids.length >= 1000)} onChange={event => repositories(event.target.checked ? [...ids, row.id] : ids.filter(id => id !== row.id))} />{row.name || copy("project-creation.nameUnavailable")}</label>{duplicateName || !row.supported ? <RepositoryIdentity id={row.id} /> : null}{duplicateName ? <span id={identityId} className="project-repository-id"><code>{row.id}</code></span> : null}{!row.supported ? <small>{copy("project-creation.unsupportedRepository")}</small> : null}</div>; })}
        </div>
        {matches.length > 50 ? <nav className="actions" aria-label={copy("project-creation.resultPages")}><button type="button" disabled={pageIndex === 0} onClick={() => setPage(pageIndex - 1)}>{copy("project-creation.previousResults")}</button><button type="button" disabled={(pageIndex + 1) * 50 >= matches.length} onClick={() => setPage(pageIndex + 1)}>{copy("project-creation.nextResults")}</button></nav> : null}
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
        <p>{copy("configuration-fields.theHarnessStartsInThisRepository_8c3af5")}</p><h4>{copy("project-creation.selectedRepositories")}</h4>{selected(false)}
      </fieldset>
    </section>
    <section hidden={step !== Step.Restrictions}>
      <h3 tabIndex={-1} data-project-focus="heading">{stepName(Step.Restrictions)}</h3>
      <fieldset disabled={blocked || step !== Step.Restrictions}>
        <RestrictionFields label={copy("configuration-fields.agentWorkers_e60c23")} kind={EntityKind.AGENT} value={data.agents} active={active && visible && step === Step.Restrictions} change={agents => change({ ...data, agents })} />
        <RestrictionFields label={copy("configuration-fields.aiAccounts_050a21")} kind={EntityKind.ACCOUNT} value={data.accounts} active={active && visible && step === Step.Restrictions} change={accounts => change({ ...data, accounts })} />
      </fieldset>
      <section className="project-creation-summary"><h4>{copy("project-creation.summary")}</h4><dl><dt>{copy("project-creation.name")}</dt><dd>{text(data.name)}</dd><dt>{copy("configuration-fields.primaryRepository_b2bbc5")}</dt><dd>{projectRepositoryOption(text(data.primary_repository), ids.indexOf(text(data.primary_repository)), names)}</dd><dt>{copy("configuration-fields.repositories_1e32af")}</dt><dd><ol>{ids.map(id => <li key={id}>{names.get(id) ?? copy("project-creation.nameUnavailable")}{!names.has(id) || ids.some(other => other !== id && names.get(other) === names.get(id)) ? <RepositoryIdentity id={id} /> : null}</li>)}</ol></dd></dl></section>
    </section>
    {problem ? <p role="alert">{problem}</p> : null}
    {children}
    <SettingsTaskActions form={formId}>
      <button type="button" data-settings-task-cancel disabled={cancelDisabled} onClick={cancel}>{copy("project-creation.cancel")}</button>
      {step !== Step.Repositories ? <button type="button" disabled={blocked} onClick={() => { setProblem(""); setValidationField(undefined); setStep(step - 1); }}>{copy("project-creation.previous")}</button> : null}
      {uncertain ? <button type="button" disabled={busy} onClick={retry}>{copy("settings.retryTheSameConfiguration_630088")}</button> : null}
      {/* Keep the submit button separate: a browser can run the click's default
          action after React turns the clicked Next button into a submit button. */}
      {step === Step.Restrictions ? <button key="save" type="submit" className="primary" disabled={saveDisabled}>{copy("project-creation.save")}</button> : <button key="next" type="button" className="primary" disabled={blocked || (step === Step.Repositories ? !validRepositories : !configured)} onClick={event => { event.preventDefault(); next(); }}>{copy("project-creation.next")}</button>}
    </SettingsTaskActions>
  </form>;
}
