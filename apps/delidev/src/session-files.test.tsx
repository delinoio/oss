import { create } from "@bufbuild/protobuf";
import { useState } from "react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { ErrorDetailSchema, SessionService, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { SessionFiles } from "./session-files";

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
  return { sessionId, first, primary, read, client, View };
}

it("browses the actual selected root and retains inert bounded previews with exact file size", async () => {
  const f = fixture(); const { container } = render(<f.View />);
  await screen.findByRole("button", { name: "note.txt 42 bytes" });
  expect(JSON.parse(new TextDecoder().decode(f.read.mock.calls[1][0].queryJson))).toMatchObject({ operation: "directory", repository_id: f.primary, path: "." });
  expect(screen.getByText("Symbolic link · Preview unavailable")).toBeTruthy();
  expect(screen.queryByRole("button", { name: /symlink/ })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "note.txt 42 bytes" }));
  expect(await screen.findByText("<script>globalThis.unsafe = true</script>")).toBeTruthy();
  expect(screen.getByText("9007199254740993 bytes · Preview limited to 64 KiB")).toBeTruthy();
  expect(container.querySelector("script, iframe, a")).toBeNull();
  expect(document.activeElement?.textContent).toBe("note.txt");
  expect((screen.getByLabelText("Relative directory") as HTMLInputElement).value).toBe(".");
  fireEvent.click(screen.getByRole("button", { name: "Up" }));
  fireEvent.click(await screen.findByRole("button", { name: "folder Folder" }));
  await waitFor(() => expect(JSON.parse(new TextDecoder().decode(f.read.mock.calls.at(-1)![0].queryJson)).path).toBe("folder"));
  fireEvent.change(screen.getByLabelText("Workspace repository"), { target: { value: f.first } });
  await waitFor(() => expect(JSON.parse(new TextDecoder().decode(f.read.mock.calls.at(-1)![0].queryJson))).toMatchObject({ repository_id: f.first, path: "." }));
});

it("pages directory observations, exposes failures and discards content when closed", async () => {
  const f = fixture(); render(<f.View />);
  await screen.findByRole("button", { name: "note.txt 42 bytes" });
  fireEvent.click(screen.getByRole("button", { name: "Load more Directory pages" }));
  await waitFor(() => expect(screen.queryByRole("button", { name: "Load more Directory pages" })).toBeNull());
  expect(screen.getByRole("button", { name: "note.txt 42 bytes" })).toBeTruthy();
  expect(JSON.parse(new TextDecoder().decode(f.read.mock.calls.at(-1)![0].queryJson)).page_token).toBe("page-two");
  await screen.findByRole("button", { name: "note.txt 42 bytes" });
  f.read.mockRejectedValueOnce(new ConnectError("Worker unavailable", Code.Unavailable));
  fireEvent.click(screen.getByRole("button", { name: "Refresh files" }));
  await screen.findByText("Refresh failed. The last observation is shown below.");
  fireEvent.keyDown(screen.getByRole("complementary", { name: "Session files" }), { key: "Escape" });
  expect(screen.queryByRole("complementary")).toBeNull();
  await waitFor(() => expect(f.client.getQueryCache().getAll().length).toBe(0));
  fireEvent.click(screen.getByRole("button", { name: "Reopen files" }));
  await screen.findByRole("button", { name: "note.txt 42 bytes" });
  expect(f.read.mock.calls.filter(([request]) => JSON.parse(new TextDecoder().decode(request.queryJson)).operation === "roots")).toHaveLength(2);
});

it("renders binary data as unavailable and malformed observations as errors", async () => {
  const f = fixture(); render(<f.View />);
  await screen.findByRole("button", { name: "note.txt 42 bytes" });
  f.read.mockResolvedValueOnce({ documentJson: encode({ size: "42", binary: true, truncated: false }) });
  fireEvent.click(screen.getByRole("button", { name: "note.txt 42 bytes" }));
  await screen.findByText("This file has no UTF-8 text preview.");
  f.read.mockResolvedValueOnce({ documentJson: encode({ size: 42, binary: false, truncated: false, text: "invalid" }) });
  fireEvent.click(screen.getByRole("button", { name: "Refresh files" }));
  await screen.findAllByRole("alert");
  expect(screen.queryByText("invalid")).toBeNull();
});

it("keeps keyboard focus inside the panel after opening a file so Escape can close it", async () => {
  const f = fixture(); render(<f.View />);
  fireEvent.click(await screen.findByRole("button", { name: "note.txt 42 bytes" }));
  await screen.findByText("<script>globalThis.unsafe = true</script>");
  expect(document.activeElement?.textContent).toBe("note.txt");
  fireEvent.keyDown(document.activeElement!, { key: "Escape" });
  expect(screen.queryByRole("complementary")).toBeNull();
});

it("retains directory metadata on a digest-bound cursor failure and requires explicit reload", async () => {
  const f = fixture(); render(<f.View />);
  await screen.findByRole("button", { name: "note.txt 42 bytes" });
  f.read.mockRejectedValueOnce(new ConnectError("Directory changed", Code.Aborted, undefined, [{ desc: ErrorDetailSchema, value: create(ErrorDetailSchema, { code: "conflict" }) }]));
  fireEvent.click(screen.getByRole("button", { name: "Load more Directory pages" }));
  await screen.findByRole("button", { name: "Reload list" });
  expect(screen.getByRole("button", { name: "note.txt 42 bytes" })).toBeTruthy();
  expect(JSON.parse(new TextDecoder().decode(f.read.mock.calls.at(-1)![0].queryJson)).page_token).toBe("page-two");
  const before = f.read.mock.calls.length;
  fireEvent.scroll(screen.getByRole("button", { name: "note.txt 42 bytes" }).closest(".conversation-page-scroll")!);
  expect(f.read).toHaveBeenCalledTimes(before);
  fireEvent.click(screen.getByRole("button", { name: "Reload list" }));
  await screen.findByRole("button", { name: "Load more Directory pages" });
  expect(JSON.parse(new TextDecoder().decode(f.read.mock.calls.at(-1)![0].queryJson)).page_token).toBe("");
});


it.each(["continuation", "refresh"])("rejects a whole directory page with duplicate entry names during %s", async stage => {
 const f = fixture(); render(<f.View />);
 await screen.findByRole("button", { name: "note.txt 42 bytes" });
 const prior = f.read.mock.calls.length;
 f.read.mockResolvedValueOnce({ documentJson: encode({ size: "0", binary: false, truncated: false, entries: [{ name: "duplicate.txt", kind: "file", size: "1" }, { name: "duplicate.txt", kind: "directory", size: "0" }, { name: "poison.txt", kind: "file", size: "2" }] }) });
 fireEvent.click(screen.getByRole("button", { name: stage === "continuation" ? "Load more Directory pages" : "Refresh files" }));
 await screen.findByText("Refresh failed. The last observation is shown below.");
 expect(screen.getByRole("button", { name: "note.txt 42 bytes" })).toBeTruthy();
 expect(screen.queryByText("duplicate.txt")).toBeNull(); expect(screen.queryByText("poison.txt")).toBeNull();
 expect(f.read).toHaveBeenCalledTimes(prior + 1);
 const query = JSON.parse(new TextDecoder().decode(f.read.mock.calls.at(-1)![0].queryJson));
 expect(query).toMatchObject({ operation: "directory", repository_id: f.primary, path: ".", page_token: stage === "continuation" ? "page-two" : "" });
 fireEvent.scroll(screen.getByRole("button", { name: "note.txt 42 bytes" }).closest(".conversation-page-scroll")!);
 expect(f.read).toHaveBeenCalledTimes(prior + 1);
 expect(f.read.mock.calls.every(([request]) => JSON.parse(new TextDecoder().decode(request.queryJson)).operation !== "file")).toBe(true);
});
