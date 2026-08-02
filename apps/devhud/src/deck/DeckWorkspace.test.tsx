import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import axe from "axe-core";
import { afterEach, describe, expect, it, vi } from "vitest";

import { DeckProvider } from "./DeckProvider";
import { DeckWorkspace } from "./DeckWorkspace";
import {
  DeckFailureCode,
  DeckFreshness,
  DeckGrouping,
  DeckMergeMethod,
  DeckMutationKind,
  DeckOwnerKind,
  DeckProductError,
  DeckPullRequestLifecycle,
  DeckSort,
  type DeckGateway,
  type DeckOwner,
  type DeckPullRequest,
  type DeckView,
} from "./contracts";
import { manualRefreshWarning } from "./refreshController";

const tauri = vi.hoisted(() => ({
  enabled: false,
  listeners: new Map<string, (event: { readonly payload: unknown }) => void>(),
}));

vi.mock("@tauri-apps/api/core", () => ({ isTauri: () => tauri.enabled }));
vi.mock("@tauri-apps/api/event", () => ({
  listen: vi.fn(async (
    event: string,
    listener: (event: { readonly payload: unknown }) => void,
  ) => {
    tauri.listeners.set(event, listener);
    return () => {
      if (tauri.listeners.get(event) === listener) tauri.listeners.delete(event);
    };
  }),
}));

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  tauri.enabled = false;
  tauri.listeners.clear();
  Object.defineProperty(navigator, "onLine", { configurable: true, value: true });
  window.dispatchEvent(new Event("online"));
});

const billing = {
  organizationId: "018f0000-0000-7000-8000-000000000009",
  teamId: "018f0000-0000-7000-8000-000000000010",
};
const owner: DeckOwner = {
  ownerId: "018f0000-0000-7000-8000-000000000001",
  kind: DeckOwnerKind.Personal,
  label: "Personal",
  canManage: true,
  billingSelections: [billing],
};
const organization: DeckOwner = {
  ownerId: "018f0000-0000-7000-8000-000000000009",
  kind: DeckOwnerKind.Organization,
  label: "Deli",
  canManage: true,
  billingSelections: [billing],
};
const view: DeckView = {
  viewId: "018f0000-0000-7000-8000-000000000002",
  owner,
  billing,
  name: "Needs review",
  rawQuery: "review-requested:@me custom:future",
  sort: DeckSort.RecentlyUpdated,
  grouping: DeckGrouping.Repository,
  notificationPreference: { enabled: false, transitions: [] },
  connection: "connected",
  revision: { value: 1n, etag: "view-1" },
  widgetAttached: false,
};
const pullRequest: DeckPullRequest = {
  repositoryOwner: "delinoio",
  repositoryName: "oss",
  number: 755,
  title: "Add Deck",
  author: "octocat",
  reviewDecision: "required",
  checks: "success",
  mergeability: "mergeable",
  isDraft: false,
  lifecycle: DeckPullRequestLifecycle.Open,
  updatedAt: "2026-08-02T00:00:00.000Z",
  revision: { value: 3n, etag: "pr-3" },
  reviewers: [{ kind: "team", label: "delinoio/reviewers" }],
  assignees: ["octocat"],
  labels: ["internal"],
  supportedMutations: Object.values(DeckMutationKind),
  mergeMethods: Object.values(DeckMergeMethod),
};

function gateway(overrides: Partial<DeckGateway> = {}): DeckGateway {
  return {
    listOwners: vi.fn(async () => [owner]),
    listViews: vi.fn(async () => ({ items: [view], nextCursor: "" })),
    createView: vi.fn(async (_owner, input) => ({ ...view, ...input })),
    updateView: vi.fn(async (current, input) => ({ ...current, ...input, revision: { value: 2n, etag: "view-2" } })),
    deleteView: vi.fn(async () => undefined),
    getView: vi.fn(async () => view),
    listPullRequests: vi.fn(async () => ({ items: [pullRequest], nextCursor: "", freshness: DeckFreshness.Fresh, truncated: true, resultLimit: 500 })),
    listMutationCandidates: vi.fn(async () => ({ items: [{ kind: "user" as const, value: "hubot" }], nextCursor: "", pullRequestRevision: pullRequest.revision })),
    mutatePullRequest: vi.fn(async () => ({ pullRequest, refreshRequired: false })),
    refreshAfterMutation: vi.fn(async () => undefined),
    refreshView: vi.fn(async (_viewId, confirm) => {
      if (!await confirm(manualRefreshWarning(50n))) return;
    }),
    openPullRequest: vi.fn(async () => undefined),
    recordViewOpened: vi.fn(),
    synchronizeShortcuts: vi.fn(async () => undefined),
    clearShortcuts: vi.fn(async () => undefined),
    startEligibleRefreshes: vi.fn(() => () => undefined),
    ...overrides,
  };
}

function renderDeck(value: DeckGateway) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0, networkMode: "always" } },
  });
  return {
    ...render(
      <QueryClientProvider client={queryClient}>
        <DeckProvider gateway={value}>
          <DeckWorkspace />
        </DeckProvider>
      </QueryClientProvider>,
    ),
    queryClient,
  };
}

describe("Deck composable production workspace", () => {
  it("synchronizes account shortcuts for an empty view page and clears them only on teardown", async () => {
    const synchronizeShortcuts = vi.fn(async () => undefined);
    const clearShortcuts = vi.fn(async () => undefined);
    const backend = gateway({
      listViews: vi.fn(async () => ({ items: [], nextCursor: "" })),
      synchronizeShortcuts,
      clearShortcuts,
    });

    const rendered = renderDeck(backend);

    await waitFor(() => expect(backend.listViews).toHaveBeenCalledOnce());
    await waitFor(() => expect(synchronizeShortcuts).toHaveBeenCalled());
    expect(clearShortcuts).not.toHaveBeenCalled();

    rendered.unmount();
    expect(clearShortcuts).toHaveBeenCalledOnce();
  });

  it("navigates explicit personal and organization ownership scopes", async () => {
    const backend = gateway({ listOwners: vi.fn(async () => [owner, organization]) });
    const user = userEvent.setup();
    renderDeck(backend);
    expect(await screen.findByRole("button", { name: /Personal/u })).toHaveAttribute("aria-current", "page");
    await user.click(screen.getByRole("button", { name: /Deli/u }));
    await waitFor(() => expect(backend.listViews).toHaveBeenCalledWith(organization, ""));
  });

  it("leaves edit mode when the selected owner clears the selected view", async () => {
    const backend = gateway({
      listOwners: vi.fn(async () => [owner, organization]),
      listViews: vi.fn(async (selected) => ({
        items: selected.ownerId === owner.ownerId ? [view] : [],
        nextCursor: "",
      })),
    });
    const user = userEvent.setup();
    renderDeck(backend);
    await user.click(await screen.findByRole("button", { name: /Needs review/u }));
    await user.click(screen.getByRole("button", { name: "Edit view" }));
    expect(screen.getByRole("heading", { name: "Edit Needs review" })).toBeVisible();

    await user.click(screen.getByRole("button", { name: /Deli/u }));

    expect(await screen.findByRole("button", { name: "New view" })).toBeVisible();
    expect(screen.queryByRole("heading", { name: "Edit Needs review" })).not.toBeInTheDocument();
  });

  it("replaces a selected owner removed by an authoritative refetch", async () => {
    const listOwners = vi.fn()
      .mockResolvedValueOnce([owner, organization])
      .mockResolvedValue([organization]);
    const backend = gateway({ listOwners });
    const { queryClient } = renderDeck(backend);
    expect(await screen.findByRole("button", { name: /Personal/u })).toHaveAttribute(
      "aria-current",
      "page",
    );

    await queryClient.invalidateQueries();

    await waitFor(() => expect(
      screen.getByRole("button", { name: /Deli/u }),
    ).toHaveAttribute("aria-current", "page"));
    await waitFor(() => expect(backend.listViews).toHaveBeenCalledWith(organization, ""));
  });

  it("resolves synchronized shortcuts outside the loaded owner rows", async () => {
    tauri.enabled = true;
    const shortcutView = {
      ...view,
      viewId: "018f0000-0000-7000-8000-000000000012",
      owner: organization,
      name: "Organization shortcut",
    };
    const backend = gateway({
      getView: vi.fn(async () => shortcutView),
      listOwners: vi.fn(async () => [owner, organization]),
      listViews: vi.fn(async (selected) => selected.ownerId === owner.ownerId
        ? { items: [view], nextCursor: "" }
        : { items: [], nextCursor: "next" }),
    });
    renderDeck(backend);
    await waitFor(() => expect(tauri.listeners.has("devhud://deck-shortcut")).toBe(true));

    tauri.listeners.get("devhud://deck-shortcut")?.({ payload: shortcutView.viewId });

    await waitFor(() => expect(backend.getView).toHaveBeenCalledWith(shortcutView.viewId));
    expect(await screen.findByRole("button", { name: /Deli/u })).toHaveAttribute(
      "aria-current",
      "page",
    );
    await waitFor(() => expect(backend.listPullRequests).toHaveBeenCalledWith(
      shortcutView.viewId,
      "",
    ));
  });

  it("loads permission-filtered rows, exposes truncation, and hands comments to GitHub", async () => {
    const backend = gateway();
    const user = userEvent.setup();
    renderDeck(backend);
    await user.click(await screen.findByRole("button", { name: /Needs review/u }));
    expect(await screen.findByRole("heading", { name: "Add Deck" })).toBeVisible();
    expect(screen.getByText(/truncated at 500/u)).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Comment or review on GitHub" }));
    expect(backend.openPullRequest).toHaveBeenCalledWith(pullRequest);
  });

  it("requires explicit merge and billed-refresh confirmations", async () => {
    const backend = gateway();
    const user = userEvent.setup();
    renderDeck(backend);
    await user.click(await screen.findByRole("button", { name: /Needs review/u }));
    await screen.findByRole("heading", { name: "Add Deck" });

    await user.click(screen.getByRole("button", { name: "merge" }));
    expect(screen.getByRole("dialog", { name: "Confirm pull request merge" })).toBeVisible();
    expect(backend.mutatePullRequest).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Confirm merge" }));
    await waitFor(() => expect(backend.mutatePullRequest).toHaveBeenCalledWith(
      view.viewId,
      pullRequest,
      expect.objectContaining({ kind: DeckMutationKind.Merge, mergeConfirmed: true }),
    ));

    await user.click(screen.getByRole("button", { name: "Refresh" }));
    expect(await screen.findByRole("dialog", { name: "Confirm billed refresh" })).toHaveTextContent("$0.000050 USD");
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(backend.refreshView).toHaveBeenCalledTimes(1);
  });

  it("disables merge confirmation while the provider mutation is pending", async () => {
    let completeMutation: ((result: {
      pullRequest: DeckPullRequest;
      refreshRequired: false;
    }) => void) | undefined;
    const mutatePullRequest = vi.fn(() => new Promise<{
      pullRequest: DeckPullRequest;
      refreshRequired: false;
    }>((resolve) => {
      completeMutation = resolve;
    }));
    const backend = gateway({ mutatePullRequest });
    const user = userEvent.setup();
    renderDeck(backend);

    await user.click(await screen.findByRole("button", { name: /Needs review/u }));
    await user.click(await screen.findByRole("button", { name: "merge" }));
    const confirm = screen.getByRole("button", { name: "Confirm merge" });
    await user.click(confirm);

    await waitFor(() => expect(confirm).toBeDisabled());
    await user.click(confirm);
    expect(mutatePullRequest).toHaveBeenCalledOnce();

    completeMutation?.({ pullRequest, refreshRequired: false });
    await waitFor(() => expect(
      screen.queryByRole("dialog", { name: "Confirm pull request merge" }),
    ).not.toBeInTheDocument());
  });

  it("disables picker apply while a provider mutation is pending", async () => {
    let completeMutation: ((result: {
      pullRequest: DeckPullRequest;
      refreshRequired: false;
    }) => void) | undefined;
    const mutatePullRequest = vi.fn(() => new Promise<{
      pullRequest: DeckPullRequest;
      refreshRequired: false;
    }>((resolve) => {
      completeMutation = resolve;
    }));
    const backend = gateway({ mutatePullRequest });
    const user = userEvent.setup();
    renderDeck(backend);

    await user.click(await screen.findByRole("button", { name: /Needs review/u }));
    await user.click(await screen.findByRole("button", { name: "remove labels" }));
    await user.click(screen.getByRole("checkbox", { name: /internal/u }));
    const apply = screen.getByRole("button", { name: "Apply" });
    await user.click(apply);

    await waitFor(() => expect(apply).toBeDisabled());
    await user.click(apply);
    expect(mutatePullRequest).toHaveBeenCalledOnce();

    completeMutation?.({ pullRequest, refreshRequired: false });
    await waitFor(() => expect(
      screen.queryByRole("button", { name: "Apply" }),
    ).not.toBeInTheDocument());
  });

  it("applies synchronized mutation detail to the visible pull request", async () => {
    const updatedPullRequest = {
      ...pullRequest,
      title: "Updated after mutation",
      revision: { value: 4n, etag: "pr-4" },
    };
    const backend = gateway({
      mutatePullRequest: vi.fn(async () => ({
        pullRequest: updatedPullRequest,
        refreshRequired: false,
      })),
    });
    const user = userEvent.setup();
    renderDeck(backend);

    await user.click(await screen.findByRole("button", { name: /Needs review/u }));
    await user.click(await screen.findByRole("button", { name: "close" }));

    expect(await screen.findByRole("heading", { name: "Updated after mutation" })).toBeVisible();
    expect(backend.listPullRequests).toHaveBeenCalledOnce();
  });

  it("renders only server-advertised actions", async () => {
    const restrictedPullRequest = {
      ...pullRequest,
      supportedMutations: [DeckMutationKind.MarkDraft, DeckMutationKind.Close],
    };
    const backend = gateway({
      listPullRequests: vi.fn(async () => ({
        items: [restrictedPullRequest],
        nextCursor: "",
        freshness: DeckFreshness.Fresh,
        truncated: false,
        resultLimit: 500,
      })),
    });
    const user = userEvent.setup();
    renderDeck(backend);
    await user.click(await screen.findByRole("button", { name: /Needs review/u }));
    await screen.findByRole("heading", { name: "Add Deck" });

    expect(screen.getByRole("button", { name: "mark draft" })).toBeVisible();
    expect(screen.getByRole("button", { name: "close" })).toBeVisible();
    expect(screen.queryByRole("button", { name: "merge" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "assign users" })).not.toBeInTheDocument();
  });

  it("refreshes the view after an accepted mutation reload failure", async () => {
    const backend = gateway({
      mutatePullRequest: vi.fn(async () => ({ refreshRequired: true })),
    });
    const user = userEvent.setup();
    renderDeck(backend);
    await user.click(await screen.findByRole("button", { name: /Needs review/u }));
    await user.click(await screen.findByRole("button", { name: "close" }));
    await waitFor(() => expect(backend.refreshAfterMutation).toHaveBeenCalledWith(view.viewId));
    expect(backend.mutatePullRequest).toHaveBeenCalledTimes(1);
  });

  it("lets the user remove only selected current labels", async () => {
    const backend = gateway({
      listPullRequests: vi.fn(async () => ({
        items: [{ ...pullRequest, labels: ["internal", "ready"] }],
        nextCursor: "",
        freshness: DeckFreshness.Fresh,
        truncated: false,
        resultLimit: 500,
      })),
    });
    const user = userEvent.setup();
    renderDeck(backend);
    await user.click(await screen.findByRole("button", { name: /Needs review/u }));
    await user.click(await screen.findByRole("button", { name: "remove labels" }));
    await user.click(screen.getByRole("checkbox", { name: /internal/u }));
    await user.click(screen.getByRole("button", { name: "Apply" }));
    await waitFor(() => expect(backend.mutatePullRequest).toHaveBeenCalledWith(
      view.viewId,
      expect.anything(),
      expect.objectContaining({ kind: DeckMutationKind.RemoveLabels, labels: ["internal"] }),
    ));
  });

  it("loads later mutation candidate pages", async () => {
    const listMutationCandidates = vi.fn()
      .mockResolvedValueOnce({ items: [{ kind: "user", value: "hubot" }], nextCursor: "next", pullRequestRevision: pullRequest.revision })
      .mockResolvedValueOnce({ items: [{ kind: "user", value: "octocat" }], nextCursor: "", pullRequestRevision: pullRequest.revision });
    const backend = gateway({ listMutationCandidates });
    const user = userEvent.setup();
    renderDeck(backend);
    await user.click(await screen.findByRole("button", { name: /Needs review/u }));
    await user.click(await screen.findByRole("button", { name: "assign users" }));
    await user.click(screen.getByRole("button", { name: "Search" }));
    await user.click(await screen.findByRole("button", { name: "Load more candidates" }));
    expect(await screen.findByRole("checkbox", { name: /octocat user/ })).toBeVisible();
    expect(listMutationCandidates).toHaveBeenLastCalledWith(
      view.viewId,
      expect.anything(),
      DeckMutationKind.AssignUsers,
      "",
      "next",
    );
  });

  it("uses the synchronized candidate revision and clears replaced selections", async () => {
    const synchronizedRevision = { value: 9n, etag: "pr-9" };
    const listMutationCandidates = vi.fn()
      .mockResolvedValueOnce({
        items: [{ kind: "user", value: "hubot" }],
        nextCursor: "",
        pullRequestRevision: synchronizedRevision,
      })
      .mockResolvedValueOnce({
        items: [{ kind: "user", value: "octocat" }],
        nextCursor: "",
        pullRequestRevision: synchronizedRevision,
      });
    const backend = gateway({ listMutationCandidates });
    const user = userEvent.setup();
    renderDeck(backend);
    await user.click(await screen.findByRole("button", { name: /Needs review/u }));
    await user.click(await screen.findByRole("button", { name: "assign users" }));
    await user.click(screen.getByRole("button", { name: "Search" }));
    await user.click(await screen.findByRole("checkbox", { name: /hubot user/u }));
    await user.click(screen.getByRole("button", { name: "Search" }));

    expect(await screen.findByRole("checkbox", { name: /octocat user/u })).not.toBeChecked();
    expect(screen.getByRole("button", { name: "Apply" })).toBeDisabled();
    await user.click(screen.getByRole("checkbox", { name: /octocat user/u }));
    await user.click(screen.getByRole("button", { name: "Apply" }));

    await waitFor(() => expect(backend.mutatePullRequest).toHaveBeenCalledWith(
      view.viewId,
      expect.objectContaining({ revision: synchronizedRevision }),
      expect.objectContaining({ users: ["octocat"] }),
    ));
  });

  it("surfaces mutation candidate search failures through Deck operation state", async () => {
    const backend = gateway({
      listMutationCandidates: vi.fn(async () => {
        throw new DeckProductError(DeckFailureCode.ProviderRateLimited, 30);
      }),
    });
    const user = userEvent.setup();
    renderDeck(backend);
    await user.click(await screen.findByRole("button", { name: /Needs review/u }));
    await user.click(await screen.findByRole("button", { name: "assign users" }));
    await user.click(screen.getByRole("button", { name: "Search" }));

    expect(await screen.findByRole("heading", { name: "Pull requests unavailable" })).toBeVisible();
    expect(screen.getByRole("alert")).toHaveTextContent("Retry in 30 seconds");
  });

  it("invalidates pull requests after an automatic refresh completes", async () => {
    let onRefreshed: ((viewId: string) => void) | undefined;
    const backend = gateway({
      startEligibleRefreshes: vi.fn((_views, callback) => {
        onRefreshed = callback;
        return () => undefined;
      }),
    });
    const user = userEvent.setup();
    renderDeck(backend);
    await user.click(await screen.findByRole("button", { name: /Needs review/u }));
    await screen.findByRole("heading", { name: "Add Deck" });
    expect(backend.listPullRequests).toHaveBeenCalledTimes(1);

    onRefreshed?.(view.viewId);

    await waitFor(() => expect(backend.listPullRequests).toHaveBeenCalledTimes(2));
  });

  it("replaces the selected view with its refetched server row", async () => {
    const disconnectedView = {
      ...view,
      connection: "reauthentication-required" as const,
      name: "Needs review (reauthentication required)",
      revision: { value: 2n, etag: "view-2" },
    };
    const backend = gateway({
      listViews: vi.fn()
        .mockResolvedValueOnce({ items: [view], nextCursor: "" })
        .mockResolvedValue({ items: [disconnectedView], nextCursor: "" }),
      listPullRequests: vi.fn()
        .mockRejectedValueOnce(new DeckProductError(DeckFailureCode.ServiceUnavailable))
        .mockResolvedValue({ items: [pullRequest], nextCursor: "", freshness: DeckFreshness.Fresh, truncated: false, resultLimit: 500 }),
    });
    const user = userEvent.setup();
    renderDeck(backend);
    await user.click(await screen.findByRole("button", { name: /Needs review/u }));
    await user.click(await screen.findByRole("button", { name: "Try again" }));

    expect(await screen.findByRole("heading", { name: "GitHub connection required" })).toBeVisible();
    expect(screen.getByRole("button", { name: /reauthentication required/u })).toHaveAttribute("aria-current", "page");
  });

  it("surfaces owner-loading failures before the empty selection state", async () => {
    renderDeck(gateway({
      listOwners: vi.fn(async () => {
        throw new DeckProductError(DeckFailureCode.ServiceUnavailable);
      }),
    }));
    expect(await screen.findByRole("heading", { name: "Pull requests unavailable" })).toBeVisible();
    expect(screen.queryByRole("heading", { name: "Select a view" })).not.toBeInTheDocument();
  });

  it("retries only queries whose selection prerequisites are available", async () => {
    const listOwners = vi.fn()
      .mockRejectedValueOnce(new DeckProductError(DeckFailureCode.ServiceUnavailable))
      .mockResolvedValue([owner]);
    const listViews = vi.fn(async () => ({ items: [view], nextCursor: "" }));
    const backend = gateway({ listOwners, listViews });
    const user = userEvent.setup();
    renderDeck(backend);
    await screen.findByRole("heading", { name: "Pull requests unavailable" });
    expect(listViews).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "Try again" }));

    await waitFor(() => expect(listViews).toHaveBeenCalledOnce());
    expect(listViews).toHaveBeenCalledWith(owner, "");
  });

  it("compares and reapplies edits after a revision conflict", async () => {
    const current = {
      ...view,
      name: "Changed remotely",
      rawQuery: "author:hubot",
      revision: { value: 2n, etag: "view-2" },
    };
    const updateView = vi.fn()
      .mockRejectedValueOnce(new DeckProductError(DeckFailureCode.StaleRevision))
      .mockImplementationOnce(async (base: DeckView, input: Parameters<DeckGateway["updateView"]>[1]) => ({
        ...base,
        ...input,
        revision: { value: 3n, etag: "view-3" },
      }));
    const backend = gateway({
      getView: vi.fn(async () => current),
      updateView,
    });
    const user = userEvent.setup();
    renderDeck(backend);
    await user.click(await screen.findByRole("button", { name: /Needs review/u }));
    await user.click(screen.getByRole("button", { name: "Edit view" }));
    expect(screen.queryByRole("checkbox", { name: "Notify me when this view changes" }))
      .not.toBeInTheDocument();
    const name = screen.getByLabelText("View name");
    await user.clear(name);
    await user.type(name, "My reapplied edit");
    await user.click(screen.getByRole("button", { name: "Save view" }));

    const conflict = await screen.findByRole("dialog", { name: "View changed elsewhere" });
    expect(conflict).toHaveTextContent("My reapplied edit");
    expect(conflict).toHaveTextContent("Changed remotely");
    await user.click(screen.getByRole("button", { name: "Reapply my changes" }));
    await waitFor(() => expect(updateView).toHaveBeenLastCalledWith(
      current,
      expect.objectContaining({ name: "My reapplied edit" }),
    ));
  });

  it("never renders the regular PR list offline and has no automated accessibility violations", async () => {
    const backend = gateway();
    const user = userEvent.setup();
    const { container } = renderDeck(backend);
    await user.click(await screen.findByRole("button", { name: /Needs review/u }));
    await screen.findByRole("heading", { name: "Add Deck" });
    Object.defineProperty(navigator, "onLine", { configurable: true, value: false });
    window.dispatchEvent(new Event("offline"));
    expect(await screen.findByRole("heading", { name: "Deck is offline" })).toBeVisible();
    expect(screen.queryByRole("heading", { name: "Add Deck" })).not.toBeInTheDocument();
    const results = await axe.run(container, { rules: { "color-contrast": { enabled: false } } });
    expect(results.violations).toEqual([]);
  });

  it("suppresses cached rows when the service reports offline freshness", async () => {
    Object.defineProperty(navigator, "onLine", { configurable: true, value: true });
    const backend = gateway({
      listPullRequests: vi.fn(async () => ({
        items: [pullRequest],
        nextCursor: "",
        freshness: DeckFreshness.Offline,
        truncated: false,
        resultLimit: 500,
      })),
    });
    const user = userEvent.setup();
    renderDeck(backend);
    await user.click(await screen.findByRole("button", { name: /Needs review/u }));
    expect(await screen.findByRole("heading", { name: "Deck service unavailable" })).toBeVisible();
    expect(screen.queryByRole("heading", { name: "Add Deck" })).not.toBeInTheDocument();
  });
});
