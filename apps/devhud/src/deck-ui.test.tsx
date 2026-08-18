// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { DeckPollingBoundary, DeckSurface } from "./deck-ui.tsx";
import { DeckCacheVersion, deckCacheKey, writeDeckCache } from "./deck.ts";
import { createGitHubProvider } from "./github-provider.ts";
import { messages } from "./localization.ts";
import type { NativeBridgeRequestV1, NativeBridgeResponseV1, NativeBridgeV1 } from "./native-bridge.ts";
import type { IdentitySettingsValue } from "./service-boundary.tsx";
import { defaultDevHudSettings, parseDevHudSettings } from "./settings-contract.ts";

let identity: IdentitySettingsValue;

vi.mock("./service-boundary.tsx", () => ({ useIdentitySettings: () => identity }));

const profile = { id: "018f47a2-7b3c-7def-8abc-1234567890ab", name: "Work", kind: "fine-grained" as const };
const deck = { id: "018f47a2-7b3c-7def-8abc-1234567890ac", name: "Deck", profileRef: profile.id, query: "repo:octo/widgets is:pr label:\"needs review\"", builder: { repository: "octo/widgets", author: null, review: null, label: "needs review", state: null }, display: { groupBy: "none" as const, showDrafts: true }, refreshMinutes: 5 as const, notifications: [] };
const settings = parseDevHudSettings({ ...defaultDevHudSettings, github: { ...defaultDevHudSettings.github, profiles: [profile] }, decks: [deck] });

function identityWith(overrides: Partial<IdentitySettingsValue> = {}): IdentitySettingsValue {
  return {
    status: "guest", bootstrap: null, account: null, settings, revision: 0n, readOnly: false, shortcutHydrationReady: true, activeShortcutBindings: settings.shortcuts.desktop, setActiveShortcutBindings: vi.fn(), offline: false, error: null, accountError: null, settingsError: null, deletionCleanupFailed: false, deckAccessSuspended: false, importDiff: null, conflict: null, signInPending: false, identityResetAvailable: false, githubPatScopeId: Promise.resolve("origin.scope"), githubPatCleanupPending: false, reconcileGitHubPats: vi.fn(async () => true),
    signIn: vi.fn(), retryIdentity: vi.fn(), resetIdentity: vi.fn(), retryAccount: vi.fn(), retrySettings: vi.fn(), continueLocally: vi.fn(), uploadLocal: vi.fn(), replaceLocal: vi.fn(), replaceSettings: vi.fn(async () => true), adoptConflictServer: vi.fn(), reapplyConflictLocal: vi.fn(), logout: vi.fn(), deleteAccount: vi.fn(), restoreAccount: vi.fn(), retryDeletionCleanup: vi.fn(), profileRequiresSetup: vi.fn(),
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
