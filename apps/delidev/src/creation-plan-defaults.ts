// SPDX-License-Identifier: Apache-2.0
import { Code, ConnectError } from "@connectrpc/connect";
import { EntityKind, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { document, items, Mode, object } from "./documents";
const invalid=()=>new ConnectError("The current creation defaults could not be verified.",Code.FailedPrecondition);
const uuid=(value:string)=>/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value);
function current(row:Resource,kind:EntityKind,prior?:Resource) {
 if(row.kind!==kind || !uuid(row.id) || row.revision<=0n || !supportsResourceSchema(row))throw invalid();
 if(prior){if(row.id!==prior.id || row.revision<prior.revision)throw invalid();if(row.revision===prior.revision && (row.schemaVersion!==prior.schemaVersion || row.documentJson.length!==prior.documentJson.length || row.documentJson.some((byte,index)=>byte!==prior.documentJson[index])))throw invalid();}
}
/** Direct reads bypass mounted query freshness and precede request freezing. */
export async function readCreationPlanDefault({readSettings,readProject,projectId,agentId,priorSettings,priorProject}:{readSettings:()=>Promise<{resources:Resource[];nextPageToken?:string}>;readProject:(id:string)=>Promise<{resource?:Resource}>;projectId:string;agentId:string;priorSettings?:Resource;priorProject?:Resource}):Promise<Mode> {
 const [settings,project]=await Promise.all([readSettings(),projectId?readProject(projectId):Promise.resolve(undefined)]);
 if(settings.resources.length>1 || settings.nextPageToken)throw invalid();
 const global=settings.resources[0];
 if(global)current(global,EntityKind.SETTINGS,priorSettings);
 const enabled=document(global).plan_mode_default;
 if(enabled!==undefined && typeof enabled!=="boolean")throw invalid();
 let override:unknown="inherit";
 if(projectId){const row=project?.resource;if(!row || row.id!==projectId)throw invalid();current(row,EntityKind.PROJECT,priorProject);const data=document(row),agents=object(data.agents);if(data.disabled===true || data.enabled===false || agents.configured===true && !items(agents.ids).includes(agentId))throw invalid();const settings=object(data.settings);override=Object.hasOwn(settings,"plan_mode_default")?settings.plan_mode_default:"inherit";}
 if(!["inherit","enabled","disabled"].includes(String(override)))throw invalid();
 return override==="enabled" || override==="inherit" && enabled===true?Mode.Plan:Mode.Execute;
}
