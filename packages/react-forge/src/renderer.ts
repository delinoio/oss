import { createContext, type ReactNode } from "react";
import Reconciler, { type HostConfig } from "react-reconciler";
import { ConcurrentRoot, DefaultEventPriority } from "react-reconciler/constants.js";
import { v7 } from "uuid";
import packageManifest from "../package.json" with { type: "json" };
import { ForgeError, abortable } from "./errors.js";
import { ErrorCode, limits, type NodeHandle } from "./types.js";

export interface HostNode {
  type: string;
  props: Record<string, unknown>;
  children: HostNode[];
  hidden: boolean;
  handle: NodeHandle;
}

export interface SerializedNode {
  id: string;
  type: string;
  props: Record<string, unknown>;
  children: SerializedNode[];
}

interface Container {
  children: HostNode[];
  documentId: string;
  changed(): void;
}

function append(parent: Pick<HostNode, "children">, child: HostNode) {
  const old = parent.children.indexOf(child);
  if (old !== -1) parent.children.splice(old, 1);
  parent.children.push(child);
}

function remove(parent: Pick<HostNode, "children">, child: HostNode) {
  const at = parent.children.indexOf(child);
  if (at !== -1) parent.children.splice(at, 1);
}

function insert(parent: Pick<HostNode, "children">, child: HostNode, before: HostNode) {
  remove(parent, child);
  const at = parent.children.indexOf(before);
  if (at === -1) throw new ForgeError(ErrorCode.Render, "Invalid React host insertion.");
  parent.children.splice(at, 0, child);
}

function makeNode(type: string, props: Record<string, unknown>, root: Container): HostNode {
  return { type, props, children: [], hidden: false,
    handle: Object.freeze({ documentId: root.documentId, nodeId: v7() }) };
}

let priority = 0;
const context = Object.freeze({});
const noop = () => {};
const config = {
  rendererVersion: packageManifest.version, rendererPackageName: "@delino/react-forge",
  isPrimaryRenderer: true, supportsMutation: true, supportsPersistence: false,
  supportsHydration: false, supportsResources: false, supportsSingletons: false,
  supportsMicrotasks: true, supportsTestSelectors: false, warnsIfNotActing: false,
  getRootHostContext: () => context,
  getChildHostContext: () => context,
  getPublicInstance: (node: HostNode) => node.handle,
  createInstance: makeNode,
  createTextInstance: (text: string, root: Container) => makeNode("#text", { text }, root),
  appendInitialChild: append,
  finalizeInitialChildren: () => false,
  shouldSetTextContent: () => false,
  prepareForCommit: () => null,
  resetAfterCommit: (root: Container) => root.changed(),
  appendChild: append, appendChildToContainer: append,
  removeChild: remove, removeChildFromContainer: remove,
  insertBefore: insert, insertInContainerBefore: insert,
  clearContainer: (root: Container) => { root.children = []; },
  commitUpdate: (node: HostNode, _type: string, _old: unknown, props: Record<string, unknown>) => { node.props = props; },
  commitTextUpdate: (node: HostNode, _old: string, text: string) => { node.props = { text }; },
  resetTextContent: (node: HostNode) => { node.children = []; },
  hideInstance: (node: HostNode) => { node.hidden = true; },
  hideTextInstance: (node: HostNode) => { node.hidden = true; },
  unhideInstance: (node: HostNode) => { node.hidden = false; },
  unhideTextInstance: (node: HostNode) => { node.hidden = false; },
  detachDeletedInstance: noop, commitMount: noop, preparePortalMount: noop,
  scheduleTimeout: setTimeout, cancelTimeout: clearTimeout, noTimeout: -1,
  scheduleMicrotask: queueMicrotask,
  setCurrentUpdatePriority: (value: number) => { priority = value; },
  getCurrentUpdatePriority: () => priority,
  resolveUpdatePriority: () => priority || DefaultEventPriority,
  shouldAttemptEagerTransition: () => false,
  maySuspendCommit: () => false,
  maySuspendCommitOnUpdate: () => false,
  maySuspendCommitInSyncRender: () => false,
  preloadInstance: () => true,
  startSuspendingCommit: noop, suspendInstance: noop,
  waitForCommitToBeReady: () => null,
  getSuspendedCommitReason: () => null,
  NotPendingTransition: null, HostTransitionContext: createContext(null),
  resetFormInstance: noop,
  requestPostPaintCallback: (callback: (time: number) => void) => setImmediate(() => callback(performance.now())),
  resolveEventType: () => null, resolveEventTimeStamp: () => -1.1,
  trackSchedulerEvent: noop,
};

// DefinitelyTyped currently describes 0.32. The exact pinned 0.33 host contract
// is implemented above and verified by the React behavior suite. Remove this
// adapter cast when matching 0.33 declarations are available.
const reconciler = Reconciler(config as unknown as HostConfig<
  string, Record<string, unknown>, Container, HostNode, HostNode, never, never,
  never, NodeHandle, object, unknown, ReturnType<typeof setTimeout>, -1, null
>);

interface Fiber {
  tag: number;
  memoizedState: unknown;
  child: Fiber | null;
  sibling: Fiber | null;
}

/** Version-pinned read-only inspection is necessary because React has no public
 * renderer API reporting unresolved Suspense boundaries. Never mutate Fibers.
 * A hidden Activity subtree is intentionally absent from the exported revision.
 */
function hasPendingBoundary(root: Fiber): boolean {
  const work: Fiber[] = [root];
  while (work.length) {
    const fiber = work.pop()!;
    if (fiber.sibling) work.push(fiber.sibling);
    if (fiber.tag === 22 && fiber.memoizedState !== null) continue;
    if (fiber.tag === 13 && fiber.memoizedState !== null) return true;
    if (fiber.child) work.push(fiber.child);
  }
  return false;
}

export class RenderRoot {
  private readonly listeners = new Set<() => void>();
  private readonly container: Container;
  private readonly root;
  private disposed = false;
  private error?: ForgeError;
  revision = 0;

  constructor(documentId: string) {
    this.container = { children: [], documentId, changed: () => {
      this.revision++;
      // React assigns root.current after resetAfterCommit; inspect it only after
      // the entire commit, including layout effects, has finished.
      queueMicrotask(() => this.notify());
    } };
    const uncaught = (error: unknown) => {
      // The cause stays outside ForgeError.toJSON(); only the trusted MCP
      // execution child may turn it into a bounded caller-facing diagnostic.
      this.error = new ForgeError(ErrorCode.Render, "React rendering failed. Correct the component and render again.", {}, error);
      this.notify();
    };
    this.root = reconciler.createContainer(this.container, ConcurrentRoot, null, false, null,
      `forge-${documentId}-`, uncaught, noop, uncaught, noop, null);
  }

  private notify() { for (const listener of this.listeners) listener(); }
  private assertActive() {
    if (this.disposed) throw new ForgeError(ErrorCode.Disposed, "The document session is disposed.");
    if (this.error) throw this.error;
  }

  async render(element: ReactNode): Promise<void> {
    if (this.disposed) this.assertActive();
    this.error = undefined;
    const committed = new Promise<void>((resolve, reject) => {
      const failed = () => {
        if (this.error || this.disposed) {
          this.listeners.delete(failed);
          reject(this.error ?? new ForgeError(ErrorCode.Disposed, "The document session is disposed."));
        }
      };
      this.listeners.add(failed);
      reconciler.updateContainer(element, this.root, null, () => {
        this.listeners.delete(failed);
        if (this.error) reject(this.error); else resolve();
      });
    });
    await committed;
    // The root error callback can follow the update callback in the same commit.
    this.assertActive();
  }

  private pending(): boolean {
    const root = this.root as unknown as { current: Fiber; pendingLanes: number };
    // OffscreenLane work belongs to hidden Activity and must not stall export.
    return (root.pendingLanes & ~536870912) !== 0 || hasPendingBoundary(root.current);
  }

  isSettled(): boolean {
    this.assertActive();
    return !this.pending();
  }

  async settled(signal?: AbortSignal): Promise<void> {
    this.assertActive();
    await abortable(new Promise<void>(resolve => setImmediate(resolve)), signal);
    reconciler.flushPassiveEffects();
    this.assertActive();
    if (!this.pending()) return;
    let changed!: () => void;
    const wait = new Promise<void>((resolve, reject) => {
      changed = () => {
        try {
          this.assertActive();
          if (!this.pending()) resolve();
        } catch (error) { reject(error); }
      };
      this.listeners.add(changed);
      changed();
    });
    try { await abortable(wait, signal); }
    finally { this.listeners.delete(changed); }
  }

  snapshot(): SerializedNode[] {
    this.assertActive();
    let count = 0;
    const copy = (node: HostNode, depth: number): SerializedNode => {
      if (++count > limits.treeNodes || depth > limits.treeDepth) {
        throw new ForgeError(ErrorCode.ResourceLimit, "Rendered tree node or depth limit exceeded.");
      }
      const props: Record<string, unknown> = {};
      for (const [key, value] of Object.entries(node.props)) {
        if (key !== "children" && key !== "ref") props[key] = value;
      }
      return { id: node.handle.nodeId, type: node.type, props,
        children: node.children.filter(n => !n.hidden).map(n => copy(n, depth + 1)) };
    };
    try {
      const tree = this.container.children.filter(n => !n.hidden).map(n => copy(n, 1));
      const json = JSON.stringify(tree, (_key, value: unknown) => {
        if (typeof value === "function" || typeof value === "symbol" || typeof value === "bigint" ||
          (typeof value === "number" && !Number.isFinite(value))) {
          throw new ForgeError(ErrorCode.MalformedInput, "Document props must be finite serializable values.");
        }
        return value;
      });
      if (Buffer.byteLength(json) > limits.treeBytes) throw new ForgeError(ErrorCode.ResourceLimit, "Rendered tree exceeds 16 MiB.");
      return JSON.parse(json) as SerializedNode[];
    } catch (error) {
      if (error instanceof ForgeError) throw error;
      throw new ForgeError(ErrorCode.MalformedInput, "Document props contain unsupported or cyclic values.");
    }
  }

  async dispose(): Promise<void> {
    if (this.disposed) return;
    this.disposed = true;
    this.notify();
    await new Promise<void>(resolve => reconciler.updateContainer(null, this.root, null, resolve));
    reconciler.flushPassiveEffects();
    this.listeners.clear();
  }
}
