// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport, type Transport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { useState, type ComponentProps } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  EntityKind,
  InboxService,
  NotificationPreferencesSchema,
  ResourceSchema,
  ResourceService,
  SessionService,
  SystemService,
  newRequestId,
  type Resource,
} from "@delinoio/delidev-api-client";
import { App } from "./App";
import { encode } from "./documents";
import {
  dispatchAnimation,
  reducedMotion,
  setAnimation,
} from "./entrance-test-support";
import { MutationIntents } from "./mutation";
import { Sidebar } from "./sidebar";
import { Surface } from "./views";

function row(
  kind: EntityKind,
  name: string,
  projectId = "",
  values: Record<string, unknown> = {},
): Resource {
  const id = newRequestId();
  return create(ResourceSchema, {
    id,
    sessionId: kind === EntityKind.SESSION ? id : "",
    projectId,
    kind,
    revision: 1n,
    schemaVersion: 1,
    documentJson: encode({ name, ...values }),
  });
}

const project = row(EntityKind.PROJECT, "Entrance project");
const projectSession = row(EntityKind.SESSION, "Entrance session", project.id, {
  workspace: "worktree",
  outcome: "succeeded",
  archive: "active",
});

function transport(): Transport {
  return createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({ version: "0.1.0", protocolVersion: 1 }),
    });
    router.service(ResourceService, {
      listResources: (request) =>
        request.filter?.kind === EntityKind.PROJECT
          ? { resources: [project] }
          : { resources: [] },
    });
    router.service(SessionService, {
      listSessions: (request) => ({
        sessions: request.projectId === project.id ? [projectSession] : [],
      }),
    });
    router.service(InboxService, {
      listInbox: () => ({ entries: [] }),
      getNotificationPreferences: () => ({
        preferences: create(NotificationPreferencesSchema, { revision: 1n }),
      }),
    });
  });
}

function mountSidebar() {
  let selectSurface!: (surface: Surface) => void;
  let setSelected!: (id: string) => void;
  function Harness() {
    const [surface, setSurface] = useState(Surface.Sessions);
    const [selectedSessionId, select] = useState("");
    selectSurface = setSurface;
    setSelected = select;
    const props: ComponentProps<typeof Sidebar> = {
      surface,
      selectedSessionId,
      navigate: vi.fn(),
      openSession: vi.fn(),
      newSession: vi.fn(),
      newGeneralChat: vi.fn(),
      newProject: vi.fn(),
      openSettings: vi.fn(),
    };
    return <Sidebar {...props} />;
  }
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } },
  });
  const view = render(
    <TransportProvider transport={transport()}>
      <QueryClientProvider client={client}>
        <MutationIntents>
          <Harness />
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>,
  );
  return {
    ...view,
    client,
    setSurface: (surface: Surface) => act(() => selectSurface(surface)),
    select: (id: string) => act(() => setSelected(id)),
  };
}

// The wrapper around the project groups, inside the sidebar list scroll root.
const homeGroups = () =>
  document
    .querySelector<HTMLElement>(".sidebar-projects-heading")!
    .closest<HTMLElement>(".sidebar-list > div")!;
const homeEntering = () => homeGroups().getAttribute("data-entrance");
const welcome = () => document.querySelector<HTMLElement>("section.welcome")!;

beforeEach(() => {
  reducedMotion(false);
});
afterEach(() => {
  vi.unstubAllGlobals();
  delete document.documentElement.dataset.animation;
});

it("plays one Y4 entrance per Home activation on the project groups and never on the scroll root, dialog or rail", async () => {
  const value = mountSidebar();
  await screen.findByRole("button", { name: "Entrance project" });
  // Startup counts as the first Home activation.
  expect(homeEntering()).toBe("y4");
  const wrapper = homeGroups();
  expect(wrapper.hasAttribute("style")).toBe(false);
  // Neither the scroll root, the pane nor the native dialog host animates.
  for (
    let node: Element | null = wrapper.parentElement;
    node;
    node = node.parentElement
  )
    expect(node.hasAttribute("data-entrance")).toBe(false);
  dispatchAnimation(wrapper);
  expect(homeEntering()).toBeNull();

  // Expanding a group, loading its rows, archive filtering, selecting a session
  // and switching Home to New session never replay and keep the same controller.
  const group = screen.getByRole("button", { name: "Entrance project" });
  fireEvent.click(group);
  await screen.findByRole("button", { name: /Entrance session/ });
  fireEvent.click(
    screen.getByRole("button", { name: "Project and conversation options" }),
  );
  fireEvent.click(screen.getByRole("checkbox", { name: "Include archived" }));
  expect(homeEntering()).toBeNull();
  value.select(projectSession.id);
  expect(homeEntering()).toBeNull();
  value.setSurface(Surface.NewSession);
  expect(homeEntering()).toBeNull();
  value.setSurface(Surface.Sessions);
  expect(homeEntering()).toBeNull();
  await act(async () => {
    await value.client.invalidateQueries();
  });
  expect(homeEntering()).toBeNull();
  expect(homeGroups()).toBe(wrapper);
  expect(screen.getByRole("button", { name: "Entrance project" })).toBe(group);
  expect(group.getAttribute("aria-expanded")).toBe("true");

  // Leaving Home and returning replays exactly once per return.
  value.setSurface(Surface.PullRequests);
  expect(homeEntering()).toBeNull();
  value.setSurface(Surface.Sessions);
  expect(homeEntering()).toBe("y4");
  dispatchAnimation(wrapper);
  expect(homeEntering()).toBeNull();
});

it("replaces an in-flight Home entrance on rapid leave and re-entry", async () => {
  const value = mountSidebar();
  await screen.findByRole("button", { name: "Entrance project" });
  expect(homeEntering()).toBe("y4");
  value.setSurface(Surface.Usage);
  value.setSurface(Surface.Sessions);
  expect(homeEntering()).toBe("y4");
  dispatchAnimation(homeGroups());
  expect(homeEntering()).toBeNull();
});

it("suppresses Home motion under Animation Off and reduced motion and never replays on re-enable", async () => {
  const setReduced = reducedMotion(false);
  const value = mountSidebar();
  await screen.findByRole("button", { name: "Entrance project" });
  expect(homeEntering()).toBe("y4");
  await setAnimation("false");
  expect(homeEntering()).toBeNull();
  await setAnimation(undefined);
  expect(homeEntering()).toBeNull();

  setReduced(true);
  value.setSurface(Surface.Usage);
  value.setSurface(Surface.Sessions);
  expect(homeEntering()).toBeNull();
  setReduced(false);
  expect(homeEntering()).toBeNull();

  // A new return after the preference is back plays again.
  value.setSurface(Surface.Usage);
  value.setSurface(Surface.Sessions);
  expect(homeEntering()).toBe("y4");
  setReduced(true);
  expect(homeEntering()).toBeNull();
  setReduced(false);
  expect(homeEntering()).toBeNull();
});

it("plays the welcome entrance once per Home activation and never for reads", async () => {
  render(<App transport={transport()} />);
  await screen.findByRole("heading", { name: "Your sessions, in one place" });
  expect(welcome().getAttribute("data-entrance")).toBe("y4");
  expect(welcome().hasAttribute("style")).toBe(false);
  dispatchAnimation(welcome());
  expect(welcome().getAttribute("data-entrance")).toBeNull();

  // Visiting another destination does not animate the hidden welcome content.
  fireEvent.click(await screen.findByRole("button", { name: "Pull requests" }));
  await waitFor(() =>
    expect(document.querySelector("section.pull-requests-page")).not.toBeNull(),
  );
  expect(welcome().getAttribute("data-entrance")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Sessions" }));
  expect(welcome().getAttribute("data-entrance")).toBe("y4");
  // Animation Off settles the in-flight entrance without a replay.
  await setAnimation("false");
  expect(welcome().getAttribute("data-entrance")).toBeNull();
  await setAnimation(undefined);
  expect(welcome().getAttribute("data-entrance")).toBeNull();
});

it("settles the Home entrance before the options dialog opens", async () => {
  mountSidebar();
  await screen.findByRole("button", { name: "Entrance project" });
  expect(homeEntering()).toBe("y4");
  fireEvent.click(
    screen.getByRole("button", { name: "Project and conversation options" }),
  );
  expect(screen.getByRole("dialog")).toBeTruthy();
  expect(homeEntering()).toBeNull();
});

it("settles the Home entrance before positioning the New project tooltip", async () => {
  mountSidebar();
  await screen.findByRole("button", { name: "Entrance project" });
  expect(homeEntering()).toBe("y4");
  const button = screen.getByRole("button", { name: "New project" });
  fireEvent.pointerEnter(button);
  expect(document.querySelector(".sidebar-action-tooltip")).not.toBeNull();
  expect(homeEntering()).toBeNull();
});

it("settles the Home entrance before measuring a focused session hover card", async () => {
  mountSidebar();
  const group = await screen.findByRole("button", { name: "Entrance project" });
  fireEvent.click(group);
  const sessionRow = await screen.findByRole("button", {
    name: /Entrance session/,
  });
  expect(homeEntering()).toBe("y4");
  fireEvent.focus(sessionRow);
  await waitFor(() =>
    expect(document.querySelector(".sidebar-session-tooltip")).not.toBeNull(),
  );
  expect(homeEntering()).toBeNull();
});
