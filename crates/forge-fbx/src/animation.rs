//! Native FBX animation/deformer records, independently checked with ufbx.
use forge_scene::{AnimationPath, AnimationSampler, Interpolation};

use super::*;

const TICKS_PER_SECOND: f64 = 46_186_158_000.;
fn ticks(time: f64) -> Result<i64> {
    let value = time * TICKS_PER_SECOND;
    if !value.is_finite() || value < 0. || value >= i64::MAX as f64 {
        return Err(forge_scene::invalid("animations/time"));
    }
    Ok(value.round() as i64)
}

impl Fbx<'_> {
    pub(super) fn deformation(&mut self) -> Result<()> {
        for node in self.p.nodes.values() {
            checkpoint()?;
            let Kind::Mesh {
                geometry,
                morph_weights,
                ..
            } = &node.kind
            else {
                continue;
            };
            let g = &self.p.geometries[geometry];
            let owner = node.id.to_string();
            let mesh = self.mesh_ids[&owner];
            if let Some(skin) = self.p.skins.get(&node.id) {
                let deformer = self.object("Deformer", "Skin", "Skin")?;
                self.leaf("Version", &[A::I(101)])?;
                self.leaf("Link_DeformAcuracy", &[A::D(50.)])?;
                self.leaf("SkinningType", &[A::S("Linear")])?;
                self.close()?;
                self.link(deformer, mesh, None);
                let vertices = g.skin.as_ref().unwrap();
                // Collect each influence once. Scanning all vertices separately
                // for every joint makes large but valid rigs quadratic.
                let mut clusters: Vec<(Vec<i32>, Vec<f64>)> =
                    (0..skin.joints.len()).map(|_| (vec![], vec![])).collect();
                for (i, (joints, weights)) in
                    vertices.joints.iter().zip(&vertices.weights).enumerate()
                {
                    if i % 4096 == 0 {
                        checkpoint()?;
                    }
                    for k in 0..4 {
                        if weights[k] > 0. {
                            let c = &mut clusters[joints[k] as usize];
                            c.0.push(i as i32);
                            c.1.push(weights[k] as f64);
                        }
                    }
                }
                for (i, (indices, weights)) in clusters.into_iter().enumerate() {
                    let cluster = self.object("Deformer", "Cluster", "Cluster")?;
                    self.leaf("Version", &[A::I(100)])?;
                    self.leaf("Indexes", &[A::Ints(indices)])?;
                    self.leaf("Weights", &[A::Doubles(weights)])?;
                    // FBX Transform is mesh-to-bone; TransformLink is the bone's
                    // bind-to-world matrix. Keeping the two distinct matters
                    // when the rest mesh and skeleton have different parents.
                    self.leaf(
                        "Transform",
                        &[A::Doubles(
                            skin.inverse_bind_matrices[i].to_cols_array().to_vec(),
                        )],
                    )?;
                    self.leaf(
                        "TransformLink",
                        &[A::Doubles(
                            self.p.rest_world[&skin.joints[i]].to_cols_array().to_vec(),
                        )],
                    )?;
                    self.leaf("Mode", &[A::S("Normalize")])?;
                    self.close()?;
                    self.link(cluster, deformer, None);
                    self.link(self.model_ids[&skin.joints[i].to_string()], cluster, None);
                }
                let pose = self.object("Pose", "BindPose", "BindPose")?;
                let _ = pose;
                self.leaf("Type", &[A::S("BindPose")])?;
                self.leaf("Version", &[A::I(100)])?;
                self.leaf("NbPoseNodes", &[A::I((skin.joints.len() + 1) as i32)])?;
                for id in std::iter::once(&node.id).chain(&skin.joints) {
                    self.open("PoseNode", &[])?;
                    self.leaf("Node", &[A::L(self.model_ids[&id.to_string()])])?;
                    self.leaf(
                        "Matrix",
                        &[A::Doubles(self.p.rest_world[id].to_cols_array().to_vec())],
                    )?;
                    self.close()?;
                }
                self.close()?;
            }
            if !g.morph_targets.is_empty() {
                let deformer = self.object("Deformer", "Morphs", "BlendShape")?;
                self.leaf("Version", &[A::I(100)])?;
                self.close()?;
                self.link(deformer, mesh, None);
                let mut channels = vec![];
                for (i, morph) in g.morph_targets.iter().enumerate() {
                    let channel = self.object("Deformer", &morph.name, "BlendShapeChannel")?;
                    self.leaf("Version", &[A::I(100)])?;
                    let weight = morph_weights.get(i).copied().unwrap_or(0.) * 100.;
                    self.leaf("DeformPercent", &[A::D(weight)])?;
                    self.open("Properties70", &[])?;
                    self.number("DeformPercent", weight)?;
                    self.close()?;
                    self.leaf("FullWeights", &[A::Doubles(vec![100.])])?;
                    self.close()?;
                    self.link(channel, deformer, None);
                    let shape = self.object("Geometry", &morph.name, "Shape")?;
                    self.leaf("Version", &[A::I(100)])?;
                    self.leaf(
                        "Indexes",
                        &[A::Ints((0..g.positions.len() as i32).collect())],
                    )?;
                    self.leaf(
                        "Vertices",
                        &[A::Doubles(
                            morph
                                .positions
                                .iter()
                                .flatten()
                                .map(|v| *v as f64)
                                .collect(),
                        )],
                    )?;
                    if let Some(normals) = &morph.normals {
                        self.leaf(
                            "Normals",
                            &[A::Doubles(
                                normals.iter().flatten().map(|v| *v as f64).collect(),
                            )],
                        )?;
                    }
                    self.close()?;
                    self.link(shape, channel, None);
                    channels.push(channel);
                }
                self.blend_channels.insert(owner, channels);
            }
        }
        Ok(())
    }

    fn curve(
        &mut self,
        parent: i64,
        component: &str,
        times: &[f64],
        values: Vec<f32>,
        step: bool,
    ) -> Result<()> {
        let curve = self.object("AnimationCurve", component, "")?;
        self.leaf("Default", &[A::D(values[0] as f64)])?;
        self.leaf("KeyVer", &[A::I(4008)])?;
        let times = times
            .iter()
            .map(|t| ticks(*t))
            .collect::<Result<Vec<_>>>()?;
        if times.windows(2).any(|p| p[0] >= p[1]) {
            return Err(forge_scene::invalid("animations/times"));
        }
        self.leaf("KeyTime", &[A::Longs(times)])?;
        let count = values.len();
        self.leaf("KeyValueFloat", &[A::Floats(values)])?;
        self.leaf("KeyAttrFlags", &[A::Ints(vec![if step { 2 } else { 4 }])])?;
        self.leaf("KeyAttrDataFloat", &[A::Floats(vec![0.; 4])])?;
        self.leaf("KeyAttrRefCount", &[A::Ints(vec![count as i32])])?;
        self.close()?;
        self.link(curve, parent, Some(component));
        Ok(())
    }

    fn curve_node(
        &mut self,
        layer: i64,
        target: i64,
        property: &str,
        components: &[&str],
        defaults: &[f64],
    ) -> Result<i64> {
        let id = self.object("AnimationCurveNode", property, "")?;
        self.open("Properties70", &[])?;
        for (component, value) in components.iter().zip(defaults) {
            self.number(component, *value)?;
        }
        self.close()?;
        self.close()?;
        self.link(id, layer, None);
        self.link(id, target, Some(property));
        Ok(id)
    }

    pub(super) fn animations(&mut self) -> Result<()> {
        let mut remaining = MAX_BYTES;
        for clip in &self.p.scene.animations {
            checkpoint()?;
            let duration = clip
                .tracks
                .iter()
                .map(|t| self.p.samplers[&t.sampler].duration())
                .fold(0., f64::max);
            let stop = ticks(duration)?;
            let stack = self.object("AnimationStack", &clip.name, "")?;
            self.open("Properties70", &[])?;
            for (name, value) in [
                ("LocalStart", 0),
                ("LocalStop", stop),
                ("ReferenceStart", 0),
                ("ReferenceStop", stop),
            ] {
                self.prop(name, "KTime", "Time", &[A::L(value)])?;
            }
            self.close()?;
            self.close()?;
            let layer = self.object("AnimationLayer", &clip.name, "")?;
            self.open("Properties70", &[])?;
            self.number("Weight", 100.)?;
            self.prop("BlendMode", "enum", "", &[A::I(1)])?;
            self.close()?;
            self.close()?;
            self.link(layer, stack, None);
            for track in &clip.tracks {
                let s = &self.p.samplers[&track.sampler];
                let node = self.p.nodes[&track.target];
                let baked = s.interpolation == Interpolation::Cubic
                    || (s.path == AnimationPath::Rotation
                        && s.interpolation != Interpolation::Step);
                let times =
                    sample_times(s, self.p.scene.animation_bake_fps, baked, &mut remaining)?;
                let mut columns: Vec<Vec<f32>> = (0..if s.path == AnimationPath::Rotation {
                    3
                } else {
                    s.width
                })
                    .map(|_| Vec::with_capacity(times.len()))
                    .collect();
                let mut previous = None;
                for (i, time) in times.iter().enumerate() {
                    if i % 1024 == 0 {
                        checkpoint()?;
                    }
                    let mut value = s.evaluate(*time)?;
                    if s.path == AnimationPath::Rotation {
                        let rotation =
                            rotation_euler(node, value.as_slice().try_into().unwrap(), previous);
                        previous = Some(rotation);
                        value = rotation.to_vec();
                    } else if s.path == AnimationPath::Weights {
                        for v in &mut value {
                            *v *= 100.;
                        }
                    }
                    for (c, v) in columns.iter_mut().zip(value) {
                        if !(v as f32).is_finite() {
                            return Err(forge_scene::invalid("animations/values"));
                        }
                        c.push(v as f32);
                    }
                }
                let step = s.interpolation == Interpolation::Step;
                if s.path == AnimationPath::Weights {
                    let channels = self.blend_channels[&node.id.to_string()].clone();
                    for (channel, values) in channels.into_iter().zip(columns) {
                        let cn = self.curve_node(
                            layer,
                            channel,
                            "DeformPercent",
                            &["d|DeformPercent"],
                            &[values[0] as f64],
                        )?;
                        self.curve(cn, "d|DeformPercent", &times, values, step)?;
                    }
                } else {
                    let property = match s.path {
                        AnimationPath::Translation => "Lcl Translation",
                        AnimationPath::Rotation => "Lcl Rotation",
                        _ => "Lcl Scaling",
                    };
                    let defaults: Vec<_> = columns.iter().map(|c| c[0] as f64).collect();
                    let cn = self.curve_node(
                        layer,
                        self.model_ids[&node.id.to_string()],
                        property,
                        &["d|X", "d|Y", "d|Z"],
                        &defaults,
                    )?;
                    for (name, values) in ["d|X", "d|Y", "d|Z"].into_iter().zip(columns) {
                        self.curve(cn, name, &times, values, step)?;
                    }
                }
            }
        }
        Ok(())
    }
}
fn sample_times(
    s: &AnimationSampler,
    fps: u32,
    baked: bool,
    remaining: &mut usize,
) -> Result<Vec<f64>> {
    let frames = if baked {
        (s.duration() * fps as f64).ceil()
    } else {
        0.
    };
    let upper = frames + 1. + s.times.len() as f64;
    // Bound expansion across the complete export, before any dense allocation.
    // 32 bytes per component/sample covers curves, times and temporary columns.
    let bytes = upper * s.width as f64 * 32.;
    if !bytes.is_finite() || bytes > *remaining as f64 {
        return Err(forge_scene::limited());
    }
    *remaining -= bytes as usize;
    let mut times: Vec<_> = s.times.iter().map(|v| *v as f64).collect();
    if baked {
        for i in 0..=frames as usize {
            if i % 4096 == 0 {
                checkpoint()?;
            }
            times.push((i as f64 / fps as f64).min(s.duration()));
        }
    }
    times.sort_by(f64::total_cmp);
    times.dedup_by(|a, b| a == b);
    Ok(times)
}
fn rotation_euler(node: &Node, value: [f64; 4], previous: Option<[f64; 3]>) -> [f64; 3] {
    let mut q = DQuat::from_array(value);
    match node.kind {
        Kind::PerspectiveCamera { .. } | Kind::OrthographicCamera { .. } => {
            q *= DQuat::from_rotation_y(std::f64::consts::FRAC_PI_2)
        }
        Kind::DirectionalLight { .. } | Kind::PointLight { .. } | Kind::SpotLight { .. } => {
            q *= DQuat::from_rotation_x(std::f64::consts::FRAC_PI_2)
        }
        _ => {}
    }
    let (x, y, z) = q.to_euler(EulerRot::XYZEx);
    let a = [x.to_degrees(), y.to_degrees(), z.to_degrees()];
    let Some(previous) = previous else { return a };
    let unwrap = |mut v: [f64; 3]| {
        for k in 0..3 {
            v[k] += ((previous[k] - v[k]) / 360.).round() * 360.;
        }
        v
    };
    let a = unwrap(a);
    let b = unwrap([a[0] + 180., 180. - a[1], a[2] + 180.]);
    let distance = |v: [f64; 3]| (0..3).map(|i| (v[i] - previous[i]).powi(2)).sum::<f64>();
    if distance(a) <= distance(b) { a } else { b }
}
