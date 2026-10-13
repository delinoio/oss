// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { StrictMode, type ReactNode } from "react";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  EntityKind,
  GitHubTokenIdentityState as State,
  GitHubTokenKind,
  IntegrationService,
  ResourceSchema,
  ResourceService,
  SystemCapability,
  SystemService,
  newRequestId,
  type InspectGitHubTokenRequest,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import {
  dispatchAnimation,
  reducedMotion,
  setAnimation,
  unoptedSettingsCategory,
} from "./entrance-test-support";
import { githubDraftTokenFormURL } from "./github-opening";
import { MutationIntents } from "./mutation";
import { ProductIdentityProvider } from "./product-identity-labels";
import { Settings, SettingsEntryDestination } from "./settings";

// Issue 3212: Git Profiles (SettingsCategory.Integrations) category and owned
// task entrance. Every token value below is a synthetic fixture string.
const CATEGORY = "Git Profiles";
const syntheticToken = "fixture-only-token";

const native = vi.hoisted(() => ({
  invoke: vi.fn(async (_command: string, _args: unknown) => undefined),
  isTauri: () => true,
}));
vi.mock("@tauri-apps/api/core", () => native);

function fixture() {
  native.invoke.mockClear();
  const profile = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.INTEGRATION,
    revision: 3n,
    schemaVersion: 1,
    documentJson: encode({
      name: "Work",
      provider: "github.com",
      token_kind: "fine-grained",
      resource_owner: "test-owner",
    }),
  });
  let release: (() => void) | undefined;
  let hold = false;
  const status = vi.fn(() => ({
    capabilities: [SystemCapability.GITHUB_TOKEN_ONBOARDING_V1],
  }));
  const list = vi.fn(() => ({ resources: [profile], nextPageToken: "" }));
  const inspect = vi.fn(async (request: InspectGitHubTokenRequest) => {
    if (hold)
      await new Promise<void>((resolve) => {
        release = resolve;
      });
    return {
      requestId: request.requestId,
      state: State.VERIFIED,
      identity: { id: "17", nodeId: "U_17", login: "fixture-user" },
      problemJson: new Uint8Array(),
    };
  });
  const save = vi.fn(async () => ({ profile }));
  const replace = vi.fn(async () => ({
    profile,
    problemJson: new Uint8Array(),
  }));
  const validate = vi.fn(async () => ({ profile }));
  const remove = vi.fn(async () => ({ deleted: true }));
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: status });
    router.service(ResourceService, {
      listResources: list,
      getResource: () => ({ resource: profile }),
    });
    router.service(IntegrationService, {
      inspectGitHubToken: inspect,
      saveIntegrationProfile: save,
      replaceIntegrationToken: replace,
      validateIntegrationProfile: validate,
      deleteIntegrationProfile: remove,
      getGitHubTokenForm: () => ({
        schemaVersion: 1,
        documentJson: new Uint8Array(),
      }),
      prepareGitHubTokenForm: (request) => ({
        requestId: request.requestId,
        tokenKind: request.tokenKind,
        resourceOwner: request.resourceOwner,
        access: request.access,
        url: githubDraftTokenFormURL(
          request.tokenKind === GitHubTokenKind.FINE_GRAINED
            ? "fine-grained"
            : "classic",
          request.resourceOwner,
          request.access as 1 | 2 | 3,
        )!,
      }),
    });
  });
  const client = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
      mutations: { retry: false, gcTime: 0 },
    },
  });
  const view = (children: ReactNode) => (
    <StrictMode>
      <TransportProvider transport={transport}>
        <QueryClientProvider client={client}>
          <ProductIdentityProvider>
            <MutationIntents>{children}</MutationIntents>
          </ProductIdentityProvider>
        </QueryClientProvider>
      </TransportProvider>
    </StrictMode>
  );
  const operations = () => ({
    status: status.mock.calls.length,
    list: list.mock.calls.length,
    inspect: inspect.mock.calls.length,
    save: save.mock.calls.length,
    replace: replace.mock.calls.length,
    validate: validate.mock.calls.length,
    remove: remove.mock.calls.length,
    external: native.invoke.mock.calls.length,
  });
  return {
    status,
    list,
    inspect,
    save,
    replace,
    validate,
    remove,
    client,
    view,
    operations,
    holdInspection: () => {
      hold = true;
    },
    releaseInspection: async () => {
      await act(async () => release?.());
    },
  };
}

const column = () =>
  document.querySelector<HTMLElement>(".settings-content-column > div")!;
const dialogs = () =>
  [
    ...document.querySelectorAll<HTMLDialogElement>(
      "dialog.settings-task-dialog",
    ),
  ].filter((dialog) => dialog.open);
const topDialog = () => dialogs().at(-1)!;
const taskHeader = (dialog = topDialog()) =>
  dialog.querySelector<HTMLElement>(".settings-task-header")!;
const category = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));
const cancel = (dialog = topDialog()) =>
  fireEvent(dialog, new Event("cancel", { cancelable: true }));

beforeEach(() => {
  vi.stubGlobal("matchMedia", () => ({
    matches: false,
    addEventListener: () => {},
    removeEventListener: () => {},
  }));
});
afterEach(() => {
  vi.unstubAllGlobals();
  delete document.documentElement.dataset.animation;
});

it("plays the Git Profiles category entrance once per explicit opening without replay", async () => {
  const value = fixture();
  render(value.view(<Settings />));
  category(unoptedSettingsCategory);
  expect(column().getAttribute("data-entrance")).toBeNull();
  category(CATEGORY);
  const node = column();
  expect(node.getAttribute("data-entrance")).toBe("y4");
  expect(node.querySelectorAll("[data-entrance]")).toHaveLength(0);
  dispatchAnimation(node);
  expect(node.getAttribute("data-entrance")).toBeNull();
  await screen.findByRole("button", { name: "Manage Work" });
  const operations = value.operations();
  // Refetch and re-selecting the current category never replay it.
  await act(async () => {
    await value.client.invalidateQueries();
  });
  category(CATEGORY);
  expect(column()).toBe(node);
  expect(node.getAttribute("data-entrance")).toBeNull();
  expect(value.operations()).toMatchObject({
    inspect: 0,
    save: 0,
    replace: 0,
    validate: 0,
    remove: 0,
    external: 0,
  });
  expect(operations.inspect).toBe(0);
  // Leaving and returning is a new explicit activation.
  category(unoptedSettingsCategory);
  expect(column().getAttribute("data-entrance")).toBeNull();
  category(CATEGORY);
  expect(column().getAttribute("data-entrance")).toBe("y4");
});

it("plays the entrance for a targeted Git Profiles entry", () => {
  const value = fixture();
  render(
    value.view(
      <Settings entryDestination={SettingsEntryDestination.GitProfiles} />,
    ),
  );
  expect(column().getAttribute("data-entrance")).toBe("y4");
  expect(screen.getByRole("button", { name: "New GitHub profile" })).toBeTruthy();
  dispatchAnimation(column());
  expect(column().getAttribute("data-entrance")).toBeNull();
});

it("leaves no stale Git Profiles motion when the category changes before completion", () => {
  const value = fixture();
  render(value.view(<Settings />));
  category(CATEGORY);
  const old = column();
  expect(old.getAttribute("data-entrance")).toBe("y4");
  category(unoptedSettingsCategory);
  expect(document.contains(old)).toBe(false);
  dispatchAnimation(old);
  expect(column().getAttribute("data-entrance")).toBeNull();
  category(CATEGORY);
  const fresh = column();
  expect(fresh).not.toBe(old);
  expect(fresh.getAttribute("data-entrance")).toBe("y4");
  // The stale completion of the first visit cannot settle the new one.
  dispatchAnimation(old);
  expect(fresh.getAttribute("data-entrance")).toBe("y4");
});

it("settles Git Profiles motion for Animation Off then OS reduced motion without replay", async () => {
  document.documentElement.dataset.animation = "true";
  const setReduced = reducedMotion(false);
  const value = fixture();
  render(value.view(<Settings />));
  category(CATEGORY);
  const node = column();
  expect(node.getAttribute("data-entrance")).toBe("y4");
  await setAnimation("false");
  expect(node.getAttribute("data-entrance")).toBeNull();
  await setAnimation("true");
  expect(node.getAttribute("data-entrance")).toBeNull();
  category(unoptedSettingsCategory);
  category(CATEGORY);
  expect(column().getAttribute("data-entrance")).toBe("y4");
  setReduced(true);
  expect(column().getAttribute("data-entrance")).toBeNull();
  setReduced(false);
  expect(column().getAttribute("data-entrance")).toBeNull();
  await setAnimation("false");
  category(unoptedSettingsCategory);
  category(CATEGORY);
  expect(column().getAttribute("data-entrance")).toBeNull();
});

it("animates New profile once and never replays it for the Verify and Back stages", async () => {
  const value = fixture();
  render(value.view(<Settings />));
  category(CATEGORY);
  dispatchAnimation(column());
  const opener = await screen.findByRole("button", {
    name: "New GitHub profile",
  });
  opener.focus();
  fireEvent.click(opener);
  const token = (await screen.findByLabelText(
    "GitHub personal access token",
  )) as HTMLInputElement;
  const host = topDialog();
  const header = taskHeader();
  expect(header.getAttribute("data-entrance")).toBe("y4");
  expect(host.hasAttribute("data-entrance")).toBe(false);
  expect(host.querySelectorAll("[data-entrance]")).toHaveLength(1);
  dispatchAnimation(header);
  fireEvent.change(token, { target: { value: syntheticToken } });
  fireEvent.click(screen.getByRole("button", { name: "Verify token" }));
  // Verify -> Confirm is derived from the async inspection reply.
  const name = await screen.findByLabelText("Profile name");
  expect(topDialog()).toBe(host);
  expect(taskHeader(host).getAttribute("data-entrance")).toBeNull();
  expect(host.querySelectorAll("[data-entrance]")).toHaveLength(0);
  // Confirm -> Token is the back path and never replays either.
  fireEvent.change(name, { target: { value: "Team" } });
  fireEvent.click(screen.getByRole("button", { name: "Back" }));
  expect(taskHeader(host).getAttribute("data-entrance")).toBeNull();
  expect(host.querySelectorAll("[data-entrance]")).toHaveLength(0);
  expect(value.save).not.toHaveBeenCalled();
  cancel(host);
  expect(screen.queryByRole("dialog")).toBeNull();
  await waitFor(() => expect(document.activeElement).toBe(opener));
});

it("animates Rename and Manage once each without replaying for the Delete confirmation step", async () => {
  const value = fixture();
  render(value.view(<Settings />));
  category(CATEGORY);
  dispatchAnimation(column());
  const rename = await screen.findByRole("button", { name: "Rename Work" });
  rename.focus();
  fireEvent.click(rename);
  const renameHost = topDialog();
  expect(taskHeader().getAttribute("data-entrance")).toBe("y4");
  expect(renameHost.hasAttribute("data-entrance")).toBe(false);
  dispatchAnimation(taskHeader());
  cancel();
  expect(screen.queryByRole("dialog")).toBeNull();
  // The profile editor returns focus to the unchanged New GitHub profile action.
  await waitFor(() =>
    expect(document.activeElement).toBe(
      screen.getByRole("button", { name: "New GitHub profile" }),
    ),
  );

  const manage = screen.getByRole("button", { name: "Manage Work" });
  manage.focus();
  fireEvent.click(manage);
  const manageHost = topDialog();
  expect(manageHost.getAttribute("data-size")).toBe("wide");
  expect(taskHeader().getAttribute("data-entrance")).toBe("y4");
  expect(manageHost.hasAttribute("data-entrance")).toBe(false);
  expect(manageHost.querySelectorAll("[data-entrance]")).toHaveLength(1);
  dispatchAnimation(taskHeader());
  fireEvent.click(screen.getByRole("button", { name: "Delete profile" }));
  // The confirmation is a step of the Manage window (one native dialog), so it
  // never opens another window and never replays the window entrance.
  expect(dialogs()).toEqual([manageHost]);
  expect(topDialog().querySelector("h2")?.textContent).toBe("Delete profile");
  expect(taskHeader(manageHost).getAttribute("data-entrance")).toBeNull();
  expect(manageHost.querySelectorAll("[data-entrance]")).toHaveLength(0);
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 300));
  });
  expect(value.remove).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Keep profile" }));
  expect(taskHeader(manageHost).getAttribute("data-entrance")).toBeNull();
  expect(value.remove).not.toHaveBeenCalled();
  expect(value.validate).not.toHaveBeenCalled();
  expect(value.replace).not.toHaveBeenCalled();
});

// Opens New profile, uses the existing Classic token-type shortcut, starts a
// held verification and dismisses the task while it is pending.
async function pendingVerificationScenario(animation: boolean) {
  document.documentElement.dataset.animation = String(animation);
  const value = fixture();
  value.holdInspection();
  render(value.view(<Settings />));
  category(CATEGORY);
  const opener = await screen.findByRole("button", {
    name: "New GitHub profile",
  });
  opener.focus();
  fireEvent.click(opener);
  const token = (await screen.findByLabelText(
    "GitHub personal access token",
  )) as HTMLInputElement;
  const host = topDialog();
  const observed = {
    header: taskHeader().getAttribute("data-entrance"),
    host: host.hasAttribute("data-entrance"),
    externalBeforeClick: native.invoke.mock.calls.length,
    externalAfterClick: -1,
    tokenAfterDismiss: "unset",
    tokenAfterReopen: "unset",
    focusRestored: false,
    lateReply: "unset",
  };
  if (animation) dispatchAnimation(taskHeader());
  fireEvent.click(screen.getByRole("button", { name: "Classic" }));
  await waitFor(() => expect(native.invoke).toHaveBeenCalledTimes(1));
  observed.externalAfterClick = native.invoke.mock.calls.length;
  fireEvent.change(token, { target: { value: syntheticToken } });
  fireEvent.click(screen.getByRole("button", { name: "Verify token" }));
  await waitFor(() => expect(value.inspect).toHaveBeenCalledTimes(1));
  // Dismiss while verification is pending.
  cancel(host);
  expect(screen.queryByRole("dialog")).toBeNull();
  observed.tokenAfterDismiss = token.value;
  await waitFor(() => expect(document.activeElement).toBe(opener));
  observed.focusRestored = true;
  // The late reply never advances or reopens anything.
  await value.releaseInspection();
  observed.lateReply = screen.queryByLabelText("Profile name")
    ? "advanced"
    : "discarded";
  fireEvent.click(opener);
  observed.tokenAfterReopen = (
    (await screen.findByLabelText(
      "GitHub personal access token",
    )) as HTMLInputElement
  ).value;
  cancel();
  const operations = value.operations();
  cleanup();
  return { observed, operations };
}
it("keeps the pending-verification dismissal identical to the no-motion baseline", async () => {
  const baseline = await pendingVerificationScenario(false);
  const animated = await pendingVerificationScenario(true);
  expect(baseline.observed.header).toBeNull();
  expect(animated.observed.header).toBe("y4");
  expect(animated.observed.host).toBe(false);
  // Browser opening stays click-driven: none before the shortcut click.
  expect(animated.observed.externalBeforeClick).toBe(0);
  expect(animated.observed.externalAfterClick).toBe(1);
  // The token is cleared on dismiss exactly as before and never re-appears.
  expect(animated.observed.tokenAfterDismiss).toBe("");
  expect(animated.observed.tokenAfterReopen).toBe("");
  expect(animated.observed.focusRestored).toBe(true);
  expect(animated.observed.lateReply).toBe("discarded");
  expect(animated.observed.tokenAfterDismiss).toBe(
    baseline.observed.tokenAfterDismiss,
  );
  expect(animated.observed.lateReply).toBe(baseline.observed.lateReply);
  // System status, inspection, replacement, validation, deletion and
  // external-open counts equal the no-motion baseline.
  expect(animated.operations).toEqual(baseline.operations);
  expect(animated.operations).toMatchObject({
    inspect: 1,
    save: 0,
    replace: 0,
    validate: 0,
    remove: 0,
    external: 1,
  });
});

it("never copies the token into the animated elements", async () => {
  const value = fixture();
  render(value.view(<Settings />));
  category(CATEGORY);
  fireEvent.click(
    await screen.findByRole("button", { name: "New GitHub profile" }),
  );
  const token = await screen.findByLabelText("GitHub personal access token");
  fireEvent.change(token, { target: { value: syntheticToken } });
  const animated = [...document.querySelectorAll("[data-entrance]")];
  expect(animated.length).toBeGreaterThan(0);
  for (const node of animated)
    expect(
      [...node.attributes].some((attr) => attr.value.includes(syntheticToken)),
    ).toBe(false);
  expect(token.closest("[data-entrance]")).toBeNull();
});
