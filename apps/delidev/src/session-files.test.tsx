import { create } from "@bufbuild/protobuf";
import { useState } from "react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ErrorDetailSchema, SessionService, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { SessionFiles, SessionFilePreview } from "./session-files";

function fixture() {
  const sessionId = newRequestId(), first = newRequestId(), primary = newRequestId();
  const reply = (value: object) => ({ documentJson: encode({ size: "0", binary: false, truncated: false, ...value }) });
  const read = vi.fn(async (request: { sessionId: string; queryJson: Uint8Array }) => {
    const query = JSON.parse(new TextDecoder().decode(request.queryJson));
    if (query.operation === "roots") return reply({ roots: [{ repository_id: first, name: "First", primary: false }, { repository_id: primary, name: "Primary", primary: true }] });
    if (query.operation === "file") return reply({ size: "9007199254740993", text: "<script>globalThis.unsafe = true</script>", truncated: true });
    if (query.page_token) return reply({ entries: [] });
    return reply({ entries: [{ name: "folder", kind: "directory", size: "0" }, { name: "note.txt", kind: "file", size: "42" }, { name: "symlink", kind: "link", size: "4" }], next_page_token: "page-two" });
  });
  const transport = createRouterTransport((router) => router.service(SessionService, { readSessionWorkspace: read }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  function View() {
    const [open, setOpen] = useState(true);
    return <TransportProvider transport={transport}><QueryClientProvider client={client}>{open ? <SessionFiles sessionId={sessionId} close={() => setOpen(false)} /> : <button onClick={() => setOpen(true)}>Reopen files</button>}</QueryClientProvider></TransportProvider>;
  }
  return { sessionId, first, primary, read, client, transport, View };
}

it("browses the actual selected root and retains inert bounded previews with exact file size", async () => {
  const f = fixture(); const { container } = render(<f.View />);
  await screen.findByRole("treeitem", { name: "note.txt 42 bytes" });
  expect(JSON.parse(new TextDecoder().decode(f.read.mock.calls[1][0].queryJson))).toMatchObject({ operation: "directory", repository_id: f.primary, path: "." });
  expect(screen.getByText("Symbolic link · Preview unavailable")).toBeTruthy();
  expect(screen.queryByRole("button", { name: /symlink/ })).toBeNull();
  fireEvent.click(screen.getByRole("treeitem", { name: "note.txt 42 bytes" }));
  expect(await screen.findByText("<script>globalThis.unsafe = true</script>")).toBeTruthy();
  expect(screen.getByText("9007199254740993 bytes · Preview limited to 64 KiB")).toBeTruthy();
  expect(container.querySelector("script, iframe, a")).toBeNull();
  expect(document.activeElement?.textContent).toBe("note.txt");
  fireEvent.click(screen.getByRole("button", { name: "Back to files" }));
  fireEvent.click(await screen.findByRole("treeitem", { name: "folder Folder" }));
  await waitFor(() => expect(JSON.parse(new TextDecoder().decode(f.read.mock.calls.at(-1)![0].queryJson)).path).toBe("folder"));
  fireEvent.change(screen.getByLabelText("Workspace repository"), { target: { value: f.first } });
  await waitFor(() => expect(JSON.parse(new TextDecoder().decode(f.read.mock.calls.at(-1)![0].queryJson))).toMatchObject({ repository_id: f.first, path: "." }));
});

it("pages directory observations, exposes failures and discards content when closed", async () => {
  const f = fixture(); render(<f.View />);
  await screen.findByRole("treeitem", { name: "note.txt 42 bytes" });
  fireEvent.click(screen.getByRole("button", { name: "Load more Directory pages" }));
  await waitFor(() => expect(screen.queryByRole("button", { name: "Load more Directory pages" })).toBeNull());
  expect(screen.getByRole("treeitem", { name: "note.txt 42 bytes" })).toBeTruthy();
  expect(JSON.parse(new TextDecoder().decode(f.read.mock.calls.at(-1)![0].queryJson)).page_token).toBe("page-two");
  await screen.findByRole("treeitem", { name: "note.txt 42 bytes" });
  f.read.mockRejectedValueOnce(new ConnectError("Worker unavailable", Code.Unavailable));
  fireEvent.click(screen.getByRole("button", { name: "Refresh files" }));
  await screen.findByText("Refresh failed. The last observation is shown below.");
  fireEvent.keyDown(screen.getByRole("complementary", { name: "Session files" }), { key: "Escape" });
  expect(screen.queryByRole("complementary")).toBeNull();
  await waitFor(() => expect(f.client.getQueryCache().getAll().length).toBe(0));
  fireEvent.click(screen.getByRole("button", { name: "Reopen files" }));
  await screen.findByRole("treeitem", { name: "note.txt 42 bytes" });
  expect(f.read.mock.calls.filter(([request]) => JSON.parse(new TextDecoder().decode(request.queryJson)).operation === "roots")).toHaveLength(2);
});

it("renders binary data as unavailable and malformed observations as errors", async () => {
  const f = fixture(); render(<f.View />);
  await screen.findByRole("treeitem", { name: "note.txt 42 bytes" });
  f.read.mockResolvedValueOnce({ documentJson: encode({ size: "42", binary: true, truncated: false }) });
  fireEvent.click(screen.getByRole("treeitem", { name: "note.txt 42 bytes" }));
  await screen.findByText("This file has no UTF-8 text preview.");
  f.read.mockResolvedValueOnce({ documentJson: encode({ size: 42, binary: false, truncated: false, text: "invalid" }) });
  fireEvent.click(screen.getByRole("button", { name: "Refresh files" }));
  await screen.findAllByRole("alert");
  expect(screen.queryByText("invalid")).toBeNull();
});

it("keeps keyboard focus inside the panel after opening a file so Escape can close it", async () => {
  const f = fixture(); render(<f.View />);
  fireEvent.click(await screen.findByRole("treeitem", { name: "note.txt 42 bytes" }));
  await screen.findByText("<script>globalThis.unsafe = true</script>");
  expect(document.activeElement?.textContent).toBe("note.txt");
  fireEvent.keyDown(document.activeElement!, { key: "Escape" });
  expect(screen.queryByRole("complementary")).toBeNull();
});

it("retains directory metadata on a digest-bound cursor failure and requires explicit reload", async () => {
  const f = fixture(); render(<f.View />);
  await screen.findByRole("treeitem", { name: "note.txt 42 bytes" });
  f.read.mockRejectedValueOnce(new ConnectError("Directory changed", Code.Aborted, undefined, [{ desc: ErrorDetailSchema, value: create(ErrorDetailSchema, { code: "conflict" }) }]));
  fireEvent.click(screen.getByRole("button", { name: "Load more Directory pages" }));
  await screen.findByRole("button", { name: "Reload list" });
  expect(screen.getByRole("treeitem", { name: "note.txt 42 bytes" })).toBeTruthy();
  expect(JSON.parse(new TextDecoder().decode(f.read.mock.calls.at(-1)![0].queryJson)).page_token).toBe("page-two");
  const before = f.read.mock.calls.length;
  fireEvent.scroll(screen.getByRole("treeitem", { name: "note.txt 42 bytes" }).closest(".conversation-page-scroll")!);
  expect(f.read).toHaveBeenCalledTimes(before);
  fireEvent.click(screen.getByRole("button", { name: "Reload list" }));
  await screen.findByRole("button", { name: "Load more Directory pages" });
  expect(JSON.parse(new TextDecoder().decode(f.read.mock.calls.at(-1)![0].queryJson)).page_token).toBe("");
});


it.each(["continuation", "refresh"])("rejects a whole directory page with duplicate entry names during %s", async stage => {
 const f = fixture(); render(<f.View />);
 await screen.findByRole("treeitem", { name: "note.txt 42 bytes" });
 const prior = f.read.mock.calls.length;
 f.read.mockResolvedValueOnce({ documentJson: encode({ size: "0", binary: false, truncated: false, entries: [{ name: "duplicate.txt", kind: "file", size: "1" }, { name: "duplicate.txt", kind: "directory", size: "0" }, { name: "poison.txt", kind: "file", size: "2" }] }) });
 fireEvent.click(screen.getByRole("button", { name: stage === "continuation" ? "Load more Directory pages" : "Refresh files" }));
 await screen.findByText("Refresh failed. The last observation is shown below.");
 expect(screen.getByRole("treeitem", { name: "note.txt 42 bytes" })).toBeTruthy();
 expect(screen.queryByText("duplicate.txt")).toBeNull(); expect(screen.queryByText("poison.txt")).toBeNull();
 expect(f.read).toHaveBeenCalledTimes(prior + 1);
 const query = JSON.parse(new TextDecoder().decode(f.read.mock.calls.at(-1)![0].queryJson));
 expect(query).toMatchObject({ operation: "directory", repository_id: f.primary, path: ".", page_token: stage === "continuation" ? "page-two" : "" });
 fireEvent.scroll(screen.getByRole("treeitem", { name: "note.txt 42 bytes" }).closest(".conversation-page-scroll")!);
 expect(f.read).toHaveBeenCalledTimes(prior + 1);
 expect(f.read.mock.calls.every(([request]) => JSON.parse(new TextDecoder().decode(request.queryJson)).operation !== "file")).toBe(true);
});

it("restores tree scroll, selected focus and metadata on Back without another observation", async () => {
  const f = fixture(); const mounted = render(<div className="session-workspace"><div className="session-upper-content"><div className="session-app-panel"><f.View /></div></div></div>);
  const row = await screen.findByRole("treeitem", { name: "note.txt 42 bytes" });
  const scroll = row.closest<HTMLElement>(".file-tree-scroll")!, outer = mounted.container.querySelector<HTMLElement>(".session-app-panel")!, upper = mounted.container.querySelector<HTMLElement>(".session-upper-content")!;
  row.focus(); scroll.scrollTop = 137; outer.scrollTop = 238; upper.scrollTop = 59;
  Object.defineProperties(outer, { clientHeight: { value: 88 }, scrollHeight: { value: 366 } });
  const nativeFocus = HTMLElement.prototype.focus;
  const focus = vi.spyOn(HTMLElement.prototype, "focus").mockImplementation(function(this: HTMLElement, options?: FocusOptions) {
    nativeFocus.call(this, options);
    // Model native focus scrolling unchanged-range compact ancestors.
    if (!options?.preventScroll) { outer.scrollTop = 191; upper.scrollTop = 37; }
  });
  fireEvent.keyDown(row, { key: "Enter" }); await screen.findByText("<script>globalThis.unsafe = true</script>");
  const before = f.read.mock.calls.length;
  fireEvent.click(screen.getByRole("button", { name: "Back to files" }));
  const restored = screen.getByRole("treeitem", { name: "note.txt 42 bytes" });
  expect(restored.getAttribute("aria-selected")).toBe("true"); expect(document.activeElement).toBe(restored);
  expect(restored.closest<HTMLElement>(".file-tree-scroll")!.scrollTop).toBe(137);
  expect(outer.scrollTop).toBe(238); expect(upper.scrollTop).toBe(59);
  expect(focus.mock.calls.some(([options]) => options?.preventScroll === true)).toBe(true); focus.mockRestore();
  expect(screen.queryByText("<script>globalThis.unsafe = true</script>")).toBeNull();
  expect(f.read).toHaveBeenCalledTimes(before);
  await waitFor(() => expect(f.client.getQueryCache().getAll()).toHaveLength(0));
});

it("uses tree navigation and keeps symbolic links inert", async () => {
  const f = fixture(); render(<f.View />);
  const folder = await screen.findByRole("treeitem", { name: "folder Folder" }); folder.focus();
  fireEvent.keyDown(folder, { key: "ArrowDown" }); expect(document.activeElement).toBe(screen.getByRole("treeitem", { name: "note.txt 42 bytes" }));
  fireEvent.keyDown(document.activeElement!, { key: "End" }); const link = screen.getByRole("treeitem", { name: /symlink/ }); expect(document.activeElement).toBe(link);
  const before = f.read.mock.calls.length; fireEvent.keyDown(link, { key: "Enter" }); expect(f.read).toHaveBeenCalledTimes(before); expect(screen.queryByRole("region", { name: "File preview" })).toBeNull();
  fireEvent.keyDown(link, { key: "Home" }); expect(document.activeElement).toBe(folder);
  f.read.mockResolvedValueOnce({ documentJson: encode({ size: "0", binary: false, truncated: false, entries: [{ name: "nested.txt", kind: "file", size: "2" }] }) });
  fireEvent.keyDown(folder, { key: "ArrowRight" }); await screen.findByRole("treeitem", { name: "nested.txt 2 bytes" });
  expect(folder.getAttribute("aria-expanded")).toBe("true");
  fireEvent.keyDown(folder, { key: "ArrowRight" }); expect(document.activeElement).toBe(screen.getByRole("treeitem", { name: "nested.txt 2 bytes" }));
  fireEvent.keyDown(document.activeElement!, { key: "ArrowLeft" }); expect(document.activeElement).toBe(folder);
  fireEvent.keyDown(folder, { key: "ArrowLeft" }); expect(folder.getAttribute("aria-expanded")).toBe("false"); expect(screen.queryByRole("treeitem", { name: "nested.txt 2 bytes" })).toBeNull();
});

it("joins an ignored-abort Connect handler before preview after directory cancellation", async () => {
  const sessionId = newRequestId(), root = newRequestId(); let busy = 0, maximum = 0, finishPage = () => {};
  const held = new Promise<void>(resolve => { finishPage = resolve; }); const calls: string[] = [];
  const transport = createRouterTransport(router => router.service(SessionService, { readSessionWorkspace: async request => {
    const query = JSON.parse(new TextDecoder().decode(request.queryJson)); calls.push(query.operation + (query.page_token ? ":next" : "")); maximum = Math.max(maximum, ++busy);
    try {
      if (query.operation === "roots") return { documentJson: encode({ size: "0", binary: false, truncated: false, roots: [{ repository_id: root, name: "Workspace", primary: true }] }) };
      if (query.operation === "file") return { documentJson: encode({ size: "4", binary: false, truncated: false, text: "safe" }) };
      if (query.page_token) { await held; return { documentJson: encode({ size: "0", binary: false, truncated: false, entries: [{ name: "late.txt", kind: "file", size: "1" }] }) }; }
      return { documentJson: encode({ size: "0", binary: false, truncated: false, entries: [{ name: "note.txt", kind: "file", size: "4" }], next_page_token: "next" }) };
    } finally { busy--; }
  } }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={client}><TransportProvider transport={transport}><SessionFiles sessionId={sessionId} close={() => {}} /></TransportProvider></QueryClientProvider>);
  const note = await screen.findByRole("treeitem", { name: "note.txt 4 bytes" });
  fireEvent.click(screen.getByRole("button", { name: "Load more Directory pages" })); await waitFor(() => expect(calls.at(-1)).toBe("directory:next"));
  fireEvent.click(note); await screen.findByText("Reading file…");
  await new Promise(resolve => setTimeout(resolve, 50));
  expect(calls).toEqual(["roots", "directory", "directory:next"]); expect(busy).toBe(1);
  finishPage(); await screen.findByText("safe"); expect(maximum).toBe(1);
  fireEvent.click(screen.getByRole("button", { name: "Back to files" })); expect(screen.queryByText("late.txt")).toBeNull();
  await waitFor(() => expect(client.getQueryCache().getAll()).toHaveLength(0));
});

it("opens active typed file previews through the serialized read owner and discards bytes on departure",async()=>{
 const f=fixture();const view=render(<TransportProvider transport={f.transport}><QueryClientProvider client={f.client}><SessionFilePreview sessionId={f.sessionId} repository={f.primary} path="note.txt" close={()=>{}}/></QueryClientProvider></TransportProvider>);
 await screen.findByText("<script>globalThis.unsafe = true</script>");expect(JSON.parse(new TextDecoder().decode(f.read.mock.calls[0][0].queryJson))).toMatchObject({operation:"file",repository_id:f.primary,path:"note.txt"});expect(view.container.querySelector("script,iframe,a")).toBeNull();view.unmount();await waitFor(()=>expect(f.client.getQueryCache().getAll()).toHaveLength(0));
});
