// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ManagedMCPQuery, ManagedMcpAuthentication, SystemCapability, SystemQuery } from "@delinoio/delidev-api-client";
import { ResourceChoice } from "./configuration-fields";
import { items, object, text, type Document } from "./documents";
import { copy, useLocale } from "./localization";
import { Problem } from "./ui";

export function AgentManagedMCP({ data, change, active }: { data: Document; change: (data: Document) => void; active: boolean }) {
  useLocale();
  const [machine, setMachine] = useState("");
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active, retry: false });
  const supported = status.data?.capabilities.includes(SystemCapability.MANAGED_MCP_V1) === true;
  const catalog = useQuery(ManagedMCPQuery.listManagedMcp, { machineId: machine }, { enabled: active && supported && Boolean(machine), retry: false });
  const selections = items(object(data.managed_mcp).selections).map(object);
  const rows = catalog.data?.definitions ?? [];
  const publish = (selections: Document[]) => change({ ...data, managed_mcp: { selections } });
  const eligible = (row: typeof rows[number]) => row.machineId === machine && row.enabled && row.supportedHarnesses.includes(text(data.harness)) && (row.authentication === ManagedMcpAuthentication.NONE || row.authenticated);
  return <section className="server-preference-section" data-settings-search-target="managed-mcp">
    <h4>{copy("agent-managed-mcp.title")}</h4><p>{copy("agent-managed-mcp.help")}</p>
    <ul>{selections.map((selection, index) => {
      const row = rows.find(row => row.id === selection.definition_id && row.workerDeviceId === selection.worker_device_id && row.machineId === selection.machine_id);
      return <li key={`${text(selection.definition_id)}:${index}`}><span>{row?.name ?? copy("agent-managed-mcp.retained")} · {selection.unresolved ? copy("agent-managed-mcp.unresolved") : copy("agent-managed-mcp.bound")}</span> <button type="button" onClick={() => publish(selections.filter((_, at) => at !== index))}>{copy("agent-managed-mcp.remove")}</button></li>;
    })}</ul>
    {!supported ? <p role="status">{copy("agent-managed-mcp.unsupported")}</p> : <>
      <ResourceChoice label={copy("agent-managed-mcp.runner")} kind={EntityKind.MACHINE} value={machine} active={active} change={setMachine} />
      <Problem error={catalog.error} />
      {machine && catalog.isFetching ? <p role="status">{copy("agent-managed-mcp.loading")}</p> : null}
      {machine && catalog.data ? <label>{copy("agent-managed-mcp.add")}<select value="" onChange={event => { const row = rows.find(row => row.id === event.target.value); if (row && eligible(row)) publish([...selections, { machine_id: row.machineId, worker_device_id: row.workerDeviceId, definition_id: row.id }]); }}><option value="">{copy("agent-managed-mcp.choose")}</option>{rows.filter(row => !selections.some(selection => selection.definition_id === row.id && selection.worker_device_id === row.workerDeviceId)).map(row => <option key={row.id} value={row.id} disabled={!eligible(row)}>{row.name} · {eligible(row) ? copy("agent-managed-mcp.eligible") : copy("agent-managed-mcp.noNative")}</option>)}</select></label> : null}
    </>}
  </section>;
}
