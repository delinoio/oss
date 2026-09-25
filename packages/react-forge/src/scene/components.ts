import { createElement } from "react";
import type { ElementProps, AssetHandle } from "../types.js";
export enum SceneAssetKind { Geometry = "geometry", Texture = "texture" }
export enum AlphaMode { Opaque = "opaque", Blend = "blend" }
export type Vec3 = readonly [number, number, number];
export type Quaternion = readonly [number, number, number, number];
export interface GeometryHandle extends AssetHandle { readonly kind: SceneAssetKind.Geometry }
export interface TextureHandle extends AssetHandle { readonly kind: SceneAssetKind.Texture }
export interface GeometryInput {
  positions: Float32Array;
  normals: Float32Array;
  indices: Uint32Array;
  uv?: Float32Array;
  tangents?: Float32Array;
}
export interface Material {
  baseColor?: readonly [number, number, number, number];
  metallic?: number;
  roughness?: number;
  emissive?: Vec3;
  alphaMode?: AlphaMode;
  doubleSided?: boolean;
  baseColorTexture?: TextureHandle;
  metallicTexture?: TextureHandle;
  roughnessTexture?: TextureHandle;
  normalTexture?: TextureHandle;
  emissiveTexture?: TextureHandle;
}
export interface TransformProps extends ElementProps {
  name?: string;
  translation?: Vec3;
  rotation?: Quaternion;
  scale?: Vec3;
}
const component = <P extends ElementProps>(name: string) => (props: P) => createElement(`scene:${name}`, props);
export const Scene = component<ElementProps & { name?: string }>("scene");
export const Group = component<TransformProps>("group");
export const Mesh = component<TransformProps & { geometry: GeometryHandle; material?: Material }>("mesh");
export const PerspectiveCamera = component<TransformProps & { yfov: number; aspect?: number; near?: number; far?: number }>("perspective_camera");
export const OrthographicCamera = component<TransformProps & { xmag: number; ymag: number; near?: number; far?: number }>("orthographic_camera");
export const DirectionalLight = component<TransformProps & { color?: Vec3; intensity?: number }>("directional_light");
export const PointLight = component<TransformProps & { color?: Vec3; intensity?: number }>("point_light");
export const SpotLight = component<TransformProps & { color?: Vec3; intensity?: number; innerCone?: number; outerCone?: number }>("spot_light");
