//! Self-contained glTF 2.0 binary export of validated static scenes.
#![forbid(unsafe_code)]
use std::collections::BTreeMap;

use forge_scene::{AlphaMode, Geometry, Kind, MAX_BYTES, Material, Node, Prepared, limited};
use forge_tree_doc::{Result, cancellation::checkpoint};
use serde_json::{Value, json};

struct Writer<'a> {
    prepared: &'a Prepared<'a>,
    bin: Vec<u8>,
    views: Vec<Value>,
    accessors: Vec<Value>,
    images: Vec<Value>,
    textures: Vec<Value>,
    materials: Vec<Value>,
    meshes: Vec<Value>,
    nodes: Vec<Value>,
    cameras: Vec<Value>,
    lights: Vec<Value>,
    image_ids: BTreeMap<String, usize>,
    material_ids: BTreeMap<String, usize>,
    geometry_ids: BTreeMap<String, (Value, usize)>,
}
impl Writer<'_> {
    fn bytes(&mut self, bytes: &[u8], target: Option<u32>) -> Result<usize> {
        checkpoint()?;
        while !self.bin.len().is_multiple_of(4) {
            self.bin.push(0);
        }
        if self
            .bin
            .len()
            .checked_add(bytes.len())
            .is_none_or(|n| n > MAX_BYTES)
        {
            return Err(limited());
        }
        let id = self.views.len();
        let mut view = json!({"buffer":0,"byteOffset":self.bin.len(),"byteLength":bytes.len()});
        if let Some(target) = target {
            view["target"] = target.into();
        }
        self.bin.extend_from_slice(bytes);
        self.views.push(view);
        Ok(id)
    }

    fn floats<const N: usize>(
        &mut self,
        values: &[[f32; N]],
        kind: &str,
        bounds: bool,
    ) -> Result<usize> {
        let mut bytes = Vec::with_capacity(values.len() * N * 4);
        let mut min = [f32::INFINITY; N];
        let mut max = [f32::NEG_INFINITY; N];
        for (i, v) in values.iter().enumerate() {
            if i % 4096 == 0 {
                checkpoint()?;
            }
            for k in 0..N {
                bytes.extend_from_slice(&v[k].to_le_bytes());
                min[k] = min[k].min(v[k]);
                max[k] = max[k].max(v[k]);
            }
        }
        let view = self.bytes(&bytes, Some(34962))?;
        let id = self.accessors.len();
        let mut a =
            json!({"bufferView":view,"componentType":5126,"count":values.len(),"type":kind});
        if bounds {
            a["min"] = json!(min.as_slice());
            a["max"] = json!(max.as_slice());
        }
        self.accessors.push(a);
        Ok(id)
    }

    fn geometry(&mut self, id: &str, g: &Geometry) -> Result<(Value, usize)> {
        if let Some(v) = self.geometry_ids.get(id) {
            return Ok(v.clone());
        }
        let mut attr = json!({"POSITION":self.floats(&g.positions,"VEC3",true)?,"NORMAL":self.floats(&g.normals,"VEC3",false)?});
        if let Some(uv) = &g.uv {
            attr["TEXCOORD_0"] = self.floats(uv, "VEC2", false)?.into();
        }
        if let Some(t) = &g.tangents {
            attr["TANGENT"] = self.floats(t, "VEC4", false)?.into();
        }
        let mut bytes = Vec::with_capacity(g.indices.len() * 4);
        for (i, index) in g.indices.iter().enumerate() {
            if i % 8192 == 0 {
                checkpoint()?;
            }
            bytes.extend_from_slice(&index.to_le_bytes());
        }
        let view = self.bytes(&bytes, Some(34963))?;
        let indices = self.accessors.len();
        self.accessors.push(
            json!({"bufferView":view,"componentType":5125,"count":g.indices.len(),"type":"SCALAR"}),
        );
        self.geometry_ids.insert(id.into(), (attr.clone(), indices));
        Ok((attr, indices))
    }

    fn image(&mut self, key: &str, bytes: &[u8], mime: &str) -> Result<usize> {
        if let Some(id) = self.image_ids.get(key) {
            return Ok(*id);
        }
        let view = self.bytes(bytes, None)?;
        let source = self.images.len();
        self.images.push(json!({"bufferView":view,"mimeType":mime}));
        let id = self.textures.len();
        self.textures.push(json!({"source":source,"sampler":0}));
        self.image_ids.insert(key.into(), id);
        Ok(id)
    }

    fn registered_texture(&mut self, id: &str) -> Result<Value> {
        let mime = if self.prepared.textures[id].0 == "png" {
            "image/png"
        } else {
            "image/jpeg"
        };
        Ok(json!({"index":self.image(id,&self.prepared.assets[id],mime)?}))
    }

    fn material(&mut self, m: &Material) -> Result<usize> {
        let key = serde_json::to_string(m).map_err(|_| forge_scene::invalid("material"))?;
        if let Some(id) = self.material_ids.get(&key) {
            return Ok(*id);
        }
        let mut pbr = json!({"baseColorFactor":m.base_color,"metallicFactor":m.metallic,"roughnessFactor":m.roughness});
        if let Some(t) = &m.base_color_texture {
            pbr["baseColorTexture"] = self.registered_texture(t)?;
        }
        if let Some(bytes) = forge_scene::metallic_roughness(self.prepared, m)? {
            let key = format!("mr:{:?}:{:?}", m.metallic_texture, m.roughness_texture);
            pbr["metallicRoughnessTexture"] = json!({"index":self.image(&key,&bytes,"image/png")?});
        }
        let mut value = json!({"pbrMetallicRoughness":pbr,"emissiveFactor":m.emissive,"doubleSided":m.double_sided,"alphaMode":if m.alpha_mode==AlphaMode::Blend {"BLEND"} else {"OPAQUE"}});
        if let Some(t) = &m.normal_texture {
            value["normalTexture"] = self.registered_texture(t)?;
        }
        if let Some(t) = &m.emissive_texture {
            value["emissiveTexture"] = self.registered_texture(t)?;
        }
        let id = self.materials.len();
        self.materials.push(value);
        self.material_ids.insert(key, id);
        Ok(id)
    }

    fn node(&mut self, node: &Node) -> Result<usize> {
        checkpoint()?;
        let id = self.nodes.len();
        self.nodes.push(Value::Null);
        let mut value = json!({"name":node.name,"translation":node.translation,"rotation":node.rotation,"scale":node.scale,"extras":{"reactForgeId":node.id}});
        match &node.kind {
            Kind::Group => {}
            Kind::Mesh { geometry, material } => {
                let (attributes, indices) =
                    self.geometry(geometry, &self.prepared.geometries[geometry])?;
                let m = self.material(material)?;
                value["mesh"] = self.meshes.len().into();
                self.meshes.push(json!({"primitives":[{"attributes":attributes,"indices":indices,"material":m,"mode":4}]}));
            }
            Kind::PerspectiveCamera {
                yfov,
                aspect,
                near,
                far,
            } => {
                value["camera"] = self.cameras.len().into();
                self.cameras.push(json!({"type":"perspective","perspective":{"yfov":yfov,"aspectRatio":aspect,"znear":near,"zfar":far}}));
            }
            Kind::OrthographicCamera {
                xmag,
                ymag,
                near,
                far,
            } => {
                value["camera"] = self.cameras.len().into();
                self.cameras.push(json!({"type":"orthographic","orthographic":{"xmag":xmag,"ymag":ymag,"znear":near,"zfar":far}}));
            }
            Kind::DirectionalLight { color, intensity }
            | Kind::PointLight { color, intensity }
            | Kind::SpotLight {
                color, intensity, ..
            } => {
                let kind = match node.kind {
                    Kind::DirectionalLight { .. } => "directional",
                    Kind::PointLight { .. } => "point",
                    _ => "spot",
                };
                let mut light = json!({"type":kind,"color":color,"intensity":intensity});
                if let Kind::SpotLight {
                    inner_cone,
                    outer_cone,
                    ..
                } = node.kind
                {
                    light["spot"] =
                        json!({"innerConeAngle":inner_cone,"outerConeAngle":outer_cone});
                }
                value["extensions"] = json!({"KHR_lights_punctual":{"light":self.lights.len()}});
                self.lights.push(light);
            }
        }
        if !node.children.is_empty() {
            let children = node
                .children
                .iter()
                .map(|n| self.node(n))
                .collect::<Result<Vec<_>>>()?;
            value["children"] = json!(children);
        }
        self.nodes[id] = value;
        Ok(id)
    }
}
pub fn export(prepared: &Prepared<'_>) -> Result<Vec<u8>> {
    let mut w = Writer {
        prepared,
        bin: vec![],
        views: vec![],
        accessors: vec![],
        images: vec![],
        textures: vec![],
        materials: vec![],
        meshes: vec![],
        nodes: vec![],
        cameras: vec![],
        lights: vec![],
        image_ids: BTreeMap::new(),
        material_ids: BTreeMap::new(),
        geometry_ids: BTreeMap::new(),
    };
    let roots = prepared
        .scene
        .nodes
        .iter()
        .map(|n| w.node(n))
        .collect::<Result<Vec<_>>>()?;
    let mut root = json!({"asset":{"version":"2.0","generator":"React Forge"},"scene":0,"scenes":[{"nodes":roots}],"nodes":w.nodes,"extras":{"reactForgeDocumentId":prepared.scene.document_id}});
    for (name, values) in [
        ("bufferViews", w.views),
        ("accessors", w.accessors),
        ("images", w.images),
        ("textures", w.textures),
        ("materials", w.materials),
        ("meshes", w.meshes),
        ("cameras", w.cameras),
    ] {
        if !values.is_empty() {
            root[name] = json!(values);
        }
    }
    if !w.bin.is_empty() {
        root["buffers"] = json!([{"byteLength":w.bin.len()}]);
    }
    if root.get("textures").is_some() {
        root["samplers"] = json!([{"magFilter":9729,"minFilter":9987,"wrapS":10497,"wrapT":10497}]);
    }
    if !w.lights.is_empty() {
        root["extensionsUsed"] = json!(["KHR_lights_punctual"]);
        root["extensions"] = json!({"KHR_lights_punctual":{"lights":w.lights}});
    }
    let mut json = serde_json::to_vec(&root).map_err(|_| forge_scene::invalid("scene"))?;
    while !json.len().is_multiple_of(4) {
        json.push(b' ');
    }
    while !w.bin.len().is_multiple_of(4) {
        w.bin.push(0);
    }
    let total = 12 + 8 + json.len() + if w.bin.is_empty() { 0 } else { 8 + w.bin.len() };
    if total > MAX_BYTES {
        return Err(limited());
    }
    let mut out = Vec::with_capacity(total);
    out.extend_from_slice(b"glTF");
    out.extend_from_slice(&2u32.to_le_bytes());
    out.extend_from_slice(&(total as u32).to_le_bytes());
    out.extend_from_slice(&(json.len() as u32).to_le_bytes());
    out.extend_from_slice(b"JSON");
    out.extend_from_slice(&json);
    if !w.bin.is_empty() {
        out.extend_from_slice(&(w.bin.len() as u32).to_le_bytes());
        out.extend_from_slice(b"BIN\0");
        out.extend_from_slice(&w.bin);
    }
    checkpoint()?;
    Ok(out)
}
