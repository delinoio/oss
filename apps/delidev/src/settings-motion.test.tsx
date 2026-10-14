// SPDX-License-Identifier: Apache-2.0
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { useState, type ReactNode } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ConfigurationTransfer } from "./configuration-transfer";
import { EntranceTarget } from "./entrance-motion";
import { MutationIntents } from "./mutation";
import { Settings } from "./settings";
import { unoptedSettingsCategory } from "./entrance-test-support";
import {
  SettingsDialogSize,
  SettingsTaskDialog,
  SettingsTasks,
} from "./settings-task";
import { SettingsTaskEntranceContext } from "./settings-task-context";
import { Modal } from "./ui";

function dispatch(node: Element) {
  const event = new Event("animationend", { bubbles: true });
  Object.defineProperty(event, "animationName", { value: "entrance-y4" });
  act(() => {
    node.dispatchEvent(event);
  });
}
function providers(children: ReactNode) {
  const transport = createRouterTransport(() => {});
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <MutationIntents>{children}</MutationIntents>
      </QueryClientProvider>
    </TransportProvider>
  );
}
const column = () =>
  document.querySelector<HTMLElement>(".settings-content-column > div")!;
const heading = () =>
  document.querySelector<HTMLElement>(".settings-category-heading")!;
const category = (name: string) =>
  fireEvent.click(screen.getByRole("button", { name }));

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

// Issues 3222, 3221, 3220, 3217, 3216 and 3214: whole-category entrance.
it.each([
  "Keyboard shortcuts",
  "Server preferences",
  "Paired devices",
  "Notifications",
  "Import / Export",
  "Backups",
  "Agent Workers",
])("plays the %s category entrance once per explicit opening", (name) => {
  render(providers(<Settings />));
  // The default landing category (AI Subscription) opted in too, so leave it first.
  category(unoptedSettingsCategory);
  expect(column().getAttribute("data-entrance")).toBeNull();
  category(name);
  const node = column();
  expect(node.getAttribute("data-entrance")).toBe("y4");
  // The heading itself never carries a second, separate motion.
  expect(heading()?.getAttribute("data-entrance") ?? null).toBeNull();
  dispatch(node);
  expect(node.getAttribute("data-entrance")).toBeNull();
  // Re-selecting the current category is not a new activation.
  category(name);
  expect(column()).toBe(node);
  expect(column().getAttribute("data-entrance")).toBeNull();
  category(unoptedSettingsCategory);
  expect(column().getAttribute("data-entrance")).toBeNull();
  category(name);
  expect(column().getAttribute("data-entrance")).toBe("y4");
});

// Issues 3223 and 3219: the body holds persistent hosts, so only the static
// heading moves and no ancestor of any host receives a transform.
it.each(["App information", "Connections"])(
  "moves only the %s heading and never an ancestor of its persistent hosts",
  (name) => {
    render(providers(<Settings />));
    category(name);
    const title = heading();
    expect(title.getAttribute("data-entrance")).toBe("y4");
    expect(column().getAttribute("data-entrance")).toBeNull();
    const panels = document.querySelector(".settings-panels")!;
    for (
      let node: Element | null = panels;
      node && node !== document.body;
      node = node.parentElement
    )
      expect(node.hasAttribute("data-entrance")).toBe(false);
    expect(title.contains(panels)).toBe(false);
    dispatch(title);
    expect(title.getAttribute("data-entrance")).toBeNull();
    category(unoptedSettingsCategory);
    expect(heading().getAttribute("data-entrance")).toBeNull();
    category(name);
    expect(heading().getAttribute("data-entrance")).toBe("y4");
  },
);

it("settles a heading entrance for Animation Off then reduced motion without replay", async () => {
  document.documentElement.dataset.animation = "true";
  const listeners = new Set<() => void>();
  const media = {
    matches: false,
    addEventListener: (_: string, listener: () => void) =>
      listeners.add(listener),
    removeEventListener: (_: string, listener: () => void) =>
      listeners.delete(listener),
  };
  vi.stubGlobal("matchMedia", () => media);
  render(providers(<Settings />));
  category("Connections");
  const title = heading();
  expect(title.getAttribute("data-entrance")).toBe("y4");
  await act(async () => {
    document.documentElement.dataset.animation = "false";
    await Promise.resolve();
  });
  expect(title.getAttribute("data-entrance")).toBeNull();
  await act(async () => {
    document.documentElement.dataset.animation = "true";
    await Promise.resolve();
  });
  expect(title.getAttribute("data-entrance")).toBeNull();
  act(() => {
    media.matches = true;
    listeners.forEach((changed) => changed());
    media.matches = false;
    listeners.forEach((changed) => changed());
  });
  expect(heading()).toBe(title);
  expect(title.getAttribute("data-entrance")).toBeNull();
});

it("does not play a category entrance when Animation is Off at activation", () => {
  document.documentElement.dataset.animation = "false";
  render(providers(<Settings />));
  category("Backups");
  expect(column().getAttribute("data-entrance")).toBeNull();
  category("App information");
  expect(heading().getAttribute("data-entrance")).toBeNull();
});

function taskHarness(target?: EntranceTarget) {
  function Harness() {
    const [open, setOpen] = useState(false);
    const [text, setText] = useState("");
    return (
      <SettingsTaskEntranceContext.Provider value={target}>
        <SettingsTasks>
          <button onClick={() => setOpen(true)}>Open task</button>
          {open ? (
            <SettingsTaskDialog
              title="Entrance task"
              size={SettingsDialogSize.Form}
              close={() => setOpen(false)}
            >
              <input
                aria-label="Draft"
                value={text}
                onChange={(event) => setText(event.target.value)}
              />
            </SettingsTaskDialog>
          ) : null}
        </SettingsTasks>
      </SettingsTaskEntranceContext.Provider>
    );
  }
  return render(providers(<Harness />));
}
const dialog = () =>
  document.querySelector<HTMLDialogElement>("dialog.settings-task-dialog")!;
const taskHeader = () =>
  document.querySelector<HTMLElement>(".settings-task-header")!;

it("animates a task header once per explicit opening and never for drafts", () => {
  taskHarness(EntranceTarget.Surface);
  fireEvent.click(screen.getByRole("button", { name: "Open task" }));
  const node = dialog();
  const header = taskHeader();
  // The native dialog host never carries the transform: it would become the
  // containing block of fixed descendants such as portaled tooltips.
  expect(node.hasAttribute("data-entrance")).toBe(false);
  expect(node.parentElement?.closest("[data-entrance]")).toBeNull();
  expect(header.getAttribute("data-entrance")).toBe("y4");
  const input = screen.getByRole("textbox", { name: "Draft" });
  // Edits during the motion keep the same dialog and control identity.
  fireEvent.change(input, { target: { value: "draft while entering" } });
  expect(dialog()).toBe(node);
  expect(screen.getByRole("textbox", { name: "Draft" })).toBe(input);
  expect(header.getAttribute("data-entrance")).toBe("y4");
  dispatch(header);
  expect(header.getAttribute("data-entrance")).toBeNull();
  fireEvent.change(input, { target: { value: "draft afterwards" } });
  expect(header.getAttribute("data-entrance")).toBeNull();
  // Dismissal is immediate with no exit animation; reopening replays.
  fireEvent.click(screen.getByRole("button", { name: /^Close/ }));
  expect(document.querySelector("dialog.settings-task-dialog")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Open task" }));
  expect(dialog().hasAttribute("data-entrance")).toBe(false);
  expect(taskHeader().getAttribute("data-entrance")).toBe("y4");
});

it("moves only the task heading for heading-only categories", () => {
  taskHarness(EntranceTarget.Heading);
  fireEvent.click(screen.getByRole("button", { name: "Open task" }));
  expect(dialog().getAttribute("data-entrance")).toBeNull();
  expect(taskHeader().getAttribute("data-entrance")).toBe("y4");
  dispatch(taskHeader());
  expect(taskHeader().getAttribute("data-entrance")).toBeNull();
});

it("does not animate tasks outside an opted-in category", () => {
  taskHarness(undefined);
  fireEvent.click(screen.getByRole("button", { name: "Open task" }));
  expect(dialog().getAttribute("data-entrance")).toBeNull();
  expect(taskHeader().getAttribute("data-entrance")).toBeNull();
});

it("settles a task entrance when Animation turns Off mid-flight without replay", async () => {
  document.documentElement.dataset.animation = "true";
  taskHarness(EntranceTarget.Surface);
  fireEvent.click(screen.getByRole("button", { name: "Open task" }));
  const node = dialog();
  const header = taskHeader();
  const input = screen.getByRole("textbox", { name: "Draft" });
  expect(header.getAttribute("data-entrance")).toBe("y4");
  await act(async () => {
    document.documentElement.dataset.animation = "false";
    await Promise.resolve();
  });
  expect(header.getAttribute("data-entrance")).toBeNull();
  await act(async () => {
    document.documentElement.dataset.animation = "true";
    await Promise.resolve();
  });
  expect(header.getAttribute("data-entrance")).toBeNull();
  expect(dialog()).toBe(node);
  expect(screen.getByRole("textbox", { name: "Draft" })).toBe(input);
});

it("plays a Modal heading entrance only for explicit presentation requests", () => {
  function Harness() {
    const [open, setOpen] = useState(false);
    const [request, setRequest] = useState(0);
    return (
      <>
        <button onClick={() => setOpen((value) => !value)}>Slot</button>
        <button
          onClick={() => {
            setRequest((value) => value + 1);
            setOpen(true);
          }}
        >
          Request
        </button>
        <Modal
          title="Saved server task"
          visible={open}
          close={() => setOpen(false)}
          headingEntrance
          presentationRequest={request}
        >
          <p>Body</p>
        </Modal>
      </>
    );
  }
  render(<Harness />);
  const modal = () => document.querySelector("dialog")!;
  const header = () => modal().querySelector("header")!;
  // A hidden Modal never carries the attribute.
  expect(header().getAttribute("data-entrance")).toBeNull();
  // Visibility changes without a request (a host slot appearing) stay immediate.
  fireEvent.click(screen.getByRole("button", { name: "Slot" }));
  expect(header().getAttribute("data-entrance")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Slot" }));
  fireEvent.click(screen.getByRole("button", { name: "Slot" }));
  expect(header().getAttribute("data-entrance")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Slot" }));
  // An explicit request plays once on the heading, never on the dialog.
  fireEvent.click(screen.getByRole("button", { name: "Request" }));
  expect(header().getAttribute("data-entrance")).toBe("y4");
  expect(modal().getAttribute("data-entrance")).toBeNull();
  dispatch(header());
  expect(header().getAttribute("data-entrance")).toBeNull();
  // Hiding and showing again without a new request does not replay.
  fireEvent.click(screen.getByRole("button", { name: "Slot" }));
  fireEvent.click(screen.getByRole("button", { name: "Slot" }));
  expect(header().getAttribute("data-entrance")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Request" }));
  expect(header().getAttribute("data-entrance")).toBe("y4");
});

it("plays a Modal heading entrance for a mount that starts visible", () => {
  render(
    <Modal title="Mounted task" close={() => {}} headingEntrance>
      <p>Body</p>
    </Modal>,
  );
  expect(
    document.querySelector("dialog header")!.getAttribute("data-entrance"),
  ).toBe("y4");
});

// Issue 3221: only user-requested stages animate.
function transferFixture() {
  const id = "019abcde-1234-7123-8123-123456789abc";
  const bundle = {
    version: 4,
    entries: [{ id, kind: "template", document: { name: "T", contents: "x" } }],
    machines: [],
  };
  return bundle;
}
it("animates the Map and Review stage surfaces only for explicit user requests", async () => {
  const { ConfigurationService, SystemService, ResourceService } =
    await import("@delinoio/delidev-api-client");
  const { encode } = await import("./documents");
  const bundle = transferFixture();
  const previewBytes = encode({
    plan: {
      version: 4,
      changes: [
        {
          source_id: bundle.entries[0]!.id,
          id: "019abcde-1234-7123-8123-123456789abd",
          kind: "template",
          action: "create",
          expected_revision: 0,
          after: bundle.entries[0]!.document,
        },
      ],
      machines: [],
    },
    token: "server-scoped-preview",
  });
  const transport = createRouterTransport((router) => {
    router.service(ConfigurationService, {
      previewConfigurationImport: async () => ({ previewJson: previewBytes }),
    });
    router.service(SystemService, { getStatus: () => ({ capabilities: [] }) });
    router.service(ResourceService, {
      listResources: () => ({ resources: [] }),
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <MutationIntents>
          <ConfigurationTransfer active />
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>,
  );
  const mapping = () =>
    document.querySelector<HTMLElement>(".transfer-mapping");
  expect(mapping()).toBeNull();
  fireEvent.change(
    screen.getByRole("textbox", { name: "Configuration JSON" }),
    { target: { value: JSON.stringify(bundle) } },
  );
  // Typing is not a stage request.
  expect(mapping()).toBeNull();
  fireEvent.click(
    screen.getByRole("button", { name: "Load configuration document" }),
  );
  const map = mapping()!;
  expect(map.getAttribute("data-entrance")).toBe("y4");
  dispatch(map);
  expect(map.getAttribute("data-entrance")).toBeNull();
  // Loading again while a document is already mapped is a new explicit Load
  // request even though the Map surface never leaves the screen.
  fireEvent.click(
    screen.getByRole("button", { name: "Load configuration document" }),
  );
  expect(mapping()).toBe(map);
  expect(map.getAttribute("data-entrance")).toBe("y4");
  dispatch(map);
  expect(map.getAttribute("data-entrance")).toBeNull();
  fireEvent.click(
    screen.getByRole("button", { name: "Preview configuration changes" }),
  );
  const review = await screen.findByRole("region", {
    name: "Configuration change preview",
  });
  expect(review.getAttribute("data-entrance")).toBe("y4");
  expect(mapping()).toBe(map);
  expect(map.getAttribute("data-entrance")).toBeNull();
  dispatch(review);
  expect(review.getAttribute("data-entrance")).toBeNull();
});
