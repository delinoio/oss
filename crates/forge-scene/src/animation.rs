//! Animation evaluation is shared by measurements and FBX curve baking.
use crate::{assets::Reader, *};

pub fn default_bake_fps() -> u32 {
    60
}
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum AnimationPath {
    Translation,
    Rotation,
    Scale,
    Weights,
}
impl AnimationPath {
    pub fn name(self) -> &'static str {
        match self {
            Self::Translation => "translation",
            Self::Rotation => "rotation",
            Self::Scale => "scale",
            Self::Weights => "weights",
        }
    }
}
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Interpolation {
    Step,
    Linear,
    Cubic,
}
impl Interpolation {
    pub fn name(self) -> &'static str {
        match self {
            Self::Step => "STEP",
            Self::Linear => "LINEAR",
            Self::Cubic => "CUBICSPLINE",
        }
    }
}
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Skin {
    pub joints: Vec<Uuid>,
}
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AnimationTrack {
    pub target: Uuid,
    pub sampler: String,
}
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AnimationClip {
    pub id: Uuid,
    pub name: String,
    pub tracks: Vec<AnimationTrack>,
}
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AnimationSample {
    pub clip: Uuid,
    pub time: f64,
}
pub struct PreparedSkin {
    pub joints: Vec<Uuid>,
    pub inverse_bind_matrices: Vec<DMat4>,
}
#[derive(Clone, Debug)]
pub struct AnimationSampler {
    pub path: AnimationPath,
    pub interpolation: Interpolation,
    pub width: usize,
    pub times: Vec<f32>,
    pub values: Vec<f32>,
    pub in_tangents: Vec<f32>,
    pub out_tangents: Vec<f32>,
}

pub fn animation_sampler(bytes: &[u8]) -> Result<AnimationSampler> {
    let mut r = Reader::new(bytes)?;
    if r.take(4)? != b"FSA1" {
        return Err(invalid("animations/sampler"));
    }
    let path = match r.u32()? {
        0 => AnimationPath::Translation,
        1 => AnimationPath::Rotation,
        2 => AnimationPath::Scale,
        3 => AnimationPath::Weights,
        _ => return Err(invalid("animations/path")),
    };
    let interpolation = match r.u32()? {
        0 => Interpolation::Step,
        1 => Interpolation::Linear,
        2 => Interpolation::Cubic,
        _ => return Err(invalid("animations/interpolation")),
    };
    let width = r.u32()? as usize;
    let count = r.u32()? as usize;
    if count == 0
        || width == 0
        || width > MAX_MORPHS
        || (path != AnimationPath::Weights
            && width
                != if path == AnimationPath::Rotation {
                    4
                } else {
                    3
                })
        || (interpolation == Interpolation::Cubic && count < 2)
    {
        return Err(invalid("animations/sampler"));
    }
    let floats = count.checked_mul(width).ok_or_else(limited)?;
    let times: Vec<f32> = r.floats::<1>(count)?.into_iter().flatten().collect();
    if times[0] < 0. || times.windows(2).any(|t| t[0] >= t[1]) {
        return Err(invalid("animations/times"));
    }
    let values: Vec<f32> = r.floats::<1>(floats)?.into_iter().flatten().collect();
    let (in_tangents, out_tangents) = if interpolation == Interpolation::Cubic {
        (
            r.floats::<1>(floats)?.into_iter().flatten().collect(),
            r.floats::<1>(floats)?.into_iter().flatten().collect(),
        )
    } else {
        (vec![], vec![])
    };
    r.finish()?;
    for (i, value) in values.chunks_exact(width).enumerate() {
        if i % 4096 == 0 {
            checkpoint()?;
        }
        if path == AnimationPath::Rotation
            && (value.iter().map(|v| (*v as f64).powi(2)).sum::<f64>() - 1.).abs() > 2e-6
        {
            return Err(invalid("animations/rotation"));
        }
        if path == AnimationPath::Scale && value.iter().any(|v| v.abs() < 1e-12) {
            return Err(invalid("animations/scale"));
        }
    }
    let sampler = AnimationSampler {
        path,
        interpolation,
        width,
        times,
        values,
        in_tangents,
        out_tangents,
    };
    sampler.validate_segments()?;
    Ok(sampler)
}
impl AnimationSampler {
    pub fn duration(&self) -> f64 {
        *self.times.last().expect("validated sampler") as f64
    }

    fn polynomial(&self, index: usize, component: usize) -> [f64; 4] {
        let at = index * self.width + component;
        let a = self.values[at] as f64;
        let b = self.values[at + self.width] as f64;
        let dt = (self.times[index + 1] as f64) - (self.times[index] as f64);
        let m = self.out_tangents[at] as f64 * dt;
        let n = self.in_tangents[at + self.width] as f64 * dt;
        [2. * a - 2. * b + m + n, -3. * a + 3. * b - 2. * m - n, m, a]
    }

    fn validate_segments(&self) -> Result<()> {
        if self.interpolation == Interpolation::Step
            || !matches!(self.path, AnimationPath::Scale | AnimationPath::Rotation)
        {
            return Ok(());
        }
        for i in 0..self.times.len() - 1 {
            if i % 1024 == 0 {
                checkpoint()?;
            }
            if self.path == AnimationPath::Scale {
                for k in 0..3 {
                    let a = self.values[i * 3 + k] as f64;
                    let b = self.values[(i + 1) * 3 + k] as f64;
                    if a.signum() != b.signum() {
                        return Err(invalid("animations/scale"));
                    }
                    if self.interpolation == Interpolation::Cubic {
                        let p = self.polynomial(i, k);
                        for t in extrema(p) {
                            let v = polynomial(p, t);
                            if v.abs() < 1e-12 || v.signum() != a.signum() {
                                return Err(invalid("animations/scale"));
                            }
                        }
                    }
                }
            } else if self.interpolation == Interpolation::Cubic {
                // A zero quaternion requires every component polynomial to be
                // zero. Isolate roots of one nonzero component, then check the
                // complete quaternion there; this catches interior singularities.
                let ps: Vec<_> = (0..4).map(|k| self.polynomial(i, k)).collect();
                let p = ps
                    .iter()
                    .max_by(|a, b| {
                        a.iter()
                            .map(|v| v.abs())
                            .sum::<f64>()
                            .total_cmp(&b.iter().map(|v| v.abs()).sum::<f64>())
                    })
                    .unwrap();
                let mut stops = vec![0.];
                stops.extend(extrema(*p));
                stops.push(1.);
                stops.sort_by(f64::total_cmp);
                for pair in stops.windows(2) {
                    let (mut lo, mut hi) = (pair[0], pair[1]);
                    if polynomial(*p, lo) * polynomial(*p, hi) <= 0. {
                        for _ in 0..60 {
                            let mid = (lo + hi) * 0.5;
                            if polynomial(*p, lo) * polynomial(*p, mid) <= 0. {
                                hi = mid;
                            } else {
                                lo = mid;
                            }
                        }
                        if ps
                            .iter()
                            .map(|p| polynomial(*p, (lo + hi) * 0.5).powi(2))
                            .sum::<f64>()
                            < 1e-20
                        {
                            return Err(invalid("animations/rotation"));
                        }
                    }
                }
                for t in stops {
                    if ps.iter().map(|p| polynomial(*p, t).powi(2)).sum::<f64>() < 1e-20 {
                        return Err(invalid("animations/rotation"));
                    }
                }
            }
        }
        Ok(())
    }

    pub fn evaluate(&self, time: f64) -> Result<Vec<f64>> {
        if !time.is_finite() {
            return Err(invalid("animations/time"));
        }
        let upper = self.times.partition_point(|t| (*t as f64) <= time);
        let i = upper.saturating_sub(1).min(self.times.len() - 1);
        let value = |i: usize| {
            self.values[i * self.width..(i + 1) * self.width]
                .iter()
                .map(|v| *v as f64)
                .collect::<Vec<_>>()
        };
        let mut out = value(i);
        if upper != 0 && upper < self.times.len() && self.interpolation != Interpolation::Step {
            let t =
                (time - self.times[i] as f64) / (self.times[i + 1] as f64 - self.times[i] as f64);
            if self.interpolation == Interpolation::Cubic {
                for (k, v) in out.iter_mut().enumerate() {
                    *v = polynomial(self.polynomial(i, k), t);
                }
            } else if self.path == AnimationPath::Rotation {
                let a = DQuat::from_array(out.as_slice().try_into().unwrap()).normalize();
                let b = DQuat::from_array(value(i + 1).as_slice().try_into().unwrap()).normalize();
                out = a.slerp(b, t).to_array().to_vec();
            } else {
                for (a, b) in out.iter_mut().zip(value(i + 1)) {
                    *a += (b - *a) * t;
                }
            }
        }
        if self.path == AnimationPath::Rotation {
            let q = DQuat::from_array(out.as_slice().try_into().unwrap());
            if q.length_squared() < 1e-20 {
                return Err(invalid("animations/rotation"));
            }
            out = q.normalize().to_array().to_vec();
        }
        if out.iter().any(|v| !v.is_finite()) {
            return Err(invalid("animations/values"));
        }
        Ok(out)
    }
}
fn polynomial(p: [f64; 4], t: f64) -> f64 {
    ((p[0] * t + p[1]) * t + p[2]) * t + p[3]
}
fn extrema(p: [f64; 4]) -> Vec<f64> {
    let (a, b, c) = (3. * p[0], 2. * p[1], p[2]);
    let mut out = vec![];
    if a.abs() < 1e-30 {
        if b.abs() > 1e-30 {
            out.push(-c / b);
        }
    } else {
        let d = b * b - 4. * a * c;
        if d >= 0. {
            out.extend([(-b - d.sqrt()) / (2. * a), (-b + d.sqrt()) / (2. * a)]);
        }
    }
    out.retain(|t| *t > 0. && *t < 1.);
    out
}

pub(crate) fn prepare_animation(p: &mut Prepared<'_>) -> Result<()> {
    if !(1..=240).contains(&p.scene.animation_bake_fps) {
        return Err(invalid("animations/fps"));
    }
    let mut bind_bytes = 0usize;
    for node in p.nodes.values() {
        checkpoint()?;
        if let Kind::Mesh {
            geometry,
            skin,
            morph_weights,
            ..
        } = &node.kind
        {
            let g = &p.geometries[geometry];
            if (!morph_weights.is_empty() && morph_weights.len() != g.morph_targets.len())
                || morph_weights
                    .iter()
                    .any(|v| !v.is_finite() || !(*v as f32).is_finite())
            {
                return Err(invalid("geometry/morph_weights"));
            }
            if skin.is_some() != g.skin.is_some() {
                return Err(invalid("geometry/skin"));
            }
            if let Some(skin) = skin {
                if skin.joints.is_empty()
                    || skin.joints.len() > 20_000
                    || skin.joints.iter().collect::<BTreeSet<_>>().len() != skin.joints.len()
                {
                    return Err(invalid("skin/joints"));
                }
                // Instancing a small shared mesh can expand into many independent
                // bind palettes. Bound that expansion before allocating each one.
                bind_bytes = bind_bytes
                    .checked_add(
                        skin.joints
                            .len()
                            .checked_mul(std::mem::size_of::<DMat4>() + std::mem::size_of::<Uuid>())
                            .ok_or_else(limited)?,
                    )
                    .ok_or_else(limited)?;
                if bind_bytes > MAX_BYTES {
                    return Err(limited());
                }
                let mut inverse_bind_matrices = Vec::with_capacity(skin.joints.len());
                for joint in &skin.joints {
                    checkpoint()?;
                    if !p
                        .nodes
                        .get(joint)
                        .is_some_and(|n| matches!(n.kind, Kind::Joint))
                    {
                        return Err(invalid("skin/joints"));
                    }
                    let inverse = p.rest_world[joint].inverse() * p.rest_world[&node.id];
                    if !inverse.is_finite()
                        || inverse
                            .to_cols_array()
                            .iter()
                            .any(|v| !(*v as f32).is_finite())
                    {
                        return Err(invalid("skin/inverse_bind_matrices"));
                    }
                    inverse_bind_matrices.push(inverse);
                }
                for (i, js) in g.skin.as_ref().unwrap().joints.iter().enumerate() {
                    if i % 4096 == 0 {
                        checkpoint()?;
                    }
                    if js.iter().any(|j| *j as usize >= skin.joints.len()) {
                        return Err(invalid("geometry/joints"));
                    }
                }
                p.skins.insert(
                    node.id,
                    PreparedSkin {
                        joints: skin.joints.clone(),
                        inverse_bind_matrices,
                    },
                );
            }
        }
    }
    let mut ids: BTreeSet<_> = p.nodes.keys().copied().collect();
    let mut names = BTreeSet::new();
    let mut count = ids.len();
    for clip in &p.scene.animations {
        checkpoint()?;
        count = count
            .checked_add(1 + clip.tracks.len())
            .ok_or_else(limited)?;
        if count > 20_000 {
            return Err(limited());
        }
        if !valid_id(clip.id)
            || !ids.insert(clip.id)
            || clip.name.is_empty()
            || clip.name.len() > 256
            || clip.name.contains('\0')
            || !names.insert(&clip.name)
            || clip.tracks.is_empty()
        {
            return Err(invalid("animations"));
        }
        let mut targets = BTreeSet::new();
        for track in &clip.tracks {
            checkpoint()?;
            let node = p
                .nodes
                .get(&track.target)
                .ok_or_else(|| invalid("animations/target"))?;
            if !p.samplers.contains_key(&track.sampler) {
                let bytes = p
                    .assets
                    .get(&track.sampler)
                    .ok_or_else(|| invalid("animations/sampler"))?;
                p.samplers
                    .insert(track.sampler.clone(), animation_sampler(bytes)?);
            }
            let s = &p.samplers[&track.sampler];
            if !targets.insert((track.target, s.path)) {
                return Err(invalid("animations/target"));
            }
            if s.path == AnimationPath::Weights {
                if !matches!(&node.kind, Kind::Mesh { geometry, .. } if p.geometries[geometry].morph_targets.len() == s.width)
                {
                    return Err(invalid("animations/weights"));
                }
            } else if p.skins.contains_key(&node.id) {
                return Err(invalid("animations/skin"));
            }
        }
    }
    Ok(())
}

#[derive(Clone)]
pub struct Transform {
    pub translation: [f64; 3],
    pub rotation: [f64; 4],
    pub scale: [f64; 3],
    pub weights: Vec<f64>,
}
impl Prepared<'_> {
    pub fn pose(&self, sample: Option<&AnimationSample>) -> Result<BTreeMap<Uuid, Transform>> {
        let mut pose = BTreeMap::new();
        for node in self.nodes.values() {
            checkpoint()?;
            let weights = match &node.kind {
                Kind::Mesh {
                    geometry,
                    morph_weights,
                    ..
                } if morph_weights.is_empty() => {
                    vec![0.; self.geometries[geometry].morph_targets.len()]
                }
                Kind::Mesh { morph_weights, .. } => morph_weights.clone(),
                _ => vec![],
            };
            pose.insert(
                node.id,
                Transform {
                    translation: node.translation,
                    rotation: node.rotation,
                    scale: node.scale,
                    weights,
                },
            );
        }
        if let Some(sample) = sample {
            if !sample.time.is_finite() {
                return Err(invalid("animations/time"));
            }
            let clip = self
                .scene
                .animations
                .iter()
                .find(|c| c.id == sample.clip)
                .ok_or_else(|| invalid("animations/clip"))?;
            for track in &clip.tracks {
                checkpoint()?;
                let s = &self.samplers[&track.sampler];
                let v = s.evaluate(sample.time)?;
                let node = pose.get_mut(&track.target).unwrap();
                match s.path {
                    AnimationPath::Translation => {
                        node.translation = v.as_slice().try_into().unwrap()
                    }
                    AnimationPath::Rotation => node.rotation = v.as_slice().try_into().unwrap(),
                    AnimationPath::Scale => node.scale = v.as_slice().try_into().unwrap(),
                    AnimationPath::Weights => node.weights = v,
                }
            }
        }
        Ok(pose)
    }

    pub fn evaluate(&self, sample: Option<&AnimationSample>) -> Result<BTreeMap<Uuid, Bounds>> {
        let pose = self.pose(sample)?;
        let mut world = BTreeMap::new();
        fn transforms(
            node: &Node,
            parent: DMat4,
            pose: &BTreeMap<Uuid, Transform>,
            world: &mut BTreeMap<Uuid, DMat4>,
        ) -> Result<()> {
            checkpoint()?;
            let t = &pose[&node.id];
            let m = parent
                * DMat4::from_scale_rotation_translation(
                    DVec3::from_array(t.scale),
                    DQuat::from_array(t.rotation),
                    DVec3::from_array(t.translation),
                );
            if !m.is_finite() {
                return Err(invalid("nodes/transform"));
            }
            world.insert(node.id, m);
            for child in &node.children {
                transforms(child, m, pose, world)?;
            }
            Ok(())
        }
        transforms(&self.scene.nodes[0], DMat4::IDENTITY, &pose, &mut world)?;
        let mut bounds = BTreeMap::new();
        for node in self.nodes.values() {
            if let Kind::Mesh { geometry, .. } = &node.kind {
                let g = &self.geometries[geometry];
                let mut bound: Option<Bounds> = None;
                let matrices = self.skins.get(&node.id).map(|s| {
                    s.joints
                        .iter()
                        .zip(&s.inverse_bind_matrices)
                        .map(|(j, b)| world[j] * *b)
                        .collect::<Vec<_>>()
                });
                for (i, position) in g.positions.iter().enumerate() {
                    if i % 1024 == 0 {
                        checkpoint()?;
                    }
                    let mut vertex = DVec3::from_array(position.map(f64::from));
                    for (m, w) in g.morph_targets.iter().zip(&pose[&node.id].weights) {
                        vertex += DVec3::from_array(m.positions[i].map(f64::from)) * *w;
                    }
                    let vertex = if let (Some(matrices), Some(skin)) = (&matrices, &g.skin) {
                        (0..4)
                            .map(|k| {
                                matrices[skin.joints[i][k] as usize].transform_point3(vertex)
                                    * skin.weights[i][k] as f64
                            })
                            .sum()
                    } else {
                        world[&node.id].transform_point3(vertex)
                    };
                    if !vertex.is_finite() {
                        return Err(invalid("geometry"));
                    }
                    let b = Bounds::point(vertex);
                    if let Some(existing) = &mut bound {
                        existing.include(b);
                    } else {
                        bound = Some(b);
                    }
                }
                if let Some(bound) = bound {
                    bounds.insert(node.id, bound);
                }
            }
        }
        fn aggregate(node: &Node, bounds: &mut BTreeMap<Uuid, Bounds>) -> Result<Option<Bounds>> {
            checkpoint()?;
            let mut bound = bounds.get(&node.id).copied();
            for child in &node.children {
                if let Some(b) = aggregate(child, bounds)? {
                    if let Some(v) = &mut bound {
                        v.include(b);
                    } else {
                        bound = Some(b);
                    }
                }
            }
            if let Some(b) = bound {
                bounds.insert(node.id, b);
            }
            Ok(bound)
        }
        aggregate(&self.scene.nodes[0], &mut bounds)?;
        Ok(bounds)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn sampler(path: AnimationPath, width: usize, values: Vec<f32>) -> AnimationSampler {
        AnimationSampler {
            path,
            width,
            values,
            interpolation: Interpolation::Linear,
            times: vec![1., 3.],
            in_tangents: vec![],
            out_tangents: vec![],
        }
    }

    #[test]
    fn vector_keys_clamping_steps_and_derivatives_use_seconds() {
        let mut s = sampler(AnimationPath::Translation, 3, vec![0., 0., 0., 2., 4., 6.]);
        assert_eq!(s.evaluate(-1.).unwrap(), [0., 0., 0.]);
        assert_eq!(s.evaluate(2.).unwrap(), [1., 2., 3.]);
        assert_eq!(s.evaluate(4.).unwrap(), [2., 4., 6.]);
        s.interpolation = Interpolation::Step;
        assert_eq!(s.evaluate(2.999).unwrap(), [0., 0., 0.]);
        assert_eq!(s.evaluate(3.).unwrap(), [2., 4., 6.]);
        s.interpolation = Interpolation::Cubic;
        s.in_tangents = vec![0.; 6];
        s.out_tangents = vec![2., 0., 0., 0., 0., 0.];
        assert_eq!(s.evaluate(2.).unwrap(), [1.5, 2., 3.]);
    }

    #[test]
    fn quaternion_sign_wrap_and_cubic_normalization_preserve_orientation() {
        let a = DQuat::from_rotation_z(170f64.to_radians());
        let b = -DQuat::from_rotation_z(190f64.to_radians());
        let mut s = sampler(
            AnimationPath::Rotation,
            4,
            a.to_array()
                .into_iter()
                .chain(b.to_array())
                .map(|v| v as f32)
                .collect(),
        );
        let mid = DQuat::from_array(s.evaluate(2.).unwrap().try_into().unwrap());
        assert!((mid.dot(DQuat::from_rotation_z(std::f64::consts::PI)).abs() - 1.).abs() < 1e-8);
        s.values = DQuat::IDENTITY
            .to_array()
            .into_iter()
            .chain(DQuat::from_rotation_y(std::f64::consts::FRAC_PI_2).to_array())
            .map(|v| v as f32)
            .collect();
        s.interpolation = Interpolation::Cubic;
        s.in_tangents = vec![0.; 8];
        s.out_tangents = vec![0.; 8];
        s.validate_segments().unwrap();
        let mid = DQuat::from_array(s.evaluate(2.).unwrap().try_into().unwrap());
        assert!((mid.length() - 1.).abs() < 1e-12);
        assert!(
            (mid.dot(DQuat::from_rotation_y(std::f64::consts::FRAC_PI_4))
                .abs()
                - 1.)
                .abs()
                < 1e-8
        );
    }

    #[test]
    fn cubic_interior_scale_and_rotation_singularities_are_rejected() {
        let mut s = sampler(AnimationPath::Scale, 3, vec![1.; 6]);
        s.interpolation = Interpolation::Cubic;
        s.in_tangents = vec![0., 0., 0., 4., 0., 0.];
        s.out_tangents = vec![-4., 0., 0., 0., 0., 0.];
        assert!(s.validate_segments().is_err());
        s = sampler(
            AnimationPath::Rotation,
            4,
            vec![0., 0., 0., 1., 0., 0., 0., -1.],
        );
        s.interpolation = Interpolation::Cubic;
        s.in_tangents = vec![0.; 8];
        s.out_tangents = vec![0.; 8];
        assert!(s.validate_segments().is_err());
    }
}
