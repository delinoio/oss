import { ForgeError } from "../errors.js";
import { ErrorCode } from "../types.js";
import type { SerializedNode } from "../renderer.js";
import { SceneAssetKind } from "./components.js";
export interface SceneNode { id: string; name: string; type: string; children: SceneNode[]; [key: string]: unknown }
export interface SceneClip { id: string; name: string; tracks: { target: string; sampler: string }[] }
export interface SceneModel { document_id: string; nodes: SceneNode[]; animations: SceneClip[]; animation_bake_fps?: number }
const fail = () => new ForgeError(ErrorCode.MalformedInput, "Unsupported or malformed scene properties.");
const transforms = ["name", "translation", "rotation", "scale"];
const fields: Record<string, string[]> = {
  scene: ["name"], group: transforms, joint: transforms, mesh: [...transforms, "geometry", "material", "skin", "morphWeights"],
  perspective_camera: [...transforms, "yfov", "aspect", "near", "far"],
  orthographic_camera: [...transforms, "xmag", "ymag", "near", "far"],
  directional_light: [...transforms, "color", "intensity"], point_light: [...transforms, "color", "intensity"],
  spot_light: [...transforms, "color", "intensity", "innerCone", "outerCone"],
};
const materialFields: Record<string, string> = { baseColor: "base_color", metallic: "metallic", roughness: "roughness", emissive: "emissive", alphaMode: "alpha_mode", doubleSided: "double_sided", baseColorTexture: "base_color_texture", metallicTexture: "metallic_texture", roughnessTexture: "roughness_texture", normalTexture: "normal_texture", emissiveTexture: "emissive_texture" };
export function sceneModel(tree: SerializedNode[], documentId: string, assets: ReadonlyMap<string, Buffer>): SceneModel {
  const asset = (value: unknown, kind: SceneAssetKind) => {
    if (!value || typeof value !== "object") throw fail();
    const h = value as { documentId?: unknown; assetId?: unknown; kind?: unknown };
    if (h.documentId !== documentId || h.kind !== kind || typeof h.assetId !== "string" || !assets.has(h.assetId)) throw new ForgeError(ErrorCode.InvalidTarget, "Scene asset is unregistered or belongs to another session.");
    return h.assetId;
  };
  const present = new Set<string>();
  const gather = (n: SerializedNode) => { present.add(n.id); n.children.forEach(gather); };
  tree.forEach(gather);
  const target = (value: unknown): string => {
    if (value && typeof value === "object" && "current" in value) value = value.current;
    if (!value || typeof value !== "object" || !("documentId" in value) || !("nodeId" in value) || value.documentId !== documentId || typeof value.nodeId !== "string" || !present.has(value.nodeId)) throw new ForgeError(ErrorCode.InvalidTarget, "Scene target is missing or belongs to another session.");
    return value.nodeId;
  };
  const animations: SceneClip[] = [];
  const clip = (n: SerializedNode): void => {
    if (Object.keys(n.props).some(k => k !== "name") || typeof n.props.name !== "string") throw fail();
    animations.push({ id: n.id, name: n.props.name, tracks: n.children.map(track => {
      if (track.type !== "scene:animation_track" || track.children.length || Object.keys(track.props).some(k => !["target", "sampler"].includes(k))) throw fail();
      return { target: target(track.props.target), sampler: asset(track.props.sampler, SceneAssetKind.AnimationSampler) };
    }) });
  };
  const node = (n: SerializedNode, root: boolean): SceneNode => {
    const kind = n.type.startsWith("scene:") ? n.type.slice(6) : "";
    if (!Object.hasOwn(fields, kind) || root !== (kind === "scene") || Object.keys(n.props).some(k => !fields[kind]!.includes(k)) || (!["scene", "group", "joint"].includes(kind) && n.children.length)) throw fail();
    const p = n.props;
    const out: SceneNode = { id: n.id, name: (p.name ?? "") as string, type: kind === "scene" ? "group" : kind, children: n.children.filter(c => { if (root && c.type === "scene:animation_clip") { clip(c); return false; } return true; }).map(c => node(c, false)) };
    for (const k of ["translation", "rotation", "scale"]) if (p[k] !== undefined) out[k] = p[k];
    if (kind === "mesh") {
      out.geometry = asset(p.geometry, SceneAssetKind.Geometry);
      if (p.skin !== undefined) {
        if (!p.skin || typeof p.skin !== "object" || Object.keys(p.skin).some(k => k !== "joints") || !("joints" in p.skin) || !Array.isArray(p.skin.joints)) throw fail();
        out.skin = { joints: p.skin.joints.map(target) };
      }
      if (p.morphWeights !== undefined) out.morph_weights = p.morphWeights;
      const m = p.material ?? {};
      if (!m || typeof m !== "object" || Array.isArray(m)) throw fail();
      const material: Record<string, unknown> = {};
      for (const [k, v] of Object.entries(m)) { if (!Object.hasOwn(materialFields, k)) throw fail(); material[materialFields[k]!] = k.endsWith("Texture") ? asset(v, SceneAssetKind.Texture) : v; }
      out.material = material;
    } else if (kind.endsWith("camera")) {
      out.near = p.near ?? 0.01; out.far = p.far ?? 1000;
      if (kind === "perspective_camera") { out.yfov = p.yfov; out.aspect = p.aspect ?? 1; }
      else { out.xmag = p.xmag; out.ymag = p.ymag; }
    } else if (kind.endsWith("light")) {
      out.color = p.color ?? [1, 1, 1]; out.intensity = p.intensity ?? 1;
      if (kind === "spot_light") { out.inner_cone = p.innerCone ?? 0; out.outer_cone = p.outerCone ?? Math.PI / 4; }
    }
    return out;
  };
  if (tree.length !== 1) throw fail();
  const nodes = [node(tree[0]!, true)];
  return { document_id: documentId, nodes, animations };
}
