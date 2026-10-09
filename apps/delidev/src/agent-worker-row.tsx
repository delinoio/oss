// SPDX-License-Identifier: Apache-2.0
import { SettingsActionButton, SettingsActionIcon, SettingsActionPresentation } from "./settings-action";
import { DisclosureButton, DisclosureContent, DisclosureDensity } from "./disclosure";
import { useId, useState } from "react";
import { supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { document, items, object, resourceName, text } from "./documents";
import { copy, LocalizedText, useLocale } from "./localization";
import { Harness } from "./configuration-fields";
import { workerHarnessNames } from "./worker-harness-picker";

import { HarnessMark, knownHarness as projectedHarness } from "./harness-mark";
import "./agent-worker-row.css";

export function AgentWorkerRow({ row, edit, preview, remove }: { row: Resource; edit: () => void; preview: () => void; remove: () => void }) {
  useLocale();
  const data = document(row), supported = supportsResourceSchema(row);
  const [expanded, expand] = useState(false), region = useId();
  const routes = supported ? items(data.routes).map(object) : [];
  const modelIDs = routes.map(route => text(object(route.model).native_id));
  const accountCount = (index: number) => {
    const accounts = routes[index]?.accounts;
    return <span className="agent-route-account-count">{Array.isArray(accounts) ? copy("agent-worker-row.accountCount", { count: accounts.length }) : copy("agent-worker-row.accountCountUnavailable")}</span>;
  };
  const harness = text(data.harness), knownHarness = Object.hasOwn(workerHarnessNames, harness);
  const model = (index: number) => { const metadata=object(routes[index]?.model); const id=text(metadata.native_id);const name=text(metadata.name);return <><code className="agent-model-native">{id}</code>{name && name!==id?<span className="agent-model-name">{name}</span>:null}</>; };
  let name = resourceName(row);
  // Unsupported schemas stay non-actionable. Only bounded inert name text is
  // projected within the Agent name's 256-byte UTF-8 limit for identifying the
  // row; it never enters an editor or request.
  // Remove this projection once the shared document parser supports that schema.
  if (!supportsResourceSchema(row) && row.documentJson.byteLength <= 1 << 20) {
    try {
      const display = object(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(row.documentJson)));
      const projectedName = text(display.name) || text(display.alias);
      // Check code units first so malformed large values never allocate an
      // equally large UTF-8 buffer merely to validate display text.
      if (projectedName.length <= 256 && new TextEncoder().encode(projectedName).byteLength <= 256) name = projectedName || name;
    } catch { /* Malformed display text keeps the existing unnamed fallback. */ }
  }
  return <article className="settings-agent-row">
    <div className="settings-agent-details">
      <div className="settings-agent-mark" aria-hidden="true"><HarnessMark harness={supported ? projectedHarness(data.harness) : undefined} /></div>
      <div className="settings-agent-text">
      <div className="settings-agent-heading"><h3>{name}</h3>{supported && harness ? <span>{knownHarness ? workerHarnessNames[harness as Harness] : harness}</span> : null}</div>
      {text(data.health) ? <p><LocalizedText id="settings.status_ae149d" components={{ s0: <>{text(data.health)}</> }} /></p> : null}
      {supported ? <div className="agent-model-summary"><span>{copy("agent-worker-row.configuredModel")}</span>{model(0)}{accountCount(0)}{modelIDs.length > 1 ? <DisclosureButton density={DisclosureDensity.Settings} type="button" aria-expanded={expanded} aria-controls={region} onClick={() => expand(value => !value)}>{copy("agent-worker-row.moreModels", { count: modelIDs.length - 1 })}</DisclosureButton> : null}</div> : null}
      {supported && modelIDs.length > 1 ? <DisclosureContent id={region} role="region" hidden={!expanded} aria-label={copy("agent-worker-row.configuredModels")}><ol className="agent-model-routes">{modelIDs.map((id, index) => <li key={index}>{model(index)}{accountCount(index)}</li>)}</ol></DisclosureContent> : null}
      {!supported && [1, 2, 3].includes(row.schemaVersion) ? <span className="agent-route-account-count">{copy("agent-worker-row.accountCountUnavailable")}</span> : null}
      {data.reconfiguration_required === true ? <p role="status">{copy("settings.reconfigurationRequired_a84a37")}</p> : null}
      <small>{row.id}</small>
      </div>
    </div>
    <div className="actions settings-agent-actions">
      <SettingsActionButton icon={SettingsActionIcon.Edit} presentation={SettingsActionPresentation.Icon} targetId={row.id} type="button" disabled={!supportsResourceSchema(row)} aria-label={copy("settings.edit_f1be7e", { v0: name })} onClick={edit}>{copy("settings.edit_464c4f")}</SettingsActionButton>
      <SettingsActionButton icon={SettingsActionIcon.Inspect} type="button" disabled={!supportsResourceSchema(row) || data.reconfiguration_required === true} aria-label={copy("settings.previewRoutingFor_ee49d7", { v0: name })} onClick={preview}>{copy("settings.previewRouting_02d4d9")}</SettingsActionButton>
      <SettingsActionButton icon={SettingsActionIcon.Delete} presentation={SettingsActionPresentation.Icon} targetId={row.id} type="button" disabled={!supportsResourceSchema(row)} aria-label={copy("settings.delete_cd822e", { v0: name })} onClick={remove}>{copy("settings.delete_e2d0a5")}</SettingsActionButton>
    </div>
  </article>;
}

