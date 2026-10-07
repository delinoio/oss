// SPDX-License-Identifier: Apache-2.0
import { useId } from "react";
import { supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { document, items, object, resourceName, text } from "./documents";
import { copy, LocalizedText, useLocale } from "./localization";
import { RepositoryGitHubItems } from "./github-items";
import "./repository-list.css";

export function RepositoryIcon() {
  return <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d="M6 3v10m0 4v4m12-14v4a4 4 0 0 1-4 4h-4" /><circle cx="6" cy="15" r="2" /><circle cx="18" cy="5" r="2" /></svg>;
}

// Inventory presentation uses saved metadata only. GitHub observations remain
// explicit actions owned by the existing read controllers below.
export function RepositoryRow({ row, active, edit, remove }: { row: Resource; active: boolean; edit: () => void; remove: () => void }) {
  useLocale();
  const heading = useId(), name = resourceName(row), data = document(row);
  const supported = supportsResourceSchema(row);
  const owner = text(data.github_owner), githubName = text(data.github_name);
  const configured = Boolean(owner && githubName && text(data.integration_id));
  const checkouts = items(data.checkouts).map(object).filter(checkout => text(checkout.path));
  // Raw inspected remotes and configured URLs are never projected here. A
  // source-type label cannot reveal embedded credentials or grant execution.
  const remoteSource = Boolean(text(data.remote_url));
  return <article className="repository-row" aria-labelledby={heading}>
    <header className="repository-row-heading">
      <span className="repository-icon-tile"><RepositoryIcon /></span>
      <div className="repository-identity"><h2 id={heading}>{name}</h2>{supported ? <p>{remoteSource ? copy("settings.repositoryRemoteSource") : copy("settings.repositoryCheckoutSource")}</p> : null}</div>
      <span className={`repository-badge${supported && !configured ? " repository-badge-attention" : ""}`}>{!supported ? copy("settings.repositoryUnsupported") : configured ? copy("settings.repositoryGithubConfigured") : copy("settings.repositoryGithubNeedsSetup")}</span>
      <div className="actions repository-manage-actions">
        <button type="button" disabled={row.schemaVersion !== 1} aria-label={copy("settings.edit_f1be7e", { v0: name })} onClick={edit}>{copy("settings.edit_464c4f")}</button>
        <button type="button" className="repository-delete" disabled={row.schemaVersion !== 1} aria-label={copy("settings.delete_cd822e", { v0: name })} onClick={remove}>{copy("settings.delete_e2d0a5")}</button>
      </div>
    </header>
    {supported ? <dl className="repository-metadata">
      <div><dt>{copy("settings.repositoryFolders")}</dt><dd>{checkouts.length ? <ul>{checkouts.map((checkout, index) => <li key={index}><code>{text(checkout.path)}</code></li>)}</ul> : copy("settings.repositoryNoFolders")}</dd></div>
      <div><dt>GitHub</dt><dd>{owner && githubName ? <span className="repository-github-name">{owner}/{githubName}</span> : copy("settings.repositoryNoGithubIdentity")}</dd></div>
    </dl> : null}
    {text(data.health) ? <p><LocalizedText id="settings.status_ae149d" components={{ s0: <>{text(data.health)}</> }} /></p> : null}
    {text(data.harness) ? <p><LocalizedText id="settings.harness_db1faa" components={{ s0: <>{text(data.harness)}</> }} /></p> : null}
    <div className="repository-github-tools"><RepositoryGitHubItems selected={row} active={active} /></div>
    <p className="repository-resource-id"><span>{copy("settings.repositoryId")}</span><code>{row.id}</code></p>
  </article>;
}
