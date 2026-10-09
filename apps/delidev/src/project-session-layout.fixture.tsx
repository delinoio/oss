// SPDX-License-Identifier: Apache-2.0
// Synthetic creation geometry and draft checks, without native/account authority.
import { create } from "@bufbuild/protobuf";
import { createRoot } from "react-dom/client";
import { useState } from "react";
import { Code, ConnectError } from "@connectrpc/connect";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { EntityKind, ResourceSchema, ResourceService, SessionService, SystemCapability, SystemService, WorkerService, WorkerCapability, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { i18n } from "./localization";
import { MutationIntents } from "./mutation";
import { NewSession } from "./new-session";
import "./themes.css";
import "./styles.css";
import "./general-chat-layout.fixture.css";
const args = new URLSearchParams(location.search);
await i18n.changeLanguage(args.get("language") ?? "en");
document.documentElement.dataset.theme = args.get("theme") ?? "light";
const make = (kind: EntityKind, name: string) => create(ResourceSchema, { id: newRequestId(), kind, revision: 1n, schemaVersion: 1, documentJson: encode({ name }) });
const agent = make(EntityKind.AGENT, "Agent One"), machine = make(EntityKind.MACHINE, "Worker One"), project = make(EntityKind.PROJECT, "Project One");
const repositories = [make(EntityKind.REPOSITORY, "Primary repository"), make(EntityKind.REPOSITORY, "Secondary repository")].slice(0,args.get("repositories") === "1" ? 1 : 2);
project.documentJson = encode({name:"Project One",repositories:repositories.map(row=>row.id),primary_repository:repositories[0].id});
machine.documentJson = encode({name:"Worker One",worker_capabilities:[WorkerCapability.REPOSITORY_BRANCH_DISCOVERY_V1]});
for (const repository of repositories) repository.documentJson = encode({name:JSON.parse(new TextDecoder().decode(repository.documentJson)).name,comparison_base:{type:"remote-branch",remote:"upstream",name:"main"}});
const rows = [agent, machine, project, ...repositories], creates: Record<string, unknown>[] = [];
let release: (() => void) | undefined;
document.documentElement.dataset.discoveries = "0";
document.documentElement.dataset.proofReads = "0";
const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
let report: (() => void) | undefined;
const transport = createRouterTransport(router => {
  router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.AUTOMATIC_TITLES_V1, ...(args.get("supported") === "false" ? [] : [SystemCapability.REPOSITORY_BRANCH_DISCOVERY_V1])] }) });
  router.service(ResourceService, { getResource: request => ({ resource: rows.find(row => row.id === request.id && row.kind === request.kind) }), listResources: request => ({ resources: rows.filter(row => row.kind === request.filter?.kind) }) });
  router.service(WorkerService, { discoverRepositoryBranches: request => {
    document.documentElement.dataset.discoveries = String(Number(document.documentElement.dataset.discoveries)+1);
    const repository=rows.find(row=>row.id===request.repositoryId)!;
    const input={project_id:project.id,project_revision:1,repository_id:repository.id,repository_revision:1,machine_id:machine.id,machine_revision:1,remote:"upstream"};
    const job=make(EntityKind.JOB,"Synthetic discovery");job.documentJson=encode({type:"discover-repository-branches",state:"succeeded",input,output:{...input,observed_at:new Date().toISOString(),branches:["feature/topic","main"]}});
    return {job};
  } });
  router.service(SessionService, { createSession: async request => {
    const data = JSON.parse(new TextDecoder().decode(request.documentJson)); creates.push({...data,request_id:request.requestId}); report?.();
    if(args.get("creation") === "pending") await new Promise<void>(resolve=>{release=resolve;});
    if(args.get("creation") === "uncertain") throw new ConnectError("Synthetic lost response",Code.Unavailable);
    const session = make(EntityKind.SESSION, "Synthetic accepted conversation"); session.sessionId = session.id;
    return { change: { session } };
  } });
});
function Fixture() {
  const [,setCount] = useState(0);
  report=()=>setCount(creates.length);
  return <div className="app general-chat-fixture" data-fixture="__projectSessionFixture"><aside><h1>DeliDev</h1><button onClick={()=>release?.()}>Fixture release</button><button onClick={()=>void i18n.changeLanguage("ko")}>Fixture Korean</button><button onClick={()=>void i18n.changeLanguage("en")}>Fixture English</button><output data-fixture-creates={creates.length}>{JSON.stringify(creates.at(-1)??{})}</output></aside><main><MutationIntents>
    <NewSession active ownsActivation activation={1} entryProjectId={project.id} readLocalWorker={async()=>{document.documentElement.dataset.proofReads=String(Number(document.documentElement.dataset.proofReads)+1);return {machineId:machine.id,token:"A".repeat(43)};}} back={()=>{}} openSettings={()=>{}} open={()=>{}} created={()=>{}} />
  </MutationIntents></main></div>;
}
createRoot(document.getElementById("root")!).render(<TransportProvider transport={transport}><QueryClientProvider client={client}><Fixture /></QueryClientProvider></TransportProvider>);
