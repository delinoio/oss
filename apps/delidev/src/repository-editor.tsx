// SPDX-License-Identifier: Apache-2.0
import { Disclosure, DisclosureSummary, DisclosureDensity } from "./disclosure";
import { useEffect, useRef, type ReactNode, type SyntheticEvent } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery } from "@delinoio/delidev-api-client";
import { copy, useLocale } from "./localization";
import { document, text } from "./documents";
import "./repository-editor.css";

export function revealRepositoryInvalidControl(event: SyntheticEvent<HTMLFormElement>) {
  event.preventDefault();
  const target = Array.from(event.currentTarget.elements).find(element =>
    (element instanceof HTMLInputElement || element instanceof HTMLSelectElement || element instanceof HTMLTextAreaElement) && element.willValidate && !element.validity.valid
  ) as HTMLElement | undefined;
  if (!target) return;
  let parent = target.parentElement;
  while (parent && parent !== event.currentTarget) {
    if (parent instanceof HTMLDetailsElement) parent.open = true;
    parent = parent.parentElement;
  }
  window.requestAnimationFrame(() => { if (target.isConnected) target.focus(); });
}

function RepositoryDisclosure({ title, summary, children, problem = false }: { title: string; summary?: string; children: ReactNode; problem?: boolean }) {
  const disclosure = useRef<HTMLDetailsElement>(null);
  useEffect(() => { if (problem && disclosure.current) disclosure.current.open = true; }, [problem]);
  useEffect(() => {
    const element = disclosure.current;
    if (!element) return;
    // Section-local read failures must remain visible even when their controls
    // are collapsed. Keep the controllers mounted and leave polling unchanged.
    const reveal = () => { if (element.querySelector('[role="alert"]')) element.open = true; };
    const observer = new MutationObserver(reveal);
    observer.observe(element, { subtree: true, childList: true });
    reveal();
    return () => observer.disconnect();
  }, []);
  return <Disclosure density={DisclosureDensity.Settings} ref={disclosure} className="repository-disclosure"><DisclosureSummary>{title}{summary ? <span>{summary}</span> : null}</DisclosureSummary><div className="repository-disclosure-body">{children}</div></Disclosure>;
}

export function RepositoryCheckoutIdentity({ machineId, path, active }: { machineId: string; path: string; active: boolean }) {
  const machine = useQuery(ResourceQuery.listResources, { filter: { kind: EntityKind.MACHINE, pageSize: 50, pageToken: "" } }, { enabled: active && Boolean(machineId) });
  const row = machine.data?.resources.find(resource => resource.id === machineId);
  const name = row?.id === machineId && row.kind === EntityKind.MACHINE ? text(document(row).name) : "";
  return <p>{name ? <><span>{name}</span> · </> : null}<code>{machineId}</code> · <code>{path}</code></p>;
}

export function RepositoryEditSections({ identity, github, checkouts, addition, advanced, inspectionProblem }: { identity: ReactNode; github: ReactNode; checkouts: ReactNode; addition: ReactNode; advanced: ReactNode; inspectionProblem: boolean }) {
  useLocale();
  return <div className="repository-edit-sections">
    <section><h4>{copy("configuration-fields.repository_13d6ff")}</h4>{identity}</section>
    <section><h4>GitHub</h4>{github}<p className="repository-hint">{copy("configuration-fields.repositoryEdit.githubHelp")}</p></section>
    <section><h4>{copy("configuration-fields.repositoryEdit.checkouts")}</h4><p className="repository-hint">{copy("configuration-fields.repositoryEdit.checkoutHelp")}</p>{checkouts}<RepositoryDisclosure title={copy("configuration-fields.addACheckout_0ee4d5")} problem={inspectionProblem}>{addition}</RepositoryDisclosure><p className="repository-hint">{copy("configuration-fields.repositoryEdit.inspectionHelp")}</p></section>
    <section><RepositoryDisclosure title={copy("configuration-fields.repositoryEdit.advanced")} summary={copy("configuration-fields.repositoryEdit.advancedSummary")}>{advanced}</RepositoryDisclosure></section>
  </div>;
}
