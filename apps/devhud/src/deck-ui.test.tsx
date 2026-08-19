// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { DeckPollingBoundary, DeckSurface } from "./deck-ui.tsx";
import { DeckCacheVersion, deckCacheKey, writeDeckCache } from "./deck.ts";
import { createGitHubProvider } from "./github-provider.ts";
import { messages } from "./localization.ts";
import { NativeBridgeError, NativeBridgeErrorCode, type NativeBridgeRequestV1, type NativeBridgeResponseV1, type NativeBridgeV1 } from "./native-bridge.ts";
import type { IdentitySettingsValue } from "./service-boundary.tsx";
import { defaultDevHudSettings, parseDevHudSettings } from "./settings-contract.ts";

let identity: IdentitySettingsValue;

vi.mock("./service-boundary.tsx", () => ({ useIdentitySettings: () => identity }));

const profile = { id: "018f47a2-7b3c-7def-8abc-1234567890ab", name: "Work", kind: "fine-grained" as const };
const deck = { id: "018f47a2-7b3c-7def-8abc-1234567890ac", name: "Deck", profileRef: profile.id, query: "repo:octo/widgets is:pr label:\"needs review\"", builder: { repository: "octo/widgets", author: null, review: null, label: "needs review", state: null }, display: { groupBy: "none" as const, showDrafts: true }, refreshMinutes: 5 as const, notifications: [] };
const settings = parseDevHudSettings({ ...defaultDevHudSettings, github: { ...defaultDevHudSettings.github, profiles: [profile] }, decks: [deck] });
const pullRequest = { nodeId: "PR_kwDOA", number: 1, title: "Keep this result", url: "https://github.com/octo/widgets/pull/1", draft: false, repository: { owner: "octo", name: "widgets" }, author: "octocat", state: "open" as const, reviewDecision: null, requestedReviewers: [], checkRollup: { state: "PENDING", contexts: [] }, mergeable: "MERGEABLE", labels: [], updatedAt: "2026-08-18T00:00:00.000Z" };

function identityWith(overrides: Partial<IdentitySettingsValue> = {}): IdentitySettingsValue {
  return {
    status: "guest", bootstrap: null, account: null, settings, revision: 0n, readOnly: false, shortcutHydrationReady: true, activeShortcutBindings: settings.shortcuts.desktop, setActiveShortcutBindings: vi.fn(), offline: false, error: null, accountError: null, settingsError: null, deletionCleanupFailed: false, deckAccessSuspended: false, importDiff: null, conflict: null, signInPending: false, identityResetAvailable: false, githubPatScopeId: Promise.resolve("origin.scope"), githubPatCleanupPending: false, reconcileGitHubPats: vi.fn(async () => true),
    signIn: vi.fn(), retryIdentity: vi.fn(), resetIdentity: vi.fn(), retryAccount: vi.fn(), retrySettings: vi.fn(), continueLocally: vi.fn(), uploadLocal: vi.fn(), replaceLocal: vi.fn(), replaceSettings: vi.fn(async () => true), replaceSettingsAt: vi.fn(async () => true), adoptConflictServer: vi.fn(), reapplyConflictLocal: vi.fn(), logout: vi.fn(), deleteAccount: vi.fn(), restoreAccount: vi.fn(), retryDeletionCleanup: vi.fn(), profileRequiresSetup: vi.fn(),
    ...overrides,
  };
}

function bridgeWith(request: (request: NativeBridgeRequestV1) => Promise<NativeBridgeResponseV1>): NativeBridgeV1 {
  return { request, listen: vi.fn(async () => () => undefined) };
}

function provider() {
  return {
    ...createGitHubProvider({ fetch: vi.fn() }),
    validateRepository: vi.fn(async (_credential, repository) => ({ repository, private: true, permissions: { metadata: true, pullRequests: true, issues: true, contents: true }, metadata: { etag: null, rate: { limit: null, remaining: null, used: null, resetAt: null, resource: null, retryAfterSeconds: null } } })),
  };
}

beforeEach(() => { localStorage.clear(); identity = identityWith(); });
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe("Deck surface", () => {
  it("renders an unavailable state instead of empty results when an uncached Deck is offline", () => {
    const bridge = bridgeWith(async () => { throw new Error("unexpected request"); });
    render(<DeckPollingBoundary bridge={bridge} active={false} online={false} provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    expect(screen.getByRole("heading", { name: messages.en.offlineTitle })).toBeTruthy();
    expect(screen.queryByText(messages.en.empty)).toBeNull();
  });

  it("renders loading instead of empty results during an uncached initial refresh", async () => {
    const searchPullRequests = vi.fn(() => new Promise<never>(() => {}));
    const bridge = bridgeWith(async (request) => request.operation === "secure.read" ? { kind: "secure-value", value: "token" } : { kind: "ok" });
    render(<DeckPollingBoundary bridge={bridge} active online provider={{ ...provider(), searchPullRequests }}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    await waitFor(() => expect(searchPullRequests).toHaveBeenCalledOnce());
    expect(screen.getByRole("heading", { name: messages.en.loadingTitle })).toBeTruthy();
    expect(screen.queryByText(messages.en.empty)).toBeNull();
  });

  it("surfaces a failed Deck deletion and leaves its action available for retry", async () => {
    const replaceSettings: IdentitySettingsValue["replaceSettings"] = vi.fn(async () => { throw new Error("offline"); });
    identity = identityWith({ replaceSettings });
    const bridge = bridgeWith(async () => ({ kind: "ok" as const }));
    render(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    fireEvent.click(screen.getByRole("button", { name: messages.en.deckDelete }));

    await waitFor(() => expect(screen.getByRole("alert").textContent).toBe(messages.en.deckDeleteFailed));
    expect(screen.getByRole("button", { name: messages.en.deckDelete })).not.toHaveProperty("disabled", true);
  });

  it("retains hydrated cached results when the first refresh fails", async () => {
    let resolveScope: (scopeId: string) => void = () => {};
    const scope = new Promise<string>((resolve) => { resolveScope = resolve; });
    const cacheScope = `origin.scope.${profile.id}`;
    writeDeckCache(localStorage, cacheScope, { version: DeckCacheVersion, deckId: deck.id, query: deck.query, queryEtag: "cached-etag", results: [pullRequest], lastSuccessfulAt: "2026-08-17T00:00:00.000Z", rate: null, failures: 0, nextRefreshAt: null, transitionKeys: [] });
    identity = identityWith({ githubPatScopeId: scope });
    let rejectSearch: (error: Error) => void = () => {};
    const searchPullRequests = vi.fn(() => new Promise<never>((_resolve, reject) => { rejectSearch = reject; }));
    const bridge = bridgeWith(async (request) => request.operation === "secure.read" ? { kind: "secure-value", value: "token" } : { kind: "ok" });
    render(<DeckPollingBoundary bridge={bridge} active online provider={{ ...provider(), searchPullRequests }}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    resolveScope("origin.scope");
    await waitFor(() => expect(searchPullRequests).toHaveBeenCalledWith(expect.anything(), deck.query, { etag: "cached-etag" }));
    rejectSearch(new Error("offline"));

    await waitFor(() => {
      const cache = JSON.parse(localStorage.getItem(deckCacheKey(cacheScope, deck.id)) ?? "null") as { results: unknown; lastSuccessfulAt: string | null };
      expect(cache.results).toEqual([pullRequest]);
      expect(cache.lastSuccessfulAt).toBe("2026-08-17T00:00:00.000Z");
    });
  });

  it("persists a delivered transition key after polling is suspended", async () => {
    const notificationsDeck = { ...deck, notifications: ["review" as const] };
    const cacheScope = `origin.scope.${profile.id}`;
    writeDeckCache(localStorage, cacheScope, { version: DeckCacheVersion, deckId: deck.id, query: deck.query, queryEtag: null, results: [pullRequest], lastSuccessfulAt: "2026-08-17T00:00:00.000Z", rate: null, failures: 0, nextRefreshAt: null, transitionKeys: [] });
    identity = identityWith({ settings: parseDevHudSettings({ ...settings, decks: [notificationsDeck] }) });
    let completeNotification: () => void = () => {};
    const request = vi.fn<(request: NativeBridgeRequestV1) => Promise<NativeBridgeResponseV1>>(async (request) => {
      if (request.operation === "secure.read") return { kind: "secure-value", value: "token" };
      if (request.operation === "notifications.publish-deck-change") return new Promise<NativeBridgeResponseV1>((resolve) => { completeNotification = () => resolve({ kind: "ok" }); });
      return { kind: "ok" };
    });
    const bridge = bridgeWith(request);
    const updated = { ...pullRequest, reviewDecision: "approved" as const, updatedAt: "2026-08-18T00:01:00.000Z" };
    const providerWithTransition = { ...provider(), searchPullRequests: vi.fn(async () => ({ items: [{ nodeId: pullRequest.nodeId, number: pullRequest.number, title: pullRequest.title, url: pullRequest.url, draft: pullRequest.draft, repository: pullRequest.repository }], nextPage: null, notModified: false, metadata: { etag: null, rate: { limit: null, remaining: null, used: null, resetAt: null, resource: null, retryAfterSeconds: null } } })), enrichPullRequests: vi.fn(async () => ({ items: [updated], metadata: { etag: null, rate: { limit: null, remaining: null, used: null, resetAt: null, resource: null, retryAfterSeconds: null } } })) };
    const view = render(<DeckPollingBoundary bridge={bridge} active online provider={providerWithTransition}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);
    await waitFor(() => expect(request).toHaveBeenCalledWith(expect.objectContaining({ operation: "notifications.publish-deck-change" })));

    view.rerender(<DeckPollingBoundary bridge={bridge} active={false} online provider={providerWithTransition}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);
    await new Promise((resolve) => setTimeout(resolve, 0));
    completeNotification();

    await waitFor(() => {
      const cache = JSON.parse(localStorage.getItem(deckCacheKey(cacheScope, deck.id)) ?? "null") as { transitionKeys: readonly string[] };
      expect(cache.transitionKeys).toEqual([`${pullRequest.nodeId}:review:approved:${updated.updatedAt}`]);
    });
  });

  it("retries an undelivered transition without recording its key", async () => {
    const notificationsDeck = { ...deck, notifications: ["review" as const] };
    const cacheScope = `origin.scope.${profile.id}`;
    writeDeckCache(localStorage, cacheScope, { version: DeckCacheVersion, deckId: deck.id, query: deck.query, queryEtag: null, results: [pullRequest], lastSuccessfulAt: "2026-08-17T00:00:00.000Z", rate: null, failures: 0, nextRefreshAt: null, transitionKeys: [] });
    identity = identityWith({ settings: parseDevHudSettings({ ...settings, decks: [notificationsDeck] }) });
    vi.stubGlobal("Notification", { permission: "denied" });
    let publicationAttempts = 0;
    const request = vi.fn(async (request: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (request.operation === "secure.read") return { kind: "secure-value", value: "token" };
      if (request.operation === "notifications.publish-deck-change") { publicationAttempts += 1; if (publicationAttempts === 1) throw new NativeBridgeError(NativeBridgeErrorCode.PlatformFailure); }
      return { kind: "ok" };
    });
    const bridge = bridgeWith(request);
    const updated = { ...pullRequest, reviewDecision: "approved" as const, updatedAt: "2026-08-18T00:01:00.000Z" };
    const providerWithTransition = { ...provider(), searchPullRequests: vi.fn(async () => ({ items: [{ nodeId: pullRequest.nodeId, number: pullRequest.number, title: pullRequest.title, url: pullRequest.url, draft: pullRequest.draft, repository: pullRequest.repository }], nextPage: null, notModified: false, metadata: { etag: null, rate: { limit: null, remaining: null, used: null, resetAt: null, resource: null, retryAfterSeconds: null } } })), enrichPullRequests: vi.fn(async () => ({ items: [updated], metadata: { etag: null, rate: { limit: null, remaining: null, used: null, resetAt: null, resource: null, retryAfterSeconds: null } } })) };
    render(<DeckPollingBoundary bridge={bridge} active online provider={providerWithTransition}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    await waitFor(() => expect(publicationAttempts).toBe(1));
    expect(JSON.parse(localStorage.getItem(deckCacheKey(cacheScope, deck.id)) ?? "null").transitionKeys).toEqual([]);

    fireEvent.click(screen.getByRole("button", { name: messages.en.deckRefresh }));
    await waitFor(() => expect(publicationAttempts).toBe(2));
    await waitFor(() => expect(JSON.parse(localStorage.getItem(deckCacheKey(cacheScope, deck.id)) ?? "null").transitionKeys).toEqual([`${pullRequest.nodeId}:review:approved:${updated.updatedAt}`]));
  });

  it("renders the secure-storage diagnostic when reading a Deck credential fails", async () => {
    const bridge = bridgeWith(async (request) => {
      if (request.operation === "secure.read") throw new NativeBridgeError(NativeBridgeErrorCode.StorageFailure);
      return { kind: "ok" };
    });
    render(<DeckPollingBoundary bridge={bridge} active online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    await waitFor(() => expect(screen.getByRole("alert").textContent).toBe(messages.en.githubErrorSecureStorage));
  });

  it("keeps a missing deep link visible until the user returns to the Deck list", () => {
    const dismiss = vi.fn();
    const bridge = bridgeWith(async () => { throw new Error("unexpected request"); });
    const view = render(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} selectedDeckId="018f47a2-7b3c-7def-8abc-1234567890ad" onDismissMissingLink={dismiss} /></DeckPollingBoundary>);

    expect(screen.getByRole("alert").textContent).toBe(messages.en.deckNotFound);
    fireEvent.click(screen.getByRole("button", { name: messages.en.deckReturnToList }));
    expect(dismiss).toHaveBeenCalledOnce();

    view.rerender(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} onDismissMissingLink={dismiss} /></DeckPollingBoundary>);
    expect(screen.getByRole("button", { name: messages.en.deckCreate })).toBeTruthy();
  });

  it("clears a consumed deep link when the user manually selects another Deck", () => {
    const other = { ...deck, id: "018f47a2-7b3c-7def-8abc-1234567890ad", name: "Other Deck" };
    identity = identityWith({ settings: parseDevHudSettings({ ...settings, decks: [deck, other] }) });
    const dismiss = vi.fn();
    const bridge = bridgeWith(async () => { throw new Error("unexpected request"); });
    const view = render(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} selectedDeckId={deck.id} onDismissMissingLink={dismiss} /></DeckPollingBoundary>);

    fireEvent.click(screen.getByRole("button", { name: other.name }));
    expect(dismiss).toHaveBeenCalledOnce();

    identity = identityWith({ settings: parseDevHudSettings({ ...settings, decks: [deck, other] }) });
    view.rerender(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} selectedDeckId={null} onDismissMissingLink={dismiss} /></DeckPollingBoundary>);
    expect(screen.getByRole("button", { name: other.name }).className).toContain("active");
  });

  it("cancels native notifications when synchronized Decks disappear", async () => {
    const request = vi.fn(async () => ({ kind: "ok" as const }));
    const bridge = bridgeWith(request);
    const view = render(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);
    await new Promise((resolve) => setTimeout(resolve, 0));

    identity = identityWith({ settings: parseDevHudSettings({ ...settings, decks: [] }) });
    view.rerender(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    await waitFor(() => expect(request).toHaveBeenCalledWith({ operation: "notifications.cancel-deck", deckId: deck.id }));
  });

  it("removes the profile-scoped cache when synchronized Decks disappear", async () => {
    const cacheScope = `origin.scope.${profile.id}`;
    writeDeckCache(localStorage, cacheScope, { version: DeckCacheVersion, deckId: deck.id, query: deck.query, queryEtag: null, results: [], lastSuccessfulAt: null, rate: null, failures: 0, nextRefreshAt: null, transitionKeys: [] });
    const bridge = bridgeWith(async () => ({ kind: "ok" as const }));
    const view = render(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);
    await waitFor(() => expect(localStorage.getItem(deckCacheKey(cacheScope, deck.id))).not.toBeNull());

    identity = identityWith({ settings: parseDevHudSettings({ ...settings, decks: [] }) });
    view.rerender(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    await waitFor(() => expect(localStorage.getItem(deckCacheKey(cacheScope, deck.id))).toBeNull());
  });

  it("cancels native notifications when the polling boundary unmounts", async () => {
    const request = vi.fn(async () => ({ kind: "ok" as const }));
    const bridge = bridgeWith(request);
    const view = render(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);
    await new Promise((resolve) => setTimeout(resolve, 0));

    view.unmount();

    await waitFor(() => expect(request).toHaveBeenCalledWith({ operation: "notifications.cancel-deck", deckId: deck.id }));
  });

  it("ignores a failed refresh that started before the Deck was renamed", async () => {
    let rejectSearch: (error: Error) => void = () => {};
    const searchPullRequests = vi.fn(() => new Promise<never>((_resolve, reject) => { rejectSearch = reject; }));
    const bridge = bridgeWith(async (request) => request.operation === "secure.read" ? { kind: "secure-value", value: "token" } : { kind: "ok" });
    const setCache = vi.spyOn(Storage.prototype, "setItem");
    const view = render(<DeckPollingBoundary bridge={bridge} active online provider={{ ...provider(), searchPullRequests }}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);
    await waitFor(() => expect(searchPullRequests).toHaveBeenCalledOnce());

    identity = identityWith({ settings: parseDevHudSettings({ ...settings, decks: [{ ...deck, name: "Renamed Deck" }] }) });
    view.rerender(<DeckPollingBoundary bridge={bridge} active online provider={{ ...provider(), searchPullRequests }}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);
    rejectSearch(new Error("offline"));
    await new Promise((resolve) => setTimeout(resolve, 10));

    expect(setCache).not.toHaveBeenCalledWith(expect.stringContaining(deck.id), expect.any(String));
  });

  it("ignores a failed refresh that started before its interval changed", async () => {
    let rejectSearch: (error: Error) => void = () => {};
    const searchPullRequests = vi.fn(() => new Promise<never>((_resolve, reject) => { rejectSearch = reject; }));
    const bridge = bridgeWith(async (request) => request.operation === "secure.read" ? { kind: "secure-value", value: "token" } : { kind: "ok" });
    const initial = parseDevHudSettings({ ...settings, decks: [{ ...deck, refreshMinutes: 30 }] });
    identity = identityWith({ settings: initial });
    const setCache = vi.spyOn(Storage.prototype, "setItem");
    const view = render(<DeckPollingBoundary bridge={bridge} active online provider={{ ...provider(), searchPullRequests }}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);
    await waitFor(() => expect(searchPullRequests).toHaveBeenCalledOnce());

    identity = identityWith({ settings: parseDevHudSettings({ ...settings, decks: [{ ...deck, refreshMinutes: 1 }] }) });
    view.rerender(<DeckPollingBoundary bridge={bridge} active online provider={{ ...provider(), searchPullRequests }}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);
    rejectSearch(new Error("offline"));
    await new Promise((resolve) => setTimeout(resolve, 10));

    expect(setCache).not.toHaveBeenCalledWith(expect.stringContaining(deck.id), expect.any(String));
  });

  it("shares the two-repository validation limit across scheduled Decks", async () => {
    const releases: Array<() => void> = [];
    const validateRepository: ReturnType<typeof provider>["validateRepository"] = vi.fn((_credential, repository) => new Promise((resolve) => {
      releases.push(() => resolve({ repository, private: true, permissions: { metadata: true, pullRequests: true, issues: true, contents: true }, metadata: { etag: null, rate: { limit: null, remaining: null, used: null, resetAt: null, resource: null, retryAfterSeconds: null } } }));
    }));
    const firstDeck = { ...deck, query: "repo:octo/one repo:octo/two is:pr", builder: null };
    const secondDeck = { ...deck, id: "018f47a2-7b3c-7def-8abc-1234567890ad", name: "Other Deck", query: "repo:octo/three repo:octo/four is:pr", builder: null };
    identity = identityWith({ settings: parseDevHudSettings({ ...settings, decks: [firstDeck, secondDeck] }) });
    const bridge = bridgeWith(async (request) => request.operation === "secure.read" ? { kind: "secure-value", value: "token" } : { kind: "ok" });
    render(<DeckPollingBoundary bridge={bridge} active online provider={{ ...provider(), validateRepository }}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    await waitFor(() => expect(validateRepository).toHaveBeenCalledTimes(2));
    releases[0]?.();
    await waitFor(() => expect(validateRepository).toHaveBeenCalledTimes(3));
    releases[1]?.();
    await waitFor(() => expect(validateRepository).toHaveBeenCalledTimes(4));
    releases[2]?.();
    releases[3]?.();
  });

  it("revalidates queued repositories after polling resumes", async () => {
    const releases: Array<() => void> = [];
    const validateRepository: ReturnType<typeof provider>["validateRepository"] = vi.fn(() => new Promise((resolve) => { releases.push(() => resolve({ repository: { owner: "octo", name: "widgets" }, private: true, permissions: { metadata: true, pullRequests: true, issues: true, contents: true }, metadata: { etag: null, rate: { limit: null, remaining: null, used: null, resetAt: null, resource: null, retryAfterSeconds: null } } })); }));
    const searchPullRequests = vi.fn();
    const queuedDeck = { ...deck, query: "repo:octo/one repo:octo/two repo:octo/three is:pr", builder: null };
    identity = identityWith({ settings: parseDevHudSettings({ ...settings, decks: [queuedDeck] }) });
    const bridge = bridgeWith(async (request) => request.operation === "secure.read" ? { kind: "secure-value", value: "token" } : { kind: "ok" });
    const view = render(<DeckPollingBoundary bridge={bridge} active online provider={{ ...provider(), validateRepository, searchPullRequests }}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);
    await waitFor(() => expect(validateRepository).toHaveBeenCalledTimes(2));

    view.rerender(<DeckPollingBoundary bridge={bridge} active={false} online provider={{ ...provider(), validateRepository, searchPullRequests }}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);
    await new Promise((resolve) => setTimeout(resolve, 0));
    for (const release of releases) release();

    await new Promise((resolve) => setTimeout(resolve, 10));
    expect(validateRepository).toHaveBeenCalledTimes(2);
    expect(searchPullRequests).not.toHaveBeenCalled();

    view.rerender(<DeckPollingBoundary bridge={bridge} active online provider={{ ...provider(), validateRepository, searchPullRequests }}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);
    await waitFor(() => expect(validateRepository).toHaveBeenCalledTimes(3));
    releases[2]?.();
    await waitFor(() => expect(searchPullRequests).toHaveBeenCalledOnce());
  });

  it("normalizes builder values and reprojects repeated qualifiers before saving", async () => {
    const repeated = { ...deck, query: "repo:octo/widgets is:pr label:one label:two", builder: { ...deck.builder, label: "one" } };
    const replaceSettings: IdentitySettingsValue["replaceSettings"] = vi.fn(async (update) => {
      const current = parseDevHudSettings({ ...settings, decks: [repeated] });
      const next = typeof update === "function" ? update(current) : update;
      expect(next.decks[0]?.builder).toMatchObject({ author: "octocat", label: "two" });
      expect(next.decks[0]?.query).toBe("repo:octo/widgets is:pr  label:two author:octocat");
      return true;
    });
    identity = identityWith({ settings: parseDevHudSettings({ ...settings, decks: [repeated] }), replaceSettings });
    const bridge = bridgeWith(async (request) => request.operation === "secure.read" ? { kind: "secure-value", value: "token" } : { kind: "ok" });
    render(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    fireEvent.change(screen.getByLabelText(messages.en.deckBuilderAuthor), { target: { value: " octocat " } });
    fireEvent.change(screen.getByLabelText(messages.en.deckBuilderLabel), { target: { value: "" } });
    fireEvent.click(screen.getByRole("button", { name: messages.en.saved }));

    await waitFor(() => expect(replaceSettings).toHaveBeenCalledOnce());
  });

  it("persists the selected result grouping for a newly created Deck", async () => {
    const replaceSettings: IdentitySettingsValue["replaceSettings"] = vi.fn(async (update) => {
      const next = typeof update === "function" ? update(settings) : update;
      expect(next.decks.at(-1)).toMatchObject({ name: "Grouped Deck", display: { groupBy: "author", showDrafts: true } });
      return true;
    });
    identity = identityWith({ replaceSettings });
    const bridge = bridgeWith(async (request) => request.operation === "secure.read" ? { kind: "secure-value", value: "token" } : { kind: "ok" });
    render(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    fireEvent.click(screen.getByRole("button", { name: messages.en.deckCreate }));
    fireEvent.change(screen.getByLabelText(messages.en.deckName), { target: { value: "Grouped Deck" } });
    fireEvent.change(screen.getByLabelText(messages.en.deckQuery), { target: { value: "repo:octo/widgets is:pr" } });
    fireEvent.change(screen.getByLabelText(messages.en.deckGroupBy), { target: { value: "author" } });
    fireEvent.click(screen.getByRole("button", { name: messages.en.saved }));

    await waitFor(() => expect(replaceSettings).toHaveBeenCalledOnce());
  });

  it("recomputes a failed Deck cache deadline when its interval changes", async () => {
    const initialSettings = parseDevHudSettings({ ...settings, decks: [{ ...deck, refreshMinutes: 30 }] });
    const oldDeadline = new Date(Date.now() + 60 * 60_000).toISOString();
    const cacheScope = `origin.scope.${profile.id}`;
    writeDeckCache(localStorage, cacheScope, {
      version: DeckCacheVersion,
      deckId: deck.id,
      query: deck.query,
      queryEtag: null,
      results: [],
      lastSuccessfulAt: null,
      rate: null,
      failures: 1,
      nextRefreshAt: oldDeadline,
      transitionKeys: [],
    });
    identity = identityWith({ settings: initialSettings });
    const bridge = bridgeWith(async () => { throw new Error("unexpected request"); });
    const readCache = vi.spyOn(Storage.prototype, "getItem");
    const view = render(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);
    await waitFor(() => expect(readCache).toHaveBeenCalledWith(deckCacheKey(cacheScope, deck.id)));

    identity = identityWith({ settings: parseDevHudSettings({ ...settings, decks: [{ ...deck, refreshMinutes: 1 }] }) });
    view.rerender(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    await waitFor(() => {
      const cache = JSON.parse(localStorage.getItem(deckCacheKey(cacheScope, deck.id)) ?? "null") as { nextRefreshAt: string | null };
      expect(cache.nextRefreshAt).not.toBe(oldDeadline);
      expect(Date.parse(cache.nextRefreshAt ?? "")).toBeLessThan(Date.now() + 3 * 60_000);
    });
  });

});
