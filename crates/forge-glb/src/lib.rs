//! Self-contained glTF 2.0 binary export of validated scenes.
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
    skins: Vec<Value>,
    animations: Vec<Value>,
    node_ids: BTreeMap<String, usize>,
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
        let view = self.bytes(
            &bytes,
            if matches!(kind, "MAT4" | "SCALAR") {
                None
            } else {
                Some(34962)
            },
        )?;
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
        if let Some(skin) = &g.skin {
            let mut bytes = Vec::with_capacity(skin.joints.len() * 8);
            for (i, joints) in skin.joints.iter().enumerate() {
                if i % 4096 == 0 {
                    checkpoint()?;
                }
                for joint in joints {
                    bytes.extend_from_slice(&joint.to_le_bytes());
                }
            }
            let view = self.bytes(&bytes, Some(34962))?;
            attr["JOINTS_0"] = self.accessors.len().into();
            self.accessors.push(json!({"bufferView":view,"componentType":5123,"count":skin.joints.len(),"type":"VEC4"}));
            attr["WEIGHTS_0"] = self.floats(&skin.weights, "VEC4", false)?.into();
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
        self.node_ids.insert(node.id.to_string(), id);
        let mut value = json!({"name":node.name,"translation":node.translation,"rotation":node.rotation,"scale":node.scale,"extras":{"reactForgeId":node.id}});
        match &node.kind {
            Kind::Group | Kind::Joint => {}
            Kind::Mesh {
                geometry,
                material,
                morph_weights,
                skin,
                ..
            } => {
                let (attributes, indices) =
                    self.geometry(geometry, &self.prepared.geometries[geometry])?;
                let m = self.material(material)?;
                value["mesh"] = self.meshes.len().into();
                let mut primitive =
                    json!({"attributes":attributes,"indices":indices,"material":m,"mode":4});
                let g = &self.prepared.geometries[geometry];
                if !g.morph_targets.is_empty() {
                    let mut targets = vec![];
                    for morph in &g.morph_targets {
                        let mut target =
                            json!({"POSITION":self.floats(&morph.positions,"VEC3",true)?});
                        if let Some(normals) = &morph.normals {
                            target["NORMAL"] = self.floats(normals, "VEC3", false)?.into();
                        }
                        targets.push(target);
                    }
                    primitive["targets"] = json!(targets);
                    value["weights"] = if morph_weights.is_empty() {
                        json!(vec![0.; g.morph_targets.len()])
                    } else {
                        json!(morph_weights)
                    };
                }
                let mut mesh = json!({"primitives":[primitive]});
                if !g.morph_targets.is_empty() {
                    mesh["extras"] = json!({"targetNames":g.morph_targets.iter().map(|m|&m.name).collect::<Vec<_>>()});
                }
                self.meshes.push(mesh);
                if skin.is_some() {
                    // glTF ignores a skinned mesh node's transform. Bind matrices
                    // already contain its authored mesh-to-world rest transform.
                    for key in ["translation", "rotation", "scale"] {
                        value.as_object_mut().unwrap().remove(key);
                    }
                }
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

    fn deformation(&mut self) -> Result<()> {
        for (id, skin) in &self.prepared.skins {
            checkpoint()?;
            let matrices: Vec<[f32; 16]> = skin
                .inverse_bind_matrices
                .iter()
                .map(|m| m.to_cols_array().map(|v| v as f32))
                .collect();
            let accessor = self.floats(&matrices, "MAT4", false)?;
            let node = self.node_ids[&id.to_string()];
            self.nodes[node]["skin"] = self.skins.len().into();
            self.skins.push(json!({"joints":skin.joints.iter().map(|j|self.node_ids[&j.to_string()]).collect::<Vec<_>>(),"inverseBindMatrices":accessor}));
        }
        for clip in &self.prepared.scene.animations {
            let mut samplers = vec![];
            let mut channels = vec![];
            for track in &clip.tracks {
                checkpoint()?;
                let s = &self.prepared.samplers[&track.sampler];
                let times: Vec<[f32; 1]> = s.times.iter().map(|v| [*v]).collect();
                let input = self.floats(&times, "SCALAR", true)?;
                let multiplier = if s.interpolation == forge_scene::Interpolation::Cubic {
                    3
                } else {
                    1
                };
                let size = s
                    .values
                    .len()
                    .checked_mul(multiplier * 4)
                    .ok_or_else(limited)?;
                if size > MAX_BYTES || self.bin.len().saturating_add(size) > MAX_BYTES {
                    return Err(limited());
                }
                let mut bytes = Vec::with_capacity(size);
                for i in 0..s.times.len() {
                    if i % 1024 == 0 {
                        checkpoint()?;
                    }
                    let range = i * s.width..(i + 1) * s.width;
                    if multiplier == 3 {
                        for v in &s.in_tangents[range.clone()] {
                            bytes.extend_from_slice(&v.to_le_bytes());
                        }
                    }
                    for v in &s.values[range.clone()] {
                        bytes.extend_from_slice(&v.to_le_bytes());
                    }
                    if multiplier == 3 {
                        for v in &s.out_tangents[range] {
                            bytes.extend_from_slice(&v.to_le_bytes());
                        }
                    }
                }
                let view = self.bytes(&bytes, None)?;
                let output = self.accessors.len();
                let (kind, count) = match s.path {
                    forge_scene::AnimationPath::Weights => ("SCALAR", s.values.len() * multiplier),
                    forge_scene::AnimationPath::Rotation => ("VEC4", s.times.len() * multiplier),
                    _ => ("VEC3", s.times.len() * multiplier),
                };
                self.accessors.push(
                    json!({"bufferView":view,"componentType":5126,"count":count,"type":kind}),
                );
                channels.push(json!({"sampler":samplers.len(),"target":{"node":self.node_ids[&track.target.to_string()],"path":s.path.name()}}));
                samplers.push(
                    json!({"input":input,"output":output,"interpolation":s.interpolation.name()}),
                );
            }
            self.animations
                .push(json!({"name":clip.name,"channels":channels,"samplers":samplers}));
        }
        Ok(())
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
        skins: vec![],
        animations: vec![],
        node_ids: BTreeMap::new(),
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
    w.deformation()?;
    let mut root = json!({"asset":{"version":"2.0","generator":"React Forge"},"scene":0,"scenes":[{"nodes":roots}],"nodes":w.nodes,"extras":{"reactForgeDocumentId":prepared.scene.document_id}});
    for (name, values) in [
        ("bufferViews", w.views),
        ("accessors", w.accessors),
        ("images", w.images),
        ("textures", w.textures),
        ("materials", w.materials),
        ("meshes", w.meshes),
        ("cameras", w.cameras),
        ("skins", w.skins),
        ("animations", w.animations),
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
