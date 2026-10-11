// SPDX-License-Identifier: Apache-2.0
import { createContext, useContext, useRef, type ReactNode } from "react";
import { QueryClientContext, type QueryClient } from "@tanstack/react-query";
import { EntityKind, type Resource } from "@delinoio/delidev-api-client";
import { copy, useLocale } from "./localization";
import { document, text } from "./documents";

export enum ProductKind { Resource="resource", Project="project", Repository="repository", Agent="agent", Runner="runner", Account="account", Provider="provider", Session="session", Backup="backup", Request="request", Execution="execution", Connection="connection", Device="device", Input="input", Template="template" }
// Presentation numbers are never persisted, recycled or accepted as operation
// identities. Query clients own connection lifetimes and release this registry
// with their caches. A detached view owns its own registry instead.
export class PresentationNumbers {
  private readonly numbers = new Map<string, number>();
  private readonly names = new Map<string, string>();
  retainedName(id: string, kind: ProductKind, name?: string): string | undefined {
    const key = `${kind}:${id}`;
    if (name !== undefined) this.names.set(key, name);
    return this.names.get(key);
  }
  number(id: string): number { let number=this.numbers.get(id); if(number===undefined){number=this.numbers.size+1;this.numbers.set(id,number);} return number; }
}
const resourceKinds: Partial<Record<ProductKind,EntityKind>> = {
  [ProductKind.Project]:EntityKind.PROJECT,[ProductKind.Repository]:EntityKind.REPOSITORY,
  [ProductKind.Agent]:EntityKind.AGENT,[ProductKind.Runner]:EntityKind.MACHINE,
  [ProductKind.Account]:EntityKind.ACCOUNT,[ProductKind.Provider]:EntityKind.PROVIDER,
  [ProductKind.Session]:EntityKind.SESSION,[ProductKind.Device]:EntityKind.DEVICE,
};
const scopes = new WeakMap<QueryClient, PresentationNumbers>();
const DetachedScope=createContext<PresentationNumbers | undefined>(undefined);
export function ProductPresentationScope({children}:{children:ReactNode}) {
  const numbers=useRef<PresentationNumbers>(null);if(!numbers.current)numbers.current=new PresentationNumbers();
  return <DetachedScope.Provider value={numbers.current}>{children}</DetachedScope.Provider>;
}
export function usePresentationNumbers() {
  const client=useContext(QueryClientContext),detached=useContext(DetachedScope), local=useRef<PresentationNumbers>(null);
  if(!local.current) local.current=new PresentationNumbers();
  if(client&&!scopes.has(client)) scopes.set(client,new PresentationNumbers());
  return client?scopes.get(client)!:detached??local.current;
}
export function useProductLabels() {
  const client=useContext(QueryClientContext),numbers=usePresentationNumbers();
  return (id: string, kind=ProductKind.Resource, observedName?: string): string => {
    if(!id) return copy(`product-identity.${kind}Unavailable`);
    let name=observedName;
    if(name===undefined&&client) {
      // Only inspect observations already admitted by their original owners.
      // This projection introduces no query, invalidation or subscription.
      for(const query of client.getQueryCache().getAll()) {
        const data=query.state.data as {resource?:Resource;resources?:Resource[]} | undefined;
        const row=data?.resource?.id===id?data.resource:Array.isArray(data?.resources)?data.resources.find(row=>row.id===id):undefined;
        if(row && (resourceKinds[kind]===undefined || row.kind===resourceKinds[kind])) {
          const value=document(row);name=text(value.name)||text(value.alias);if(name)break;
        }
      }
    }
    name = numbers.retainedName(id, kind, name);
    return copy("product-identity.numbered",{name:name||copy(`product-identity.${kind}Unavailable`),number:numbers.number(id)});
  };
}
export function ProductIdentity({id,kind=ProductKind.Resource,name}:{id:string;kind?:ProductKind;name?:string}) { useLocale();const label=useProductLabels();return <>{label(id,kind,name)}</>; }
/** Only application-generated diagnostic text enters this boundary. User,
 * native/model/tool text, exports and original failure records stay unchanged. */
export function diagnosticText(value:string):string {return value.replace(/\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b/gi,()=>copy("product-identity.privateReference"));}

export function presentationMetadata(value:unknown):unknown {
  if(Array.isArray(value)) return value.map(presentationMetadata);
  if(!value||typeof value!=="object")return value;
  const result:Record<string,unknown>={};
  const identities=new Set(["id","server_id","device_id","machine_id","project_id","repository_id","session_id","execution_id","input_id","account_id","provider_id","connection_id","request_id","correlation_id","runtime_generation"]);
  for(const [key,item] of Object.entries(value)) {
    if((identities.has(key) || key.endsWith("_id"))&&!key.startsWith("native_")&&typeof item==="string"&&/^[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$/i.test(item))continue;
    // Original artifact subtrees remain byte-for-byte data projections.
    result[key]=["text","content","prompt","name","alias","native","metadata","output"].includes(key)||key.startsWith("native_")?item:presentationMetadata(item);
  }
  return result;
}

/** Project structured application metadata without parsing numeric literals into
 * floating-point values. Original preview bytes remain the mutation authority. */
export function presentationJSON(raw: string): string {
  const tokens = raw.match(/"(?:\\.|[^"\\])*"|[{}\[\],:]|[^\s{}\[\],:]+/g) ?? [];
  let at = 0;
  const protectedFields = new Set(["name", "alias", "text", "content", "contents", "prompt", "native", "metadata", "output"]);
  const identity = (key: string) => key === "id" || key.endsWith("_id") || key.endsWith("_ids") || ["ids", "repositories", "accounts", "runtime_generation"].includes(key);
  const project = (key = "", preserved = false): string => {
    const token = tokens[at++];
    if (token === "{") {
      const fields: string[] = [];
      while (tokens[at] !== "}") {
        const field = tokens[at++], fieldName = JSON.parse(field) as string;
        at++; // Colon in the already validated JSON.
        fields.push(`${field}:${project(fieldName, preserved || protectedFields.has(fieldName) || fieldName.startsWith("native_"))}`);
        if (tokens[at] !== ",") break;
        at++;
      }
      at++;
      return `{${fields.join(",")}}`;
    }
    if (token === "[") {
      const values: string[] = [];
      while (tokens[at] !== "]") {
        values.push(project(key, preserved));
        if (tokens[at] !== ",") break;
        at++;
      }
      at++;
      return `[${values.join(",")}]`;
    }
    if (!preserved && identity(key) && token?.startsWith('"')) {
      const value = JSON.parse(token) as string;
      if (/^[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$/i.test(value)) return JSON.stringify(copy("product-identity.privateReference"));
    }
    return token;
  };
  return project();
}
