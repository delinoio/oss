// SPDX-License-Identifier: Apache-2.0
import type { ReadSessionAppsResponse, SessionNativeAppScope, SessionNativeAppSelection } from "./gen/delidev/v1/session_native_apps_pb.js";
import { isEntityId } from "./validation.js";
export interface SessionNativeAppView { id: string; name: string; discovered: boolean; accessible: boolean; installed: boolean; enabled: boolean; callable: boolean; selected: boolean }
const text = (v: string) => v.length > 0 && new TextEncoder().encode(v).byteLength <= 1024 && v.trim() === v && !/[\u0000-\u001f\u007f]/u.test(v);
export function sameNativeAppScope(a: SessionNativeAppScope, b: SessionNativeAppScope): boolean {
 return a.sessionId === b.sessionId && a.machineId === b.machineId && a.originalAccountId === b.originalAccountId && a.originalConnectionId === b.originalConnectionId && a.configurationDigest === b.configurationDigest;
}
export function validNativeAppScope(v: SessionNativeAppScope): boolean { return [v.sessionId,v.machineId,v.originalAccountId,v.originalConnectionId].every(isEntityId) && /^[0-9a-f]{64}$/u.test(v.configurationDigest); }
export function validNativeAppSelection(v: SessionNativeAppSelection, scope: SessionNativeAppScope): boolean {
 return !!v.scope && validNativeAppScope(v.scope) && sameNativeAppScope(v.scope,scope) && isEntityId(v.inventoryId) && v.revision > 0n && v.appIds.length <= 256 && new Set(v.appIds).size === v.appIds.length && v.appIds.every(text);
}
// Metadata grants no tool authority. The original server and native owner
// revalidate current selection before each admitted native effect.
export function sessionNativeAppViews(v: ReadSessionAppsResponse, scope: SessionNativeAppScope, requestId: string): SessionNativeAppView[] | undefined {
 if (!validNativeAppScope(scope) || !isEntityId(requestId) || !v.complete || v.requestId !== requestId || v.sessionId !== scope.sessionId || v.originalAccountId !== scope.originalAccountId || v.originalConnectionId !== scope.originalConnectionId || v.configurationDigest !== scope.configurationDigest || !isEntityId(v.inventoryId) || !Number.isFinite(Date.parse(v.observedAt)) || v.discovered.length > 256 || v.installed.length > 256 || v.selection && !validNativeAppSelection(v.selection,scope)) return undefined;
 const rows = new Map<string,SessionNativeAppView>();
 for (const a of v.discovered) {
  if (!text(a.id) || !text(a.name) || rows.has(a.id)) return undefined;
  rows.set(a.id,{id:a.id,name:a.name,discovered:true,accessible:a.accessible,installed:false,enabled:a.enabled,callable:false,selected:v.selection?.appIds.includes(a.id) ?? false});
 }
 const installed = new Set<string>();
 for (const a of v.installed) {
  if (!text(a.id) || installed.has(a.id) || a.callable && !a.enabled) return undefined;
  installed.add(a.id); const row=rows.get(a.id);
  if (row) Object.assign(row,{installed:true,enabled:a.enabled,callable:a.callable});
  else rows.set(a.id,{id:a.id,name:`Installed app ${installed.size}`,discovered:false,accessible:false,installed:true,enabled:a.enabled,callable:a.callable,selected:v.selection?.appIds.includes(a.id) ?? false});
  // Preserve installed-only observations with safe ordinal labels. An absent
  // discovery name must not expose an internal identifier as product text.
 }
 return [...rows.values()];
}
