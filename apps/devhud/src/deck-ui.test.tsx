// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { DeckPollingBoundary, DeckSurface } from "./deck-ui.tsx";
import { DeckCacheVersion, deckCacheKey, writeDeckCache } from "./deck.ts";
import { invalidateDeckPolling } from "./deck-polling-cancellation.ts";
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
  it("requires explicit privacy consent before copying only the selected Deck into widget storage", async () => {
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => value.operation === "widgets.status" ? { kind: "widget-status", enabledDeckIds: [] } : { kind: "ok" });
    const bridge = bridgeWith(request);
    render(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} language="en" /></DeckPollingBoundary>);

    await screen.findByRole("button", { name: messages.en.widgetEnable });
    fireEvent.click(screen.getByRole("button", { name: messages.en.widgetEnable }));
    expect(screen.getByRole("alertdialog").textContent).toContain(messages.en.widgetPrivacyWarning);
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("button", { name: messages.en.widgetPrivacyCancel })));
    fireEvent.keyDown(window, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("button", { name: messages.en.widgetEnable })));
    fireEvent.click(screen.getByRole("button", { name: messages.en.widgetEnable }));
    fireEvent.click(screen.getByRole("button", { name: messages.en.widgetPrivacyConfirm }));

    await waitFor(() => expect(request).toHaveBeenCalledWith({
      operation: "widgets.enable-deck",
      configuration: { version: 1, deckId: deck.id, name: deck.name, query: deck.query, repositories: [{ owner: "octo", name: "widgets" }], profileId: profile.id, profileKind: profile.kind, scopeId: "origin.scope", language: "en" },
    }));
    expect(JSON.stringify(request.mock.calls)).not.toMatch(/github[_-]?pat|Bearer|token-value/iu);
  });

  it("publishes the cached refresh attempt instead of treating synchronization as a new attempt", async () => {
    const lastSuccessfulAt = "2026-08-17T00:00:00.000Z";
    writeDeckCache(localStorage, `origin.scope.${profile.id}`, { version: DeckCacheVersion, deckId: deck.id, query: deck.query, queryEtag: null, totalCount: 1, results: [pullRequest], lastSuccessfulAt, rate: null, failures: 0, nextRefreshAt: null, transitionKeys: [] });
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => value.operation === "widgets.status" ? { kind: "widget-status", enabledDeckIds: [deck.id] } : { kind: "ok" });
    const bridge = bridgeWith(request);

    render(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    await waitFor(() => expect(request).toHaveBeenCalledWith({
      operation: "widgets.replace-deck-snapshot",
      snapshot: expect.objectContaining({ deckId: deck.id, lastSuccessfulAt, lastAttemptedAt: lastSuccessfulAt }),
    }));
  });

  it("preserves the cached attempt failure when publishing after restart", async () => {
    const lastSuccessfulAt = "2026-08-17T00:00:00.000Z";
    const lastAttemptedAt = "2026-08-17T00:05:00.000Z";
    writeDeckCache(localStorage, `origin.scope.${profile.id}`, { version: DeckCacheVersion, deckId: deck.id, query: deck.query, queryEtag: null, totalCount: 1, results: [pullRequest], lastSuccessfulAt, lastAttemptedAt, rate: null, failures: 1, failure: "rate-limit", nextRefreshAt: "2026-08-17T00:10:00.000Z", transitionKeys: [] });
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => value.operation === "widgets.status" ? { kind: "widget-status", enabledDeckIds: [deck.id] } : { kind: "ok" });
    const bridge = bridgeWith(request);

    render(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    await waitFor(() => expect(request).toHaveBeenCalledWith({
      operation: "widgets.replace-deck-snapshot",
      snapshot: expect.objectContaining({ deckId: deck.id, state: "rate-limit", lastSuccessfulAt, lastAttemptedAt }),
    }));
  });

  it("does not publish the previous profile cache when an enabled Deck changes profile", async () => {
    writeDeckCache(localStorage, `origin.scope.${profile.id}`, { version: DeckCacheVersion, deckId: deck.id, query: deck.query, queryEtag: null, totalCount: 1, results: [pullRequest], lastSuccessfulAt: "2026-08-17T00:00:00.000Z", rate: null, failures: 0, nextRefreshAt: null, transitionKeys: [] });
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => value.operation === "widgets.status" ? { kind: "widget-status", enabledDeckIds: [deck.id] } : { kind: "ok" });
    const bridge = bridgeWith(request);
    const view = render(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);
    await waitFor(() => expect(request).toHaveBeenCalledWith(expect.objectContaining({ operation: "widgets.replace-deck-snapshot" })));
    request.mockClear();

    const nextProfile = { id: "018f47a2-7b3c-7def-8abc-1234567890ad", name: "Personal", kind: "fine-grained" as const };
    identity = identityWith({ settings: parseDevHudSettings({ ...settings, github: { ...settings.github, profiles: [profile, nextProfile] }, decks: [{ ...deck, profileRef: nextProfile.id }] }) });
    view.rerender(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    await waitFor(() => expect(request).toHaveBeenCalledWith(expect.objectContaining({ operation: "widgets.enable-deck", configuration: expect.objectContaining({ profileId: nextProfile.id }) })));
    await new Promise((resolve) => setTimeout(resolve, 10));
    expect(request).not.toHaveBeenCalledWith(expect.objectContaining({ operation: "widgets.replace-deck-snapshot" }));
  });

  it("does not publish an enabled widget snapshot when a legacy cache has no exact total", async () => {
    writeDeckCache(localStorage, `origin.scope.${profile.id}`, { version: DeckCacheVersion, deckId: deck.id, query: deck.query, queryEtag: "legacy-etag", results: [pullRequest], lastSuccessfulAt: "2026-08-17T00:00:00.000Z", rate: null, failures: 0, nextRefreshAt: null, transitionKeys: [] });
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => value.operation === "widgets.status" ? { kind: "widget-status", enabledDeckIds: [deck.id] } : { kind: "ok" });
    const bridge = bridgeWith(request);

    render(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    await waitFor(() => expect(request).toHaveBeenCalledWith(expect.objectContaining({ operation: "widgets.enable-deck" })));
    await new Promise((resolve) => setTimeout(resolve, 10));
    expect(request.mock.calls.map(([value]) => value.operation)).not.toContain("widgets.replace-deck-snapshot");
  });

  it("enables a widget without publishing a legacy cache whose exact total is unknown", async () => {
    writeDeckCache(localStorage, `origin.scope.${profile.id}`, { version: DeckCacheVersion, deckId: deck.id, query: deck.query, queryEtag: "legacy-etag", results: [pullRequest], lastSuccessfulAt: "2026-08-17T00:00:00.000Z", rate: null, failures: 0, nextRefreshAt: null, transitionKeys: [] });
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => value.operation === "widgets.status" ? { kind: "widget-status", enabledDeckIds: [] } : { kind: "ok" });
    const bridge = bridgeWith(request);
    render(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    await screen.findByRole("button", { name: messages.en.widgetEnable });
    fireEvent.click(screen.getByRole("button", { name: messages.en.widgetEnable }));
    fireEvent.click(screen.getByRole("button", { name: messages.en.widgetPrivacyConfirm }));

    await waitFor(() => expect(request).toHaveBeenCalledWith(expect.objectContaining({ operation: "widgets.enable-deck" })));
    await new Promise((resolve) => setTimeout(resolve, 10));
    expect(request.mock.calls.map(([value]) => value.operation)).not.toContain("widgets.replace-deck-snapshot");
  });

  it("retains native enablement when initial snapshot publication fails", async () => {
    writeDeckCache(localStorage, `origin.scope.${profile.id}`, { version: DeckCacheVersion, deckId: deck.id, query: deck.query, queryEtag: null, totalCount: 1, results: [pullRequest], lastSuccessfulAt: "2026-08-17T00:00:00.000Z", rate: null, failures: 0, nextRefreshAt: null, transitionKeys: [] });
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (value.operation === "widgets.status") return { kind: "widget-status", enabledDeckIds: [] };
      if (value.operation === "widgets.replace-deck-snapshot") throw new Error("storage-failure");
      return { kind: "ok" };
    });
    const bridge = bridgeWith(request);
    render(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    await screen.findByText(new RegExp(pullRequest.title, "u"));
    fireEvent.click(screen.getByRole("button", { name: messages.en.widgetEnable }));
    fireEvent.click(screen.getByRole("button", { name: messages.en.widgetPrivacyConfirm }));

    await waitFor(() => expect(request).toHaveBeenCalledWith(expect.objectContaining({ operation: "widgets.replace-deck-snapshot" })));
    await waitFor(() => expect(screen.getByRole("button", { name: messages.en.widgetDisable })).toBeTruthy());
    expect(screen.getByRole("alert").textContent).toBe(messages.en.widgetActionFailed);
    expect(screen.queryByRole("button", { name: messages.en.widgetEnable })).toBeNull();
  });

  it("keeps an enabled Deck available for disablement when configuration resynchronization fails", async () => {
    let failConfigurationSync = false;
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (value.operation === "widgets.status") return { kind: "widget-status", enabledDeckIds: [deck.id] };
      if (value.operation === "widgets.enable-deck" && failConfigurationSync) throw new Error("storage-failure");
      return { kind: "ok" };
    });
    const bridge = bridgeWith(request);
    const view = render(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} language="en" /></DeckPollingBoundary>);

    await screen.findByRole("button", { name: messages.en.widgetDisable });
    await waitFor(() => expect(request).toHaveBeenCalledWith(expect.objectContaining({ operation: "widgets.enable-deck" })));
    failConfigurationSync = true;
    const editedDeck = { ...deck, name: "Edited Deck" };
    identity = identityWith({ settings: parseDevHudSettings({ ...settings, decks: [editedDeck] }) });
    view.rerender(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} language="en" /></DeckPollingBoundary>);

    await waitFor(() => expect(screen.getByRole("alert").textContent).toBe(messages.en.widgetActionFailed));
    expect(screen.getByRole("button", { name: messages.en.widgetDisable })).toBeTruthy();
    expect(screen.queryByRole("button", { name: messages.en.widgetEnable })).toBeNull();
  });

  it("reconciles an enabled non-visible Deck after synchronized settings change", async () => {
    const hiddenDeck = { ...deck, id: "018f47a2-7b3c-7def-8abc-1234567890ad", name: "Hidden Deck", query: "repo:octo/hidden is:pr", builder: null };
    identity = identityWith({ settings: parseDevHudSettings({ ...settings, decks: [deck, hiddenDeck] }) });
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => value.operation === "widgets.status" ? { kind: "widget-status", enabledDeckIds: [hiddenDeck.id] } : { kind: "ok" });
    const bridge = bridgeWith(request);
    const view = render(<DeckPollingBoundary bridge={bridge} active={false} online language="en" provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    await waitFor(() => expect(request).toHaveBeenCalledWith(expect.objectContaining({ operation: "widgets.enable-deck", configuration: expect.objectContaining({ deckId: hiddenDeck.id, query: hiddenDeck.query }) })));
    request.mockClear();
    const editedHiddenDeck = { ...hiddenDeck, query: "repo:octo/renamed is:pr", builder: null };
    identity = identityWith({ settings: parseDevHudSettings({ ...settings, decks: [deck, editedHiddenDeck] }) });
    view.rerender(<DeckPollingBoundary bridge={bridge} active={false} online language="ko" provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    await waitFor(() => expect(request).toHaveBeenCalledWith({
      operation: "widgets.enable-deck",
      configuration: { version: 1, deckId: hiddenDeck.id, name: hiddenDeck.name, query: editedHiddenDeck.query, repositories: [{ owner: "octo", name: "renamed" }], profileId: profile.id, profileKind: profile.kind, scopeId: "origin.scope", language: "ko" },
    }));
  });

  it("publishes foreground refreshes for enabled non-visible Decks", async () => {
    const hiddenDeck = { ...deck, id: "018f47a2-7b3c-7def-8abc-1234567890ad", name: "Hidden Deck", query: "repo:octo/hidden is:pr", builder: null };
    const hiddenPullRequest = { ...pullRequest, nodeId: "PR_kwDOB", repository: { owner: "octo", name: "hidden" } };
    identity = identityWith({ settings: parseDevHudSettings({ ...settings, decks: [deck, hiddenDeck] }) });
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (value.operation === "widgets.status") return { kind: "widget-status", enabledDeckIds: [hiddenDeck.id] };
      if (value.operation === "secure.read") return { kind: "secure-value", value: "token" };
      return { kind: "ok" };
    });
    const bridge = bridgeWith(request);
    const providerWithResult = {
      ...provider(),
      searchPullRequests: vi.fn(async () => ({ items: [{ nodeId: hiddenPullRequest.nodeId, number: hiddenPullRequest.number, title: hiddenPullRequest.title, url: hiddenPullRequest.url, draft: hiddenPullRequest.draft, repository: hiddenPullRequest.repository }], nextPage: null, notModified: false, totalCount: 1, incompleteResults: false, metadata: { etag: null, rate: { limit: null, remaining: null, used: null, resetAt: null, resource: null, retryAfterSeconds: null } } })),
      enrichPullRequests: vi.fn(async () => ({ items: [hiddenPullRequest], metadata: { etag: null, rate: { limit: null, remaining: null, used: null, resetAt: null, resource: null, retryAfterSeconds: null } } })),
    };

    render(<DeckPollingBoundary bridge={bridge} active online provider={providerWithResult}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    await waitFor(() => expect(request).toHaveBeenCalledWith({
      operation: "widgets.replace-deck-snapshot",
      snapshot: expect.objectContaining({ deckId: hiddenDeck.id, query: hiddenDeck.query, results: [expect.objectContaining({ nodeId: hiddenPullRequest.nodeId })] }),
    }));
  });

  it("keeps manual disablement authoritative over a stale reconciliation", async () => {
    let statusCalls = 0;
    let resolveStaleStatus: (value: NativeBridgeResponseV1) => void = () => {};
    const staleStatus = new Promise<NativeBridgeResponseV1>((resolve) => { resolveStaleStatus = resolve; });
    const operations: string[] = [];
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (value.operation === "widgets.status") {
        statusCalls += 1;
        return statusCalls === 1 ? { kind: "widget-status", enabledDeckIds: [deck.id] } : staleStatus;
      }
      operations.push(value.operation);
      return { kind: "ok" };
    });
    const bridge = bridgeWith(request);
    const view = render(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);
    await screen.findByRole("button", { name: messages.en.widgetDisable });
    await waitFor(() => expect(operations).toContain("widgets.enable-deck"));
    operations.length = 0;

    identity = identityWith({ settings: parseDevHudSettings({ ...settings, decks: [{ ...deck, name: "Edited Deck" }] }) });
    view.rerender(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);
    await waitFor(() => expect(statusCalls).toBe(2));
    fireEvent.click(screen.getByRole("button", { name: messages.en.widgetDisable }));
    await waitFor(() => expect(operations.filter((operation) => operation.startsWith("widgets."))).toEqual(["widgets.disable-deck"]));
    resolveStaleStatus({ kind: "widget-status", enabledDeckIds: [deck.id] });
    await new Promise((resolve) => setTimeout(resolve, 10));

    expect(operations.filter((operation) => operation.startsWith("widgets."))).toEqual(["widgets.disable-deck"]);
    expect(screen.getByRole("button", { name: messages.en.widgetEnable })).toBeTruthy();
  });

  it("drops widget synchronization that was still building configuration when manually disabled", async () => {
    let resolveScope: (scopeId: string) => void = () => {};
    const scope = new Promise<string>((resolve) => { resolveScope = resolve; });
    identity = identityWith({ githubPatScopeId: scope });
    const operations: string[] = [];
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (value.operation === "widgets.status") return { kind: "widget-status", enabledDeckIds: [deck.id] };
      operations.push(value.operation);
      return { kind: "ok" };
    });
    const bridge = bridgeWith(request);
    render(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);
    await screen.findByRole("button", { name: messages.en.widgetDisable });

    fireEvent.click(screen.getByRole("button", { name: messages.en.widgetDisable }));
    await waitFor(() => expect(operations).toEqual(["widgets.disable-deck"]));
    resolveScope("origin.scope");
    await new Promise((resolve) => setTimeout(resolve, 10));

    expect(operations).toEqual(["widgets.disable-deck"]);
    expect(screen.getByRole("button", { name: messages.en.widgetEnable })).toBeTruthy();
  });

  it("drops widget synchronization that is still building configuration when the boundary unmounts", async () => {
    let resolveScope: (scopeId: string) => void = () => {};
    const scope = new Promise<string>((resolve) => { resolveScope = resolve; });
    identity = identityWith({ githubPatScopeId: scope });
    const widgetOperations: string[] = [];
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (value.operation === "widgets.status") return { kind: "widget-status", enabledDeckIds: [deck.id] };
      if (value.operation.startsWith("widgets.")) widgetOperations.push(value.operation);
      return { kind: "ok" };
    });
    const bridge = bridgeWith(request);
    const view = render(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);
    await screen.findByRole("button", { name: messages.en.widgetDisable });

    view.unmount();
    resolveScope("origin.scope");
    await new Promise((resolve) => setTimeout(resolve, 10));

    expect(widgetOperations).toEqual([]);
  });

  it("drops queued widget synchronization when the boundary unmounts", async () => {
    let releaseFirstEnable: () => void = () => {};
    const firstEnablePending = new Promise<void>((resolve) => { releaseFirstEnable = resolve; });
    let statusCalls = 0;
    let enableCalls = 0;
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (value.operation === "widgets.status") {
        statusCalls += 1;
        return { kind: "widget-status", enabledDeckIds: [deck.id] };
      }
      if (value.operation === "widgets.enable-deck") {
        enableCalls += 1;
        if (enableCalls === 1) await firstEnablePending;
      }
      return { kind: "ok" };
    });
    const bridge = bridgeWith(request);
    const view = render(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);
    await waitFor(() => expect(enableCalls).toBe(1));

    identity = identityWith({ settings: parseDevHudSettings({ ...settings, decks: [{ ...deck, name: "Edited Deck" }] }) });
    view.rerender(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);
    await waitFor(() => expect(statusCalls).toBe(2));
    view.unmount();
    releaseFirstEnable();
    await new Promise((resolve) => setTimeout(resolve, 10));

    expect(enableCalls).toBe(1);
  });

  it("continues reconciling other enabled Decks after one Deck is manually disabled", async () => {
    const otherDeck = { ...deck, id: "018f47a2-7b3c-7def-8abc-1234567890ad", name: "Other Deck", query: "repo:octo/other is:pr", builder: null };
    identity = identityWith({ settings: parseDevHudSettings({ ...settings, decks: [deck, otherDeck] }) });
    let blockEditedDeck = false;
    let releaseEditedDeck: () => void = () => {};
    const editedDeckPending = new Promise<void>((resolve) => { releaseEditedDeck = resolve; });
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (value.operation === "widgets.status") return { kind: "widget-status", enabledDeckIds: [deck.id, otherDeck.id] };
      if (value.operation === "widgets.enable-deck" && value.configuration.deckId === deck.id && blockEditedDeck) await editedDeckPending;
      return { kind: "ok" };
    });
    const bridge = bridgeWith(request);
    const view = render(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);
    await waitFor(() => expect(request).toHaveBeenCalledWith(expect.objectContaining({ operation: "widgets.enable-deck", configuration: expect.objectContaining({ deckId: otherDeck.id }) })));

    request.mockClear();
    blockEditedDeck = true;
    const editedDeck = { ...deck, name: "Edited Deck" };
    const editedOtherDeck = { ...otherDeck, name: "Edited Other Deck" };
    identity = identityWith({ settings: parseDevHudSettings({ ...settings, decks: [editedDeck, editedOtherDeck] }) });
    view.rerender(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);
    await waitFor(() => expect(request).toHaveBeenCalledWith(expect.objectContaining({ operation: "widgets.enable-deck", configuration: expect.objectContaining({ deckId: deck.id, name: editedDeck.name }) })));

    fireEvent.click(screen.getByRole("button", { name: messages.en.widgetDisable }));
    releaseEditedDeck();

    await waitFor(() => expect(request).toHaveBeenCalledWith({ operation: "widgets.disable-deck", deckId: deck.id }));
    await waitFor(() => expect(request).toHaveBeenCalledWith(expect.objectContaining({ operation: "widgets.enable-deck", configuration: expect.objectContaining({ deckId: otherDeck.id, name: editedOtherDeck.name }) })));
  });

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
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => value.operation === "widgets.status" ? { kind: "widget-status", enabledDeckIds: [deck.id] } : { kind: "ok" });
    const bridge = bridgeWith(request);
    render(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    fireEvent.click(screen.getByRole("button", { name: messages.en.deckDelete }));

    await waitFor(() => expect(screen.getByRole("alert").textContent).toBe(messages.en.deckDeleteFailed));
    expect(screen.getByRole("button", { name: messages.en.deckDelete })).not.toHaveProperty("disabled", true);
    expect(request).not.toHaveBeenCalledWith({ operation: "widgets.disable-deck", deckId: deck.id });
  });

  it("commits Deck deletion before clearing selected widget state", async () => {
    const operations: string[] = [];
    const replaceSettings: IdentitySettingsValue["replaceSettings"] = vi.fn(async () => { operations.push("delete-settings"); return true; });
    identity = identityWith({ replaceSettings });
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (value.operation === "widgets.status") return { kind: "widget-status", enabledDeckIds: [deck.id] };
      if (value.operation === "widgets.disable-deck") operations.push("clear-widget");
      return { kind: "ok" };
    });
    const bridge = bridgeWith(request);
    render(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    fireEvent.click(screen.getByRole("button", { name: messages.en.deckDelete }));

    await waitFor(() => expect(replaceSettings).toHaveBeenCalledOnce());
    await waitFor(() => expect(operations.slice(-2)).toEqual(["delete-settings", "clear-widget"]));
  });

  it("keeps Deck deletion authoritative over widget synchronization still building configuration", async () => {
    let resolveScope: (scopeId: string) => void = () => {};
    const scope = new Promise<string>((resolve) => { resolveScope = resolve; });
    const replaceSettings: IdentitySettingsValue["replaceSettings"] = vi.fn(async () => true);
    identity = identityWith({ githubPatScopeId: scope, replaceSettings });
    const widgetOperations: string[] = [];
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (value.operation === "widgets.status") return { kind: "widget-status", enabledDeckIds: [deck.id] };
      if (value.operation.startsWith("widgets.")) widgetOperations.push(value.operation);
      return { kind: "ok" };
    });
    const bridge = bridgeWith(request);
    render(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);
    await screen.findByRole("button", { name: messages.en.widgetDisable });

    fireEvent.click(screen.getByRole("button", { name: messages.en.deckDelete }));
    await waitFor(() => expect(replaceSettings).toHaveBeenCalledOnce());
    await waitFor(() => expect(widgetOperations).toEqual(["widgets.disable-deck"]));
    resolveScope("origin.scope");
    await new Promise((resolve) => setTimeout(resolve, 10));

    expect(widgetOperations).toEqual(["widgets.disable-deck"]);
  });

  it("disables builder controls until a Boolean Deck query is simplified", () => {
    const booleanDeck = { ...deck, query: "repo:octo/widgets is:pr OR repo:octo/tools is:pr", builder: null };
    identity = identityWith({ settings: parseDevHudSettings({ ...settings, decks: [booleanDeck] }) });
    const bridge = bridgeWith(async () => ({ kind: "ok" as const }));
    render(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    const repository = screen.getByLabelText(messages.en.deckBuilderRepository);
    expect(repository.closest("fieldset")).toHaveProperty("disabled", true);
    fireEvent.change(screen.getByLabelText(messages.en.deckQuery), { target: { value: "repo:octo/widgets is:pr" } });
    expect(repository.closest("fieldset")).toHaveProperty("disabled", false);
  });

  it("retains hydrated cached results when the first refresh fails", async () => {
    let resolveScope: (scopeId: string) => void = () => {};
    const scope = new Promise<string>((resolve) => { resolveScope = resolve; });
    const cacheScope = `origin.scope.${profile.id}`;
    const pendingNotifications = [{ key: "PR_kwDOA:review:approved:2026-08-18T00:01:00.000Z", kind: "review" as const, body: pullRequest.title }];
    writeDeckCache(localStorage, cacheScope, { version: DeckCacheVersion, deckId: deck.id, query: deck.query, queryEtag: "cached-etag", totalCount: 1, results: [pullRequest], lastSuccessfulAt: "2026-08-17T00:00:00.000Z", rate: null, failures: 0, nextRefreshAt: null, transitionKeys: [], pendingNotifications });
    identity = identityWith({ githubPatScopeId: scope, settings: parseDevHudSettings({ ...settings, decks: [{ ...deck, notifications: ["review" as const] }] }) });
    let rejectSearch: (error: Error) => void = () => {};
    const searchPullRequests = vi.fn(() => new Promise<never>((_resolve, reject) => { rejectSearch = reject; }));
    const bridge = bridgeWith(async (request) => request.operation === "secure.read" ? { kind: "secure-value", value: "token" } : { kind: "ok" });
    render(<DeckPollingBoundary bridge={bridge} active online provider={{ ...provider(), searchPullRequests }}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    resolveScope("origin.scope");
    await waitFor(() => expect(searchPullRequests).toHaveBeenCalledWith(expect.anything(), deck.query, { etag: "cached-etag" }));
    rejectSearch(new Error("offline"));

    await waitFor(() => {
      const cache = JSON.parse(localStorage.getItem(deckCacheKey(cacheScope, deck.id)) ?? "null") as { results: unknown; lastSuccessfulAt: string | null; pendingNotifications: unknown };
      expect(cache.results).toEqual([pullRequest]);
      expect(cache.lastSuccessfulAt).toBe("2026-08-17T00:00:00.000Z");
      expect(cache.pendingNotifications).toEqual(pendingNotifications);
    });
  });

  it("clears obsolete rate metadata when a newer foreground attempt fails before a response", async () => {
    const cacheScope = `origin.scope.${profile.id}`;
    const oldRate = { limit: 30, remaining: 0, used: 30, resetAt: "2026-08-17T01:00:00.000Z", resource: "search", retryAfterSeconds: 3_600 };
    writeDeckCache(localStorage, cacheScope, { version: DeckCacheVersion, deckId: deck.id, query: deck.query, queryEtag: null, totalCount: 1, results: [pullRequest], lastSuccessfulAt: "2026-08-17T00:00:00.000Z", rate: oldRate, failures: 0, nextRefreshAt: null, transitionKeys: [] });
    const request = vi.fn(async (value: NativeBridgeRequestV1): Promise<NativeBridgeResponseV1> => {
      if (value.operation === "widgets.status") return { kind: "widget-status", enabledDeckIds: [deck.id] };
      if (value.operation === "secure.read") throw new NativeBridgeError(NativeBridgeErrorCode.StorageFailure);
      return { kind: "ok" };
    });
    const bridge = bridgeWith(request);

    render(<DeckPollingBoundary bridge={bridge} active online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    await waitFor(() => expect(screen.getByRole("alert").textContent).toBe(messages.en.githubErrorSecureStorage));
    await waitFor(() => {
      const cache = JSON.parse(localStorage.getItem(deckCacheKey(cacheScope, deck.id)) ?? "null") as { rate: unknown; results: unknown };
      expect(cache.rate).toBeNull();
      expect(cache.results).toEqual([pullRequest]);
    });
    await waitFor(() => expect(request).toHaveBeenCalledWith({ operation: "widgets.replace-deck-snapshot", snapshot: expect.objectContaining({ deckId: deck.id, rate: null }) }));
  });

  it("keeps a legacy total absent across failure and retries without an ETag", async () => {
    const cacheScope = `origin.scope.${profile.id}`;
    writeDeckCache(localStorage, cacheScope, { version: DeckCacheVersion, deckId: deck.id, query: deck.query, queryEtag: "legacy-etag", results: [pullRequest], lastSuccessfulAt: "2026-08-17T00:00:00.000Z", rate: null, failures: 0, nextRefreshAt: null, transitionKeys: [] });
    const searchPullRequests = vi.fn()
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValueOnce({ items: [], nextPage: null, notModified: false, totalCount: 0, incompleteResults: false, metadata: { etag: "fresh-etag", rate: { limit: null, remaining: null, used: null, resetAt: null, resource: null, retryAfterSeconds: null } } });
    const bridge = bridgeWith(async (request) => request.operation === "secure.read" ? { kind: "secure-value", value: "token" } : { kind: "ok" });
    render(<DeckPollingBoundary bridge={bridge} active online provider={{ ...provider(), searchPullRequests }}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    await waitFor(() => expect(searchPullRequests).toHaveBeenCalledWith(expect.anything(), deck.query, { etag: undefined }));
    await waitFor(() => {
      const cache = JSON.parse(localStorage.getItem(deckCacheKey(cacheScope, deck.id)) ?? "null") as Record<string, unknown>;
      expect(cache).not.toHaveProperty("totalCount");
      expect(cache.results).toEqual([pullRequest]);
    });

    fireEvent.click(screen.getByRole("button", { name: messages.en.deckRefresh }));
    await waitFor(() => expect(searchPullRequests).toHaveBeenCalledTimes(2));
    expect(searchPullRequests).toHaveBeenNthCalledWith(2, expect.anything(), deck.query, { etag: undefined });
  });

  it("retains the complete cache and retries when GitHub search is incomplete", async () => {
    const cacheScope = `origin.scope.${profile.id}`;
    writeDeckCache(localStorage, cacheScope, { version: DeckCacheVersion, deckId: deck.id, query: deck.query, queryEtag: "cached-etag", results: [pullRequest], lastSuccessfulAt: "2026-08-17T00:00:00.000Z", rate: null, failures: 0, nextRefreshAt: null, transitionKeys: [], pendingNotifications: [] });
    const searchPullRequests = vi.fn(async () => ({ items: [], nextPage: null, notModified: false, totalCount: 0, incompleteResults: true, metadata: { etag: "partial-etag", rate: { limit: null, remaining: null, used: null, resetAt: null, resource: null, retryAfterSeconds: null } } }));
    const enrichPullRequests = vi.fn();
    const bridge = bridgeWith(async (request) => request.operation === "secure.read" ? { kind: "secure-value", value: "token" } : { kind: "ok" });
    render(<DeckPollingBoundary bridge={bridge} active online provider={{ ...provider(), searchPullRequests, enrichPullRequests }}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    await waitFor(() => expect(searchPullRequests).toHaveBeenCalledOnce());
    await waitFor(() => {
      const cache = JSON.parse(localStorage.getItem(deckCacheKey(cacheScope, deck.id)) ?? "null") as { queryEtag: string | null; results: unknown; lastSuccessfulAt: string | null; failures: number; nextRefreshAt: string | null };
      expect(cache).not.toHaveProperty("totalCount");
      expect(cache.queryEtag).toBe("cached-etag");
      expect(cache.results).toEqual([pullRequest]);
      expect(cache.lastSuccessfulAt).toBe("2026-08-17T00:00:00.000Z");
      expect(cache.failures).toBe(1);
      expect(cache.nextRefreshAt).not.toBeNull();
    });
    expect(enrichPullRequests).not.toHaveBeenCalled();
    expect(screen.getByRole("alert").textContent).toBe(messages.en.deckErrorIncomplete);
  });

  it("retains the complete cache when enrichment omits a searched pull request", async () => {
    const cacheScope = `origin.scope.${profile.id}`;
    const lastSuccessfulAt = "2026-08-17T00:00:00.000Z";
    writeDeckCache(localStorage, cacheScope, { version: DeckCacheVersion, deckId: deck.id, query: deck.query, queryEtag: "cached-etag", totalCount: 1, results: [pullRequest], lastSuccessfulAt, rate: null, failures: 0, nextRefreshAt: null, transitionKeys: [] });
    const missing = { nodeId: "PR_kwDOB", number: 2, title: "Missing result", url: "https://github.com/octo/widgets/pull/2", draft: false, repository: { owner: "octo", name: "widgets" } };
    const searchPullRequests = vi.fn(async () => ({ items: [pullRequest, missing], nextPage: null, notModified: false, totalCount: 2, incompleteResults: false, metadata: { etag: "partial-etag", rate: { limit: null, remaining: null, used: null, resetAt: null, resource: null, retryAfterSeconds: null } } }));
    const enrichPullRequests = vi.fn(async () => ({ items: [pullRequest], metadata: { etag: null, rate: { limit: null, remaining: null, used: null, resetAt: null, resource: null, retryAfterSeconds: null } } }));
    const bridge = bridgeWith(async (request) => request.operation === "secure.read" ? { kind: "secure-value", value: "token" } : { kind: "ok" });

    render(<DeckPollingBoundary bridge={bridge} active online provider={{ ...provider(), searchPullRequests, enrichPullRequests }}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    await waitFor(() => expect(enrichPullRequests).toHaveBeenCalledOnce());
    await waitFor(() => {
      const cache = JSON.parse(localStorage.getItem(deckCacheKey(cacheScope, deck.id)) ?? "null") as { queryEtag: string | null; totalCount: number; results: unknown; lastSuccessfulAt: string | null; failures: number; failure: string | null };
      expect(cache.queryEtag).toBe("cached-etag");
      expect(cache.totalCount).toBe(1);
      expect(cache.results).toEqual([pullRequest]);
      expect(cache.lastSuccessfulAt).toBe(lastSuccessfulAt);
      expect(cache.failures).toBe(1);
      expect(cache.failure).toBe("unknown");
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
    const providerWithTransition = { ...provider(), searchPullRequests: vi.fn(async () => ({ items: [{ nodeId: pullRequest.nodeId, number: pullRequest.number, title: pullRequest.title, url: pullRequest.url, draft: pullRequest.draft, repository: pullRequest.repository }], nextPage: null, notModified: false, totalCount: 1, incompleteResults: false, metadata: { etag: null, rate: { limit: null, remaining: null, used: null, resetAt: null, resource: null, retryAfterSeconds: null } } })), enrichPullRequests: vi.fn(async () => ({ items: [updated], metadata: { etag: null, rate: { limit: null, remaining: null, used: null, resetAt: null, resource: null, retryAfterSeconds: null } } })) };
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

  it("caches refreshed results and retries an undelivered transition", async () => {
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
    const providerWithTransition = { ...provider(), searchPullRequests: vi.fn(async () => ({ items: [{ nodeId: pullRequest.nodeId, number: pullRequest.number, title: pullRequest.title, url: pullRequest.url, draft: pullRequest.draft, repository: pullRequest.repository }], nextPage: null, notModified: false, totalCount: 1, incompleteResults: false, metadata: { etag: null, rate: { limit: null, remaining: null, used: null, resetAt: null, resource: null, retryAfterSeconds: null } } })), enrichPullRequests: vi.fn(async () => ({ items: [updated], metadata: { etag: null, rate: { limit: null, remaining: null, used: null, resetAt: null, resource: null, retryAfterSeconds: null } } })) };
    render(<DeckPollingBoundary bridge={bridge} active online provider={providerWithTransition}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    await waitFor(() => expect(publicationAttempts).toBe(1));
    await waitFor(() => {
      const cache = JSON.parse(localStorage.getItem(deckCacheKey(cacheScope, deck.id)) ?? "null") as { results: unknown; failures: number; lastSuccessfulAt: string | null; pendingNotifications: readonly { key: string; kind: string; body: string }[]; transitionKeys: readonly string[] };
      expect(cache.results).toEqual([updated]);
      expect(cache.failures).toBe(0);
      expect(cache.lastSuccessfulAt).not.toBeNull();
      expect(cache.transitionKeys).toEqual([]);
      expect(cache.pendingNotifications).toEqual([{ key: `${pullRequest.nodeId}:review:approved:${updated.updatedAt}`, kind: "review", body: updated.title }]);
    });

    fireEvent.click(screen.getByRole("button", { name: messages.en.deckRefresh }));
    await waitFor(() => expect(publicationAttempts).toBe(2));
    await waitFor(() => {
      const cache = JSON.parse(localStorage.getItem(deckCacheKey(cacheScope, deck.id)) ?? "null") as { pendingNotifications: readonly unknown[]; transitionKeys: readonly string[] };
      expect(cache.transitionKeys).toEqual([`${pullRequest.nodeId}:review:approved:${updated.updatedAt}`]);
      expect(cache.pendingNotifications).toEqual([]);
    });
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

  it("removes the previous profile-scoped cache when a synchronized Deck changes profile", async () => {
    const nextProfile = { id: "018f47a2-7b3c-7def-8abc-1234567890ad", name: "Personal", kind: "fine-grained" as const };
    const previousScope = `origin.scope.${profile.id}`;
    writeDeckCache(localStorage, previousScope, { version: DeckCacheVersion, deckId: deck.id, query: deck.query, queryEtag: null, results: [], lastSuccessfulAt: null, rate: null, failures: 0, nextRefreshAt: null, transitionKeys: [] });
    const bridge = bridgeWith(async () => ({ kind: "ok" as const }));
    const view = render(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);
    await waitFor(() => expect(localStorage.getItem(deckCacheKey(previousScope, deck.id))).not.toBeNull());

    identity = identityWith({ settings: parseDevHudSettings({ ...settings, github: { ...settings.github, profiles: [profile, nextProfile] }, decks: [{ ...deck, profileRef: nextProfile.id }] }) });
    view.rerender(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    await waitFor(() => expect(localStorage.getItem(deckCacheKey(previousScope, deck.id))).toBeNull());
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

  it("prevents an in-flight refresh from writing after synchronous account-deletion cancellation", async () => {
    let rejectSearch: (error: Error) => void = () => {};
    const searchPullRequests = vi.fn(() => new Promise<never>((_resolve, reject) => { rejectSearch = reject; }));
    const bridge = bridgeWith(async (request) => request.operation === "secure.read" ? { kind: "secure-value", value: "token" } : { kind: "ok" });
    const setCache = vi.spyOn(Storage.prototype, "setItem");
    render(<DeckPollingBoundary bridge={bridge} active online provider={{ ...provider(), searchPullRequests }}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);
    await waitFor(() => expect(searchPullRequests).toHaveBeenCalledOnce());

    invalidateDeckPolling();
    rejectSearch(new Error("offline"));
    await new Promise((resolve) => setTimeout(resolve, 10));

    expect(setCache).not.toHaveBeenCalledWith(expect.stringContaining(deck.id), expect.any(String));
  });

  it("removes queued notifications disabled by updated Deck preferences", async () => {
    const cacheScope = `origin.scope.${profile.id}`;
    const pendingNotifications = [{ key: "PR_kwDOA:review:approved:2026-08-18T00:01:00.000Z", kind: "review" as const, body: pullRequest.title }];
    writeDeckCache(localStorage, cacheScope, { version: DeckCacheVersion, deckId: deck.id, query: deck.query, queryEtag: null, results: [pullRequest], lastSuccessfulAt: "2026-08-17T00:00:00.000Z", rate: null, failures: 0, nextRefreshAt: null, transitionKeys: [], pendingNotifications });
    const bridge = bridgeWith(async () => ({ kind: "ok" as const }));
    const view = render(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);
    await waitFor(() => expect(localStorage.getItem(deckCacheKey(cacheScope, deck.id))).not.toBeNull());

    identity = identityWith({ settings: parseDevHudSettings({ ...settings, decks: [{ ...deck, notifications: ["merged" as const] }] }) });
    view.rerender(<DeckPollingBoundary bridge={bridge} active={false} online provider={provider()}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    await waitFor(() => {
      const cache = JSON.parse(localStorage.getItem(deckCacheKey(cacheScope, deck.id)) ?? "null") as { pendingNotifications: readonly unknown[] };
      expect(cache.pendingNotifications).toEqual([]);
    });
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

  it("keeps the Deck schedule when unrelated settings change", async () => {
    const scope = Promise.resolve("origin.scope");
    const validateRepository = provider().validateRepository;
    const searchPullRequests = vi.fn(async () => ({ items: [], nextPage: null, notModified: false, totalCount: 0, incompleteResults: false, metadata: { etag: null, rate: { limit: null, remaining: null, used: null, resetAt: null, resource: null, retryAfterSeconds: null } } }));
    const bridge = bridgeWith(async (request) => request.operation === "secure.read" ? { kind: "secure-value", value: "token" } : { kind: "ok" });
    identity = identityWith({ githubPatScopeId: scope });
    const suppliedProvider = { ...provider(), validateRepository, searchPullRequests };
    const view = render(<DeckPollingBoundary bridge={bridge} active online provider={suppliedProvider}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);

    await waitFor(() => expect(searchPullRequests).toHaveBeenCalledOnce());
    expect(validateRepository).toHaveBeenCalledOnce();

    identity = identityWith({ githubPatScopeId: scope, settings: parseDevHudSettings({ ...settings, appearance: { ...settings.appearance, theme: "dark" } }) });
    view.rerender(<DeckPollingBoundary bridge={bridge} active online provider={suppliedProvider}><DeckSurface copy={messages.en} bridge={bridge} /></DeckPollingBoundary>);
    await new Promise((resolve) => setTimeout(resolve, 10));

    expect(validateRepository).toHaveBeenCalledOnce();
    expect(searchPullRequests).toHaveBeenCalledOnce();
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
