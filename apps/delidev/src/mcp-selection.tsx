// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { EntityKind, McpAuthenticationState, type McpSelection } from "@delinoio/delidev-api-client";
import { ResourceChoice } from "./configuration-fields";
import { useMcpCatalog } from "./mcp-management";
import { items, object, text, type Document } from "./documents";
import { copy, useLocale } from "./localization";
import { Problem } from "./ui";
import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
export function mcpSelections(data:Document):McpSelection[]|undefined {
 if(data.mcp_selections===undefined)return undefined;
 return items(object(data.mcp_selections).selections).map<McpSelection>(item=>{const value=object(item);return {$typeName:"delidev.v1.McpSelection",machineId:text(value.machine_id),workerDeviceId:text(value.device_id),serverId:text(value.server_id),expectedRevision:BigInt(text(value.revision)||"0")};});
}
export function McpSelectionFields({data,change,active,disabled}:{data:Document;change:(data:Document)=>void;active:boolean;disabled:boolean}) {
 useLocale();const [machine,setMachine]=useState("");const catalog=useMcpCatalog(machine,active),selected=mcpSelections(data)??[];
 const save=(values:McpSelection[])=>change({...data,mcp_selections:{selections:values.map(value=>({machine_id:value.machineId,device_id:value.workerDeviceId,server_id:value.serverId,revision:String(value.expectedRevision)}))}});
 return <section className="mcp-selection"><h3>{copy("mcp.title")}</h3><p>{copy("mcp.selectionHelp")}</p>{object(data.mcp_selections).rebinding_required===true?<p role="status">{copy("mcp.rebind")}</p>:null}<ResourceChoice kind={EntityKind.MACHINE} label={copy("mcp.runner")} value={machine} change={setMachine} active={active} disabled={disabled}/><Problem error={catalog.error}/>
 {selected.map(value=><div key={value.serverId+value.machineId}><span>{catalog.data?.servers.find(row=>row.definition?.id===value.serverId&&row.machineId===value.machineId)?.definition?.name??value.serverId}</span> <SettingsActionButton icon={SettingsActionIcon.Delete} type="button" disabled={disabled} onClick={()=>save(selected.filter(row=>row!==value))}>{copy("mcp.removeSelection")}</SettingsActionButton></div>)}
 {(catalog.data?.servers??[]).map(row=>{const definition=row.definition!,old=selected.find(value=>value.serverId===definition.id&&value.machineId===row.machineId);const selectable=definition.enabled&&[McpAuthenticationState.READY,McpAuthenticationState.NOT_REQUIRED].includes(row.authenticationState);return <label key={definition.id}><input type="checkbox" checked={Boolean(old)} disabled={disabled||Boolean(catalog.error)||!selectable||!old&&selected.length>=16} onChange={event=>save(event.target.checked?[...selected,{$typeName:"delidev.v1.McpSelection",machineId:row.machineId,workerDeviceId:row.workerDeviceId,serverId:definition.id,expectedRevision:definition.revision}]:selected.filter(value=>value!==old))}/>{definition.name}{old&&old.expectedRevision!==definition.revision?<SettingsActionButton icon={SettingsActionIcon.Refresh} type="button" disabled={disabled||!selectable||Boolean(catalog.error)} onClick={()=>save(selected.map(value=>value===old?{...old,expectedRevision:definition.revision}:value))}>{copy("mcp.updateSelection")}</SettingsActionButton>:null}<span className="scope">{copy("mcp.runtimeUnavailable")}</span></label>;})}
 </section>;
}
