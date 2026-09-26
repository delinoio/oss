//! FBX 7.4 binary scene writer. Blender-compatible material mapping is
//! deliberately bounded; this is not a general Autodesk shader interchange.
#![forbid(unsafe_code)]
mod animation;
use std::{
    collections::BTreeMap,
    io::{self, Cursor, Seek, SeekFrom, Write},
};

use fbxcel::{
    low::FbxVersion,
    writer::v7400::binary::{Error as FbxError, Writer},
};
use forge_scene::{AlphaMode, Kind, MAX_BYTES, Material, Node, Prepared, TextureRole};
use forge_tree_doc::{Diagnostic, ErrorCode, Result, cancellation::checkpoint};
use glam::{DQuat, EulerRot};

struct Sink(Cursor<Vec<u8>>);
impl Write for Sink {
    fn write(&mut self, b: &[u8]) -> io::Result<usize> {
        checkpoint().map_err(|_| io::Error::from(io::ErrorKind::Interrupted))?;
        if self.0.position().saturating_add(b.len() as u64) > MAX_BYTES as u64 {
            return Err(io::Error::from(io::ErrorKind::OutOfMemory));
        }
        self.0.write(b)
    }

    fn flush(&mut self) -> io::Result<()> {
        Ok(())
    }
}
impl Seek for Sink {
    fn seek(&mut self, p: SeekFrom) -> io::Result<u64> {
        self.0.seek(p)
    }
}
fn failure(e: FbxError) -> Diagnostic {
    match e {
        FbxError::Io(ref e) if e.kind() == io::ErrorKind::Interrupted => {
            Diagnostic::new(ErrorCode::Cancelled, "", "FBX export cancelled")
        }
        FbxError::Io(ref e) if e.kind() == io::ErrorKind::OutOfMemory => forge_scene::limited(),
        _ => forge_scene::invalid("fbx"),
    }
}
enum A<'a> {
    I(i32),
    L(i64),
    D(f64),
    S(&'a str),
    B(&'a [u8]),
    Ints(Vec<i32>),
    Doubles(Vec<f64>),
    Floats(Vec<f32>),
    Longs(Vec<i64>),
}
struct Fbx<'a> {
    writer: Writer<Sink>,
    p: &'a Prepared<'a>,
    next: i64,
    connections: Vec<(i64, i64, Option<String>)>,
    counts: BTreeMap<&'static str, i32>,
    geometries: BTreeMap<String, i64>,
    materials: BTreeMap<String, i64>,
    model_ids: BTreeMap<String, i64>,
    mesh_ids: BTreeMap<String, i64>,
    blend_channels: BTreeMap<String, Vec<i64>>,
}
impl Fbx<'_> {
    fn open(&mut self, name: &str, attrs: &[A<'_>]) -> Result<()> {
        checkpoint()?;
        let mut a = self.writer.new_node(name).map_err(failure)?;
        for attr in attrs {
            match attr {
                A::I(v) => a.append_i32(*v),
                A::L(v) => a.append_i64(*v),
                A::D(v) => a.append_f64(*v),
                A::S(v) => a.append_string_direct(v),
                A::B(v) => a.append_binary_direct(v),
                A::Ints(v) => a.append_arr_i32_from_iter(None, v.iter().copied()),
                A::Doubles(v) => a.append_arr_f64_from_iter(None, v.iter().copied()),
                A::Floats(v) => a.append_arr_f32_from_iter(None, v.iter().copied()),
                A::Longs(v) => a.append_arr_i64_from_iter(None, v.iter().copied()),
            }
            .map_err(failure)?;
        }
        Ok(())
    }

    fn close(&mut self) -> Result<()> {
        self.writer.close_node().map_err(failure)
    }

    fn leaf(&mut self, name: &str, a: &[A<'_>]) -> Result<()> {
        self.open(name, a)?;
        self.close()
    }

    fn prop(&mut self, name: &str, kind: &str, sub: &str, values: &[A<'_>]) -> Result<()> {
        let mut attrs = vec![
            A::S(name),
            A::S(kind),
            A::S(sub),
            A::S(if kind == "enum" { "" } else { "A" }),
        ];
        self.open("P", &{
            attrs.extend(values.iter().map(|v| match v {
                A::I(x) => A::I(*x),
                A::L(x) => A::L(*x),
                A::D(x) => A::D(*x),
                A::S(x) => A::S(x),
                _ => unreachable!("scalar property"),
            }));
            attrs
        })?;
        self.close()
    }

    fn number(&mut self, name: &str, value: f64) -> Result<()> {
        self.prop(name, "Number", "", &[A::D(value)])
    }

    fn vector(&mut self, name: &str, kind: &str, v: [f64; 3]) -> Result<()> {
        self.prop(name, kind, "", &v.map(A::D))
    }

    fn object(&mut self, kind: &'static str, name: &str, sub: &str) -> Result<i64> {
        self.next += 1;
        let id = self.next;
        *self.counts.entry(kind).or_default() += 1;
        // FBX object categories and name classes differ for animation objects
        // (AnimationStack versus AnimStack). Blender validates this distinction.
        let class = kind
            .strip_prefix("Animation")
            .map(|suffix| format!("Anim{suffix}"));
        let class = class.as_deref().unwrap_or(kind);
        self.open(
            kind,
            &[A::L(id), A::S(&format!("{name}\0\u{1}{class}")), A::S(sub)],
        )?;
        Ok(id)
    }

    fn link(&mut self, child: i64, parent: i64, property: Option<&str>) {
        self.connections
            .push((child, parent, property.map(str::to_owned)));
    }

    fn layer(&mut self, name: &str, mapping: &str) -> Result<()> {
        self.open(name, &[A::I(0)])?;
        self.leaf("Version", &[A::I(101)])?;
        self.leaf("Name", &[A::S("UV0")])?;
        self.leaf("MappingInformationType", &[A::S(mapping)])?;
        self.leaf("ReferenceInformationType", &[A::S("Direct")])
    }

    fn geometry(&mut self, key: &str, owner: Option<&str>) -> Result<i64> {
        let cache_key = owner.map_or_else(|| key.to_owned(), |owner| format!("{key}:{owner}"));
        if let Some(id) = self.geometries.get(&cache_key) {
            return Ok(*id);
        }
        let g = &self.p.geometries[key];
        let id = self.object("Geometry", "Mesh", "Mesh")?;
        self.leaf("GeometryVersion", &[A::I(124)])?;
        self.leaf(
            "Vertices",
            &[A::Doubles(
                g.positions.iter().flatten().map(|v| *v as f64).collect(),
            )],
        )?;
        self.leaf(
            "PolygonVertexIndex",
            &[A::Ints(
                g.indices
                    .iter()
                    .enumerate()
                    .map(|(i, v)| {
                        if i % 3 == 2 {
                            -(*v as i32) - 1
                        } else {
                            *v as i32
                        }
                    })
                    .collect(),
            )],
        )?;
        self.layer("LayerElementNormal", "ByVertice")?;
        self.leaf(
            "Normals",
            &[A::Doubles(
                g.normals.iter().flatten().map(|v| *v as f64).collect(),
            )],
        )?;
        self.close()?;
        let mut layers = vec![
            "LayerElementNormal",
            "LayerElementMaterial",
            "LayerElementSmoothing",
        ];
        if let Some(uv) = &g.uv {
            self.layer("LayerElementUV", "ByVertice")?;
            self.leaf(
                "UV",
                &[A::Doubles(
                    uv.iter()
                        .flat_map(|v| [v[0] as f64, 1.0 - v[1] as f64])
                        .collect(),
                )],
            )?;
            self.close()?;
            layers.push("LayerElementUV");
        }
        if let Some(ts) = &g.tangents {
            self.layer("LayerElementTangent", "ByVertice")?;
            self.leaf(
                "Tangents",
                &[A::Doubles(
                    ts.iter()
                        .flat_map(|t| [t[0] as f64, t[1] as f64, t[2] as f64])
                        .collect(),
                )],
            )?;
            self.close()?;
            layers.push("LayerElementTangent");
            self.layer("LayerElementBinormal", "ByVertice")?;
            self.leaf(
                "Binormals",
                &[A::Doubles(
                    ts.iter()
                        .zip(&g.normals)
                        .flat_map(|(t, n)| {
                            let s = -(t[3] as f64);
                            [
                                (n[1] * t[2] - n[2] * t[1]) as f64 * s,
                                (n[2] * t[0] - n[0] * t[2]) as f64 * s,
                                (n[0] * t[1] - n[1] * t[0]) as f64 * s,
                            ]
                        })
                        .collect(),
                )],
            )?;
            self.close()?;
            layers.push("LayerElementBinormal");
        }
        self.layer("LayerElementSmoothing", "ByPolygon")?;
        self.leaf("Smoothing", &[A::Ints(vec![1; g.indices.len() / 3])])?;
        self.close()?;
        self.open("LayerElementMaterial", &[A::I(0)])?;
        self.leaf("Version", &[A::I(101)])?;
        self.leaf("Name", &[A::S("")])?;
        self.leaf("MappingInformationType", &[A::S("AllSame")])?;
        self.leaf("ReferenceInformationType", &[A::S("IndexToDirect")])?;
        self.leaf("Materials", &[A::Ints(vec![0])])?;
        self.close()?;
        self.open("Layer", &[A::I(0)])?;
        self.leaf("Version", &[A::I(100)])?;
        for layer in layers {
            self.open("LayerElement", &[])?;
            self.leaf("Type", &[A::S(layer)])?;
            self.leaf("TypedIndex", &[A::I(0)])?;
            self.close()?;
        }
        self.close()?;
        self.close()?;
        self.geometries.insert(cache_key, id);
        Ok(id)
    }

    fn material(&mut self, m: &Material) -> Result<i64> {
        // Debug-free stable data key: no paths or source names are included.
        let key = format!("{m:?}");
        if let Some(id) = self.materials.get(&key) {
            return Ok(*id);
        }
        let id = self.object("Material", "Surface", "Phong")?;
        self.leaf("Version", &[A::I(102)])?;
        self.leaf("ShadingModel", &[A::S("phong")])?;
        self.leaf("MultiLayer", &[A::I(0)])?;
        self.open("Properties70", &[])?;
        self.vector(
            "DiffuseColor",
            "Color",
            [m.base_color[0], m.base_color[1], m.base_color[2]],
        )?;
        self.number("DiffuseFactor", 1.0)?;
        self.vector("EmissiveColor", "Color", m.emissive)?;
        self.number("EmissiveFactor", 1.0)?;
        self.vector("SpecularColor", "Color", [1.0; 3])?;
        self.number("SpecularFactor", 0.25)?;
        // Blender's legacy FBX importer maps sqrt(Shininess)/10 to smoothness
        // and ReflectionFactor to metalness. Keep this compatibility profile
        // until FBX has a portable metallic/roughness shader representation.
        self.number("Shininess", (1.0 - m.roughness).powi(2) * 100.0)?;
        self.number("ReflectionFactor", m.metallic)?;
        self.number("TransparencyFactor", 1.0 - m.base_color[3])?;
        self.number("Opacity", m.base_color[3])?;
        self.vector("TransparentColor", "Color", [1.0 - m.base_color[3]; 3])?;
        self.number("BumpFactor", 1.0)?;
        self.close()?;
        self.close()?;
        for (texture, property, role) in [
            (
                &m.base_color_texture,
                "DiffuseColor",
                TextureRole::BaseColor,
            ),
            (
                &m.metallic_texture,
                "ReflectionFactor",
                TextureRole::Metallic,
            ),
            (
                &m.roughness_texture,
                "ShininessExponent",
                TextureRole::Roughness,
            ),
            (&m.normal_texture, "NormalMap", TextureRole::Normal),
            (&m.emissive_texture, "EmissiveColor", TextureRole::Emissive),
        ] {
            if let Some(asset) = texture {
                let bytes = forge_scene::fbx_texture(&self.p.assets[asset], m, role)?;
                let file = format!("texture-{}.png", self.next + 1);
                let video = self.object("Video", "Image", "Clip")?;
                self.leaf("Type", &[A::S("Clip")])?;
                self.leaf("UseMipMap", &[A::I(1)])?;
                self.leaf("Filename", &[A::S(&file)])?;
                self.leaf("RelativeFilename", &[A::S(&file)])?;
                self.leaf("Content", &[A::B(&bytes)])?;
                self.close()?;
                let tex = self.object("Texture", "Texture", "")?;
                self.leaf("Type", &[A::S("TextureVideoClip")])?;
                self.leaf("Version", &[A::I(202)])?;
                self.leaf("TextureName", &[A::S("Texture")])?;
                self.leaf("Media", &[A::S("Image")])?;
                self.leaf("FileName", &[A::S(&file)])?;
                self.leaf("RelativeFilename", &[A::S(&file)])?;
                self.open("Properties70", &[])?;
                self.prop("UVSet", "KString", "", &[A::S("UV0")])?;
                self.vector("Scaling", "Vector3D", [1.0; 3])?;
                self.close()?;
                self.close()?;
                self.link(video, tex, None);
                self.link(tex, id, Some(property));
                if matches!(role, TextureRole::BaseColor) && m.alpha_mode == AlphaMode::Blend {
                    self.link(tex, id, Some("TransparencyFactor"));
                }
            }
        }
        self.materials.insert(key, id);
        Ok(id)
    }

    fn node(&mut self, n: &Node, parent: i64) -> Result<()> {
        checkpoint()?;
        let sub = match n.kind {
            Kind::Group => "Null",
            Kind::Joint => "LimbNode",
            Kind::Mesh { .. } => "Mesh",
            Kind::PerspectiveCamera { .. } | Kind::OrthographicCamera { .. } => "Camera",
            _ => "Light",
        };
        let id = self.object("Model", &n.name, sub)?;
        self.model_ids.insert(n.id.to_string(), id);
        self.leaf("Version", &[A::I(232)])?;
        self.open("Properties70", &[])?;
        self.vector("Lcl Translation", "Lcl Translation", n.translation)?;
        let mut q = DQuat::from_array(n.rotation);
        if sub == "Camera" {
            q *= DQuat::from_rotation_y(std::f64::consts::FRAC_PI_2);
        } else if sub == "Light" {
            q *= DQuat::from_rotation_x(std::f64::consts::FRAC_PI_2);
        }
        let (x, y, z) = q.to_euler(EulerRot::XYZEx);
        self.vector(
            "Lcl Rotation",
            "Lcl Rotation",
            [x.to_degrees(), y.to_degrees(), z.to_degrees()],
        )?;
        self.vector("Lcl Scaling", "Lcl Scaling", n.scale)?;
        self.prop("RotationActive", "bool", "", &[A::I(1)])?;
        self.prop("InheritType", "enum", "", &[A::I(1)])?;
        self.prop("ReactForgeId", "KString", "", &[A::S(&n.id.to_string())])?;
        self.close()?;
        if let Kind::Mesh { material, .. } = &n.kind {
            self.leaf(
                "Culling",
                &[A::S(if material.double_sided {
                    "CullingOff"
                } else {
                    "CullingOnCCW"
                })],
            )?;
        }
        self.close()?;
        self.link(id, parent, None);
        match &n.kind {
            Kind::Group => {}
            Kind::Joint => {
                let attribute = self.object("NodeAttribute", &n.name, "LimbNode")?;
                self.leaf("TypeFlags", &[A::S("Skeleton")])?;
                self.open("Properties70", &[])?;
                self.number("Size", 1.)?;
                self.close()?;
                self.close()?;
                self.link(attribute, id, None);
            }
            Kind::Mesh {
                geometry, material, ..
            } => {
                let deformed = self.p.skins.contains_key(&n.id)
                    || !self.p.geometries[geometry].morph_targets.is_empty();
                let owner = n.id.to_string();
                let g = self.geometry(geometry, if deformed { Some(&owner) } else { None })?;
                self.mesh_ids.insert(owner, g);
                let m = self.material(material)?;
                self.link(g, id, None);
                self.link(m, id, None);
            }
            Kind::PerspectiveCamera {
                yfov,
                aspect,
                near,
                far,
            }
            | Kind::OrthographicCamera {
                xmag: yfov,
                ymag: aspect,
                near,
                far,
            } => {
                let a = self.object("NodeAttribute", &n.name, "Camera")?;
                self.leaf("TypeFlags", &[A::S("Camera")])?;
                self.open("Properties70", &[])?;
                self.number("NearPlane", *near)?;
                self.number("FarPlane", *far)?;
                if matches!(n.kind, Kind::PerspectiveCamera { .. }) {
                    self.prop("CameraProjectionType", "enum", "", &[A::I(0)])?;
                    self.number("FilmWidth", 36.0 / 25.4)?;
                    self.number("FilmHeight", 36.0 / aspect / 25.4)?;
                    self.number("FocalLength", 18.0 / aspect / (yfov / 2.0).tan())?;
                    self.number(
                        "FieldOfView",
                        (2.0 * ((yfov / 2.0).tan() * aspect).atan()).to_degrees(),
                    )?;
                } else {
                    self.prop("CameraProjectionType", "enum", "", &[A::I(1)])?;
                    self.number("OrthoZoom", 2.0 * yfov.max(*aspect))?;
                    self.number("FilmWidth", 36.0 / 25.4)?;
                    self.number("FilmHeight", 36.0 * aspect / yfov / 25.4)?;
                }
                self.close()?;
                self.close()?;
                self.link(a, id, None);
            }
            Kind::DirectionalLight { color, intensity }
            | Kind::PointLight { color, intensity }
            | Kind::SpotLight {
                color, intensity, ..
            } => {
                let a = self.object("NodeAttribute", &n.name, "Light")?;
                self.leaf("TypeFlags", &[A::S("Light")])?;
                self.open("Properties70", &[])?;
                let kind = match n.kind {
                    Kind::DirectionalLight { .. } => 1,
                    Kind::PointLight { .. } => 0,
                    _ => 2,
                };
                self.prop("LightType", "enum", "", &[A::I(kind)])?;
                self.vector("Color", "Color", *color)?;
                // Match Blender's glTF SPEC conversion (683 lm/W). Source
                // photometric intensity is retained as a named custom property.
                self.number(
                    "Intensity",
                    intensity / 683.0
                        * 100.0
                        * if kind == 1 {
                            1.0
                        } else {
                            4.0 * std::f64::consts::PI
                        },
                )?;
                self.number("ReactForgePhotometricIntensity", *intensity)?;
                if let Kind::SpotLight {
                    inner_cone,
                    outer_cone,
                    ..
                } = n.kind
                {
                    self.number("InnerAngle", 2.0 * inner_cone.to_degrees())?;
                    self.number("OuterAngle", 2.0 * outer_cone.to_degrees())?;
                }
                self.close()?;
                self.close()?;
                self.link(a, id, None);
            }
        }
        for c in &n.children {
            self.node(c, id)?;
        }
        Ok(())
    }
}
pub fn export(p: &Prepared<'_>) -> Result<Vec<u8>> {
    let writer = Writer::new(Sink(Cursor::new(vec![])), FbxVersion::V7_4).map_err(failure)?;
    let mut w = Fbx {
        writer,
        p,
        next: 100,
        connections: vec![],
        counts: BTreeMap::new(),
        geometries: BTreeMap::new(),
        materials: BTreeMap::new(),
        model_ids: BTreeMap::new(),
        mesh_ids: BTreeMap::new(),
        blend_channels: BTreeMap::new(),
    };
    w.open("FBXHeaderExtension", &[])?;
    w.leaf("FBXHeaderVersion", &[A::I(1003)])?;
    w.leaf("FBXVersion", &[A::I(7400)])?;
    w.leaf("EncryptionType", &[A::I(0)])?;
    w.leaf("Creator", &[A::S("React Forge")])?;
    w.close()?;
    w.open("GlobalSettings", &[])?;
    w.leaf("Version", &[A::I(1000)])?;
    w.open("Properties70", &[])?;
    // FBX FrontAxis describes the view-facing axis, opposite the local -Z
    // forward direction. A negative sign here declares a left-handed basis.
    for (name, v) in [
        ("UpAxis", 1),
        ("UpAxisSign", 1),
        ("FrontAxis", 2),
        ("FrontAxisSign", 1),
        ("CoordAxis", 0),
        ("CoordAxisSign", 1),
        ("OriginalUpAxis", 1),
        ("OriginalUpAxisSign", 1),
    ] {
        w.prop(name, "int", "Integer", &[A::I(v)])?;
    }
    w.number("UnitScaleFactor", 100.0)?;
    w.number("OriginalUnitScaleFactor", 100.0)?;
    w.close()?;
    w.close()?;
    w.open("Documents", &[])?;
    w.leaf("Count", &[A::I(1)])?;
    w.open("Document", &[A::L(1), A::S("Scene"), A::S("Scene")])?;
    w.open("Properties70", &[])?;
    w.prop("SourceObject", "object", "", &[])?;
    w.prop("ActiveAnimStackName", "KString", "", &[A::S("")])?;
    w.close()?;
    w.leaf("RootNode", &[A::L(0)])?;
    w.close()?;
    w.close()?;
    w.leaf("References", &[])?;
    w.open("Objects", &[])?;
    for n in &p.scene.nodes {
        w.node(n, 0)?;
    }
    w.deformation()?;
    w.animations()?;
    w.close()?;
    w.open("Definitions", &[])?;
    w.leaf("Version", &[A::I(100)])?;
    w.leaf("Count", &[A::I(w.counts.values().sum())])?;
    for (kind, count) in w.counts.clone() {
        w.open("ObjectType", &[A::S(kind)])?;
        w.leaf("Count", &[A::I(count)])?;
        w.close()?;
    }
    w.close()?;
    w.open("Connections", &[])?;
    for (a, b, prop) in std::mem::take(&mut w.connections) {
        if let Some(p) = prop {
            w.leaf("C", &[A::S("OP"), A::L(a), A::L(b), A::S(&p)])?;
        } else {
            w.leaf("C", &[A::S("OO"), A::L(a), A::L(b)])?;
        }
    }
    w.close()?;
    let bytes = w
        .writer
        .finalize_and_flush(&Default::default())
        .map_err(failure)?
        .0
        .into_inner();
    checkpoint()?;
    Ok(bytes)
}

#[cfg(test)]
mod tests {
    use std::sync::{Arc, atomic::AtomicBool};

    use super::*;

    #[test]
    fn output_limit_precedes_allocation_and_cancellation_precedes_writes() {
        let mut sink = Sink(Cursor::new(Vec::new()));
        sink.seek(SeekFrom::Start(MAX_BYTES as u64)).unwrap();
        assert_eq!(
            sink.write(&[1]).unwrap_err().kind(),
            io::ErrorKind::OutOfMemory
        );
        assert!(sink.0.get_ref().is_empty());
        let flag = Arc::new(AtomicBool::new(true));
        forge_tree_doc::cancellation::with_cancellation(flag, || {
            sink.seek(SeekFrom::Start(0)).unwrap();
            assert_eq!(
                sink.write(&[1]).unwrap_err().kind(),
                io::ErrorKind::Interrupted
            );
            Ok::<(), Diagnostic>(())
        })
        .unwrap();
        assert!(sink.0.get_ref().is_empty());
    }
}
