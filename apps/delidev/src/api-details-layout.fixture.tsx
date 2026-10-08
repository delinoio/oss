// SPDX-License-Identifier: Apache-2.0
// Synthetic read-only services; no account, credential or native access.
import { createRoot } from "react-dom/client";
import { useState } from "react";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AccountingUnitKind, EntityKind, ResourceSchema, UsageService, UsageAccountingProfile, UsageCostState, newRequestId } from "@delinoio/delidev-api-client";
import { ApiEntryRow } from "./api-entry-row";
import { SettingsTasks, SettingsTaskBackground } from "./settings-task";
import { encode } from "./documents";
import { i18n } from "./localization";
import "./themes.css";
import "./styles.css";
import "./api-account.css";
import "./api-details-layout.fixture.css";
const args=new URLSearchParams(location.search);
await i18n.changeLanguage(args.get("language")??"en"); document.documentElement.dataset.theme=args.get("theme")??"light";
const rows=["First fixture account with complete identity","Second fixture account","Unknown fixture"].map((alias,index)=>create(ResourceSchema,{id:newRequestId(),kind:EntityKind.ACCOUNT,schemaVersion:index===2?4:3,revision:1n,documentJson:encode({alias,type:"api",provider_id:newRequestId(),api_protocol:"openai-chat",enabled:true,health:"unverified",quota:Array.from({length:12},(_,number)=>({id:`Fixture quota ${number}`,state:"observed",remaining:0.5,observed_at:"2026-10-08T01:02:03.123Z",reset_at:"2026-10-09T01:02:03.123Z"}))})}));
const reads: string[]=[],effects:string[]=[]; Object.assign(window,{__apiDetailsFixture:{reads,effects,ids:rows.map(row=>row.id)}});
const transport=createRouterTransport(router=>router.service(UsageService,{getUsageSummary:request=>{reads.push(request.accountId); return {fromUnixMs:1788642000000n,untilUnixMs:1791234000000n,accountingProfile:UsageAccountingProfile.NATIVE_UNITS_V1,totals:{responses:1,total:{knownTotal:"18446744073709551614",measuredResponses:1}},estimatedCost:UsageCostState.KNOWN_SUBTOTAL,estimates:{currencies:[{currency:"USD",knownAmount:"12345678901234567890.0000123400"},{currency:"KRW",knownAmount:"0"}]},acceptedExecutionsWithoutResponse:3,nativeAccounting:[{totals:{kind:AccountingUnitKind.CLAUDE_MAIN_LOOP_INPUT,units:2,total:{unavailableUnits:2},unpricedUnits:2}}]};}}));
function Fixture(){const [visible,setVisible]=useState(true);return <main className="api-details-fixture"><button onClick={()=>setVisible(value=>!value)}>Fixture leave category</button><section className="settings-content"><h1 tabIndex={-1}>Fixture AI API Keys</h1><SettingsTasks><SettingsTaskBackground>{visible?rows.map(row=><ApiEntryRow key={row.id} row={row} provider={{displayName:"Synthetic provider",enabled:true,protocol:"openai-chat"}} active manage={()=>effects.push("manage")} edit={()=>effects.push("edit")} remove={()=>effects.push("remove")} openUsage={()=>effects.push("usage")} />):<p>Fixture other category</p>}</SettingsTaskBackground></SettingsTasks></section></main>;}
createRoot(document.getElementById("root")!).render(<TransportProvider transport={transport}><QueryClientProvider client={new QueryClient({defaultOptions:{queries:{staleTime:Infinity}}})}><Fixture/></QueryClientProvider></TransportProvider>);
