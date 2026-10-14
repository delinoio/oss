import type { ReactNode } from "react";
import { v7 } from "uuid";
import { ForgeError, checkSignal, abortable } from "../errors.js";
import {
  digest,
  publish,
  readSource,
  withOutputReservation,
} from "../files.js";
import {
  compactSceneGeometry,
  processDocument,
  simplifySceneGeometry,
  validateSceneAsset,
} from "../native.js";
import { RenderRoot } from "../renderer.js";
import {
  ErrorCode,
  Format,
  Stage,
  limits,
  type AssetSource,
  type Diagnostic,
  type NodeHandle,
} from "../types.js";
import type { Inspection, TargetHandle } from "../session.js";
import {
  SceneAssetKind,
  type GeometryHandle,
  type TextureHandle,
  type GeometryInput,
  type Vec3,
  type AnimationSamplerHandle,
  type AnimationSamplerInput,
  type BakeAnimationSamplerInput,
  type BakeParametricGeometryInput,
  type ParametricGeometrySample,
  AnimationInterpolation,
} from "./components.js";
import { sceneModel, type SceneModel, type SceneNode } from "./model.js";
import {
  animationWidth,
  budget,
  geometryBytes,
  malformed,
  samplerBytes,
} from "./assets.js";
export interface SceneSessionOptions {
  animationBakeFps?: number;
}
export interface AnimationSample {
  clip: NodeHandle;
  time: number;
}
export interface SceneGeometry {
  readonly coordinateSpace: "world";
  readonly min: Vec3;
  readonly max: Vec3;
  readonly revision: number;
}
export interface SimplifyGeometryOptions {
  targetTriangles: number;
  maxError: number;
}
export interface SimplifyGeometryResult {
  geometry: GeometryHandle;
  inputTriangles: number;
  outputTriangles: number;
  inputVertices: number;
  outputVertices: number;
  targetReached: boolean;
  /** Accumulated plane-quadric estimate, not a visual distance guarantee. */
  estimatedError: number;
}
export interface GeometryCompaction {
  readonly geometry: GeometryHandle;
  readonly inputVertices: number;
  readonly outputVertices: number;
  readonly triangles: number;
  readonly inputBytes: number;
  readonly outputBytes: number;
}

function compactionScratchBytes(bytes: Buffer): number {
  // Registered geometry has already passed native envelope validation. This
  // mirrors the native bound and charges its worker buffers before dispatch.
  const base = bytes.subarray(
    bytes.toString("ascii", 0, 4) === "FSG2" ? 16 : 0,
  );
  const vertices = base.readUInt32LE(8);
  const indices = base.readUInt32LE(12);
  const scratch = bytes.length * 5 + vertices * 80 + indices * 8;
  if (!Number.isSafeInteger(scratch) || scratch > limits.sceneBytes)
    throw new ForgeError(
      ErrorCode.ResourceLimit,
      "Geometry compaction exceeds its scratch budget.",
    );
  return scratch;
}

function simplificationScratchBytes(bytes: Buffer): number {
  // Mirror forge-scene's preflight bound so concurrent workers reserve their
  // operation-owned scratch before native simplification begins.
  const base = bytes.subarray(
    bytes.toString("ascii", 0, 4) === "FSG2" ? 16 : 0,
  );
  const vertices = base.readUInt32LE(8);
  const indices = base.readUInt32LE(12);
  const scratch = bytes.length * 4 + vertices * 512 + indices * 384;
  if (!Number.isSafeInteger(scratch) || scratch > limits.sceneBytes)
    throw new ForgeError(
      ErrorCode.ResourceLimit,
      "Geometry simplification exceeds its scratch budget.",
    );
  return scratch;
}

function geometryHasDeformation(bytes: Buffer): boolean {
  if (bytes.toString("ascii", 0, 4) !== "FSG2") return false;
  return bytes.readUInt32LE(8) !== 0 || bytes.readUInt32LE(12) !== 0;
}

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
  private scratchReserved = 0;
  private signature = "";
  private currentRevision = 0;
  private disposal?: Promise<void>;
  private readonly animationBakeFps: number;
  constructor(
    readonly format: Format.Glb | Format.Fbx,
    options: SceneSessionOptions = {},
  ) {
    if (format !== Format.Glb && format !== Format.Fbx)
      throw new ForgeError(
        ErrorCode.UnsupportedPackage,
        "Expected a GLB or FBX format.",
      );
    const fps = options.animationBakeFps ?? 60;
    if (
      Object.keys(options).some((k) => k !== "animationBakeFps") ||
      !Number.isInteger(fps) ||
      fps < 1 ||
      fps > 240
    )
      throw malformed();
    this.animationBakeFps = fps;
  }
  get revision() {
    return this.currentRevision;
  }
  private signal(signal?: AbortSignal) {
    if (this.controller.signal.aborted)
      throw new ForgeError(
        ErrorCode.Disposed,
        "The scene session is disposed.",
      );
    return signal
      ? AbortSignal.any([signal, this.controller.signal])
      : this.controller.signal;
  }
  private track<T>(
    stage: Stage,
    work: (context: { revision: number }) => Promise<T>,
  ): Promise<T> {
    this.signal();
    const start = performance.now();
    const context = { revision: this.revision };
    const task = (async () => {
      let code: ErrorCode | undefined;
      try {
        return await work(context);
      } catch (e) {
        code = e instanceof ForgeError ? e.code : ErrorCode.Render;
        if (e instanceof ForgeError)
          throw new ForgeError(
            e.code,
            e.message,
            {
              ...e.context,
              stage,
              format: this.format,
              revision: context.revision,
            },
            e.cause,
          );
        throw e;
      } finally {
        const d = Object.freeze({
          source: "javascript" as const,
          stage,
          format: this.format,
          revision: context.revision,
          durationMs: performance.now() - start,
          code,
        });
        for (const f of this.listeners) {
          try {
            f(d);
          } catch {
            /* Observers cannot change outcomes. */
          }
        }
      }
    })();
    this.operations.add(task);
    void task.finally(() => this.operations.delete(task)).catch(() => {});
    return task;
  }
  onDiagnostic(listener: (event: Diagnostic) => void) {
    this.signal();
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }
  render(children: ReactNode): Promise<void> {
    const signal = this.signal();
    return this.track(Stage.Render, () =>
      this.ordered(signal, () => this.root.render(children)),
    );
  }

  private register(
    kind: SceneAssetKind,
    source: AssetSource,
    signal?: AbortSignal,
  ): Promise<GeometryHandle | TextureHandle | AnimationSamplerHandle> {
    const combined = this.signal(signal);
    checkSignal(combined);
    if (
      !(source instanceof Uint8Array) &&
      (!source || typeof source !== "object" || typeof source.path !== "string")
    )
      throw new ForgeError(
        ErrorCode.MalformedInput,
        "Scene assets require bytes or an explicit local path.",
      );
    const reserve = source instanceof Uint8Array ? source.byteLength : 0;
    if (
      reserve > limits.geometryBytes ||
      this.bytes + this.reserved + reserve > limits.sceneBytes
    )
      return Promise.reject(
        new ForgeError(
          ErrorCode.ResourceLimit,
          "Scene assets exceed their byte limit.",
        ),
      );
    this.reserved += reserve;
    // Copy byte input at invocation, before waiting behind another registration.
    const input =
      source instanceof Uint8Array
        ? Buffer.from(source)
        : { path: source.path };
    const task = this.registrations
      .catch(() => {})
      .then(async () => {
        try {
          const { bytes } = await readSource(
            input,
            limits.geometryBytes,
            combined,
          );
          const id = `${kind}:${digest(bytes)}`;
          if (!this.assets.has(id)) {
            if (this.bytes + bytes.length > limits.sceneBytes)
              throw new ForgeError(
                ErrorCode.ResourceLimit,
                "Scene assets exceed 256 MiB.",
              );
            await validateSceneAsset(kind, bytes, combined);
            checkSignal(combined);
            this.assets.set(id, bytes);
            this.bytes += bytes.length;
          }
          return Object.freeze({
            assetId: id,
            documentId: this.documentId,
            kind,
          }) as GeometryHandle | TextureHandle | AnimationSamplerHandle;
        } finally {
          this.reserved -= reserve;
        }
      });
    this.registrations = task;
    this.pending.add(task);
    void task.finally(() => this.pending.delete(task)).catch(() => {});
    return task;
  }
  registerTexture(
    source: AssetSource,
    options: { signal?: AbortSignal } = {},
  ): Promise<TextureHandle> {
    return this.register(
      SceneAssetKind.Texture,
      source,
      options.signal,
    ) as Promise<TextureHandle>;
  }
  registerGeometry(
    input: GeometryInput,
    options: { signal?: AbortSignal } = {},
  ): Promise<GeometryHandle> {
    this.signal(options.signal);
    checkSignal(options.signal);
    return this.register(
      SceneAssetKind.Geometry,
      geometryBytes(input, limits.sceneBytes - this.bytes - this.reserved),
      options.signal,
    ) as Promise<GeometryHandle>;
  }
  simplifyGeometry(
    handle: GeometryHandle,
    options: SimplifyGeometryOptions,
    control: { signal?: AbortSignal } = {},
  ): Promise<SimplifyGeometryResult> {
    if (
      !control ||
      typeof control !== "object" ||
      Object.keys(control).some((key) => key !== "signal") ||
      (control.signal !== undefined &&
        !(control.signal instanceof AbortSignal)) ||
      !options ||
      typeof options !== "object" ||
      Object.keys(options).some(
        (key) => key !== "targetTriangles" && key !== "maxError",
      )
    )
      throw malformed();
    const signal = this.signal(control.signal);
    checkSignal(signal);
    if (
      !handle ||
      handle.kind !== SceneAssetKind.Geometry ||
      handle.documentId !== this.documentId ||
      typeof handle.assetId !== "string"
    )
      throw new ForgeError(ErrorCode.InvalidTarget, "Invalid geometry handle.");
    const source = this.assets.get(handle.assetId);
    if (!source || !handle.assetId.startsWith(`${SceneAssetKind.Geometry}:`))
      throw new ForgeError(
        ErrorCode.InvalidTarget,
        "Geometry is not registered.",
      );
    const indexOffset = source.subarray(0, 4).toString() === "FSG2" ? 28 : 12;
    const vertexOffset = source.subarray(0, 4).toString() === "FSG2" ? 24 : 8;
    const inputTriangles = source.readUInt32LE(indexOffset) / 3;
    const inputVertices = source.readUInt32LE(vertexOffset);
    if (
      !Number.isSafeInteger(options.targetTriangles) ||
      options.targetTriangles < 1 ||
      options.targetTriangles > inputTriangles ||
      !Number.isFinite(options.maxError) ||
      options.maxError < 0
    )
      throw malformed();
    const noOp = options.targetTriangles === inputTriangles;
    const scratch = noOp ? 0 : simplificationScratchBytes(source);
    // A changed result is at most as large as the source. A known no-op does
    // not allocate a derived asset, so it remains admissible at the limit.
    const reserve =
      options.targetTriangles === inputTriangles ? 0 : source.length;
    if (
      this.bytes + this.reserved + reserve > limits.sceneBytes ||
      this.scratchReserved + scratch > limits.sceneBytes
    )
      throw new ForgeError(
        ErrorCode.ResourceLimit,
        "Geometry simplification exceeds its reserved budget.",
      );
    this.reserved += reserve;
    this.scratchReserved += scratch;
    const task = this.track(Stage.Layout, async () => {
      try {
        if (noOp) {
          await Promise.resolve();
          checkSignal(signal);
          if (geometryHasDeformation(source))
            throw new ForgeError(
              ErrorCode.UnsupportedEdit,
              "Skinned or morphed geometry cannot be simplified.",
            );
          return Object.freeze({
            geometry: Object.freeze({
              assetId: handle.assetId,
              documentId: this.documentId,
              kind: SceneAssetKind.Geometry,
            }),
            inputTriangles,
            outputTriangles: inputTriangles,
            inputVertices,
            outputVertices: inputVertices,
            targetReached: true,
            estimatedError: 0,
          });
        }
        const result = await simplifySceneGeometry(
          source,
          options.targetTriangles,
          options.maxError,
          signal,
        );
        checkSignal(signal);
        if (result.bytes.length > limits.geometryBytes)
          throw new ForgeError(
            ErrorCode.ResourceLimit,
            "Derived geometry exceeds 64 MiB.",
          );
        const id = `${SceneAssetKind.Geometry}:${digest(result.bytes)}`;
        if (!this.assets.has(id)) {
          if (this.bytes + result.bytes.length > limits.sceneBytes)
            throw new ForgeError(
              ErrorCode.ResourceLimit,
              "Scene assets exceed 256 MiB.",
            );
          // Native output is validated by the same forge-scene geometry rules
          // before it can be returned or registered here.
          checkSignal(signal);
          this.assets.set(id, result.bytes);
          this.bytes += result.bytes.length;
        }
        return Object.freeze({
          geometry: Object.freeze({
            assetId: id,
            documentId: this.documentId,
            kind: SceneAssetKind.Geometry,
          }),
          inputTriangles: result.inputTriangles,
          outputTriangles: result.outputTriangles,
          inputVertices: result.inputVertices,
          outputVertices: result.outputVertices,
          targetReached: result.targetReached,
          estimatedError: result.estimatedError,
        });
      } finally {
        this.reserved -= reserve;
        this.scratchReserved -= scratch;
      }
    });
    this.pending.add(task);
    void task.finally(() => this.pending.delete(task)).catch(() => {});
    return task;
  }
  bakeParametricGeometry(
    input: BakeParametricGeometryInput,
    options: { signal?: AbortSignal } = {},
  ): Promise<GeometryHandle> {
    if (
      !input ||
      typeof input !== "object" ||
      Object.keys(input).some(
        (key) =>
          !["uSegments", "vSegments", "maxTriangles", "sample"].includes(key),
      ) ||
      !options ||
      typeof options !== "object" ||
      Object.keys(options).some((key) => key !== "signal") ||
      (options.signal !== undefined && !(options.signal instanceof AbortSignal))
    )
      throw malformed();
    const signal = this.signal(options.signal);
    checkSignal(signal);
    const { uSegments, vSegments, maxTriangles, sample } = input;
    if (
      !Number.isSafeInteger(uSegments) ||
      uSegments < 1 ||
      !Number.isSafeInteger(vSegments) ||
      vSegments < 1 ||
      !Number.isSafeInteger(maxTriangles) ||
      maxTriangles < 1 ||
      typeof sample !== "function"
    )
      throw malformed();

    // All arithmetic and admission precede allocation and caller callbacks.
    const vertices = (uSegments + 1) * (vSegments + 1);
    const triangles = 2 * uSegments * vSegments;
    const length = 16 + 48 * vertices + 12 * triangles;
    if (
      !Number.isSafeInteger(vertices) ||
      !Number.isSafeInteger(triangles) ||
      !Number.isSafeInteger(length) ||
      triangles > maxTriangles
    )
      throw new ForgeError(
        ErrorCode.ResourceLimit,
        "Parametric geometry exceeds its triangle or byte budget.",
      );
    budget(length, limits.sceneBytes - this.bytes - this.reserved);
    this.reserved += length;
    let released = false;
    const task = this.track(Stage.Render, async () => {
      try {
        // The typed arrays account for precisely the FSG1 payload reserved above.
        const positions = new Float32Array(vertices * 3);
        const normals = new Float32Array(vertices * 3);
        const uv = new Float32Array(vertices * 2);
        const tangents = new Float32Array(vertices * 4);
        const indices = new Uint32Array(triangles * 3);
        for (let j = 0, vertex = 0; j <= vSegments; j++) {
          for (let i = 0; i <= uSegments; i++, vertex++) {
            if (vertex % 128 === 0)
              await new Promise<void>((resolve) => setImmediate(resolve));
            checkSignal(signal);
            let result: ParametricGeometrySample;
            try {
              result = sample(i / uSegments, j / vSegments);
            } catch {
              // Caller exceptions can contain private sample data, including
              // when the callback itself throws a ForgeError.
              throw new ForgeError(
                ErrorCode.Render,
                "Parametric geometry sampling failed.",
              );
            }
            if (result instanceof Promise) {
              void result.catch(() => {});
              throw malformed();
            }
            const valid = (
              value: unknown,
              count: number,
            ): value is number[] => {
              if (!Array.isArray(value) || value.length !== count) return false;
              for (let component = 0; component < count; component++)
                if (
                  typeof value[component] !== "number" ||
                  !Number.isFinite(value[component])
                )
                  return false;
              return true;
            };
            if (
              !result ||
              typeof result !== "object" ||
              Object.keys(result).some(
                (key) => !["position", "normal", "uv", "tangent"].includes(key),
              ) ||
              !valid(result.position, 3) ||
              !valid(result.normal, 3) ||
              !valid(result.uv, 2) ||
              !valid(result.tangent, 4)
            )
              throw malformed();
            positions.set(result.position, vertex * 3);
            normals.set(result.normal, vertex * 3);
            uv.set(result.uv, vertex * 2);
            tangents.set(result.tangent, vertex * 4);
          }
        }
        let offset = 0;
        for (let j = 0; j < vSegments; j++)
          for (let i = 0; i < uSegments; i++) {
            const a = j * (uSegments + 1) + i;
            const b = a + 1;
            const c = a + uSegments + 1;
            const d = c + 1;
            indices.set([a, b, d, a, d, c], offset);
            offset += 6;
          }
        checkSignal(signal);
        // This handoff is synchronous: registration replaces the bake reservation
        // before any other operation can observe the aggregate admission budget.
        this.reserved -= length;
        released = true;
        return await this.registerGeometry(
          { positions, normals, uv, tangents, indices },
          { signal },
        );
      } catch (error) {
        if (error instanceof ForgeError) throw error;
        throw new ForgeError(
          ErrorCode.Render,
          "Parametric geometry sampling failed.",
          {},
          error,
        );
      } finally {
        if (!released) this.reserved -= length;
      }
    });
    this.pending.add(task);
    void task.finally(() => this.pending.delete(task)).catch(() => {});
    return task;
  }
  compactGeometry(
    handle: GeometryHandle,
    control: { signal?: AbortSignal } = {},
  ): Promise<GeometryCompaction> {
    const signal = this.signal(control.signal);
    checkSignal(signal);
    if (
      !handle ||
      handle.documentId !== this.documentId ||
      handle.kind !== SceneAssetKind.Geometry ||
      typeof handle.assetId !== "string" ||
      !handle.assetId.startsWith(`${SceneAssetKind.Geometry}:`) ||
      !this.assets.has(handle.assetId)
    )
      throw new ForgeError(
        ErrorCode.InvalidTarget,
        "Geometry is not registered in this scene.",
      );
    const source = this.assets.get(handle.assetId)!;
    const scratch = compactionScratchBytes(source);
    if (
      this.bytes + this.reserved + source.length > limits.sceneBytes ||
      this.scratchReserved + scratch > limits.sceneBytes
    )
      throw new ForgeError(
        ErrorCode.ResourceLimit,
        "Geometry compaction exceeds its reserved budget.",
      );
    this.reserved += source.length;
    this.scratchReserved += scratch;
    const task = this.track(Stage.Render, async () => {
      try {
        const result = await compactSceneGeometry(source, signal);
        checkSignal(signal);
        if (result.bytes.length > source.length)
          throw new ForgeError(
            ErrorCode.ResourceLimit,
            "Compacted geometry exceeds its reserved output budget.",
          );
        const id = `${SceneAssetKind.Geometry}:${digest(result.bytes)}`;
        if (!this.assets.has(id)) {
          this.assets.set(id, result.bytes);
          this.bytes += result.bytes.length;
        }
        return Object.freeze({
          geometry: Object.freeze({
            assetId: id,
            documentId: this.documentId,
            kind: SceneAssetKind.Geometry,
          }),
          inputVertices: result.inputVertices,
          outputVertices: result.outputVertices,
          triangles: result.triangles,
          inputBytes: source.length,
          outputBytes: result.bytes.length,
        });
      } finally {
        this.reserved -= source.length;
        this.scratchReserved -= scratch;
      }
    });
    this.pending.add(task);
    void task.finally(() => this.pending.delete(task)).catch(() => {});
    return task;
  }
  registerAnimationSampler(
    input: AnimationSamplerInput,
    options: { signal?: AbortSignal } = {},
  ): Promise<AnimationSamplerHandle> {
    this.signal(options.signal);
    checkSignal(options.signal);
    return this.register(
      SceneAssetKind.AnimationSampler,
      samplerBytes(input, limits.sceneBytes - this.bytes - this.reserved),
      options.signal,
    ) as Promise<AnimationSamplerHandle>;
  }
  bakeAnimationSampler(
    input: BakeAnimationSamplerInput,
    options: { signal?: AbortSignal } = {},
  ): Promise<AnimationSamplerHandle> {
    const signal = this.signal(options.signal);
    checkSignal(signal);
    if (
      !input ||
      typeof input !== "object" ||
      Object.keys(input).some(
        (k) => !["path", "duration", "fps", "components", "sample"].includes(k),
      )
    )
      throw malformed();
    const { path, duration, sample } = input,
      fps = input.fps ?? 60;
    const width = animationWidth(path, input.components);
    if (
      !Number.isFinite(duration) ||
      duration <= 0 ||
      !Number.isInteger(fps) ||
      fps < 1 ||
      fps > 240 ||
      typeof sample !== "function"
    )
      throw malformed();
    const count = Math.ceil(duration * fps) + 1,
      length = 20 + count * (width + 1) * 4;
    budget(length, limits.sceneBytes - this.bytes - this.reserved);
    this.reserved += length;
    // Reserve before invoking caller code. Yielding permits cancellation and
    // other registrations, which must continue to see this reservation.
    let released = false;
    const task = this.track(Stage.Render, async () => {
      try {
        const times = new Float32Array(count),
          values = new Float32Array(count * width);
        for (let i = 0; i < count; i++) {
          if (i % 128 === 0)
            await new Promise<void>((resolve) => setImmediate(resolve));
          checkSignal(signal);
          const time = Math.min(i / fps, duration);
          const value = sample(time);
          if (
            (!Array.isArray(value) && !(value instanceof Float32Array)) ||
            value.length !== width ||
            value.some((v) => typeof v !== "number" || !Number.isFinite(v))
          )
            throw malformed();
          times[i] = time;
          values.set(value, i * width);
        }
        checkSignal(signal);
        this.reserved -= length;
        released = true;
        return await this.registerAnimationSampler(
          { path, times, values, interpolation: AnimationInterpolation.Linear },
          { signal },
        );
      } catch (error) {
        if (error instanceof ForgeError) throw error;
        throw new ForgeError(
          ErrorCode.Render,
          "Animation sampling failed.",
          {},
          error,
        );
      } finally {
        if (!released) this.reserved -= length;
      }
    });
    this.pending.add(task);
    void task.finally(() => this.pending.delete(task)).catch(() => {});
    return task;
  }
  private model(): SceneModel {
    this.signal();
    const model = sceneModel(
      this.root.snapshot(),
      this.documentId,
      this.assets,
    );
    model.animation_bake_fps = this.animationBakeFps;
    const signature = digest(
      Buffer.from(JSON.stringify([model, [...this.assets.keys()]])),
    );
    if (signature !== this.signature) {
      this.signature = signature;
      this.currentRevision++;
    }
    return model;
  }
  private inspection(model: SceneModel): Inspection {
    const targets: TargetHandle[] = [];
    const visit = (node: SceneNode) => {
      targets.push(
        Object.freeze({
          documentId: this.documentId,
          nodeId: node.id,
          kind: node.type,
          name: node.name,
          editable: false,
        }),
      );
      node.children.forEach(visit);
    };
    model.nodes.forEach(visit);
    for (const clip of model.animations)
      targets.push(
        Object.freeze({
          documentId: this.documentId,
          nodeId: clip.id,
          kind: "animation_clip",
          name: clip.name,
          editable: false,
        }),
      );
    return Object.freeze({
      revision: this.revision,
      targets: Object.freeze(targets),
    });
  }
  inspect(): Inspection {
    return this.inspection(this.model());
  }
  private async pinned(signal: AbortSignal) {
    for (;;) {
      checkSignal(signal);
      await this.root.settled(signal);
      await abortable(Promise.all([...this.pending]), signal);
      checkSignal(signal);
      if (this.root.isSettled() && this.pending.size === 0) break;
    }
    const model = this.model();
    return {
      model,
      assets: new Map(this.assets),
      revision: this.revision,
      inspection: this.inspection(model),
    };
  }
  private ordered<T>(signal: AbortSignal, work: () => Promise<T>): Promise<T> {
    const task = this.queue
      .catch(() => {})
      .then(() => {
        checkSignal(signal);
        return work();
      });
    this.queue = task;
    return task;
  }
  snapshot(options: { signal?: AbortSignal } = {}): Promise<Inspection> {
    const signal = this.signal(options.signal);
    return this.track(Stage.Render, (context) =>
      this.ordered(signal, async () => {
        const p = await this.pinned(signal);
        context.revision = p.revision;
        return p.inspection;
      }),
    );
  }
  private async process(
    signal: AbortSignal,
    context: { revision: number },
    inspect = false,
    animation?: AnimationSample,
  ) {
    const p = await this.pinned(signal);
    context.revision = p.revision;
    if (
      animation &&
      (animation.clip.documentId !== this.documentId ||
        !Number.isFinite(animation.time))
    )
      throw new ForgeError(
        ErrorCode.InvalidTarget,
        "Invalid animation sample target or time.",
      );
    const model = animation
      ? {
          ...p.model,
          sample: { clip: animation.clip.nodeId, time: animation.time },
        }
      : p.model;
    const output = await processDocument(
      this.format,
      inspect ? "inspect" : "generate",
      model,
      Buffer.alloc(0),
      p.assets,
      this.documentId,
      p.revision,
      signal,
      { system: false, ids: [] },
      (event) => {
        for (const f of this.listeners) {
          try {
            f(event);
          } catch {
            /* Observer isolation. */
          }
        }
      },
    );
    checkSignal(signal);
    return { ...output, revision: p.revision };
  }
  exportBuffer(options: { signal?: AbortSignal } = {}): Promise<Buffer> {
    return this.export(options);
  }
  export(options: { signal?: AbortSignal } = {}): Promise<Buffer> {
    const signal = this.signal(options.signal);
    return this.track(Stage.Export, (context) =>
      this.ordered(
        signal,
        async () => (await this.process(signal, context)).bytes,
      ),
    );
  }
  exportFile(
    output: string,
    options: { signal?: AbortSignal; overwrite?: boolean } = {},
  ) {
    const signal = this.signal(options.signal);
    return this.track(Stage.Export, (context) => {
      const previous = this.queue.catch(() => {});
      // Reserve both queues at invocation: waiting for the directory before
      // joining this queue lets later renders overtake the export; reserving the
      // directory only inside this queue lets other sessions overtake it.
      const task = withOutputReservation(
        output,
        signal,
        async (destination) => {
          await previous;
          checkSignal(signal);
          const result = await this.process(signal, context);
          await publish(result.bytes, destination, { ...options, signal });
          return { published: true as const, revision: result.revision };
        },
      );
      // An early reservation failure must not release the preceding operation.
      this.queue = Promise.allSettled([previous, task]);
      return task;
    });
  }
  measure(
    handle: NodeHandle,
    options: {
      revision: number;
      signal?: AbortSignal;
      animation?: AnimationSample;
    },
  ): Promise<SceneGeometry> {
    const signal = this.signal(options.signal);
    return this.track(Stage.Layout, (context) =>
      this.ordered(signal, async () => {
        if (handle.documentId !== this.documentId)
          throw new ForgeError(
            ErrorCode.InvalidTarget,
            "Node belongs to another scene.",
          );
        const result = await this.process(
          signal,
          context,
          true,
          options.animation,
        );
        if (result.revision !== options.revision)
          throw new ForgeError(
            ErrorCode.Conflict,
            "Requested scene revision is stale.",
          );
        const bound = (
          JSON.parse(result.geometry) as Record<
            string,
            { min: Vec3; max: Vec3 }
          >
        )[handle.nodeId];
        if (!bound)
          throw new ForgeError(
            ErrorCode.InvalidTarget,
            "Node has no mesh bounds in this revision.",
          );
        return Object.freeze({
          coordinateSpace: "world" as const,
          min: Object.freeze(bound.min),
          max: Object.freeze(bound.max),
          revision: result.revision,
        });
      }),
    );
  }
  dispose(): Promise<void> {
    if (this.disposal) return this.disposal;
    this.controller.abort();
    this.disposal = (async () => {
      await this.root.dispose();
      await Promise.allSettled([
        this.queue,
        this.registrations,
        ...this.operations,
      ]);
      this.assets.clear();
      this.listeners.clear();
      this.pending.clear();
      this.bytes = 0;
      this.scratchReserved = 0;
    })();
    return this.disposal;
  }
}
