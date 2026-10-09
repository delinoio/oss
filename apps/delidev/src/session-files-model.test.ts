// SPDX-License-Identifier: Apache-2.0
import { expect, it, vi } from "vitest";
import { FilesController, type WorkspaceQuery, type WorkspaceReader } from "./session-files-model";
import { EntryKind, FileOperation, type Observation } from "./session-files-observation";

const folder = (name: string) => ({ name, kind: EntryKind.Directory, size: "0" });
const file = (name: string) => ({ name, kind: EntryKind.File, size: "9007199254740993" });
const result = (value: Partial<Observation> = {}): Observation => ({ roots: [], entries: [], next: "", text: "", size: "0", binary: false, truncated: false, ...value });
const roots = result({ roots: [{ repository_id: "first", name: "First", primary: false }, { repository_id: "primary", name: "Primary", primary: true }] });
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(accept => { resolve = accept; }); return { promise, resolve }; }
async function ready(read: WorkspaceReader) { const owner = new FilesController(read); owner.start(); await vi.waitFor(() => expect(owner.getSnapshot().directories.get(".")?.loaded).toBe(true)); return owner; }

it("serializes roots, sibling expansion and preview behind an ignored-abort read", async () => {
  const pending = deferred<Observation>(), calls: WorkspaceQuery[] = []; let busy = 0, maximum = 0;
  const owner = await ready(async query => {
    calls.push(query); maximum = Math.max(maximum, ++busy);
    try { if (query.operation === FileOperation.Roots) return roots;
      if (query.path === ".") return result({ entries: [folder("a"), folder("b"), file("note.txt")] });
      if (query.path === "a") return await pending.promise;
      return result({ text: "private preview" });
    } finally { busy--; }
  });
  owner.toggle("a"); await vi.waitFor(() => expect(calls.at(-1)?.path).toBe("a"));
  owner.toggle("b"); owner.open("note.txt");
  expect(calls.map(query => query.path)).toEqual([undefined, ".", "a"]);
  pending.resolve(result({ entries: [file("late.txt")] }));
  await vi.waitFor(() => expect(owner.getSnapshot().preview?.data?.text).toBe("private preview"));
  expect(maximum).toBe(1); expect(calls.map(query => query.path)).toEqual([undefined, ".", "a", "note.txt"]);
  expect(owner.getSnapshot().directories.get("a")?.rows).toEqual([]);
  expect(owner.getSnapshot().directories.get("b")?.rows).toEqual([]);
  owner.back(); expect(owner.getSnapshot().preview).toBeUndefined();
  expect(calls).toHaveLength(4); owner.dispose();
});

it("fences collapsed directory responses and reloads unobserved expansion", async () => {
  const pending = deferred<Observation>(); let reads = 0;
  const owner = await ready(async query => query.operation === FileOperation.Roots ? roots : query.path === "." ? result({ entries: [folder("a")] }) : ++reads === 1 ? pending.promise : result({ entries: [file("current.txt")] }));
  owner.toggle("a"); await vi.waitFor(() => expect(reads).toBe(1)); owner.toggle("a");
  pending.resolve(result({ entries: [file("obsolete.txt")] }));
  await Promise.resolve(); owner.toggle("a");
  await vi.waitFor(() => expect(owner.getSnapshot().directories.get("a")?.rows[0]?.name).toBe("current.txt"));
  expect(reads).toBe(2); owner.dispose();
});

it("restores accepted nested expansion metadata without reads", async () => {
  const read = vi.fn(async (query: WorkspaceQuery) => query.operation === FileOperation.Roots ? roots : query.path === "." ? result({ entries: [folder("a")] }) : query.path === "a" ? result({ entries: [folder("b")] }) : result({ entries: [file("note.txt")] }));
  const owner = await ready(read); owner.toggle("a"); await vi.waitFor(() => expect(owner.getSnapshot().directories.get("a")?.loaded).toBe(true));
  owner.toggle("a/b"); await vi.waitFor(() => expect(owner.getSnapshot().directories.get("a/b")?.loaded).toBe(true));
  const count = read.mock.calls.length; owner.toggle("a"); expect(owner.visible("a/b")).toBe(false);
  owner.toggle("a"); expect(owner.visible("a/b")).toBe(true); expect(read).toHaveBeenCalledTimes(count); owner.dispose();
});

it("refreshes the root then expanded directories sequentially and prunes only authoritative absence", async () => {
  let mode = "initial"; const calls: string[] = [];
  const owner = await ready(async query => {
    if (query.operation === FileOperation.Roots) return roots;
    calls.push(query.path!);
    if (query.path !== ".") return result({ entries: [file("child.txt")] });
    if (mode === "partial") return result({ entries: [], next: "next" });
    if (mode === "absent") return result();
    return result({ entries: [folder("a"), folder("b")] });
  });
  owner.toggle("a"); owner.toggle("b"); await vi.waitFor(() => expect(owner.getSnapshot().directories.get("b")?.loaded).toBe(true));
  calls.length = 0; mode = "partial"; await owner.refresh();
  expect(calls).toEqual([".", "a", "b"]); expect(owner.getSnapshot().expanded.has("a")).toBe(true);
  // Reload explicitly accepts a new root chain; its complete empty range proves absence.
  mode = "absent"; owner.reload("."); await vi.waitFor(() => expect(owner.getSnapshot().directories.size).toBe(1));
  expect(owner.getSnapshot().expanded.size).toBe(0); owner.dispose();
});

it("supersedes an unfinished expansion before an explicit ordered refresh", async () => {
  const pending = deferred<Observation>(); let reads = 0;
  const owner = await ready(async query => query.operation === FileOperation.Roots ? roots : query.path === "." ? result({ entries: [folder("a")] }) : ++reads === 1 ? pending.promise : result({ entries: [file("fresh.txt")] }));
  owner.toggle("a"); await vi.waitFor(() => expect(reads).toBe(1)); const refreshing = owner.refresh();
  pending.resolve(result({ entries: [file("old.txt")] })); await refreshing;
  expect(owner.getSnapshot().directories.get("a")?.rows[0]?.name).toBe("fresh.txt"); owner.dispose();
});

it("keeps links inert and fences repository replacement and disposal", async () => {
  const pending = deferred<Observation>(); const calls: WorkspaceQuery[] = [];
  const owner = await ready(async query => { calls.push(query); if (query.operation === FileOperation.Roots) return roots;
    if (query.repository_id === "first") return result({ entries: [file("new.txt")] });
    if (query.path === ".") return result({ entries: [folder("a"), { name: "link", kind: EntryKind.Link, size: "4" }] });
    return pending.promise;
  });
  owner.open("link"); owner.toggle("link"); expect(owner.getSnapshot().preview).toBeUndefined();
  owner.toggle("a"); await vi.waitFor(() => expect(calls.at(-1)?.path).toBe("a")); owner.selectRepository("first");
  pending.resolve(result({ entries: [file("old.txt")] })); await vi.waitFor(() => expect(owner.getSnapshot().directories.get(".")?.rows[0]?.name).toBe("new.txt"));
  expect(owner.getSnapshot().directories.has("a")).toBe(false); owner.dispose();
  expect(owner.getSnapshot().roots).toBeUndefined(); expect(owner.getSnapshot().directories.size).toBe(0);
});

it("discards pending preview bytes on Back and retains prior preview only on failed explicit refresh", async () => {
  const pending = deferred<Observation>(); let previews = 0;
  const owner = await ready(async query => query.operation === FileOperation.Roots ? roots : query.operation === FileOperation.Directory ? result({ entries: [file("note.txt")] }) : ++previews === 1 ? pending.promise : previews === 2 ? result({ text: "accepted" }) : Promise.reject(new Error("Unavailable")));
  owner.open("note.txt"); await vi.waitFor(() => expect(previews).toBe(1)); owner.back();
  pending.resolve(result({ text: "abandoned" })); await Promise.resolve(); owner.open("note.txt");
  await vi.waitFor(() => expect(owner.getSnapshot().preview?.data?.text).toBe("accepted")); await owner.refresh();
  expect(owner.getSnapshot().preview?.data?.text).toBe("accepted"); expect(owner.getSnapshot().preview?.error).toBeTruthy();
  owner.back(); expect(owner.getSnapshot().preview).toBeUndefined(); owner.dispose();
});

it("suspends original explorer reads while retaining only directory navigation metadata",async()=>{
 const read=vi.fn(async(query:WorkspaceQuery)=>query.operation===FileOperation.Roots?roots:result({entries:[file("note.txt"),folder("a")]}));
 const owner=await ready(read);owner.select("note.txt");const previous=owner.getSnapshot();const count=read.mock.calls.length;owner.suspend();owner.append(".");await new Promise(resolve=>setTimeout(resolve,0));expect(read.mock.calls).toHaveLength(count);expect(owner.getSnapshot().selected).toBe("note.txt");expect(owner.getSnapshot().preview).toBeUndefined();owner.start();await new Promise(resolve=>setTimeout(resolve,0));expect(read.mock.calls).toHaveLength(count);expect(owner.getSnapshot().repository).toBe(previous.repository);expect(owner.getSnapshot().directories.get(".")?.rows).toEqual(previous.directories.get(".")?.rows);owner.dispose();
});
