import type { ReactNode } from "react";
import { v7 } from "uuid";
import { ForgeError, checkSignal, abortable } from "../errors.js";
import { digest, publish, readSource, withOutputReservation } from "../files.js";
import { processDocument, validateSceneAsset } from "../native.js";
import { RenderRoot } from "../renderer.js";
import { ErrorCode, Format, Stage, limits, type AssetSource, type Diagnostic, type NodeHandle } from "../types.js";
import type { Inspection, TargetHandle } from "../session.js";
import { SceneAssetKind, type GeometryHandle, type TextureHandle, type GeometryInput, type Vec3 } from "./components.js";
import { sceneModel, type SceneModel, type SceneNode } from "./model.js";
export interface SceneGeometry { readonly coordinateSpace: "world"; readonly min: Vec3; readonly max: Vec3; readonly revision: number }

/** Independent 3D lifecycle; shares React and atomic local publication with documents. */
export class SceneSession {
  readonly documentId = v7();
  private readonly root = new RenderRoot(this.documentId);
  private readonly controller = new AbortController();
  private readonly assets = new Map<string, Buffer>();
  private readonly pending = new Set<Promise<unknown>>();
  private readonly operations = new Set<Promise<unknown>>();
  private readonly listeners = new Set<(event: Diagnostic) => void>();
  private queue: Promise<unknown> = Promise.resolve();
  private registrations: Promise<unknown> = Promise.resolve();
  private bytes = 0;
  private reserved = 0;
  private signature = "";
  private currentRevision = 0;
  private disposal?: Promise<void>;
  constructor(readonly format: Format.Glb | Format.Fbx) {
    if (format !== Format.Glb && format !== Format.Fbx) throw new ForgeError(ErrorCode.UnsupportedPackage, "Expected a GLB or FBX format.");
  }
  get revision() { return this.currentRevision; }
  private signal(signal?: AbortSignal) {
    if (this.controller.signal.aborted) throw new ForgeError(ErrorCode.Disposed, "The scene session is disposed.");
    return signal ? AbortSignal.any([signal, this.controller.signal]) : this.controller.signal;
  }
  private track<T>(stage: Stage, work: (context: { revision: number }) => Promise<T>): Promise<T> {
    this.signal(); const start = performance.now(); const context = { revision: this.revision };
    const task = (async () => { let code: ErrorCode | undefined;
      try { return await work(context); } catch (e) { code = e instanceof ForgeError ? e.code : ErrorCode.Render; if (e instanceof ForgeError) throw new ForgeError(e.code, e.message, { ...e.context, stage, format: this.format, revision: context.revision }); throw e; }
      finally { const d = Object.freeze({ source: "javascript" as const, stage, format: this.format, revision: context.revision, durationMs: performance.now() - start, code }); for (const f of this.listeners) { try { f(d); } catch { /* Observers cannot change outcomes. */ } } }
    })();
    this.operations.add(task); void task.finally(() => this.operations.delete(task)).catch(() => {}); return task;
  }
  onDiagnostic(listener: (event: Diagnostic) => void) { this.signal(); this.listeners.add(listener); return () => { this.listeners.delete(listener); }; }
  render(children: ReactNode): Promise<void> {
    const signal = this.signal();
    return this.track(Stage.Render, () => this.ordered(signal, () => this.root.render(children)));
  }

  private register(kind: SceneAssetKind, source: AssetSource, signal?: AbortSignal): Promise<GeometryHandle | TextureHandle> {
    const combined = this.signal(signal); checkSignal(combined);
    if (!(source instanceof Uint8Array) && (!source || typeof source !== "object" || typeof source.path !== "string")) throw new ForgeError(ErrorCode.MalformedInput, "Scene assets require bytes or an explicit local path.");
    const reserve = source instanceof Uint8Array ? source.byteLength : 0;
    if (reserve > limits.geometryBytes || this.bytes + this.reserved + reserve > limits.sceneBytes) return Promise.reject(new ForgeError(ErrorCode.ResourceLimit, "Scene assets exceed their byte limit."));
    this.reserved += reserve;
    // Copy byte input at invocation, before waiting behind another registration.
    const input = source instanceof Uint8Array ? Buffer.from(source) : { path: source.path };
    const task = this.registrations.catch(() => {}).then(async () => {
      try {
        const { bytes } = await readSource(input, limits.geometryBytes, combined);
        const id = `${kind}:${digest(bytes)}`;
        if (!this.assets.has(id)) {
          if (this.bytes + bytes.length > limits.sceneBytes) throw new ForgeError(ErrorCode.ResourceLimit, "Scene assets exceed 256 MiB.");
          await validateSceneAsset(kind, bytes, combined); checkSignal(combined);
          this.assets.set(id, bytes); this.bytes += bytes.length;
        }
        return Object.freeze({ assetId: id, documentId: this.documentId, kind }) as GeometryHandle | TextureHandle;
      } finally { this.reserved -= reserve; }
    });
    this.registrations = task; this.pending.add(task); void task.finally(() => this.pending.delete(task)).catch(() => {}); return task;
  }
  registerTexture(source: AssetSource, options: { signal?: AbortSignal } = {}): Promise<TextureHandle> { return this.register(SceneAssetKind.Texture, source, options.signal) as Promise<TextureHandle>; }
  registerGeometry(input: GeometryInput, options: { signal?: AbortSignal } = {}): Promise<GeometryHandle> {
    this.signal(options.signal); checkSignal(options.signal);
    if (!input || typeof input !== "object") throw new ForgeError(ErrorCode.MalformedInput, "Geometry requires typed position, normal and index arrays.");
    const arrays = [input.positions, input.normals, ...(input.tangents ? [input.tangents] : []), ...(input.uv ? [input.uv] : []), input.indices];
    const vertices = input.positions?.length / 3;
    if (Object.keys(input).some(k => !["positions", "normals", "tangents", "uv", "indices"].includes(k)) || !Number.isInteger(vertices) || vertices < 3 || !(input.positions instanceof Float32Array) || !(input.normals instanceof Float32Array) || !(input.indices instanceof Uint32Array) || input.normals.length !== vertices * 3 || (input.uv && (!(input.uv instanceof Float32Array) || input.uv.length !== vertices * 2)) || (input.tangents && (!(input.tangents instanceof Float32Array) || input.tangents.length !== vertices * 4))) throw new ForgeError(ErrorCode.MalformedInput, "Geometry requires matching typed position, normal, tangent and UV arrays.");
    const length = 16 + arrays.reduce((n, a) => n + a.byteLength, 0);
    if (length > limits.geometryBytes || this.bytes + this.reserved + length > limits.sceneBytes) throw new ForgeError(ErrorCode.ResourceLimit, "Scene geometry exceeds its byte limit.");
    const bytes = Buffer.allocUnsafe(length); bytes.write("FSG1"); bytes.writeUInt32LE((input.uv ? 1 : 0) | (input.tangents ? 2 : 0), 4); bytes.writeUInt32LE(vertices, 8); bytes.writeUInt32LE(input.indices.length, 12);
    let offset = 16;
    for (const a of arrays) { for (const value of a) { if (a instanceof Uint32Array) bytes.writeUInt32LE(value, offset); else bytes.writeFloatLE(value, offset); offset += 4; } }
    return this.register(SceneAssetKind.Geometry, bytes, options.signal) as Promise<GeometryHandle>;
  }
  private model(): SceneModel {
    this.signal(); const model = sceneModel(this.root.snapshot(), this.documentId, this.assets);
    const signature = digest(Buffer.from(JSON.stringify([model, [...this.assets.keys()]])));
    if (signature !== this.signature) { this.signature = signature; this.currentRevision++; }
    return model;
  }
  private inspection(model: SceneModel): Inspection {
    const targets: TargetHandle[] = [];
    const visit = (node: SceneNode) => { targets.push(Object.freeze({ documentId: this.documentId, nodeId: node.id, kind: node.type, editable: false })); node.children.forEach(visit); };
    model.nodes.forEach(visit); return Object.freeze({ revision: this.revision, targets: Object.freeze(targets) });
  }
  inspect(): Inspection { return this.inspection(this.model()); }
  private async pinned(signal: AbortSignal) {
    for (;;) {
      checkSignal(signal); await this.root.settled(signal); await abortable(Promise.all([...this.pending]), signal); checkSignal(signal);
      if (this.root.isSettled() && this.pending.size === 0) break;
    }
    const model = this.model();
    return { model, assets: new Map(this.assets), revision: this.revision, inspection: this.inspection(model) };
  }
  private ordered<T>(signal: AbortSignal, work: () => Promise<T>): Promise<T> {
    const task = this.queue.catch(() => {}).then(() => { checkSignal(signal); return work(); }); this.queue = task; return task;
  }
  snapshot(options: { signal?: AbortSignal } = {}): Promise<Inspection> {
    const signal = this.signal(options.signal);
    return this.track(Stage.Render, context => this.ordered(signal, async () => { const p = await this.pinned(signal); context.revision = p.revision; return p.inspection; }));
  }
  private async process(signal: AbortSignal, context: { revision: number }, inspect = false) {
    const p = await this.pinned(signal); context.revision = p.revision;
    const output = await processDocument(this.format, inspect ? "inspect" : "generate", p.model, Buffer.alloc(0), p.assets, this.documentId, p.revision, signal, { system: false, ids: [] }, event => { for (const f of this.listeners) { try { f(event); } catch { /* Observer isolation. */ } } });
    checkSignal(signal); return { ...output, revision: p.revision };
  }
  exportBuffer(options: { signal?: AbortSignal } = {}): Promise<Buffer> { return this.export(options); }
  export(options: { signal?: AbortSignal } = {}): Promise<Buffer> {
    const signal = this.signal(options.signal);
    return this.track(Stage.Export, context => this.ordered(signal, async () => (await this.process(signal, context)).bytes));
  }
  exportFile(output: string, options: { signal?: AbortSignal; overwrite?: boolean } = {}) {
    const signal = this.signal(options.signal);
    return this.track(Stage.Export, context => withOutputReservation(output, signal, destination => this.ordered(signal, async () => {
      const result = await this.process(signal, context); await publish(result.bytes, destination, { ...options, signal }); return { published: true as const, revision: result.revision };
    })));
  }
  measure(handle: NodeHandle, options: { revision: number; signal?: AbortSignal }): Promise<SceneGeometry> {
    const signal = this.signal(options.signal);
    return this.track(Stage.Layout, context => this.ordered(signal, async () => {
      if (handle.documentId !== this.documentId) throw new ForgeError(ErrorCode.InvalidTarget, "Node belongs to another scene.");
      const result = await this.process(signal, context, true);
      if (result.revision !== options.revision) throw new ForgeError(ErrorCode.Conflict, "Requested scene revision is stale.");
      const bound = (JSON.parse(result.geometry) as Record<string, { min: Vec3; max: Vec3 }>)[handle.nodeId];
      if (!bound) throw new ForgeError(ErrorCode.InvalidTarget, "Node has no mesh bounds in this revision.");
      return Object.freeze({ coordinateSpace: "world" as const, min: Object.freeze(bound.min), max: Object.freeze(bound.max), revision: result.revision });
    }));
  }
  dispose(): Promise<void> {
    if (this.disposal) return this.disposal;
    this.controller.abort();
    this.disposal = (async () => { await this.root.dispose(); await Promise.allSettled([this.queue, this.registrations, ...this.operations]); this.assets.clear(); this.listeners.clear(); this.pending.clear(); this.bytes = 0; })();
    return this.disposal;
  }
}
