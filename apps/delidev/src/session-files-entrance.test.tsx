// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { SessionService, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { SessionFilePreview, SessionFiles } from "./session-files";
import {
  dispatchAnimation,
  setAnimation,
  stubReducedMotion,
} from "./session-tool-entrance-helpers";

function fixture() {
  const sessionId = newRequestId(),
    primary = newRequestId();
  const reply = (value: object) => ({
    documentJson: encode({
      size: "0",
      binary: false,
      truncated: false,
      ...value,
    }),
  });
  // A directory read can be held so its reply arrives after the user left.
  let heldPath: string | undefined;
  let release: (() => void) | undefined;
  const read = vi.fn(
    async (request: { sessionId: string; queryJson: Uint8Array }) => {
      const query = JSON.parse(new TextDecoder().decode(request.queryJson));
      if (query.operation === "roots")
        return reply({
          roots: [{ repository_id: primary, name: "Primary", primary: true }],
        });
      if (query.operation === "file")
        return reply({ size: "4", text: "body-bytes" });
      if (query.path === heldPath) {
        await new Promise<void>((resolve) => {
          release = resolve;
        });
        return reply({
          entries: [{ name: "late.txt", kind: "file", size: "1" }],
        });
      }
      return reply({
        entries: [
          { name: "folder", kind: "directory", size: "0" },
          { name: "note.txt", kind: "file", size: "42" },
        ],
      });
    },
  );
  const transport = createRouterTransport((router) =>
    router.service(SessionService, { readSessionWorkspace: read }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const reads = (operation: string) =>
    read.mock.calls.filter(
      ([request]) =>
        JSON.parse(new TextDecoder().decode(request.queryJson)).operation ===
        operation,
    ).length;
  return {
    sessionId,
    primary,
    read,
    reads,
    transport,
    client,
    hold: (path: string) => {
      heldPath = path;
    },
    release: () => release?.(),
  };
}

// The Files tool as session.tsx mounts it: retained after the first open and
// inactive while a File preview tab is selected.
function harness(f: ReturnType<typeof fixture>, initial = true) {
  let setters!: {
    files: (value: boolean) => void;
    preview: (value: string | undefined) => void;
    activeFiles: () => boolean;
  };
  function View() {
    const [files, setFiles] = useState(initial);
    const [preview, setPreview] = useState<string>();
    setters = {
      files: setFiles,
      preview: setPreview,
      activeFiles: () => files,
    };
    return (
      <TransportProvider transport={f.transport}>
        <QueryClientProvider client={f.client}>
          <div
            hidden={!files || Boolean(preview)}
            inert={!files || Boolean(preview)}
            className="session-app-panel"
          >
            <SessionFiles
              sessionId={f.sessionId}
              active={files && !preview}
              close={() => setFiles(false)}
              openFile={(_repository, path) => setPreview(path)}
            />
          </div>
          {preview ? (
            <div className="session-app-panel">
              <SessionFilePreview
                sessionId={f.sessionId}
                repository={f.primary}
                path={preview}
                close={() => setPreview(undefined)}
              />
            </div>
          ) : null}
        </QueryClientProvider>
      </TransportProvider>
    );
  }
  return { View, set: () => setters };
}

const filesHeader = () =>
  document.querySelector<HTMLElement>("aside.session-files > header")!;
const previewHeader = () =>
  document.querySelector<HTMLElement>("section.file-preview > header");
const entranceNodes = () => document.querySelectorAll("[data-entrance]");

beforeEach(() => {
  stubReducedMotion(false);
});
afterEach(() => {
  vi.unstubAllGlobals();
  delete document.documentElement.dataset.animation;
});

it("animates the Files header once per activation and never the tree", async () => {
  const f = fixture();
  const h = harness(f);
  render(<h.View />);
  await screen.findByRole("treeitem", { name: "note.txt 42 bytes" });
  const node = filesHeader();
  expect(node.getAttribute("data-entrance")).toBe("y2");
  expect(entranceNodes().length).toBe(1);
  expect(node.closest("[data-entrance]")).toBe(node);
  dispatchAnimation(node);
  expect(filesHeader().getAttribute("data-entrance")).toBeNull();
  // Refresh and directory expansion do not replay the entrance.
  fireEvent.click(screen.getByRole("button", { name: "Refresh files" }));
  fireEvent.click(
    await screen.findByRole("treeitem", { name: "folder Folder" }),
  );
  await waitFor(() => expect(f.reads("directory")).toBeGreaterThan(1));
  expect(filesHeader()).toBe(node);
  expect(entranceNodes().length).toBe(0);
});

it("replays once for a File preview round trip with the retained controller and a held late reply", async () => {
  const f = fixture();
  const h = harness(f);
  render(<h.View />);
  const folder = await screen.findByRole("treeitem", { name: "folder Folder" });
  dispatchAnimation(filesHeader());
  f.hold("folder");
  fireEvent.click(folder);
  await waitFor(() => expect(f.reads("directory")).toBe(2));
  const readsBefore = f.read.mock.calls.length;
  const files = filesHeader();
  // Opening the preview hides Files and mounts the preview with its own entrance.
  act(() => h.set().preview("note.txt"));
  // The shared serialized read owner queues the file read behind the held one.
  expect(previewHeader()!.getAttribute("data-entrance")).toBe("y2");
  expect(files.getAttribute("data-entrance")).toBeNull();
  expect(entranceNodes().length).toBe(1);
  dispatchAnimation(previewHeader()!);
  // The retained Files panel (same header node) plays one entrance on return.
  act(() => h.set().preview(undefined));
  expect(previewHeader()).toBeNull();
  expect(filesHeader()).toBe(files);
  expect(files.getAttribute("data-entrance")).toBe("y2");
  expect(entranceNodes().length).toBe(1);
  dispatchAnimation(files);
  // The held directory reply arriving now neither replays nor navigates.
  await act(async () => {
    f.release();
    await Promise.resolve();
  });
  expect(entranceNodes().length).toBe(0);
  expect(f.reads("roots")).toBe(1);
  expect(f.read.mock.calls.length).toBeGreaterThanOrEqual(readsBefore);
  expect(screen.getByRole("treeitem", { name: "folder Folder" })).toBe(folder);
});

it("keeps the preview entrance to the explicit open and replaces it for another file", async () => {
  const f = fixture();
  const h = harness(f);
  render(<h.View />);
  await screen.findByRole("treeitem", { name: "note.txt 42 bytes" });
  act(() => h.set().preview("note.txt"));
  const node = previewHeader()!;
  expect(node.getAttribute("data-entrance")).toBe("y2");
  expect(node.closest("[data-entrance]")).toBe(node);
  dispatchAnimation(node);
  await screen.findByText("body-bytes");
  fireEvent.click(screen.getByRole("button", { name: "Refresh files" }));
  await waitFor(() => expect(f.reads("file")).toBe(2));
  expect(entranceNodes().length).toBe(0);
  // Leaving before completion cannot leave a stale attribute on re-entry.
  act(() => h.set().preview(undefined));
  act(() => h.set().preview("note.txt"));
  expect(entranceNodes().length).toBe(1);
  expect(previewHeader()!.getAttribute("data-entrance")).toBe("y2");
});

it("is suppressed by Animation Off and reduced motion without replay on re-enable", async () => {
  const change = stubReducedMotion(false);
  const f = fixture();
  const h = harness(f);
  render(<h.View />);
  await screen.findByRole("treeitem", { name: "note.txt 42 bytes" });
  expect(filesHeader().getAttribute("data-entrance")).toBe("y2");
  await setAnimation("false");
  expect(filesHeader().getAttribute("data-entrance")).toBeNull();
  await setAnimation(undefined);
  expect(filesHeader().getAttribute("data-entrance")).toBeNull();
  // Return from a preview while Animation is Off: nothing plays.
  await setAnimation("false");
  act(() => h.set().preview("note.txt"));
  expect(previewHeader()!.getAttribute("data-entrance")).toBeNull();
  act(() => h.set().preview(undefined));
  expect(filesHeader().getAttribute("data-entrance")).toBeNull();
  await setAnimation(undefined);
  // Reduced motion mid-flight clears an in-flight entrance.
  act(() => h.set().preview("note.txt"));
  expect(previewHeader()!.getAttribute("data-entrance")).toBe("y2");
  change(true);
  await waitFor(() => expect(entranceNodes().length).toBe(0));
  change(false);
  expect(entranceNodes().length).toBe(0);
});
