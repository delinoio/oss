// SPDX-License-Identifier: Apache-2.0
import { ProductReferenceKind as Kind, ProductReferenceLabels } from "./product-references.js";
const references: Record<string, Kind> = {
  provider_id: Kind.Provider, model_id: Kind.Model, machine_id: Kind.Worker, agent_id: Kind.Worker,
  account_id: Kind.Account, project_id: Kind.Project, session_id: Kind.Session,
  primary_repository: Kind.Repository, repository_id: Kind.Repository,
};
/** Project only typed import-plan identities. Keep user/native strings and integer tokens exact. */
export function productConfigurationReview(raw: string, labels: ProductReferenceLabels, language = "en"): string {
  // Validate syntax only; never serialize the parsed numeric values.
  JSON.parse(raw);
  const tokens = raw.match(/"(?:\\.|[^"\\])*"|[{}\[\],:]|[^\s{}\[\],:]+/g) ?? [];
  let cursor = 0;
  function value(path: string[]): string {
    const token = tokens[cursor++];
    if (token === "{") {
      const fields: string[] = [];
      while (tokens[cursor] !== "}") {
        const key = tokens[cursor++]; cursor++;
        fields.push(`${key}:${value([...path, JSON.parse(key)])}`);
        if (tokens[cursor] !== ",") break;
        cursor++;
      }
      cursor++; return `{${fields.join(",")}}`;
    }
    if (token === "[") {
      const items: string[] = [];
      while (tokens[cursor] !== "]") {
        items.push(value([...path, "*"]));
        if (tokens[cursor] !== ",") break;
        cursor++;
      }
      cursor++; return `[${items.join(",")}]`;
    }
    const key = path.at(-1)!;
    const location = path.join(".");
    if (location === "token") return JSON.stringify(language.startsWith("ko") ? "내부 검증 증거" : "Internal inspection proof");
    let kind: Kind | undefined;
    if (/^plan\.(changes|machines)\.\*\.(id|source_id)$/.test(location)) kind = path[1] === "machines" ? Kind.Worker : Kind.Resource;
    if (/^plan\.changes\.\*\.(before|after)\.[^.]+$/.test(location)) kind = references[key];
    if (/^plan\.changes\.\*\.(before|after)\.checkouts\.\*\.machine_id$/.test(location)) kind = Kind.Worker;
    if (/^plan\.changes\.\*\.(before|after)\.(repositories|accounts|models|templates|agents|machines)\.\*(\.id)?$/.test(location)) {
      kind = ({ repositories: Kind.Repository, accounts: Kind.Account, models: Kind.Model, templates: Kind.Resource, agents: Kind.Worker, machines: Kind.Worker } as Record<string, Kind>)[path[4]];
    }
    if (/^plan\.changes\.\*\.(before|after)\.(accounts|agents|machines)\.ids\.\*$/.test(location)) kind = path[4] === "accounts" ? Kind.Account : path[4] === "agents" ? Kind.Agent : Kind.Worker;
    if (/^plan\.changes\.\*\.(before|after)\.sources\.\*\.model\.provider_id$/.test(location)) kind = Kind.Provider;
    if (/^plan\.changes\.\*\.(before|after)\.sources\.\*\.accounts\.\*\.id$/.test(location)) kind = Kind.Account;
    return kind && token?.startsWith('"') ? JSON.stringify(labels.label(JSON.parse(token), kind, language)) : token;
  }
  return value([]);
}
