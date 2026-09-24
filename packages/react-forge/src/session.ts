import type { ReactNode } from "react";
import { v7 } from "uuid";
import { ForgeError, checkSignal } from "./errors.js";
import { digest, publish, readSource, type SourceFingerprint } from "./files.js";
import { processDocument, type NativeOutput } from "./native.js";
import { pptxModel, pptxNode } from "./pptx-model.js";
import { xlsxModel, xlsxEdit } from "./xlsx-model.js";
import type { Address, Range } from "./xlsx.js";
import { docxModel, docxBlocks } from "./docx-model.js";
import { RenderRoot } from "./renderer.js";
import { ErrorCode, Format, Stage, limits, type AssetHandle, type AssetSource, type Diagnostic, type Geometry, type NodeHandle } from "./types.js";

type Model = Record<string, unknown>;
interface ModelNode extends Model { id: string; type: string; children?: ModelNode[] }
export interface TargetHandle extends NodeHandle { readonly kind: string; readonly editable: boolean; readonly text?: string; readonly sheet?: string; readonly address?: Address; readonly range?: Range }
interface NativeRegion { id: string; kind: string; target_index: number; part: string; start: number; end: number; text?: string; sheet?: string; address?: Address; range?: Range }
export interface Inspection { readonly revision: number; readonly targets: readonly TargetHandle[] }
export interface MountedRegion {
  render(children: ReactNode): Promise<void>;
  unmount(): Promise<void>;
}

export class DocumentSession {
  readonly documentId = v7();
  readonly format: Format;
  private readonly root = new RenderRoot(this.documentId);
  private readonly disposal = new AbortController();
  private readonly assets = new Map<string, Buffer>();
  private readonly pendingAssets = new Set<Promise<AssetHandle>>();
  private readonly listeners = new Set<(event: Diagnostic) => void>();
  private readonly targets = new Map<string, TargetHandle>();
  private readonly regions = new Map<string, NativeRegion>();
  private readonly mounts = new Map<string, RenderRoot>();
  private source: Buffer = Buffer.alloc(0);
  private imported?: Model;
  private fingerprint?: SourceFingerprint;
  private queue: Promise<unknown> = Promise.resolve();
  private signature = "";
  private currentRevision = 0;
  private readonly operations = new Set<Promise<unknown>>();

  constructor(format: Format) { this.format = format; }
  get revision(): number { return this.currentRevision; }

  private active() {
    if (this.disposal.signal.aborted) throw new ForgeError(ErrorCode.Disposed, "The document session is disposed.");
  }

  private signal(signal?: AbortSignal): AbortSignal {
    this.active();
    return signal ? AbortSignal.any([signal, this.disposal.signal]) : this.disposal.signal;
  }

  private async track<T>(stage: Stage, work: () => Promise<T>): Promise<T> {
    this.active();
    const started = performance.now();
    const operation = work();
    this.operations.add(operation);
    let code: ErrorCode | undefined;
    try { return await operation; }
    catch (error) { code = error instanceof ForgeError ? error.code : ErrorCode.Io; throw error; }
    finally {
      this.operations.delete(operation);
      const event = Object.freeze({ stage, format: this.format, revision: this.revision, durationMs: performance.now() - started, code });
      for (const listener of this.listeners) { try { listener(event); } catch { /* Observers cannot mutate operation outcomes. */ } }
    }
  }

  private mutate<T>(work: () => Promise<T>): Promise<T> {
    this.active();
    const result = this.queue.then(() => { this.active(); return work(); });
    this.queue = result.catch(() => {});
    return result;
  }

  subscribe(listener: (event: Diagnostic) => void): () => void {
    this.active(); this.listeners.add(listener); return () => { this.listeners.delete(listener); };
  }

  render(children: ReactNode): Promise<void> {
    return this.mutate(() => this.track(Stage.Render, async () => {
      if (this.imported) throw new ForgeError(ErrorCode.UnsupportedEdit, "Use a mounted region to edit an imported Office document.");
      await this.root.render(children);
    }));
  }

  registerImage(source: AssetSource, options: { signal?: AbortSignal } = {}): Promise<AssetHandle> {
    const signal = this.signal(options.signal);
    const operation = this.track(Stage.Import, async () => {
      const { bytes } = await readSource(source, limits.imageBytes, signal);
      checkSignal(signal);
      const assetId = `sha256:${digest(bytes)}`;
      let total = bytes.length;
      for (const [id, value] of this.assets) if (id !== assetId) total += value.length;
      if (total > limits.officeBytes) throw new ForgeError(ErrorCode.ResourceLimit, "Registered assets exceed 256 MiB.");
      this.assets.set(assetId, bytes);
      return Object.freeze({ assetId, documentId: this.documentId });
    });
    this.pendingAssets.add(operation);
    void operation.finally(() => this.pendingAssets.delete(operation)).catch(() => {});
    return operation;
  }

  static async import(format: Format, source: AssetSource, options: { signal?: AbortSignal } = {}): Promise<DocumentSession> {
    const session = new DocumentSession(format);
    try {
      await session.track(Stage.Import, async () => {
        const signal = session.signal(options.signal);
        const input = await readSource(source, limits.officeBytes, signal);
        const result = await processDocument(format, "inspect", {}, input.bytes, session.assets, session.documentId, 0, signal);
        session.source = result.bytes;
        session.imported = JSON.parse(result.model) as Model;
        session.fingerprint = input.fingerprint;
        session.collectTargets();
      });
      return session;
    } catch (error) { await session.dispose(); throw error; }
  }

  private collectTargets() {
    if (this.format !== Format.Pptx) {
      for (const region of this.imported?.targets as NativeRegion[] ?? []) {
        const handle = Object.freeze({ documentId: this.documentId, nodeId: region.id, kind: region.kind, editable: region.kind !== "opaque", text: region.text, sheet: region.sheet, address: region.address ? Object.freeze(region.address) : undefined, range: region.range ? Object.freeze({ first: Object.freeze(region.range.first), last: Object.freeze(region.range.last) }) : undefined });
        this.targets.set(region.id, handle);
        this.regions.set(region.id, region);
      }
      return;
    }
    const visit = (node: ModelNode) => {
      const handle = Object.freeze({ documentId: this.documentId, nodeId: node.id, kind: node.type, editable: node.type !== "opaque" });
      this.targets.set(node.id, handle);
      for (const child of node.children ?? []) visit(child);
    };
    for (const slide of (this.imported?.slides ?? []) as { content: ModelNode }[]) visit(slide.content);
  }

  inspect(): Inspection {
    this.active();
    return Object.freeze({ revision: this.revision, targets: Object.freeze(Array.from(this.targets.values())) });
  }

  private findNode(model: Model, id: string): ModelNode | undefined {
    const visit = (node: ModelNode): ModelNode | undefined => node.id === id ? node : node.children?.map(visit).find(Boolean);
    return (model.slides as { content: ModelNode }[]).map(slide => visit(slide.content)).find(Boolean);
  }

  mount(target: TargetHandle, children: ReactNode): Promise<MountedRegion> {
    return this.mutate(async () => {
      if (!this.imported || this.targets.get(target.nodeId) !== target || !target.editable) throw new ForgeError(ErrorCode.InvalidTarget, "Target is invalid, opaque, stale or belongs to another document.");
      const descendants = (node: ModelNode, id: string): boolean => node.id === id || !!node.children?.some(child => descendants(child, id));
      const selected = this.format === Format.Pptx ? this.findNode(this.imported, target.nodeId)! : undefined;
      for (const id of this.mounts.keys()) {
        let overlap: boolean;
        if (selected) overlap = descendants(selected, id) || descendants(this.findNode(this.imported, id)!, target.nodeId);
        else {
          const a = this.regions.get(target.nodeId)!; const b = this.regions.get(id)!;
          overlap = a.part === b.part && a.start < b.end && b.start < a.end;
        }
        if (overlap) throw new ForgeError(ErrorCode.Conflict, "Mounted document regions overlap.");
      }
      const root = new RenderRoot(this.documentId);
      try { await root.render(children); } catch (error) { await root.dispose(); throw error; }
      this.mounts.set(target.nodeId, root);
      let unmounted = false;
      return Object.freeze({
        render: (next: ReactNode) => this.mutate(async () => {
          if (unmounted) throw new ForgeError(ErrorCode.InvalidTarget, "Mounted region is no longer active.");
          await root.render(next);
        }),
        unmount: () => this.mutate(async () => {
          if (unmounted) return;
          unmounted = true; this.mounts.delete(target.nodeId); await root.dispose();
        }),
      });
    });
  }

  private async prepare(signal: AbortSignal): Promise<{ model: Model; revision: number; refs: Map<string, string> }> {
    await this.queue;
    await Promise.all(this.pendingAssets);
    const roots = this.imported ? Array.from(this.mounts.values()) : [this.root];
    await Promise.all(roots.map(root => root.settled(signal)));
    checkSignal(signal);
    const refs = new Map<string, string>();
    let model: Model;
    if (this.imported && this.format !== Format.Pptx) {
      model = { edits: Array.from(this.mounts, ([id, root]) => ({
        target_index: this.regions.get(id)!.target_index,
        ...(this.format === Format.Docx ? { blocks: docxBlocks(root.snapshot(), this.documentId) } : { value: xlsxEdit(root.snapshot(), this.regions.get(id)!) }),
      })) };
    } else if (this.imported) {
      model = structuredClone(this.imported);
      for (const [id, root] of this.mounts) {
        const nodes = root.snapshot();
        if (nodes.length !== 1) throw new ForgeError(ErrorCode.UnsupportedEdit, "A presentation mount must render one replacement node.");
        const target = this.findNode(model, id)!;
        const replacement = pptxNode(nodes[0]!, this.documentId);
        refs.set(nodes[0]!.id, id);
        // Keep native identity and existing geometry unless explicitly replaced.
        const frame = replacement.frame ?? target.frame;
        for (const key of Object.keys(target)) delete target[key];
        Object.assign(target, replacement, { id, frame });
      }
      model.assets = { ...(model.assets as Model), ...Object.fromEntries(Array.from(this.assets.keys(), id => [id, { handle: id }])) };
    } else {
      if (this.format === Format.Pptx) model = pptxModel(this.root.snapshot(), this.documentId, this.assets);
      else if (this.format === Format.Docx) model = docxModel(this.root.snapshot(), this.documentId);
      else if (this.format === Format.Xlsx) model = xlsxModel(this.root.snapshot(), this.documentId);
      else throw new ForgeError(ErrorCode.UnsupportedPackage, "This format is not connected to the native adapter yet.");
    }
    const json = JSON.stringify(model);
    if (Buffer.byteLength(json) > limits.treeBytes && !this.imported) throw new ForgeError(ErrorCode.ResourceLimit, "New document model exceeds 16 MiB.");
    if (json !== this.signature) { this.signature = json; this.currentRevision++; }
    return { model, revision: this.revision, refs };
  }

  private async process(signal: AbortSignal): Promise<NativeOutput & { revision: number; refs: Map<string, string> }> {
    const { model, revision, refs } = await this.prepare(signal);
    const output = await processDocument(this.format, this.imported ? "update" : "generate", model,
      this.source, new Map(this.assets), this.documentId, revision, signal);
    return { ...output, revision, refs };
  }

  exportBuffer(options: { signal?: AbortSignal } = {}): Promise<Buffer> {
    const signal = this.signal(options.signal);
    return this.track(Stage.Export, async () => (await this.process(signal)).bytes);
  }

  exportFile(path: string, options: { overwrite?: boolean; signal?: AbortSignal } = {}): Promise<{ published: true; revision: number }> {
    const signal = this.signal(options.signal);
    return this.track(Stage.Export, async () => {
      const result = await this.process(signal);
      await publish(result.bytes, path, { ...options, signal, source: this.fingerprint });
      return { published: true, revision: result.revision };
    });
  }

  measure(handle: NodeHandle, options: { revision: number; signal?: AbortSignal }): Promise<Geometry> {
    const signal = this.signal(options.signal);
    return this.track(Stage.Layout, async () => {
      if (handle.documentId !== this.documentId) throw new ForgeError(ErrorCode.InvalidTarget, "Node belongs to a different document.");
      const result = await this.process(signal);
      if (options.revision !== result.revision) throw new ForgeError(ErrorCode.Conflict, "Requested geometry revision is stale.");
      const geometry = JSON.parse(result.geometry) as { nodes: Record<string, { frame: Omit<Geometry, "revision"> }> };
      const node = geometry.nodes[result.refs.get(handle.nodeId) ?? handle.nodeId];
      if (!node) throw new ForgeError(ErrorCode.InvalidTarget, "Node is absent from this revision.");
      return Object.freeze({ ...node.frame, revision: result.revision });
    });
  }

  async dispose(): Promise<void> {
    if (this.disposal.signal.aborted) return;
    this.disposal.abort();
    await Promise.all([this.root.dispose(), ...Array.from(this.mounts.values(), root => root.dispose())]);
    await Promise.allSettled(this.operations);
    this.mounts.clear(); this.assets.clear(); this.listeners.clear(); this.targets.clear(); this.regions.clear();
    this.source = Buffer.alloc(0); this.imported = undefined;
  }
}

export const createSession = (format: Format): DocumentSession => new DocumentSession(format);
export const importOffice = DocumentSession.import;
