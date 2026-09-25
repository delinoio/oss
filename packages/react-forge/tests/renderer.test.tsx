import assert from "node:assert/strict";
import test from "node:test";
import React, { Activity, Component, Fragment, Profiler, StrictMode, Suspense, createContext, createElement,
  createRef, lazy, memo, startTransition, use, useContext, useEffect, useImperativeHandle,
  useActionState, useDeferredValue, useLayoutEffect, useReducer, useState, useSyncExternalStore, type ReactNode } from "react";
import { RenderRoot } from "../src/renderer.js";
import { ErrorCode, type NodeHandle } from "../src/types.js";
import { ForgeError } from "../src/errors.js";

function contents(root: RenderRoot): string {
  return JSON.stringify(root.snapshot());
}

test("real reconciliation preserves keyed identities, Context, state, memo, refs and effects", async () => {
  const root = new RenderRoot("session");
  const context = createContext("default");
  const node = createRef<NodeHandle>();
  let increment!: () => void;
  let cleanup = 0;
  let layout = 0;
  let profiled = 0;
  const Child = memo(function Child() {
    const value = useContext(context);
    const [count, add] = useReducer((v: number) => v + 1, 0);
    increment = add;
    useEffect(() => () => { cleanup++; }, []);
    useLayoutEffect(() => { layout++; });
    return createElement("text", { ref: node }, `${value}:${count}`);
  });
  await root.render(<StrictMode><Profiler id="test" onRender={() => profiled++}>
    <context.Provider value="context"><Fragment><Child key="stable" /></Fragment></context.Provider>
  </Profiler></StrictMode>);
  await root.settled();
  assert.match(contents(root), /context:0/);
  const id = node.current?.nodeId;
  assert.match(id!, /^[0-9a-f]{8}-[0-9a-f]{4}-7/);
  increment();
  await root.settled();
  assert.match(contents(root), /context:1/);
  assert.equal(node.current?.nodeId, id);
  assert.ok(layout >= 2 && profiled >= 2);
  await root.dispose();
  assert.equal(node.current, null);
  assert.ok(cleanup >= 1);
});

test("Suspense waits for use promises and lazy components without exporting fallback", async () => {
  const root = new RenderRoot("session");
  const pending = Promise.withResolvers<string>();
  function Async() { return createElement("text", null, use(pending.promise)); }
  const Lazy = lazy(async () => ({ default: Async }));
  await root.render(<Suspense fallback={createElement("text", null, "waiting")}><Lazy /></Suspense>);
  let settled = false;
  const ready = root.settled().then(() => { settled = true; });
  await new Promise(resolve => setTimeout(resolve, 20));
  assert.equal(settled, false);
  pending.resolve("resolved");
  await ready;
  assert.match(contents(root), /resolved/);
  assert.doesNotMatch(contents(root), /waiting/);
  await root.dispose();
});

test("unresolved Suspense is cancellable and disposal releases waiting operations", async () => {
  const root = new RenderRoot("session");
  const forever = new Promise<string>(() => {});
  function Pending() { return createElement("text", null, use(forever)); }
  await root.render(<Suspense fallback={null}><Pending /></Suspense>);
  const controller = new AbortController();
  const waiting = root.settled(controller.signal);
  controller.abort(new Error("sensitive signal reason"));
  await assert.rejects(waiting, { code: ErrorCode.Cancelled });
  const disposed = root.settled();
  await root.dispose();
  await assert.rejects(disposed, { code: ErrorCode.Disposed });
});

test("Activity excludes hidden nodes, retains state and restores effects", async () => {
  const root = new RenderRoot("session");
  let update!: (value: number) => void;
  function Counter() {
    const [value, set] = useState(7); update = set;
    return createElement("text", null, `counter:${value}`);
  }
  const view = (hidden: boolean) => <Activity mode={hidden ? "hidden" : "visible"}><Counter /></Activity>;
  await root.render(view(false)); await root.settled();
  update(9); await root.settled();
  await root.render(view(true)); await root.settled();
  assert.equal(root.snapshot().length, 0);
  await root.render(view(false)); await root.settled();
  assert.match(contents(root), /counter:9/);
  await root.dispose();
});

test("transitions and external-store subscriptions commit current state", async () => {
  const root = new RenderRoot("session");
  const listeners = new Set<() => void>();
  let value = "first";
  function Store() {
    const current = useSyncExternalStore(callback => { listeners.add(callback); return () => listeners.delete(callback); }, () => value);
    return createElement("text", null, current);
  }
  await root.render(<Store />); await root.settled();
  startTransition(() => { value = "second"; for (const callback of listeners) callback(); });
  await root.settled();
  assert.match(contents(root), /second/);
  await root.dispose();
  assert.equal(listeners.size, 0);
});

test("class error boundaries recover; uncaught latest render is never stale success", async () => {
  const root = new RenderRoot("session");
  class Boundary extends Component<{ children: ReactNode }, { failed: boolean }> {
    state = { failed: false };
    static getDerivedStateFromError() { return { failed: true }; }
    render() { return this.state.failed ? createElement("text", null, "recovered") : this.props.children; }
  }
  function Broken(): ReactNode { throw new Error("private document content"); }
  await root.render(<Boundary><Broken /></Boundary>); await root.settled();
  assert.match(contents(root), /recovered/);
  await assert.rejects(root.render(<Broken />), (error: unknown) => {
    assert.ok(error instanceof ForgeError);
    assert.doesNotMatch(error.message, /private/);
    return error.code === ErrorCode.Render;
  });
  assert.throws(() => root.snapshot(), { code: ErrorCode.Render });
  await root.render(createElement("text", null, "retry")); await root.settled();
  assert.match(contents(root), /retry/);
  await root.dispose();
});

test("imperative handles use React commit lifetime", async () => {
  const root = new RenderRoot("session");
  const ref = createRef<{ value: number }>();
  function Handle() {
    useImperativeHandle(ref, () => ({ value: 42 }), []);
    return createElement("text", null, "handle");
  }
  await root.render(<Handle />); await root.settled();
  assert.equal(ref.current?.value, 42);
  await root.dispose(); assert.equal(ref.current, null);
});

test("tree limits and nonserializable host props fail with typed diagnostics", async () => {
  const root = new RenderRoot("session");
  await root.render(createElement("text", { onClick: () => {} }, "invalid"));
  assert.throws(() => root.snapshot(), { code: ErrorCode.MalformedInput });
  let deep: ReactNode = "leaf";
  for (let i = 0; i < 50; i++) deep = createElement("column", null, deep);
  await root.render(deep);
  assert.throws(() => root.snapshot(), { code: ErrorCode.ResourceLimit });
  await root.dispose();
});


test("deferred Suspense updates wait for the current transition instead of exporting stale content", async () => {
  const root = new RenderRoot("session");
  const pending = Promise.withResolvers<string>();
  let update!: (value: string) => void;
  function Value({ value }: { value: string }) { return createElement("text", null, value === "new" ? use(pending.promise) : "old"); }
  function App() { const [value, set] = useState("old"); update = set; const deferred = useDeferredValue(value); return <Suspense fallback={createElement("text", null, "fallback")}><Value value={deferred} /></Suspense>; }
  try {
    await root.render(<App />); await root.settled(); update("new");
    let settled = false; const ready = root.settled().then(() => { settled = true; });
    await new Promise(resolve => setTimeout(resolve, 20)); assert.equal(settled, false);
    pending.resolve("resolved deferred value"); await ready; assert.match(contents(root), /resolved deferred value/);
  } finally { await root.dispose(); }
});

test("action-state dispatches run in transitions and commit their awaited result", async () => {
  const root = new RenderRoot("session");
  const pending = Promise.withResolvers<void>();
  let dispatch!: (value: string) => void;
  function App() { const [value, submit, busy] = useActionState(async (_old: string, next: string) => { await pending.promise; return next; }, "initial"); dispatch = submit; return createElement("text", null, `${value}:${busy}`); }
  try {
    await root.render(<App />); await root.settled(); startTransition(() => dispatch("completed action"));
    let settled = false; const ready = root.settled().then(() => { settled = true; });
    await new Promise(resolve => setTimeout(resolve, 20)); assert.equal(settled, false);
    pending.resolve(); await ready; assert.match(contents(root), /completed action:false/);
  } finally { await root.dispose(); }
});

test("rejected use promises reach error boundaries and can recover in a fresh render", async () => {
  const root = new RenderRoot("session");
  const pending = Promise.withResolvers<string>();
  class Boundary extends Component<{ children: ReactNode }, { failed: boolean }> {
    state = { failed: false }; static getDerivedStateFromError() { return { failed: true }; }
    render() { return this.state.failed ? createElement("text", null, "recovered promise") : this.props.children; }
  }
  function Async() { return createElement("text", null, use(pending.promise)); }
  try {
    await root.render(<Boundary><Suspense fallback={null}><Async /></Suspense></Boundary>);
    const ready = root.settled(); pending.reject(new Error("private rejected payload")); await ready;
    assert.match(contents(root), /recovered promise/); assert.doesNotMatch(contents(root), /private rejected payload/);
    await root.render(createElement("text", null, "retry")); await root.settled(); assert.match(contents(root), /retry/);
  } finally { await root.dispose(); }
});


test("rendered byte and node ceilings are enforced before native processing", async () => {
  const root = new RenderRoot("session");
  try {
    await root.render(createElement("text", null, "x".repeat(16 * 1024 * 1024)));
    assert.throws(() => root.snapshot(), { code: ErrorCode.ResourceLimit });
    await root.render(createElement(React.Fragment, null, ...Array.from({ length: 20_001 }, (_, key) => createElement("shape", { key }))));
    assert.throws(() => root.snapshot(), { code: ErrorCode.ResourceLimit });
  } finally { await root.dispose(); }
});
