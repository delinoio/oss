// SPDX-License-Identifier: Apache-2.0
import { useQuery } from "@connectrpc/connect-query";
import { SystemCapability, SystemQuery, type Resource } from "@delinoio/delidev-api-client";
import { AgentWorkerSourceWizard } from "./agent-worker-source-wizard";
import { Problem } from "./ui";
import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
import { SettingsTaskDismissButton } from "./settings-task";
import { copy, useLocale } from "./localization";
import type { Source } from "./worker-source";
import "./agent-worker-wizard.css";
export function AgentWorkerWizard(props:{initial?:Resource;active:boolean;saved:()=>void;cancel:()=>void;openAccounts?:(source:Source)=>void}){
 useLocale();const status=useQuery(SystemQuery.getStatus,{}, {enabled:props.active});
 if(status.data?.protocolVersion===2 && status.data.capabilities.includes(SystemCapability.INLINE_WORKER_MODELS_V1))return <AgentWorkerSourceWizard {...props}/>;
 return <><Problem error={status.error} actions={<SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={!props.active || status.isFetching} onClick={()=>void status.refetch()}>{copy("ui.retryCurrentRead")}</SettingsActionButton>}/><p role="status">{status.isPending?copy("agent-worker-wizard.checkingServerSupport"):copy("agent-worker-wizard.updateSourceServer")}</p><SettingsTaskDismissButton onClick={props.cancel}>{copy("agent-worker-wizard.cancel")}</SettingsTaskDismissButton></>;
}
