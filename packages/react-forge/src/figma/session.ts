import type { ReactNode } from "react";
import { v7 } from "uuid";
import { lstat } from "node:fs/promises";
import { ForgeError, abortable, checkSignal } from "../errors.js";
import {
  digest,
  readSource,
  publish as publishLocal,
  withOutputReservation,
} from "../files.js";
import { RenderRoot } from "../renderer.js";
import { planFigma, validateFigmaImage } from "../native.js";
import {
  Format,
  Stage,
  ErrorCode,
  limits,
  type AssetHandle,
  type AssetSource,
  type Diagnostic,
  type Geometry,
  type NodeHandle,
} from "../types.js";
import { CredentialReader, type CredentialOptions } from "./credentials.js";
import {
  CallSafety,
  OfficialFigmaConnection,
  RemoteError,
  withFigmaFile,
  type FigmaConnection,
} from "./transport.js";
import {
  FigmaKind,
  resourceKinds,
  model,
  type Entity,
  type Snapshot,
  type Batch,
  type Operation,
  type Plan,
} from "./model.js";
import { script, runtime } from "./runtime.js";

export enum PublishStatus {
  Complete = "complete",
  Partial = "partial",
  Unknown = "unknown",
}
export interface FigmaReceipt {
  version: 1;
  format: Format.Figma;
  documentId: string;
  fileKey?: string;
  url?: string;
  revision: number;
  status: PublishStatus;
  bindings: Record<string, string>;
  kinds: Record<string, FigmaKind>;
  fingerprints: Record<string, string>;
  imageHashes?: Record<string, string>;
  createdNodeIds: string[];
  mutatedNodeIds: string[];
  deletedNodeIds: string[];
  calls: number;
  retries: number;
  waitMs: number;
}
export interface FigmaOptions {
  fileName?: string;
  planKey?: string;
  projectId?: string;
  credentials?: CredentialOptions;
}
export interface FigmaTarget extends NodeHandle {
  readonly remoteId: string;
  readonly kind: FigmaKind;
  readonly name: string;
  readonly editable: boolean;
  readonly pageId?: string;
  readonly parentId?: string;
}
interface Mount {
  target: FigmaTarget;
  root: RenderRoot;
  keys: Set<string>;
}
const publicationBrand = Symbol.for("react-forge.figma.publish-error.v1");
export class FigmaPublishError extends ForgeError {
  readonly [publicationBrand] = true;
  static override [Symbol.hasInstance](
    value: unknown,
  ): value is FigmaPublishError {
    return !!value && typeof value === "object" && publicationBrand in value;
  }
  constructor(
    code: ErrorCode,
    readonly receipt: FigmaReceipt,
  ) {
    super(
      code,
      `Figma publication ${receipt.status}. Inspect the receipt and remote file before continuing.`,
      {
        format: Format.Figma,
        revision: receipt.revision,
        published:
          receipt.status !== PublishStatus.Unknown ||
          receipt.createdNodeIds.length +
            receipt.mutatedNodeIds.length +
            receipt.deletedNodeIds.length >
            0
            ? true
            : undefined,
      },
    );
  }
  override toJSON() {
    return { ...super.toJSON(), receipt: this.receipt };
  }
}
const canonical = (v: unknown): string =>
  JSON.stringify(v, (_k, x) =>
    x && typeof x === "object" && !Array.isArray(x)
      ? Object.fromEntries(
          Object.keys(x)
            .sort()
            .map((k) => [k, x[k]]),
        )
      : x,
  );
const fingerprint = (v: Snapshot) => digest(Buffer.from(canonical(v)));
const validRemote = (v: unknown): v is string =>
  typeof v === "string" && v.length <= 256 && /^[a-zA-Z0-9:;_,.-]+$/.test(v);
function fileKey(source: string): string {
  if (/^[a-zA-Z0-9]{22,128}$/.test(source)) return source;
  try {
    const u = new URL(source);
    const match =
      /^\/(?:design|file)\/([a-zA-Z0-9]{22,128})(?:\/branch\/([a-zA-Z0-9]{22,128}))?(?:\/|$)/.exec(
        u.pathname,
      );
    if (
      u.protocol === "https:" &&
      ["figma.com", "www.figma.com"].includes(u.hostname) &&
      !u.username &&
      !u.password &&
      !u.port &&
      match
    )
      return match[2] ?? match[1]!;
  } catch {}
  throw new ForgeError(
    ErrorCode.MalformedInput,
    "Expected a Figma Design file key or HTTPS URL.",
  );
}

export class FigmaSession {
  readonly format = Format.Figma;
  readonly documentId = v7();
  private root = new RenderRoot(this.documentId);
  private readonly disposal = new AbortController();
  private readonly mounts = new Map<string, Mount>();
  private readonly retained = new Map<string, Entity>();
  private readonly assets = new Map<string, Buffer>();
  private readonly imageHashes: Record<string, string> = {};
  private readonly pendingAssets = new Set<Promise<AssetHandle>>();
  private readonly listeners = new Set<(event: Diagnostic) => void>();
  private readonly targets = new Map<string, FigmaTarget>();
  private readonly snapshots = new Map<string, Snapshot>();
  private readonly pages = new Map<string, string>();
  private readonly reads = new Map<
    string,
    Promise<ReturnType<FigmaSession["inspect"]>>
  >();
  private readonly kinds: Record<string, FigmaKind> = {};
  private bindings: Record<string, string> = {};
  private readonly handleKeys = new Map<string, string>();
  private previous: Entity[] = [];
  private queue: Promise<unknown> = Promise.resolve();
  private publication: Promise<unknown> = Promise.resolve();
  private currentRevision = 0;
  private batchCount = 0;
  private changeCount = 0;
  private signature = "";
  private key?: string;
  private unknown = false;
  private disposalTask?: Promise<void>;
  private last?: FigmaReceipt;
  private readonly connection: FigmaConnection;
  constructor(
    private readonly options: FigmaOptions = {},
    connection?: FigmaConnection,
    private readonly uploadFetch: typeof fetch = fetch,
  ) {
    this.connection =
      connection ??
      new OfficialFigmaConnection(
        new CredentialReader(options.credentials),
        undefined,
        undefined,
        options.planKey,
      );
  }
  get revision() {
    return this.currentRevision;
  }
  get receipt(): FigmaReceipt | undefined {
    return this.last ? structuredClone(this.last) : undefined;
  }
  private signal(signal?: AbortSignal) {
    if (this.disposal.signal.aborted)
      throw new ForgeError(
        ErrorCode.Disposed,
        "The Figma session is disposed.",
      );
    return signal
      ? AbortSignal.any([signal, this.disposal.signal])
      : this.disposal.signal;
  }
  private mutate<T>(work: () => Promise<T>): Promise<T> {
    this.signal();
    const result = this.queue.then(() => {
      this.signal();
      return work();
    });
    this.queue = result.catch(() => {});
    return result;
  }
  subscribe(listener: (event: Diagnostic) => void) {
    this.signal();
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }
  private emit(stage: Stage, start: number, code?: ErrorCode) {
    const event: Diagnostic = Object.freeze({
      source: "javascript",
      format: this.format,
      stage,
      revision: this.revision,
      durationMs: performance.now() - start,
      code,
      calls: this.connection.stats.calls,
      retries: this.connection.stats.retries,
      waitMs: this.connection.stats.waitMs,
      batches: this.batchCount,
      changes: this.changeCount,
    });
    for (const listener of this.listeners)
      try {
        listener(event);
      } catch {}
  }
  render(children: ReactNode) {
    return this.mutate(() => this.root.render(children));
  }
  registerImage(
    source: AssetSource,
    options: { signal?: AbortSignal } = {},
  ): Promise<AssetHandle> {
    const signal = this.signal(options.signal);
    const task = (async () => {
      const { bytes } = await readSource(source, 10 * 1024 * 1024, signal);
      const id = `sha256:${digest(bytes)}`;
      if (
        !this.assets.has(id) &&
        [...this.assets.values()].reduce((a, b) => a + b.length, bytes.length) >
          limits.officeBytes
      )
        throw new ForgeError(
          ErrorCode.ResourceLimit,
          "Figma images exceed the asset budget.",
        );
      if (
        !bytes
          .subarray(0, 8)
          .equals(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10])) &&
        !bytes.subarray(0, 3).equals(Buffer.from([255, 216, 255]))
      )
        throw new ForgeError(
          ErrorCode.MalformedInput,
          "Figma images must be PNG or JPEG.",
        );
      if (!this.assets.has(id)) await validateFigmaImage(bytes, signal);
      this.assets.set(id, bytes);
      return Object.freeze({ assetId: id, documentId: this.documentId });
    })();
    this.pendingAssets.add(task);
    void task.finally(() => this.pendingAssets.delete(task)).catch(() => {});
    return task;
  }
  private accept(nodes: Snapshot[], pageId?: string) {
    for (const n of nodes) {
      if (
        !validRemote(n.id) ||
        typeof n.type !== "string" ||
        !Array.isArray(n.children) ||
        !n.props ||
        typeof n.props !== "object"
      )
        throw new RemoteError(ErrorCode.Remote, false);
      this.snapshots.set(n.id, n);
      if (pageId) this.pages.set(n.id, pageId);
      if (n.type === "PAGE") this.pages.set(n.id, n.id);
      let key = Object.keys(this.bindings).find(
        (key) => this.bindings[key] === n.id,
      );
      const kind = n.type as FigmaKind;
      const supported = Object.values(FigmaKind).includes(kind);
      if (!key) {
        key = v7();
        this.bindings[key] = n.id;
      }
      this.kinds[key] = kind;
      if (!this.targets.has(key))
        this.targets.set(
          key,
          Object.freeze({
            documentId: this.documentId,
            nodeId: key,
            remoteId: n.id,
            kind,
            name: String(n.props.name ?? ""),
            editable: supported,
            pageId: this.pages.get(n.id),
            parentId: n.parent ?? undefined,
          }),
        );
    }
  }
  inspect() {
    this.signal();
    return Object.freeze({
      revision: this.revision,
      targets: Object.freeze([...this.targets.values()]),
    });
  }
  refresh(
    options: {
      pageId?: string;
      resources?: boolean;
      nodeIds?: string[];
      signal?: AbortSignal;
    } = {},
  ) {
    const signal = this.signal(options.signal);
    const { signal: _signal, ...selection } = options;
    const key = canonical(selection);
    let pending = this.reads.get(key);
    if (!pending) {
      pending = this.readRemote({ ...selection, signal: this.disposal.signal });
      this.reads.set(key, pending);
      void pending.finally(() => this.reads.delete(key)).catch(() => {});
    }
    return abortable(pending, signal);
  }
  private async readRemote(
    options: {
      pageId?: string;
      resources?: boolean;
      nodeIds?: string[];
      signal?: AbortSignal;
    } = {},
  ) {
    const signal = this.signal(options.signal);
    if (!this.key)
      throw new ForgeError(
        ErrorCode.InvalidTarget,
        "Publish the new file before remote inspection.",
      );
    if (
      options.nodeIds &&
      (options.nodeIds.length > 24 ||
        options.nodeIds.some((id) => !validRemote(id)))
    )
      throw new ForgeError(
        ErrorCode.InvalidTarget,
        "Select at most 24 valid Figma node IDs per inspection.",
      );
    const input = {
      ...(options.pageId ? { page: `@${options.pageId}` } : {}),
      ...(options.nodeIds
        ? { targets: options.nodeIds.map((id) => ({ id })) }
        : {}),
    };
    let offset: number | null = 0;
    do {
      const value = await this.connection.call(
        "use_figma",
        {
          fileKey: this.key,
          code: script({
            mode: "inspect",
            bindings: {},
            offset,
            resources: options.resources,
            ...input,
          }),
          description: "Inspect selected Figma page",
          skillNames: "figma-use",
        },
        CallSafety.Read,
        signal,
      );
      if (!Array.isArray(value.nodes))
        throw new RemoteError(ErrorCode.Remote, false);
      this.accept(value.nodes, options.pageId);
      offset = value.nextOffset ?? null;
      if (
        offset !== null &&
        (!Number.isInteger(offset) || offset < 0 || offset > 20000)
      )
        throw new RemoteError(ErrorCode.ResourceLimit, true);
    } while (offset !== null);
    return this.inspect();
  }
  static async open(
    source: string | FigmaReceipt,
    options: FigmaOptions & { signal?: AbortSignal } = {},
    connection?: FigmaConnection,
  ) {
    const session = new FigmaSession(options, connection);
    try {
      if (typeof source === "string") session.key = fileKey(source);
      else {
        if (
          source.version !== 1 ||
          source.format !== Format.Figma ||
          !source.fileKey
        )
          throw new ForgeError(
            ErrorCode.MalformedInput,
            "Invalid Figma publication receipt.",
          );
        session.key = fileKey(source.fileKey);
        for (const [hash, image] of Object.entries(source.imageHashes ?? {})) {
          if (!/^sha256:[0-9a-f]{64}$/.test(hash) || !validRemote(image))
            throw new ForgeError(
              ErrorCode.MalformedInput,
              "Invalid Figma image mapping.",
            );
          session.imageHashes[hash] = image;
        }
        for (const [key, id] of Object.entries(source.bindings)) {
          if (!/^[0-9a-f-]{36}$/.test(key) || !validRemote(id))
            throw new ForgeError(
              ErrorCode.MalformedInput,
              "Invalid Figma receipt binding.",
            );
          session.bindings[key] = id;
          session.kinds[key] = source.kinds[key]!;
        }
      }
      await session.connection.connect(session.signal(options.signal));
      await session.refresh({ signal: options.signal });
      return session;
    } catch (error) {
      await session.dispose();
      throw error;
    }
  }
  mount(target: FigmaTarget, children: ReactNode) {
    return this.mutate(async () => {
      if (
        this.targets.get(target.nodeId) !== target ||
        !target.editable ||
        resourceKinds.has(target.kind)
      )
        throw new ForgeError(
          ErrorCode.InvalidTarget,
          "Invalid, stale or unsupported Figma target.",
        );
      const ancestors = (id: string) => {
        const found = new Set<string>();
        let cursor: string | null = id;
        while (cursor && !found.has(cursor)) {
          found.add(cursor);
          cursor = this.snapshots.get(cursor)?.parent ?? null;
        }
        return found;
      };
      for (const existing of this.mounts.values())
        if (
          ancestors(target.remoteId).has(existing.target.remoteId) ||
          ancestors(existing.target.remoteId).has(target.remoteId)
        )
          throw new ForgeError(
            ErrorCode.Conflict,
            "Figma mounted regions overlap.",
          );
      const root = new RenderRoot(this.documentId);
      const mount = { target, root, keys: new Set<string>() };
      this.mounts.set(target.nodeId, mount);
      try {
        await root.render(children);
        this.signal();
      } catch (error) {
        this.mounts.delete(target.nodeId);
        await root.dispose();
        throw error;
      }
      // Unmount retained these entities only to prevent deletion by omission.
      // A new mount selects ownership afresh: omitted descendants become foreign,
      // and explicitly selected descendants must not coexist with old owners.
      const released = new Set<string>();
      for (const key of this.retained.keys()) {
        const id = this.bindings[key];
        if (id && ancestors(id).has(target.remoteId)) {
          released.add(key);
          this.retained.delete(key);
        }
      }
      this.previous = this.previous.filter((entity) => !released.has(entity.key));
      let unmounted = false;
      return Object.freeze({
        render: (next: ReactNode) =>
          this.mutate(() => {
            if (unmounted)
              throw new ForgeError(
                ErrorCode.InvalidTarget,
                "The Figma mount is inactive.",
              );
            return root.render(next);
          }),
        unmount: () =>
          this.mutate(async () => {
            if (unmounted) return;
            unmounted = true;
            for (const e of this.previous)
              if (mount.keys.has(e.key)) this.retained.set(e.key, e);
            this.mounts.delete(target.nodeId);
            await root.dispose();
          }),
      });
    });
  }
  private async prepare(signal: AbortSignal): Promise<Entity[]> {
    for (;;) {
      const queue = this.queue;
      await abortable(queue, signal);
      await abortable(Promise.all(this.pendingAssets), signal);
      await Promise.all(
        [this.root, ...[...this.mounts.values()].map((m) => m.root)].map((r) =>
          r.settled(signal),
        ),
      );
      checkSignal(signal);
      if (queue === this.queue && !this.pendingAssets.size) break;
    }
    const bindings = { ...this.bindings };
    const desired = [
      ...this.retained.values(),
      ...model(this.root.snapshot(), this.documentId, bindings),
    ];
    for (const m of this.mounts.values()) {
      const nodes = m.root.snapshot();
      if (nodes.length !== 1)
        throw new ForgeError(
          ErrorCode.UnsupportedEdit,
          "A Figma mount must render one node matching the selected kind.",
        );
      this.handleKeys.set(nodes[0]!.id, m.target.nodeId);
      const base = this.snapshots.get(m.target.remoteId)!;
      const entities = model(
        nodes,
        this.documentId,
        bindings,
        base.parent ? `@${base.parent}` : null,
        m.target.pageId ? `@${m.target.pageId}` : null,
        m.target.nodeId,
      );
      if (entities[0]?.kind !== m.target.kind)
        throw new ForgeError(
          ErrorCode.UnsupportedEdit,
          "Figma mounted nodes must retain their kind.",
        );
      m.keys = new Set(entities.map((e) => e.key));
      desired.push(...entities);
    }
    const desiredKinds = new Map(desired.map((e) => [e.key, e.kind]));
    for (const old of this.previous)
      if (
        !desiredKinds.has(old.key) &&
        [FigmaKind.Component, FigmaKind.ComponentSet].includes(old.kind)
      )
        throw new ForgeError(
          ErrorCode.UnsupportedEdit,
          "Component removal cannot prove preservation of external instances.",
        );
    const referenceKind = (key: string) =>
      desiredKinds.get(key) ??
      this.snapshots.get(
        key.startsWith("@") ? key.slice(1) : (bindings[key] ?? ""),
      )?.type;
    for (const entity of desired) {
      const refs: [string, FigmaKind][] = [];
      for (const [field, kind] of [
        ["component", FigmaKind.Component],
        ["collection", FigmaKind.Collection],
        ["fillStyle", FigmaKind.PaintStyle],
        ["textStyle", FigmaKind.TextStyle],
      ] as const)
        if (entity.props[field]) refs.push([entity.props[field], kind]);
      for (const key of Object.values(entity.props.bindings ?? {}))
        refs.push([String(key), FigmaKind.Variable]);
      for (const key of entity.props.variants ?? [])
        refs.push([key, FigmaKind.Component]);
      if (refs.some(([key, kind]) => referenceKind(key) !== kind))
        throw new ForgeError(
          ErrorCode.InvalidTarget,
          "Inspect and select a Figma reference of the expected kind.",
        );
    }
    const explicit = new Set<string>();
    for (const e of desired) {
      if (e.props.image && !this.assets.has(e.props.image))
        throw new ForgeError(
          ErrorCode.InvalidTarget,
          "Register the image asset in this session before publication.",
        );
      const remote = bindings[e.key];
      if (remote) {
        if (explicit.has(remote))
          throw new ForgeError(ErrorCode.Conflict, "Figma targets overlap.");
        explicit.add(remote);
        const snapshot = this.snapshots.get(remote);
        if (!snapshot)
          throw new ForgeError(
            ErrorCode.InvalidTarget,
            "Inspect the target page before binding an existing node.",
          );
        if (e.kind !== FigmaKind.Page && !resourceKinds.has(e.kind)) {
          const parent = e.parent?.startsWith("@")
            ? e.parent.slice(1)
            : e.parent
              ? bindings[e.parent]
              : undefined;
          const variantOwner = desired.find(
            (set) =>
              set.kind === FigmaKind.ComponentSet &&
              set.props.variants?.includes(e.key),
          );
          if (
            snapshot.parent !== parent &&
            (!variantOwner || snapshot.parent !== bindings[variantOwner.key])
          )
            throw new ForgeError(
              ErrorCode.UnsupportedEdit,
              "Existing targets cannot move between parents.",
            );
        }
        if (snapshot.type !== e.kind)
          throw new ForgeError(
            ErrorCode.UnsupportedEdit,
            "Existing Figma nodes must retain their kind.",
          );
      }
      this.kinds[e.key] = e.kind;
    }
    this.bindings = bindings;
    const signature = canonical(desired);
    if (Buffer.byteLength(signature) > limits.treeBytes)
      throw new ForgeError(
        ErrorCode.ResourceLimit,
        "Figma model exceeds 16 MiB.",
      );
    if (signature !== this.signature) {
      this.currentRevision++;
      this.signature = signature;
    }
    return structuredClone(desired);
  }
  private makeReceipt(
    status: PublishStatus,
    changed: {
      createdNodeIds: string[];
      mutatedNodeIds: string[];
      deletedNodeIds: string[];
    },
  ): FigmaReceipt {
    const value: FigmaReceipt = {
      version: 1,
      format: Format.Figma,
      documentId: this.documentId,
      fileKey: this.key,
      url: this.key ? `https://www.figma.com/design/${this.key}` : undefined,
      revision: this.revision,
      status,
      bindings: { ...this.bindings },
      kinds: { ...this.kinds },
      imageHashes: { ...this.imageHashes },
      fingerprints: Object.fromEntries(
        [...this.snapshots].map(([id, s]) => [id, fingerprint(s)]),
      ),
      createdNodeIds: [...new Set(changed.createdNodeIds)],
      mutatedNodeIds: [...new Set(changed.mutatedNodeIds)],
      deletedNodeIds: [...new Set(changed.deletedNodeIds)],
      ...this.connection.stats,
    };
    this.last = structuredClone(value);
    return value;
  }
  publish(options: { signal?: AbortSignal } = {}): Promise<FigmaReceipt> {
    const signal = this.signal(options.signal);
    const run = this.publication
      .catch(() => {})
      .then(() =>
        withFigmaFile(this.key ?? this.documentId, signal, () =>
          this.perform(signal),
        ),
      );
    this.publication = run;
    return run;
  }
  private async perform(signal: AbortSignal): Promise<FigmaReceipt> {
    const start = performance.now();
    const changes = {
      createdNodeIds: [] as string[],
      mutatedNodeIds: [] as string[],
      deletedNodeIds: [] as string[],
    };
    let wrote = false;
    this.batchCount = 0;
    this.changeCount = 0;
    try {
      if (this.unknown)
        throw new ForgeError(
          ErrorCode.UnknownOutcome,
          "Reopen and inspect the Figma file after an unknown outcome.",
        );
      const desired = await this.prepare(signal);
      const revision = this.revision;
      // Native validation precedes file creation or any other remote write.
      await planFigma(
        {
          desired,
          previous: this.previous,
          bindings: this.bindings,
          payloadLimit: 30_000,
        },
        signal,
      );
      await this.connection.connect(signal);
      if (!this.key) {
        const { fileName, planKey, projectId } = this.options;
        if (
          !fileName ||
          fileName.length > 256 ||
          !planKey ||
          !/^(team|organization)::\d+$/.test(planKey)
        )
          throw new ForgeError(
            ErrorCode.MalformedInput,
            "New Figma files require fileName and an explicit planKey from whoami.",
          );
        const identity = await this.connection.call(
          "whoami",
          {},
          CallSafety.Read,
          signal,
        );
        if (!identity.plans?.some((p: any) => p.key === planKey))
          throw new ForgeError(
            ErrorCode.PermissionDenied,
            "The selected Figma plan is not available to this account.",
          );
        try {
          const file = await this.connection.call(
            "create_new_file",
            {
              fileName,
              planKey,
              editorType: "design",
              ...(projectId ? { projectId } : {}),
            },
            CallSafety.Write,
            signal,
          );
          this.key = fileKey(file.file_key ?? file.fileKey ?? file.key);
          wrote = true;
        } catch (error) {
          if (!(error instanceof RemoteError && error.safeToRetry)) {
            this.unknown = true;
            throw new FigmaPublishError(
              ErrorCode.UnknownOutcome,
              this.makeReceipt(PublishStatus.Unknown, changes),
            );
          }
          throw error;
        }
      }
      // Freeze revision before awaiting MCP; subsequent React commits belong to
      // the next publish, not this plan.
      const plan = await planFigma(
        {
          desired,
          previous: this.previous,
          bindings: this.bindings,
          payloadLimit: Math.max(
            512,
            this.connection.codeLimit - runtime.length - 3000,
          ),
        },
        signal,
      );
      this.changeCount = plan.operationCount;
      // Check every already-known region before the first mutation. Batch
      // guards repeat the check because remote publication is not atomic.
      const checks = new Map<
        string,
        Record<string, Pick<Snapshot, "id" | "type" | "stateHash">>
      >();
      for (const batch of plan.batches) {
        const page = batch.page ?? "";
        const expected = this.input(batch).expected;
        if (Object.keys(expected).length)
          checks.set(page, { ...checks.get(page), ...expected });
      }
      for (const [page, expected] of checks) {
        const entries = Object.entries(expected);
        for (let offset = 0; offset < entries.length; offset += 24) {
          const guard = await this.connection.call(
            "use_figma",
            {
              fileKey: this.key,
              code: script({
                mode: "verify",
                page: page || null,
                bindings: this.bindings,
                expected: Object.fromEntries(
                  entries.slice(offset, offset + 24),
                ),
                operations: [],
              }),
              description: "Verify Figma revision guards before publication",
              skillNames: "figma-use",
            },
            CallSafety.Read,
            signal,
          );
          if (guard.errorCode)
            throw new ForgeError(
              ErrorCode.Conflict,
              "The Figma baseline changed before publication.",
            );
        }
      }
      for (const batch of plan.batches) {
        checkSignal(signal);
        const pending = [batch];
        while (pending.length) {
          checkSignal(signal);
          const part = pending.shift()!;
          const parts = this.split(part);
          if (parts.length > 1) {
            pending.unshift(...parts);
            continue;
          }
          await this.execute(part, changes, signal);
          wrote = true;
        }
      }
      if (this.currentRevision !== revision)
        throw new ForgeError(
          ErrorCode.Conflict,
          "Figma revision changed during publication.",
        );
      this.previous = desired;
      const receipt = this.makeReceipt(PublishStatus.Complete, changes);
      this.emit(Stage.Publish, start);
      return receipt;
    } catch (error) {
      const code = error instanceof ForgeError ? error.code : ErrorCode.Remote;
      this.emit(Stage.Publish, start, code);
      if (error instanceof FigmaPublishError) throw error;
      if (
        wrote ||
        changes.createdNodeIds.length ||
        changes.mutatedNodeIds.length ||
        changes.deletedNodeIds.length
      )
        throw new FigmaPublishError(
          code,
          this.makeReceipt(
            this.unknown ? PublishStatus.Unknown : PublishStatus.Partial,
            changes,
          ),
        );
      throw error instanceof ForgeError
        ? error
        : new ForgeError(code, "Figma publication failed.");
    }
  }
  private input(batch: Batch) {
    const expected: Record<
      string,
      Pick<Snapshot, "id" | "type" | "stateHash">
    > = {};
    for (const op of batch.operations) {
      for (const key of [op.entity.key, op.entity.parent]) {
        const id = key?.startsWith("@")
          ? key.slice(1)
          : key
            ? this.bindings[key]
            : undefined;
        const snap = id ? this.snapshots.get(id) : undefined;
        if (snap)
          expected[id!] = {
            id: snap.id,
            type: snap.type,
            stateHash: snap.stateHash,
          };
      }
    }
    const relevant = new Set<string>();
    for (const op of batch.operations) {
      relevant.add(op.entity.key);
      if (op.entity.parent) relevant.add(op.entity.parent);
      if (op.entity.page) relevant.add(op.entity.page);
      for (const child of op.entity.children) relevant.add(child);
      for (const p of ["component", "collection", "fillStyle", "textStyle"])
        if (op.entity.props[p]) relevant.add(op.entity.props[p]);
      for (const v of op.entity.props.variants ?? []) relevant.add(v);
      for (const v of Object.values(op.entity.props.bindings ?? {}))
        relevant.add(String(v));
    }
    for (const key of relevant) {
      const id = key.startsWith("@") ? key.slice(1) : this.bindings[key];
      const snapshot = id ? this.snapshots.get(id) : undefined;
      if (snapshot)
        expected[id!] = {
          id: snapshot.id,
          type: snapshot.type,
          stateHash: snapshot.stateHash,
        };
    }
    return {
      mode: "apply",
      page: batch.page,
      operations: batch.operations,
      expected,
      images: this.imageHashes,
      bindings: Object.fromEntries(
        [...relevant]
          .filter((k) => this.bindings[k])
          .map((k) => [k, this.bindings[k]]),
      ),
    };
  }
  private split(batch: Batch): Batch[] {
    if (script(this.input(batch)).length <= this.connection.codeLimit)
      return [batch];
    if (batch.operations.length === 1)
      throw new ForgeError(
        ErrorCode.ResourceLimit,
        "A Figma operation and its preservation guard exceed the server code limit.",
      );
    const half = Math.ceil(batch.operations.length / 2);
    return [
      ...this.split({ ...batch, operations: batch.operations.slice(0, half) }),
      ...this.split({ ...batch, operations: batch.operations.slice(half) }),
    ];
  }
  private async execute(
    batch: Batch,
    changes: {
      createdNodeIds: string[];
      mutatedNodeIds: string[];
      deletedNodeIds: string[];
    },
    signal: AbortSignal,
    reconcile = true,
  ) {
    let value: any;
    this.batchCount++;
    try {
      value = await this.connection.call(
        "use_figma",
        {
          fileKey: this.key,
          code: script(this.input(batch)),
          description: "Apply a revision-pinned React Forge Figma batch",
          skillNames: "figma-use",
        },
        CallSafety.Write,
        signal,
      );
    } catch (error) {
      if (error instanceof RemoteError && error.safeToRetry) throw error;
      // A lost response is not evidence that a write failed. Only reconcile
      // known identities; names must never be used to infer a created node ID.
      try {
        if (
          reconcile &&
          batch.operations.every((op) => this.bindings[op.entity.key])
        ) {
          const read = await this.connection.call(
            "use_figma",
            {
              fileKey: this.key,
              code: script({ ...this.input(batch), mode: "reconcile" }),
              description: "Reconcile uncertain Figma publication",
              skillNames: "figma-use",
            },
            CallSafety.Read,
            this.disposal.signal,
          );
          if (
            Array.isArray(read.nodes) &&
            Array.isArray(read.outcomes) &&
            read.outcomes.length === batch.operations.length &&
            read.outcomes.every(
              (state: unknown) => state === "applied" || state === "pending",
            )
          ) {
            this.accept(read.nodes);
            const remaining: Operation[] = [];
            const prior = new Map(this.previous.map((e) => [e.key, e]));
            for (let i = 0; i < batch.operations.length; i++) {
              const op = batch.operations[i]!;
              const id = this.bindings[op.entity.key]!;
              if (read.outcomes[i] === "pending") {
                remaining.push(op);
                continue;
              }
              if (op.action === "delete") {
                prior.delete(op.entity.key);
                changes.deletedNodeIds.push(id);
                this.snapshots.delete(id);
                delete this.bindings[op.entity.key];
                this.targets.delete(op.entity.key);
              } else {
                prior.set(op.entity.key, op.entity);
                changes.mutatedNodeIds.push(id);
              }
            }
            this.previous = [...prior.values()];
            if (remaining.length) {
              checkSignal(signal);
              await this.execute(
                { ...batch, operations: remaining },
                changes,
                signal,
                false,
              );
            }
            return;
          }
        }
      } catch (recoveryError) {
        if (recoveryError instanceof FigmaPublishError) throw recoveryError;
        // Preserve uncertainty if a remote read cannot establish the outcome.
      }
      this.unknown = true;
      throw new FigmaPublishError(
        ErrorCode.UnknownOutcome,
        this.makeReceipt(PublishStatus.Unknown, changes),
      );
    }
    if (
      !value ||
      !value.bindings ||
      !value.snapshots ||
      !Number.isInteger(value.completed) ||
      !Array.isArray(value.createdNodeIds) ||
      !Array.isArray(value.mutatedNodeIds) ||
      !Array.isArray(value.deletedNodeIds)
    ) {
      this.unknown = true;
      throw new FigmaPublishError(
        ErrorCode.UnknownOutcome,
        this.makeReceipt(PublishStatus.Unknown, changes),
      );
    }
    Object.assign(this.bindings, value.bindings);
    for (const k of [
      "createdNodeIds",
      "mutatedNodeIds",
      "deletedNodeIds",
    ] as const)
      changes[k].push(...value[k]);
    this.accept(
      Object.values(value.snapshots),
      batch.page
        ? batch.page.startsWith("@")
          ? batch.page.slice(1)
          : this.bindings[batch.page]
        : undefined,
    );
    const previous = new Map(this.previous.map((e) => [e.key, e]));
    for (const op of batch.operations.slice(0, value.completed)) {
      if (
        op.action !== "delete" &&
        op.entity.props.image &&
        op.entity.props.image !== op.previous?.props.image
      ) {
        try {
          const hash = this.imageHashes[op.entity.props.image];
          const id = this.bindings[op.entity.key]!;
          if (hash && this.snapshots.get(id)?.props.imageHash !== hash) {
            const patch = {
              ...op,
              action: "update" as const,
              entity: {
                ...op.entity,
                props: {
                  image: op.entity.props.image,
                  imageScaleMode: op.entity.props.imageScaleMode,
                },
                children: [],
              },
            };
            await this.execute(
              { page: batch.page, operations: [patch] },
              changes,
              signal,
            );
          } else if (!hash) await this.upload(op, changes, signal);
        } catch (error) {
          this.previous = [...previous.values()];
          if (!(error instanceof RemoteError && error.safeToRetry))
            this.unknown = true;
          throw error;
        }
      }
      if (op.action === "delete") {
        previous.delete(op.entity.key);
        const id = this.bindings[op.entity.key];
        if (id) this.snapshots.delete(id);
        delete this.bindings[op.entity.key];
        this.targets.delete(op.entity.key);
      } else previous.set(op.entity.key, op.entity);
    }
    this.previous = [...previous.values()];
    if (value.errorCode)
      throw new ForgeError(
        Object.values(ErrorCode).includes(value.errorCode)
          ? value.errorCode
          : ErrorCode.Remote,
        "Figma batch stopped; confirmed bindings and changes are available in the receipt.",
      );
  }
  private async upload(
    op: Operation,
    changes: {
      createdNodeIds: string[];
      mutatedNodeIds: string[];
      deletedNodeIds: string[];
    },
    signal: AbortSignal,
  ) {
    const id = this.bindings[op.entity.key]!;
    const bytes = this.assets.get(op.entity.props.image);
    if (!bytes)
      throw new ForgeError(
        ErrorCode.InvalidTarget,
        "The image asset is not registered in this session.",
      );
    const value = await this.connection.call(
      "upload_assets",
      {
        fileKey: this.key,
        count: 1,
        nodeIds: [id],
        scaleMode: op.entity.props.imageScaleMode ?? "FILL",
      },
      CallSafety.Write,
      signal,
    );
    const entry =
      value.uploads?.[0] ??
      value.assets?.[0] ??
      value.uploadUrls?.[0] ??
      value.urls?.[0] ??
      value;
    const locator =
      typeof entry === "string"
        ? entry
        : (entry.submitUrl ?? entry.uploadUrl ?? entry.upload_url ?? entry.url);
    let url: URL;
    try {
      url = new URL(locator);
    } catch {
      throw new RemoteError(ErrorCode.UnknownOutcome, false);
    }
    if (
      url.protocol !== "https:" ||
      !/(^|\.)figma\.com$/.test(url.hostname) ||
      url.username ||
      url.password
    )
      throw new ForgeError(
        ErrorCode.Remote,
        "Figma returned an unsupported asset upload authority.",
      );
    // Upload URLs carry their own scoped authority. Never attach the OAuth token.
    const res = await this.uploadFetch(url, {
      method: "POST",
      body: new Uint8Array(bytes),
      headers: {
        "Content-Type": bytes[0] === 137 ? "image/png" : "image/jpeg",
      },
      redirect: "error",
      signal,
    });
    if (!res.ok) throw new RemoteError(ErrorCode.UnknownOutcome, false);
    await res.body?.cancel();
    changes.mutatedNodeIds.push(id);
    const read = await this.connection.call(
      "use_figma",
      {
        fileKey: this.key,
        code: script({
          mode: "inspect",
          page: op.entity.page,
          bindings: this.bindings,
          targets: [{ id, kind: op.entity.kind }],
        }),
        description: "Confirm uploaded Figma image",
        skillNames: "figma-use",
      },
      CallSafety.Read,
      signal,
    );
    this.accept(read.nodes);
    const imageHash = this.snapshots.get(id)?.props.imageHash;
    if (typeof imageHash !== "string")
      throw new RemoteError(ErrorCode.UnknownOutcome, false);
    this.imageHashes[op.entity.props.image] = imageHash;
  }
  exportFile(
    path: string,
    options: { overwrite?: boolean; signal?: AbortSignal } = {},
  ): Promise<FigmaReceipt & { published: true }> {
    const signal = this.signal(options.signal);
    if (!path.endsWith(".figma.json"))
      return Promise.reject(
        new ForgeError(
          ErrorCode.MalformedInput,
          "Figma output must use .figma.json.",
        ),
      );
    return withOutputReservation(path, signal, async (destination) => {
      if (
        !options.overwrite &&
        (await lstat(destination).then(
          () => true,
          (e: NodeJS.ErrnoException) => {
            if (e.code === "ENOENT") return false;
            throw new ForgeError(
              ErrorCode.Io,
              "Unable to inspect the receipt destination.",
            );
          },
        ))
      )
        throw new ForgeError(
          ErrorCode.Conflict,
          "Receipt exists. Enable overwrite before remote publication.",
        );
      let receipt: FigmaReceipt;
      try {
        receipt = await this.publish({ signal });
      } catch (error) {
        if (error instanceof FigmaPublishError) {
          await publishLocal(
            Buffer.from(JSON.stringify(error.receipt, null, 2) + "\n"),
            destination,
            { overwrite: options.overwrite },
          ).catch(() => {});
        }
        throw error;
      }
      try {
        await publishLocal(
          Buffer.from(JSON.stringify(receipt, null, 2) + "\n"),
          destination,
          { overwrite: options.overwrite },
        );
      } catch {
        throw new FigmaPublishError(ErrorCode.Io, receipt);
      }
      return { ...receipt, published: true };
    });
  }
  async measure(
    handle: NodeHandle,
    options: { revision: number; signal?: AbortSignal },
  ): Promise<Geometry> {
    this.signal(options.signal);
    if (
      handle.documentId !== this.documentId ||
      options.revision !== this.last?.revision ||
      this.last.status !== PublishStatus.Complete
    )
      throw new ForgeError(
        ErrorCode.Conflict,
        "Measure requires a confirmed published Figma revision.",
      );
    const id =
      this.bindings[this.handleKeys.get(handle.nodeId) ?? handle.nodeId];
    const baseline = id ? this.snapshots.get(id) : undefined;
    if (!id || !baseline)
      throw new ForgeError(
        ErrorCode.InvalidTarget,
        "The Figma node has not been published.",
      );
    const page = this.pages.get(id);
    const read = await this.connection.call(
      "use_figma",
      {
        fileKey: this.key,
        code: script({
          mode: "inspect",
          page: page ? `@${page}` : undefined,
          bindings: {},
          targets: [{ id, kind: baseline.type }],
        }),
        description: "Measure a published Figma node",
        skillNames: "figma-use",
      },
      CallSafety.Read,
      this.signal(options.signal),
    );
    const current = read.nodes?.find((n: Snapshot) => n.id === id);
    if (!current || current.stateHash !== baseline.stateHash)
      throw new ForgeError(
        ErrorCode.Conflict,
        "The Figma node changed since the published revision.",
      );
    const bounds = current.bounds;
    if (!bounds)
      throw new ForgeError(
        ErrorCode.InvalidTarget,
        "The selected Figma node has no measured geometry.",
      );
    return { ...bounds, revision: options.revision, coordinateSpace: "page" };
  }
  dispose(): Promise<void> {
    if (this.disposalTask) return this.disposalTask;
    this.disposal.abort();
    this.disposalTask = (async () => {
      await Promise.all([
        this.root.dispose(),
        ...[...this.mounts.values()].map((m) => m.root.dispose()),
      ]);
      await Promise.allSettled([
        this.queue,
        this.publication,
        ...this.pendingAssets,
        ...this.reads.values(),
      ]);
      await this.connection.close();
      this.assets.clear();
      this.mounts.clear();
      this.listeners.clear();
    })();
    return this.disposalTask;
  }
}
export const openFigma = FigmaSession.open;
