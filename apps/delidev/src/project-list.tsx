// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { document, resourceName, text } from "./documents";
import { copy, useLocale } from "./localization";
import { Failure } from "./ui";
import { projectRepositoryIds, RepositoryDetailsState, type RepositoryDetails } from "./project-list-metadata";
import "./project-list.css";

export function ProjectList({ resources, metadata, edit, remove }: { resources: Resource[]; metadata: ReadonlyMap<string, RepositoryDetails>; edit: (row: Resource) => void; remove: (row: Resource) => void }) {
  useLocale();
  if (!resources.length) return null;
  return <section className="project-list" aria-label={copy("settings.savedProjects_f85c7c")}>{resources.map(row => <ProjectRow key={row.id} row={row} metadata={metadata} edit={edit} remove={remove} />)}</section>;
}
function ProjectRow({ row, metadata, edit, remove }: { row: Resource; metadata: ReadonlyMap<string, RepositoryDetails>; edit: (row: Resource) => void; remove: (row: Resource) => void }) {
  const [expanded, setExpanded] = useState(false);
  const name = resourceName(row), ids = projectRepositoryIds(row), primary = text(document(row).primary_repository);
  return <article className="project-row project-metadata-row">
    <header className="project-row-heading"><div className="project-identity"><h3>{name}</h3><p>{copy("settings.projectRepositoryCount", { count: ids.length })}</p></div>
      <div className="actions"><button type="button" disabled={!supportsResourceSchema(row)} aria-label={copy("settings.edit_f1be7e", { v0: name })} onClick={() => edit(row)}>{copy("settings.edit_464c4f")}</button><button type="button" disabled={!supportsResourceSchema(row)} aria-label={copy("settings.delete_cd822e", { v0: name })} onClick={() => remove(row)}>{copy("settings.delete_e2d0a5")}</button></div>
    </header>
    <ol className="project-repository-rows">{(expanded ? ids : ids.slice(0, 3)).map((id, index) => {
      const details = metadata.get(id), ready = details?.state === RepositoryDetailsState.Ready;
      return <li key={`${id}:${index}`}><div className="project-repository-heading"><strong>{ready ? details.name : copy(details?.state === RepositoryDetailsState.Unavailable ? "settings.projectRepositoryUnavailable" : "settings.projectRepositoryLoading")}</strong>{id === primary ? <span className="project-primary-badge">{copy("settings.projectRepositoryPrimary")}</span> : null}</div>
        {ready ? <p className="project-repository-url">{details.url || copy("settings.projectRepositoryURLMissing")}</p> : null}
        {details?.stale ? <p className="project-repository-stale" role="status">{copy("settings.projectRepositoryStale")}</p> : null}
        <Failure failure={details?.failure} />
      </li>;
    })}</ol>
    {ids.length > 3 ? <button type="button" className="project-show-repositories" aria-expanded={expanded} onClick={() => setExpanded(value => !value)}>{copy(expanded ? "settings.projectRepositoriesShowFewer" : "settings.projectRepositoriesShowAll")}</button> : null}
    <details className="project-original-details"><summary>{copy("settings.projectDetails")}</summary><dl><dt>{copy("settings.projectOriginalID")}</dt><dd>{row.id}</dd><dt>{copy("settings.projectRepositoryOriginalIDs")}</dt><dd><ol>{ids.map((id, index) => <li key={`${id}:${index}`}>{id}</li>)}</ol></dd></dl></details>
  </article>;
}
