// SPDX-License-Identifier: Apache-2.0
import { useAppearancePreferences } from "./appearance";
import { DisclosureDefault } from "./appearance-preferences";
import { useLayoutEffect, useRef, useState, type ReactNode, type RefObject } from "react";
import { EntityKind, type Resource } from "@delinoio/delidev-api-client";
import { Disclosure, DisclosureDensity, DisclosureSummary } from "./disclosure";
import { statusLabel } from "./product-status";
import { copy, useLocale } from "./localization";
import { ScrollPayloadWindow, type PayloadWindowQuery } from "./scroll-payload-window";
import { conversationProjection, type ConversationProjection } from "./tool-turn-projection";
import "./tool-turn-transcript.css";

interface Choices { groups: Map<string, boolean>; entries: Map<string, boolean>; details: Map<string, boolean[]> }
function ToolEntry({ active, row, payload, token, query, choices, changed, render }: { active: boolean; row: ConversationProjection; payload?: Resource; token?: string; query: PayloadWindowQuery<ConversationProjection, Resource>; choices: Choices; changed: () => void; render: (row: Resource) => ReactNode }) {
  const preferences=useAppearancePreferences();
  if(!choices.entries.has(row.id))choices.entries.set(row.id,preferences.tool_disclosure===DisclosureDefault.Expanded);
  const node = useRef<HTMLDivElement>(null), open = choices.entries.get(row.id) ?? false;
  const hasPayload = Boolean(payload);
  useLayoutEffect(() => {
    const details = [...node.current?.querySelectorAll<HTMLDetailsElement>("details") ?? []];
    // The compact entry replaces only the original primary presentation toggle.
    // Its validated renderer and all nested output/argument disclosures survive.
    const remember = () => choices.details.set(row.id, [...node.current?.querySelectorAll<HTMLDetailsElement>("details") ?? []].map(detail => detail.open));
    const element = node.current;
    element?.addEventListener("toggle", remember, true);
    details.forEach((detail, index) => { if (index === 0) { detail.dataset.toolPrimary = "true"; detail.open = true; } else detail.open = choices.details.get(row.id)?.[index] ?? false; });
    return () => element?.removeEventListener("toggle", remember, true);
  }, [hasPayload, choices, row.id]);
  return <li onFocusCapture={event => { event.stopPropagation(); query.protect?.(token); }} onBlurCapture={event => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) query.protect?.(); }}><Disclosure density={DisclosureDensity.Compact} open={open} onToggle={event => { choices.entries.set(row.id, event.currentTarget.open); changed(); }}>
    <DisclosureSummary><span>{row.tool?.name || copy("session.tool_7c9bbe")}</span><small>{statusLabel(row.tool?.state ?? "")}</small></DisclosureSummary>
    {payload ? <div className="tool-entry-payload" ref={node}>{render(payload)}</div> : <button type="button" disabled={!active || Boolean(query.loading || query.error)} onClick={() => { if (active && token !== undefined) query.restore(token); }}>{copy("pagination.restore")}</button>}
  </Disclosure></li>;
}

/** One reached-record list per original owner, including evicted page anchors.
 * Full resources stay in the caller's original three-page window/live tail. */
export function ToolTurnTranscript({ sessionId, active = true, query, live, removed, arrivals, root, render, include }: { sessionId: string; active?: boolean; query: PayloadWindowQuery<ConversationProjection, Resource> & { nextPageToken: string }; live: ReadonlyMap<string, Resource>; removed: ReadonlySet<string>; arrivals: readonly string[]; root: RefObject<HTMLElement | null>; render: (row: Resource) => ReactNode; include?: (row: ConversationProjection) => boolean }) {
  useLocale();
  const preferences=useAppearancePreferences();
  const [choices] = useState<Choices>(() => ({ groups: new Map(), entries: new Map(), details: new Map() }));
  const [, update] = useState(0), changed = () => update(value => value + 1);
  const payloads = new Map<string, Resource>(), tokens = new Map<string, string>();
  for (const page of query.payloadPages) for (const row of page.payload) { const prior = payloads.get(row.id); if (!prior || row.revision > prior.revision) payloads.set(row.id, row); }
  const newestProjection = new Map<string, ConversationProjection>();
  for (const page of query.pages) for (const row of page.rows) { const prior = newestProjection.get(row.id); if (!prior || row.revision > prior.revision) newestProjection.set(row.id, row); }
  const projections: ConversationProjection[] = [], seen = new Set<string>();
  for (const page of query.pages) for (let original of page.rows) {
    if (seen.has(original.id) || removed.has(original.id)) continue;
    seen.add(original.id); tokens.set(original.id, page.token);
    original = newestProjection.get(original.id)!;
    const latest = live.get(original.id), retained = payloads.get(original.id);
    if (latest?.sessionId === sessionId && latest.kind === retained?.kind && latest.revision > (retained?.revision ?? original.revision)) payloads.set(original.id, latest);
    // A newer authoritative stream projection can also update evicted metadata.
    projections.push(latest?.sessionId === sessionId && latest.kind === EntityKind.MESSAGE && latest.revision > original.revision ? conversationProjection(latest, sessionId) : retained && payloads.get(original.id)!.revision >= original.revision ? conversationProjection(payloads.get(original.id)!, sessionId) : original);
  }
  const tail: Resource[] = [];
  if (!query.nextPageToken) for (const id of arrivals) { const row = live.get(id); if (!row || row.sessionId !== sessionId || seen.has(id) || removed.has(id)) continue; if (row.kind !== EntityKind.MESSAGE) continue;
    const projected = conversationProjection(row, sessionId);
    seen.add(id); tail.push(row); payloads.set(id, row); projections.push(projected);
  }
  if(include){for(let i=projections.length-1;i>=0;i--)if(!include(projections[i]))projections.splice(i,1);}
  const groups = new Map<string, ConversationProjection[]>();
  for (const row of projections) if (row.tool) { const group = groups.get(row.tool.owner) ?? []; group.push(row); groups.set(row.tool.owner, group); }
  const item = (projection: ConversationProjection, resource?: Resource): ReactNode => {
    if (!projection.tool) return resource ? render(resource) : null;
    const entries = groups.get(projection.tool.owner)!;
    if (entries[0].id !== projection.id) return null;
    const owner = projection.tool.owner;
    if(!choices.groups.has(owner))choices.groups.set(owner,preferences.tool_disclosure===DisclosureDefault.Expanded);
    return <Disclosure key={owner} className="tool-turn" density={DisclosureDensity.Compact} open={choices.groups.get(owner) ?? false} onToggle={event => { choices.groups.set(owner, event.currentTarget.open); changed(); }}><DisclosureSummary>{copy("session.toolCalls")}</DisclosureSummary><p className="tool-turn-coverage">{copy("session.reachedTools")}</p><ol>{entries.map(row => <ToolEntry key={row.id} active={active} row={row} payload={payloads.get(row.id)} token={tokens.get(row.id)} query={query} choices={choices} changed={changed} render={render} />)}</ol></Disclosure>;
  };
  const byId = new Map(projections.map(row => [row.id, row]));
  const presented = (row: ConversationProjection) => {
    const result = item(row, payloads.get(row.id));
    return result ? <div key={row.id}>{result}</div> : null;
  };
  return <><ScrollPayloadWindow query={query} root={root} active={active} identity={row => row.id} revision={row => row.revision} projected={rows => rows.flatMap(row => byId.has(row.id) ? [presented(byId.get(row.id)!)] : [])}>{() => null}</ScrollPayloadWindow>{tail.flatMap(row => byId.has(row.id)?[presented(byId.get(row.id)!)]:[])}</>;
}
