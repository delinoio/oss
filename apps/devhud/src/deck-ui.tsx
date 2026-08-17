import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { applyDeckBuilder, classifyDeckFailure, clearDeckCache, deckTransitionKeys, nextDeckRefresh, parseDeckBuilder, readDeckCache, validateDeckQuery, writeDeckCache, type DeckCache, type DeckFailure } from "./deck.ts";
import { createGitHubProvider, GitHubProviderError, readGitHubCredential, type GitHubDeckPullRequest, type GitHubProvider } from "./github-provider.ts";
import type { Copy } from "./localization.ts";
import { DeckNotificationKind, type NativeBridgeV1 } from "./native-bridge.ts";
import { useIdentitySettings } from "./service-boundary.tsx";
import { hasRepositoryQualifier, type DeckBuilder, type DevHudSettingsV1 } from "./settings-contract.ts";
import { EmptyState, OfflineState } from "./surface-state.tsx";

interface DeckSurfaceProps { readonly copy: Copy; readonly bridge: NativeBridgeV1; readonly active: boolean; readonly online: boolean; readonly selectedDeckId?: string | null; readonly provider?: GitHubProvider; }

export function DeckSurface({ copy, bridge, active, online, selectedDeckId = null, provider: suppliedProvider }: DeckSurfaceProps) {
  const identity = useIdentitySettings();
  const defaultProvider = useMemo(() => createGitHubProvider({ fetch: globalThis.fetch }), []);
  const provider = suppliedProvider ?? defaultProvider;
  const [selected, setSelected] = useState<string | null>(selectedDeckId);
  const [creating, setCreating] = useState(false);
  const [cache, setCache] = useState<DeckCache | null>(null);
  const [loading, setLoading] = useState(false);
  const [failure, setFailure] = useState<DeckFailure | null>(null);
  const selectedDeck = identity.settings.decks.find((item) => item.id === selected) ?? identity.settings.decks[0] ?? null;
  const deck = creating ? null : selectedDeck;
  const cacheRef = useRef<DeckCache | null>(null);
  const loadingRef = useRef(false);
  const deckRef = useRef<typeof deck>(deck);
  useEffect(() => { deckRef.current = deck; }, [deck]);
  useEffect(() => {
    if (selectedDeckId && identity.settings.decks.some((item) => item.id === selectedDeckId)) {
      setSelected(selectedDeckId);
      setCreating(false);
    }
  }, [identity.settings.decks, selectedDeckId]);
  useEffect(() => {
    const current = deck;
    const next = current === null ? null : readDeckCache(localStorage, current.profileRef, current.id);
    cacheRef.current = next;
    setCache(next);
    setFailure(null);
  }, [deck?.id, deck?.profileRef]);

  const refresh = useCallback(async (manual = false) => {
    const currentDeck = deckRef.current;
    if (currentDeck === null || loadingRef.current || !online || !active) return;
    const currentCache = cacheRef.current;
    if (!manual && currentCache?.nextRefreshAt !== null && currentCache?.nextRefreshAt !== undefined && Date.parse(currentCache.nextRefreshAt) > Date.now()) return;
    const profile = identity.settings.github.profiles.find((item) => item.id === currentDeck.profileRef);
    if (profile === undefined) { setFailure("token"); return; }
    const isCurrentDeck = () => deckRef.current?.id === currentDeck.id && deckRef.current.profileRef === currentDeck.profileRef && deckRef.current.query === currentDeck.query;
    loadingRef.current = true;
    setLoading(true); setFailure(null);
    try {
      const credential = await readGitHubCredential(bridge, profile, await identity.githubPatScopeId);
      const search = await provider.searchPullRequests(credential, currentDeck.query, { etag: currentCache?.queryEtag ?? undefined });
      const results = search.notModified ? currentCache?.results ?? [] : (await provider.enrichPullRequests(credential, search.items.slice(0, 100).map((item) => item.nodeId))).items;
      let transitionKeys = currentCache?.transitionKeys ?? [];
      if (currentCache !== null && currentDeck.notifications.length > 0) {
        const transitions = deckTransitionKeys(currentCache.results, results).filter((transition) => currentDeck.notifications.includes(transition.kind) && !transitionKeys.includes(transition.key));
        for (const transition of transitions) {
          await publishDeckNotification(bridge, currentDeck.id, transition.key, transition.kind, currentDeck.name, transition.pullRequest.title);
          transitionKeys = [...transitionKeys, transition.key];
        }
      }
      const next: DeckCache = { version: 1, deckId: currentDeck.id, queryEtag: search.metadata.etag ?? currentCache?.queryEtag ?? null, results, lastSuccessfulAt: new Date().toISOString(), rate: search.metadata.rate, failures: 0, nextRefreshAt: null, transitionKeys };
      writeDeckCache(localStorage, currentDeck.profileRef, next);
      if (isCurrentDeck()) { cacheRef.current = next; setCache(next); }
    } catch (error) {
      if (isCurrentDeck()) setFailure(classifyDeckFailure(error));
      if (currentCache !== null) {
        const failures = currentCache.failures + 1;
        const rate = error instanceof GitHubProviderError ? error.rate : currentCache.rate;
        const next = { ...currentCache, rate, failures, nextRefreshAt: nextDeckRefresh(Date.now(), currentDeck.refreshMinutes, failures, rate) };
        writeDeckCache(localStorage, currentDeck.profileRef, next);
        if (isCurrentDeck()) { cacheRef.current = next; setCache(next); }
      }
    }
    finally { loadingRef.current = false; setLoading(false); }
  }, [active, bridge, identity.githubPatScopeId, identity.settings.github.profiles, online, provider]);
  useEffect(() => {
    if (deck === null || !active || !online) return;
    const timeout = setTimeout(() => void refresh(), 0);
    const interval = setInterval(() => void refresh(), deck.refreshMinutes * 60_000);
    return () => { clearTimeout(timeout); clearInterval(interval); };
  }, [deck?.id, deck?.query, deck?.profileRef, deck?.refreshMinutes, active, online, refresh]);

  if (identity.settings.github.profiles.length === 0) return <>{online ? <EmptyState copy={copy} /> : <OfflineState copy={copy} />}<p role="status">{copy.deckNoProfiles}</p></>;
  return <section className="deck" aria-labelledby="deck-title"><div className="deck-head"><h2 id="deck-title">{copy.deckTitle}</h2><button type="button" onClick={() => { setSelected(null); setCreating(true); }}>{copy.deckCreate}</button></div>
    <div className="deck-layout"><nav aria-label={copy.deck}><ul>{identity.settings.decks.map((item) => <li key={item.id}><button className={deck?.id === item.id ? "active" : ""} onClick={() => { setSelected(item.id); setCreating(false); }}>{item.name}</button></li>)}</ul></nav>
      {deck === null ? <DeckEditor key="create" copy={copy} onSave={async (next) => { await identity.replaceSettings((current) => ({ ...current, decks: [...current.decks, next] })); setSelected(next.id); setCreating(false); }} profiles={identity.settings.github.profiles} /> : <div><DeckEditor key={deck.id} copy={copy} value={deck} profiles={identity.settings.github.profiles} disabled={identity.readOnly} onSave={async (next) => { await identity.replaceSettings((current) => ({ ...current, decks: current.decks.map((item) => item.id === deck.id ? next : item) })); }} />
        <div className="actions"><button type="button" disabled={loading || !active || !online} onClick={() => void refresh(true)}>{copy.deckRefresh}</button><button type="button" disabled={identity.readOnly} onClick={() => void identity.replaceSettings((current) => ({ ...current, decks: current.decks.filter((item) => item.id !== deck.id) })).then(async () => { clearDeckCache(localStorage, deck.profileRef, deck.id); cacheRef.current = null; setCache(null); await bridge.request({ operation: "notifications.cancel-deck", deckId: deck.id }); }).catch(() => {})}>{copy.deckDelete}</button></div>
        {cache?.lastSuccessfulAt && <p>{copy.lastSuccessfulRefresh}: <time dateTime={cache.lastSuccessfulAt}>{cache.lastSuccessfulAt}</time></p>}{cache?.rate?.resetAt && <p>{copy.deckRateReset}: <time dateTime={cache.rate.resetAt}>{cache.rate.resetAt}</time></p>}{(!online || !active) && cache && <p className="notice">{copy.deckStale}</p>}{failure && <p role="alert">{failureCopy(copy, failure)}</p>}
        <DeckResults copy={copy} results={deck.display.showDrafts ? cache?.results ?? [] : (cache?.results ?? []).filter((pullRequest) => !pullRequest.draft)} />
      </div>}</div></section>;
}

function DeckEditor({ copy, value, profiles, disabled = false, onSave }: { readonly copy: Copy; readonly value?: DevHudSettingsV1["decks"][number]; readonly profiles: DevHudSettingsV1["github"]["profiles"]; readonly disabled?: boolean; readonly onSave: (deck: DevHudSettingsV1["decks"][number]) => Promise<void> }) {
  const [name, setName] = useState(value?.name ?? ""); const [profileRef, setProfileRef] = useState(value?.profileRef ?? profiles[0]?.id ?? ""); const [query, setQuery] = useState(value?.query ?? "is:pr"); const [builder, setBuilder] = useState<DeckBuilder | null>(value?.builder ?? parseDeckBuilder(value?.query ?? "")); const [refreshMinutes, setRefreshMinutes] = useState<1 | 5 | 15 | 30>(value?.refreshMinutes ?? 5); const [showDrafts, setShowDrafts] = useState(value?.display.showDrafts ?? true); const [notifications, setNotifications] = useState<readonly DeckNotificationKind[]>(value?.notifications ?? []); const [invalid, setInvalid] = useState<"query" | "repository" | null>(null);
  useEffect(() => { if (value) { setName(value.name); setProfileRef(value.profileRef); setQuery(value.query); setBuilder(value.builder ?? parseDeckBuilder(value.query)); setRefreshMinutes(value.refreshMinutes); setShowDrafts(value.display.showDrafts); setNotifications(value.notifications); } }, [value]);
  const submit = (event: FormEvent) => { event.preventDefault(); if (!validateDeckQuery(query) || !profileRef || !name.trim()) { setInvalid("query"); return; } if (!hasRepositoryQualifier(query)) { setInvalid("repository"); return; } setInvalid(null); void onSave({ id: value?.id ?? createUuidV7(), name: name.trim(), profileRef, query, builder, display: { groupBy: value?.display.groupBy ?? "none", showDrafts }, refreshMinutes, notifications }); };
  const setBuilderValue = (field: keyof DeckBuilder, next: string) => { const value = next === "" ? null : next as DeckBuilder[typeof field]; setBuilder((current) => ({ repository: null, author: null, review: null, label: null, state: null, ...current, [field]: value })); setQuery((current) => applyDeckBuilder(current, field, value)); };
  return <form onSubmit={submit} className="deck-editor"><fieldset disabled={disabled}><label>{copy.deckName}<input required value={name} onChange={(event) => setName(event.target.value)} /></label><label>{copy.deckProfile}<select required value={profileRef} onChange={(event) => setProfileRef(event.target.value)}>{profiles.map((profile) => <option key={profile.id} value={profile.id}>{profile.name}</option>)}</select></label><label>{copy.deckQuery}<input required value={query} onChange={(event) => { setQuery(event.target.value); setBuilder(parseDeckBuilder(event.target.value)); }} /></label><fieldset><legend>{copy.deckBuilder}</legend><label>{copy.deckBuilderRepository}<input value={builder?.repository ?? ""} onChange={(event) => setBuilderValue("repository", event.target.value)} /></label><label>{copy.deckBuilderAuthor}<input value={builder?.author ?? ""} onChange={(event) => setBuilderValue("author", event.target.value)} /></label><label>{copy.deckBuilderLabel}<input value={builder?.label ?? ""} onChange={(event) => setBuilderValue("label", event.target.value)} /></label><label>{copy.deckBuilderReview}<select value={builder?.review ?? ""} onChange={(event) => setBuilderValue("review", event.target.value)}><option value="">{copy.deckAny}</option><option value="approved">{copy.deckApproved}</option><option value="changes-requested">{copy.deckChangesRequested}</option><option value="required">{copy.deckRequired}</option></select></label><label>{copy.deckBuilderState}<select value={builder?.state ?? ""} onChange={(event) => setBuilderValue("state", event.target.value)}><option value="">{copy.deckAny}</option><option value="open">{copy.deckOpen}</option><option value="closed">{copy.deckClosed}</option><option value="merged">{copy.deckMerged}</option></select></label></fieldset><label>{copy.deckRefreshInterval}<select value={refreshMinutes} onChange={(event) => setRefreshMinutes(Number(event.target.value) as 1 | 5 | 15 | 30)}>{[1, 5, 15, 30].map((minutes) => <option key={minutes} value={minutes}>{minutes} {copy.deckMinutes}</option>)}</select></label><label><input type="checkbox" checked={showDrafts} onChange={(event) => setShowDrafts(event.target.checked)} />{copy.deckShowDrafts}</label><label><input type="checkbox" checked={notifications.length > 0} onChange={(event) => setNotifications(event.target.checked ? ["review", "checks", "merged", "closed"] : [])} />{copy.deckNotifications}</label><button type="submit">{copy.saved}</button>{invalid && <p role="alert">{invalid === "repository" ? copy.deckRequireRepository : copy.deckRequirePullRequests}</p>}</fieldset></form>;
}

function DeckResults({ copy, results }: { readonly copy: Copy; readonly results: readonly GitHubDeckPullRequest[] }) { return <section aria-labelledby="deck-results-title"><h3 id="deck-results-title">{copy.deckResults}</h3>{results.length === 0 ? <p>{copy.empty}</p> : <ul className="deck-results">{results.map((pullRequest) => <li key={pullRequest.nodeId}><strong>{pullRequest.repository.owner}/{pullRequest.repository.name}#{pullRequest.number}: {pullRequest.title}</strong><span>{pullRequest.state}{pullRequest.draft ? " · draft" : ""} · {pullRequest.reviewDecision ?? "no review"} · {pullRequest.checkRollup.state ?? "no checks"}</span><span>{pullRequest.author} · {pullRequest.labels.join(", ")} · <time dateTime={pullRequest.updatedAt}>{pullRequest.updatedAt}</time></span></li>)}</ul>}</section>; }
function failureCopy(copy: Copy, failure: DeckFailure): string { return failure === "token" ? copy.deckErrorToken : failure === "permission" ? copy.deckErrorPermission : failure === "query" ? copy.deckErrorQuery : failure === "rate-limit" ? copy.deckErrorRate : copy.deckErrorNetwork; }
async function publishDeckNotification(bridge: NativeBridgeV1, deckId: string, key: string, kind: "review" | "checks" | "merged" | "closed", title: string, body: string): Promise<void> { try { await bridge.request({ operation: "notifications.publish-deck-change", notification: { id: `deck:${deckId}:${key}`, deckId, kind: kind as DeckNotificationKind, title, body } }); } catch { if (typeof Notification !== "undefined" && Notification.permission === "granted") new Notification(title, { body, tag: `deck:${deckId}:${key}` }); } }
function createUuidV7(now = Date.now()): string { const bytes = crypto.getRandomValues(new Uint8Array(16)); let timestamp = BigInt(now); for (let index = 5; index >= 0; index -= 1) { bytes[index] = Number(timestamp & 0xffn); timestamp >>= 8n; } bytes[6] = (bytes[6] & 0x0f) | 0x70; bytes[8] = (bytes[8] & 0x3f) | 0x80; const hex = [...bytes].map((value) => value.toString(16).padStart(2, "0")).join(""); return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`; }
