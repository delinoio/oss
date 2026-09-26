//! Bounded, format-independent animated scenes. No renderer, network, or file
//! I/O.
#![forbid(unsafe_code)]
use std::{
    collections::{BTreeMap, BTreeSet},
    io::Cursor,
};

mod animation;
mod assets;
pub use animation::*;
pub use assets::{MAX_MORPHS, MorphTarget, VertexSkin};
pub use forge_document::Assets;
use forge_tree_doc::{Diagnostic, ErrorCode, Result, cancellation::checkpoint};
use glam::{DMat4, DQuat, DVec3};
use serde::{Deserialize, Serialize};
use uuid::Uuid;

pub const MAX_BYTES: usize = 256 * 1024 * 1024;
pub const MAX_GEOMETRY_BYTES: usize = 64 * 1024 * 1024;
pub const MAX_PIXELS: u64 = 64_000_000;
pub fn invalid(path: &str) -> Diagnostic {
    Diagnostic::new(ErrorCode::InvalidField, path, "Invalid scene data")
}
pub fn limited() -> Diagnostic {
    Diagnostic::new(
        ErrorCode::ResourceLimit,
        "",
        "Scene resource limit exceeded",
    )
}
fn one() -> f64 {
    1.0
}
fn white() -> [f64; 4] {
    [1.0; 4]
}
fn scale() -> [f64; 3] {
    [1.0; 3]
}
fn rotation() -> [f64; 4] {
    [0.0, 0.0, 0.0, 1.0]
}
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum AlphaMode {
    #[default]
    Opaque,
    Blend,
}
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Material {
    #[serde(default = "white")]
    pub base_color: [f64; 4],
    #[serde(default)]
    pub metallic: f64,
    #[serde(default = "one")]
    pub roughness: f64,
    #[serde(default)]
    pub emissive: [f64; 3],
    #[serde(default)]
    pub alpha_mode: AlphaMode,
    #[serde(default)]
    pub double_sided: bool,
    pub base_color_texture: Option<String>,
    pub metallic_texture: Option<String>,
    pub roughness_texture: Option<String>,
    pub normal_texture: Option<String>,
    pub emissive_texture: Option<String>,
}
impl Default for Material {
    fn default() -> Self {
        serde_json::from_str("{}").expect("static material")
    }
}
impl Material {
    pub fn textures(&self) -> impl Iterator<Item = &String> {
        [
            &self.base_color_texture,
            &self.metallic_texture,
            &self.roughness_texture,
            &self.normal_texture,
            &self.emissive_texture,
        ]
        .into_iter()
        .flatten()
    }
}
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(tag = "type", rename_all = "snake_case", deny_unknown_fields)]
pub enum Kind {
    Group,
    Joint,
    Mesh {
        geometry: String,
        material: Material,
        #[serde(default)]
        skin: Option<Skin>,
        #[serde(default)]
        morph_weights: Vec<f64>,
    },
    PerspectiveCamera {
        yfov: f64,
        aspect: f64,
        near: f64,
        far: f64,
    },
    OrthographicCamera {
        xmag: f64,
        ymag: f64,
        near: f64,
        far: f64,
    },
    DirectionalLight {
        color: [f64; 3],
        intensity: f64,
    },
    PointLight {
        color: [f64; 3],
        intensity: f64,
    },
    SpotLight {
        color: [f64; 3],
        intensity: f64,
        inner_cone: f64,
        outer_cone: f64,
    },
}
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct Node {
    pub id: Uuid,
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub translation: [f64; 3],
    #[serde(default = "rotation")]
    pub rotation: [f64; 4],
    #[serde(default = "scale")]
    pub scale: [f64; 3],
    #[serde(flatten)]
    pub kind: Kind,
    #[serde(default)]
    pub children: Vec<Node>,
}
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Scene {
    pub document_id: Uuid,
    pub nodes: Vec<Node>,
    #[serde(default)]
    pub animations: Vec<AnimationClip>,
    #[serde(default = "default_bake_fps")]
    pub animation_bake_fps: u32,
    #[serde(default)]
    pub sample: Option<AnimationSample>,
}
#[derive(Clone, Debug)]
pub struct Geometry {
    pub positions: Vec<[f32; 3]>,
    pub normals: Vec<[f32; 3]>,
    pub tangents: Option<Vec<[f32; 4]>>,
    pub uv: Option<Vec<[f32; 2]>>,
    pub indices: Vec<u32>,
    pub skin: Option<VertexSkin>,
    pub morph_targets: Vec<MorphTarget>,
}
#[derive(Clone, Copy, Debug, Serialize)]
pub struct Bounds {
    pub min: [f64; 3],
    pub max: [f64; 3],
}
impl Bounds {
    pub fn point(p: DVec3) -> Self {
        Self {
            min: p.to_array(),
            max: p.to_array(),
        }
    }

    pub fn include(&mut self, other: Self) {
        for i in 0..3 {
            self.min[i] = self.min[i].min(other.min[i]);
            self.max[i] = self.max[i].max(other.max[i]);
        }
    }
}
pub struct Prepared<'a> {
    pub scene: &'a Scene,
    pub assets: &'a Assets,
    pub geometries: BTreeMap<String, Geometry>,
    pub textures: BTreeMap<String, (&'static str, u32, u32)>,
    pub bounds: BTreeMap<Uuid, Bounds>,
    pub nodes: BTreeMap<Uuid, &'a Node>,
    pub rest_world: BTreeMap<Uuid, DMat4>,
    pub skins: BTreeMap<Uuid, PreparedSkin>,
    pub samplers: BTreeMap<String, AnimationSampler>,
}
fn in_unit(v: f64) -> bool {
    v.is_finite() && (0.0..=1.0).contains(&v)
}
fn valid_id(id: Uuid) -> bool {
    id.get_version_num() == 7
}

/// FSG1 is a private little-endian transport envelope, not a public file
/// format. Header: magic, flags (UV=1/tangent=2), vertex count, index count;
/// tightly packed positions, normals, optional tangents, optional UVs, then u32
/// indices.
pub fn geometry(bytes: &[u8]) -> Result<Geometry> {
    checkpoint()?;
    if bytes.len() > MAX_GEOMETRY_BYTES {
        return Err(limited());
    }
    if bytes.starts_with(b"FSG2") {
        return assets::extended_geometry(bytes);
    }
    if bytes.len() < 16 || &bytes[..4] != b"FSG1" {
        return Err(invalid("geometry"));
    }
    let u32_at = |i| u32::from_le_bytes(bytes[i..i + 4].try_into().expect("bounded header"));
    let flags = u32_at(4);
    let n = u32_at(8) as usize;
    let ni = u32_at(12) as usize;
    let stride = 24 + if flags & 2 != 0 { 16 } else { 0 } + if flags & 1 != 0 { 8 } else { 0 };
    let expected = n
        .checked_mul(stride)
        .and_then(|v| ni.checked_mul(4).and_then(|i| v.checked_add(i)))
        .and_then(|v| v.checked_add(16))
        .ok_or_else(limited)?;
    if flags > 3 || n < 3 || ni == 0 || !ni.is_multiple_of(3) || expected != bytes.len() {
        return Err(invalid("geometry"));
    }
    let mut offset = 16;
    fn floats<const N: usize>(
        bytes: &[u8],
        offset: &mut usize,
        count: usize,
    ) -> Result<Vec<[f32; N]>> {
        let mut out = Vec::with_capacity(count);
        for i in 0..count {
            if i % 4096 == 0 {
                checkpoint()?;
            }
            let mut a = [0.0; N];
            for v in &mut a {
                *v = f32::from_le_bytes(
                    bytes[*offset..*offset + 4]
                        .try_into()
                        .expect("checked length"),
                );
                *offset += 4;
                if !v.is_finite() {
                    return Err(invalid("geometry"));
                }
            }
            out.push(a);
        }
        Ok(out)
    }
    let positions = floats(bytes, &mut offset, n)?;
    let normals: Vec<[f32; 3]> = floats(bytes, &mut offset, n)?;
    if normals
        .iter()
        .any(|v| ((v[0] * v[0] + v[1] * v[1] + v[2] * v[2]) - 1.0).abs() > 0.002)
    {
        return Err(invalid("geometry/normals"));
    }
    let tangents = if flags & 2 != 0 {
        let ts: Vec<[f32; 4]> = floats(bytes, &mut offset, n)?;
        if ts.iter().zip(&normals).any(|(t, n)| {
            ((t[0] * t[0] + t[1] * t[1] + t[2] * t[2]) - 1.0).abs() > 0.002
                || (t[0] * n[0] + t[1] * n[1] + t[2] * n[2]).abs() > 0.002
                || t[3].abs() != 1.0
        }) {
            return Err(invalid("geometry/tangents"));
        }
        Some(ts)
    } else {
        None
    };
    let uv = if flags & 1 != 0 {
        Some(floats(bytes, &mut offset, n)?)
    } else {
        None
    };
    let mut indices = Vec::with_capacity(ni);
    for (i, c) in bytes[offset..].chunks_exact(4).enumerate() {
        if i % 8192 == 0 {
            checkpoint()?;
        }
        let v = u32::from_le_bytes(c.try_into().expect("index"));
        if v as usize >= n {
            return Err(invalid("geometry/indices"));
        }
        indices.push(v);
    }
    Ok(Geometry {
        positions,
        normals,
        tangents,
        uv,
        indices,
        skin: None,
        morph_targets: vec![],
    })
}

pub fn prepare<'a>(scene: &'a Scene, assets: &'a Assets) -> Result<Prepared<'a>> {
    if !valid_id(scene.document_id) || scene.nodes.len() != 1 {
        return Err(invalid("nodes"));
    }
    if assets
        .values()
        .try_fold(0usize, |n, a| n.checked_add(a.len()))
        .ok_or_else(limited)?
        > MAX_BYTES
    {
        return Err(limited());
    }
    let mut p = Prepared {
        scene,
        assets,
        geometries: BTreeMap::new(),
        textures: BTreeMap::new(),
        bounds: BTreeMap::new(),
        nodes: BTreeMap::new(),
        rest_world: BTreeMap::new(),
        skins: BTreeMap::new(),
        samplers: BTreeMap::new(),
    };
    let mut seen = BTreeSet::new();
    fn visit<'a>(
        p: &mut Prepared<'a>,
        node: &'a Node,
        parent: DMat4,
        depth: usize,
        seen: &mut BTreeSet<Uuid>,
    ) -> Result<Option<Bounds>> {
        checkpoint()?;
        if depth > 48 || seen.len() >= 20_000 {
            return Err(limited());
        }
        if !valid_id(node.id)
            || !seen.insert(node.id)
            || node.name.len() > 256
            || node.name.contains('\0')
        {
            return Err(invalid("nodes/id"));
        }
        if node
            .translation
            .iter()
            .chain(&node.rotation)
            .chain(&node.scale)
            .any(|v| !v.is_finite())
            || node.scale.iter().any(|s| s.abs() < 1e-12)
            || (node.rotation.iter().map(|n| n * n).sum::<f64>() - 1.0).abs() > 1e-6
        {
            return Err(invalid("nodes/transform"));
        }
        if !matches!(node.kind, Kind::Group | Kind::Joint) && !node.children.is_empty() {
            return Err(invalid("nodes/children"));
        }
        let world = parent
            * DMat4::from_scale_rotation_translation(
                DVec3::from_array(node.scale),
                DQuat::from_array(node.rotation),
                DVec3::from_array(node.translation),
            );
        if !world.is_finite() {
            return Err(invalid("nodes/transform"));
        }
        p.nodes.insert(node.id, node);
        p.rest_world.insert(node.id, world);
        let mut bound = None;
        match &node.kind {
            Kind::Group | Kind::Joint => {}
            Kind::Mesh {
                geometry: id,
                material: m,
                ..
            } => {
                if !p.geometries.contains_key(id) {
                    let g = geometry(p.assets.get(id).ok_or_else(|| invalid("geometry"))?)?;
                    p.geometries.insert(id.clone(), g);
                }
                let g = &p.geometries[id];
                if !m.base_color.iter().chain(&m.emissive).copied().all(in_unit)
                    || !in_unit(m.metallic)
                    || !in_unit(m.roughness)
                {
                    return Err(invalid("material"));
                }
                if m.alpha_mode == AlphaMode::Opaque && m.base_color[3] != 1.0 {
                    return Err(invalid("material/alpha_mode"));
                }
                if m.textures().next().is_some() && g.uv.is_none()
                    || m.normal_texture.is_some() && g.tangents.is_none()
                {
                    return Err(invalid("geometry/uv"));
                }
                for t in m.textures() {
                    if !p.textures.contains_key(t) {
                        let bytes = p.assets.get(t).ok_or_else(|| invalid("material/texture"))?;
                        p.textures.insert(t.clone(), forge_document::image(bytes)?);
                    }
                }
                for (i, v) in g.positions.iter().enumerate() {
                    if i % 4096 == 0 {
                        checkpoint()?;
                    }
                    let v =
                        world.transform_point3(DVec3::new(v[0] as f64, v[1] as f64, v[2] as f64));
                    if !v.is_finite() {
                        return Err(invalid("geometry"));
                    }
                    let b = Bounds::point(v);
                    if let Some(ref mut existing) = bound {
                        Bounds::include(existing, b);
                    } else {
                        bound = Some(b);
                    }
                }
            }
            Kind::PerspectiveCamera {
                yfov,
                aspect,
                near,
                far,
            } => {
                if !yfov.is_finite()
                    || *yfov <= 0.0
                    || *yfov >= std::f64::consts::PI
                    || !aspect.is_finite()
                    || *aspect <= 0.0
                    || !valid_planes(*near, *far)
                {
                    return Err(invalid("camera"));
                }
            }
            Kind::OrthographicCamera {
                xmag,
                ymag,
                near,
                far,
            } => {
                if !xmag.is_finite()
                    || *xmag <= 0.0
                    || !ymag.is_finite()
                    || *ymag <= 0.0
                    || !valid_planes(*near, *far)
                {
                    return Err(invalid("camera"));
                }
            }
            Kind::DirectionalLight { color, intensity }
            | Kind::PointLight { color, intensity }
            | Kind::SpotLight {
                color, intensity, ..
            } => {
                if !color.iter().copied().all(in_unit)
                    || !intensity.is_finite()
                    || *intensity < 0.0
                    || *intensity > 1e9
                {
                    return Err(invalid("light"));
                }
                if let Kind::SpotLight {
                    inner_cone,
                    outer_cone,
                    ..
                } = &node.kind
                    && (!inner_cone.is_finite()
                        || !outer_cone.is_finite()
                        || *inner_cone < 0.0
                        || inner_cone >= outer_cone
                        || *outer_cone > std::f64::consts::FRAC_PI_2)
                {
                    return Err(invalid("light"));
                }
            }
        }
        for child in &node.children {
            if let Some(b) = visit(p, child, world, depth + 1, seen)? {
                if let Some(ref mut existing) = bound {
                    Bounds::include(existing, b);
                } else {
                    bound = Some(b);
                }
            }
        }
        if let Some(b) = bound {
            p.bounds.insert(node.id, b);
        }
        Ok(bound)
    }
    visit(&mut p, &scene.nodes[0], DMat4::IDENTITY, 1, &mut seen)?;
    prepare_animation(&mut p)?;
    p.bounds = p.evaluate(scene.sample.as_ref())?;
    Ok(p)
}
fn valid_planes(near: f64, far: f64) -> bool {
    near.is_finite() && far.is_finite() && near > 0.0 && far > near
}

pub fn png(image: &image::RgbaImage) -> Result<Vec<u8>> {
    checkpoint()?;
    let mut out = Cursor::new(Vec::new());
    image
        .write_to(&mut out, image::ImageFormat::Png)
        .map_err(|_| invalid("texture"))?;
    checkpoint()?;
    let bytes = out.into_inner();
    if bytes.len() > MAX_GEOMETRY_BYTES {
        return Err(limited());
    }
    Ok(bytes)
}
pub fn decode(bytes: &[u8]) -> Result<image::RgbaImage> {
    forge_document::image(bytes)?;
    checkpoint()?;
    let image = image::load_from_memory(bytes)
        .map_err(|_| invalid("texture"))?
        .to_rgba8();
    checkpoint()?;
    Ok(image)
}
/// Scalar maps use the red channel, linear transfer, and a common UV0/repeat
/// sampler. GLB packs roughness into G and metallic into B, resampling neither
/// map.
pub fn metallic_roughness(p: &Prepared<'_>, m: &Material) -> Result<Option<Vec<u8>>> {
    if m.metallic_texture.is_none() && m.roughness_texture.is_none() {
        return Ok(None);
    }
    let metal = m
        .metallic_texture
        .as_ref()
        .map(|id| decode(&p.assets[id]))
        .transpose()?;
    let rough = m
        .roughness_texture
        .as_ref()
        .map(|id| decode(&p.assets[id]))
        .transpose()?;
    let size = metal.as_ref().or(rough.as_ref()).expect("map").dimensions();
    if metal.as_ref().is_some_and(|i| i.dimensions() != size)
        || rough.as_ref().is_some_and(|i| i.dimensions() != size)
    {
        return Err(invalid("material/texture"));
    }
    let mut out = image::RgbaImage::new(size.0, size.1);
    for (y, row) in out.rows_mut().enumerate() {
        checkpoint()?;
        for (x, pixel) in row.enumerate() {
            *pixel = image::Rgba([
                255,
                rough
                    .as_ref()
                    .map_or(255, |i| i.get_pixel(x as u32, y as u32)[0]),
                metal
                    .as_ref()
                    .map_or(255, |i| i.get_pixel(x as u32, y as u32)[0]),
                255,
            ]);
        }
    }
    Ok(Some(png(&out)?))
}
#[derive(Clone, Copy)]
pub enum TextureRole {
    BaseColor,
    Metallic,
    Roughness,
    Normal,
    Emissive,
}
/// FBX importers replace scalar factors when connecting maps. Bake factors into
/// the texture in its correct transfer space so the imported material is
/// honest.
pub fn fbx_texture(bytes: &[u8], m: &Material, role: TextureRole) -> Result<Vec<u8>> {
    let mut image = decode(bytes)?;
    fn srgb(v: f64) -> f64 {
        if v <= 0.04045 {
            v / 12.92
        } else {
            ((v + 0.055) / 1.055).powf(2.4)
        }
    }
    fn encoded(v: f64) -> f64 {
        if v <= 0.0031308 {
            12.92 * v
        } else {
            1.055 * v.powf(1.0 / 2.4) - 0.055
        }
    }
    for row in image.rows_mut() {
        checkpoint()?;
        for p in row {
            match role {
                TextureRole::BaseColor | TextureRole::Emissive => {
                    let f = if matches!(role, TextureRole::BaseColor) {
                        [m.base_color[0], m.base_color[1], m.base_color[2]]
                    } else {
                        m.emissive
                    };
                    for i in 0..3 {
                        p[i] = (encoded(srgb(p[i] as f64 / 255.0) * f[i]) * 255.0).round() as u8;
                    }
                    if matches!(role, TextureRole::BaseColor) {
                        p[3] = if m.alpha_mode == AlphaMode::Opaque {
                            255
                        } else {
                            (p[3] as f64 * m.base_color[3]).round() as u8
                        };
                    }
                }
                TextureRole::Metallic | TextureRole::Roughness => {
                    let v = (p[0] as f64
                        * if matches!(role, TextureRole::Metallic) {
                            m.metallic
                        } else {
                            m.roughness
                        })
                    .round() as u8;
                    *p = image::Rgba([v, v, v, 255]);
                }
                TextureRole::Normal => {}
            }
        }
    }
    png(&image)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn packed_scalar_channels_and_baked_color_factors_keep_their_transfer_space() {
        let scene: Scene = serde_json::from_value(serde_json::json!({
            "document_id": "01956e48-8f55-7000-8000-000000000001",
            "nodes": [{"id":"01956e48-8f55-7000-8000-000000000002", "type":"group"}]
        }))
        .unwrap();
        let color = image::RgbaImage::from_pixel(1, 1, image::Rgba([128, 64, 32, 128]));
        let rough = image::RgbaImage::from_pixel(1, 1, image::Rgba([200, 7, 9, 255]));
        let bytes = png(&color).unwrap();
        let assets = Assets::from([
            ("metal".into(), bytes.clone()),
            ("rough".into(), png(&rough).unwrap()),
        ]);
        let prepared = prepare(&scene, &assets).unwrap();
        let material = Material {
            base_color: [0.5, 1.0, 1.0, 0.5],
            metallic: 0.5,
            roughness: 0.25,
            alpha_mode: AlphaMode::Blend,
            metallic_texture: Some("metal".into()),
            roughness_texture: Some("rough".into()),
            ..Default::default()
        };
        let packed = decode(&metallic_roughness(&prepared, &material).unwrap().unwrap()).unwrap();
        assert_eq!(packed.get_pixel(0, 0).0, [255, 200, 128, 255]);
        let baked =
            decode(&fbx_texture(&bytes, &material, TextureRole::BaseColor).unwrap()).unwrap();
        assert_eq!(baked.get_pixel(0, 0).0, [92, 64, 32, 64]);
        let scalar =
            decode(&fbx_texture(&bytes, &material, TextureRole::Metallic).unwrap()).unwrap();
        assert_eq!(scalar.get_pixel(0, 0).0, [64, 64, 64, 255]);
        let opaque = Material {
            alpha_mode: AlphaMode::Opaque,
            ..material
        };
        assert_eq!(
            decode(&fbx_texture(&bytes, &opaque, TextureRole::BaseColor).unwrap())
                .unwrap()
                .get_pixel(0, 0)[3],
            255
        );
        assert_eq!(decode(&bytes).unwrap(), color);
    }

    #[test]
    fn malformed_transport_lengths_flags_and_nonunit_vectors_are_rejected() {
        let mut bytes = b"FSG1".to_vec();
        for n in [0u32, u32::MAX, u32::MAX] {
            bytes.extend_from_slice(&n.to_le_bytes());
        }
        assert!(geometry(&bytes).is_err());
        bytes[4..8].copy_from_slice(&4u32.to_le_bytes());
        assert!(geometry(&bytes).is_err());
        bytes[4..8].copy_from_slice(&0u32.to_le_bytes());
        bytes[8..12].copy_from_slice(&3u32.to_le_bytes());
        bytes[12..16].copy_from_slice(&3u32.to_le_bytes());
        bytes.resize(16 + 3 * 24 + 3 * 4, 0);
        assert!(geometry(&bytes).is_err());
    }
}
